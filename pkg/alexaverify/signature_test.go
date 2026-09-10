package alexaverify

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testCertURL = "https://s3.amazonaws.com/echo.api/echo-api-cert-test.pem"

func signBody(t *testing.T, key crypto.Signer, body []byte) string {
	t.Helper()

	rsaKey, ok := key.(*rsa.PrivateKey)
	require.True(t, ok, "signBody key is %T, want *rsa.PrivateKey", key)
	digest := sha256.Sum256(body)
	signature, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, digest[:])
	require.NoError(t, err, "signing body: %v", err)
	return base64.StdEncoding.EncodeToString(signature)
}

func headersFor(signature string) http.Header {
	headers := make(http.Header)
	headers.Set(signatureHeader, signature)
	headers.Set(certChainURLHeader, testCertURL)
	return headers
}

func rewriteClient(t *testing.T, server *httptest.Server) (*http.Client, *atomic.Int64) {
	t.Helper()

	target, err := url.Parse(server.URL)
	require.NoError(t, err, "parsing test server URL: %v", err)
	calls := &atomic.Int64{}
	base := server.Client().Transport
	client := *server.Client()
	client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		clone := request.Clone(request.Context())
		cloneURL := *request.URL
		cloneURL.Scheme = target.Scheme
		cloneURL.Host = target.Host
		clone.URL = &cloneURL
		clone.Host = target.Host
		return base.RoundTrip(clone)
	})
	return &client, calls
}

func verifierServing(t *testing.T, roots *x509.CertPool, now time.Time, handler http.Handler) (*Verifier, *atomic.Int64) {
	t.Helper()

	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	client, calls := rewriteClient(t, server)
	verifier, err := New(WithRoots(roots), WithClock(at(now)), WithHTTPClient(client))
	require.NoError(t, err, "New: %v", err)
	return verifier, calls
}

func verifierServingBundle(t *testing.T, roots *x509.CertPool, now time.Time, bundle []byte) (*Verifier, *atomic.Int64) {
	t.Helper()
	return verifierServing(t, roots, now, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bundle)
	}))
}

func assertOnlySentinel(t *testing.T, err, want error) {
	t.Helper()

	require.ErrorIs(t, err, want, "got %v, want %v", err, want)
	sentinels := []error{
		ErrMissingHeader,
		ErrCertURLInvalid,
		ErrCertURLUserinfo,
		ErrCertURLTraversal,
		ErrCertFetch,
		ErrUntrustedChain,
		ErrCertHostname,
		ErrUnsupportedKey,
		ErrBadSignature,
		ErrStaleTimestamp,
	}
	matches := 0
	for _, sentinel := range sentinels {
		if errors.Is(err, sentinel) {
			matches++
		}
	}
	require.Equal(t, 1, matches, "error %v wraps %d sentinels, want exactly one", err, matches)
}

func TestVerifySignatureAndCertificateChain(t *testing.T) {
	pki := generateTestPKI(t, fixedNow)
	validLeaf := pki.issueLeaf(t, validLeafSpec(fixedNow))
	body := envelope("LaunchRequest", fixedNow)
	validSignature := signBody(t, validLeaf.key, body)

	t.Run("valid three certificate path and signature", func(t *testing.T) {
		verifier, calls := verifierServingBundle(t, pki.roots, fixedNow, validLeaf.bundle)
		{
			err := verifier.Verify(t.Context(), body, headersFor(validSignature))
			require.NoError(t, err, "Verify: %v", err)
		}
		{
			got := calls.Load()
			require.EqualValues(t, 1, got, "certificate fetches = %d, want 1", got)
		}
	})

	t.Run("body mutated after signing", func(t *testing.T) {
		verifier, _ := verifierServingBundle(t, pki.roots, fixedNow, validLeaf.bundle)
		err := verifier.Verify(t.Context(), append(body, ' '), headersFor(validSignature))
		assertOnlySentinel(t, err, ErrBadSignature)
	})

	t.Run("signature from another key", func(t *testing.T) {
		otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err, "generating other key: %v", err)
		verifier, _ := verifierServingBundle(t, pki.roots, fixedNow, validLeaf.bundle)
		err = verifier.Verify(t.Context(), body, headersFor(signBody(t, otherKey, body)))
		assertOnlySentinel(t, err, ErrBadSignature)
	})

	t.Run("signature is not base64", func(t *testing.T) {
		verifier, _ := verifierServingBundle(t, pki.roots, fixedNow, validLeaf.bundle)
		assertOnlySentinel(t,
			verifier.Verify(t.Context(), body, headersFor("%%%not-base64%%%")),
			ErrBadSignature,
		)
	})

	t.Run("wrong leaf DNS SAN", func(t *testing.T) {
		spec := validLeafSpec(fixedNow)
		spec.dnsNames = []string{"example.com"}
		leaf := pki.issueLeaf(t, spec)
		verifier, _ := verifierServingBundle(t, pki.roots, fixedNow, leaf.bundle)
		assertOnlySentinel(t,
			verifier.Verify(t.Context(), body, headersFor(signBody(t, leaf.key, body))),
			ErrCertHostname,
		)
	})

	t.Run("expired leaf", func(t *testing.T) {
		spec := validLeafSpec(fixedNow)
		spec.notBefore = fixedNow.Add(-2 * time.Hour)
		spec.notAfter = fixedNow.Add(-time.Hour)
		leaf := pki.issueLeaf(t, spec)
		verifier, _ := verifierServingBundle(t, pki.roots, fixedNow, leaf.bundle)
		assertOnlySentinel(t,
			verifier.Verify(t.Context(), body, headersFor(signBody(t, leaf.key, body))),
			ErrUntrustedChain,
		)
	})

	t.Run("not yet valid leaf", func(t *testing.T) {
		spec := validLeafSpec(fixedNow)
		spec.notBefore = fixedNow.Add(time.Hour)
		spec.notAfter = fixedNow.Add(2 * time.Hour)
		leaf := pki.issueLeaf(t, spec)
		verifier, _ := verifierServingBundle(t, pki.roots, fixedNow, leaf.bundle)
		assertOnlySentinel(t,
			verifier.Verify(t.Context(), body, headersFor(signBody(t, leaf.key, body))),
			ErrUntrustedChain,
		)
	})

	t.Run("intermediate omitted", func(t *testing.T) {
		leafOnly := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: validLeaf.certificate.Raw})
		verifier, _ := verifierServingBundle(t, pki.roots, fixedNow, leafOnly)
		assertOnlySentinel(t,
			verifier.Verify(t.Context(), body, headersFor(validSignature)),
			ErrUntrustedChain,
		)
	})

	t.Run("root absent from pool", func(t *testing.T) {
		verifier, _ := verifierServingBundle(t, x509.NewCertPool(), fixedNow, validLeaf.bundle)
		assertOnlySentinel(t,
			verifier.Verify(t.Context(), body, headersFor(validSignature)),
			ErrUntrustedChain,
		)
	})

	t.Run("ECDSA leaf key", func(t *testing.T) {
		ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err, "generating ECDSA key: %v", err)
		spec := validLeafSpec(fixedNow)
		spec.key = ecdsaKey
		leaf := pki.issueLeaf(t, spec)
		verifier, _ := verifierServingBundle(t, pki.roots, fixedNow, leaf.bundle)
		assertOnlySentinel(t,
			verifier.Verify(t.Context(), body, headersFor(validSignature)),
			ErrUnsupportedKey,
		)
	})
}

func TestValidateChainUsesTheSystemRootPoolByDefault(t *testing.T) {
	pki := generateTestPKI(t, fixedNow)
	leaf := pki.issueLeaf(t, validLeafSpec(fixedNow))
	chain, err := parsePEMChain(leaf.bundle)
	require.NoError(t, err, "parsePEMChain: %v", err)

	// The generated root cannot be in any host's system pool, so failure is
	// expected. The point is to exercise nil Roots on every CI platform and
	// prove that it resolves the host pool rather than treating nil as trust-all.
	verifier := &Verifier{}
	assertOnlySentinel(t, verifier.validateChain(chain, fixedNow), ErrUntrustedChain)
}

func TestCertificateFetchFailures(t *testing.T) {
	pki := generateTestPKI(t, fixedNow)
	leaf := pki.issueLeaf(t, validLeafSpec(fixedNow))
	body := envelope("LaunchRequest", fixedNow)
	headers := headersFor(signBody(t, leaf.key, body))

	t.Run("non-200", func(t *testing.T) {
		verifier, calls := verifierServing(t, pki.roots, fixedNow, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "no certificate here", http.StatusNotFound)
		}))
		assertOnlySentinel(t, verifier.Verify(t.Context(), body, headers), ErrCertFetch)
		{
			got := calls.Load()
			require.EqualValues(t, certificateFetchAttempts, got, "attempts = %d, want %d", got, certificateFetchAttempts)
		}
	})

	t.Run("connection error", func(t *testing.T) {
		var calls atomic.Int64
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("dial failed")
		})}
		verifier, err := New(
			WithRoots(pki.roots), WithClock(at(fixedNow)), WithHTTPClient(client),
		)
		require.NoError(t, err, "New: %v", err)
		assertOnlySentinel(t, verifier.Verify(t.Context(), body, headers), ErrCertFetch)
		{
			got := calls.Load()
			require.EqualValues(t, certificateFetchAttempts, got, "attempts = %d, want %d", got, certificateFetchAttempts)
		}
	})

	t.Run("body over size cap", func(t *testing.T) {
		verifier, _ := verifierServing(t, pki.roots, fixedNow, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(strings.Repeat("x", maxCertificateChainBytes+1)))
		}))
		assertOnlySentinel(t, verifier.Verify(t.Context(), body, headers), ErrCertFetch)
	})

	t.Run("invalid PEM", func(t *testing.T) {
		verifier, _ := verifierServingBundle(t, pki.roots, fixedNow, []byte("not a certificate"))
		assertOnlySentinel(t, verifier.Verify(t.Context(), body, headers), ErrUntrustedChain)
	})

	t.Run("redirect is not followed", func(t *testing.T) {
		var targetCalls atomic.Int64
		verifier, calls := verifierServing(t, pki.roots, fixedNow, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/target" {
				targetCalls.Add(1)
				_, _ = w.Write(leaf.bundle)
				return
			}
			http.Redirect(w, request, "/target", http.StatusFound)
		}))
		assertOnlySentinel(t, verifier.Verify(t.Context(), body, headers), ErrCertFetch)
		{
			got := calls.Load()
			require.EqualValues(t, certificateFetchAttempts, got, "attempts = %d, want %d", got, certificateFetchAttempts)
		}
		{
			got := targetCalls.Load()
			require.EqualValues(t, 0, got, "redirect target requests = %d, want 0", got)
		}
	})

	t.Run("caller deadline bounds attempts", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		})}
		verifier, err := New(
			WithRoots(pki.roots), WithClock(at(fixedNow)), WithHTTPClient(client),
		)
		require.NoError(t, err, "New: %v", err)
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		assertOnlySentinel(t, verifier.Verify(ctx, body, headers), ErrCertFetch)
	})
}

func TestRejectedCertificateURLsNeverFetch(t *testing.T) {
	tests := []struct {
		value string
		want  error
	}{
		{"https://very.bad/echo.api/cert", ErrCertURLInvalid},
		{"https://user:pw@s3.amazonaws.com/echo.api/cert", ErrCertURLUserinfo},
		{"https://s3.amazonaws.com/echo.api/%2e%2e/cert", ErrCertURLTraversal},
		{"http://s3.amazonaws.com/echo.api/cert", ErrCertURLInvalid},
		{"https://s3.amazonaws.com:563/echo.api/cert", ErrCertURLInvalid},
		{"https://s3.amazonaws.com/ECHO.API/cert", ErrCertURLInvalid},
		{"badUrl", ErrCertURLInvalid},
	}

	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			var calls atomic.Int64
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return nil, errors.New("unexpected fetch")
			})}
			verifier, err := New(WithClock(at(fixedNow)), WithHTTPClient(client))
			require.NoError(t, err, "New: %v", err)
			headers := signedHeaders()
			headers.Set(certChainURLHeader, tc.value)
			assertOnlySentinel(t,
				verifier.Verify(t.Context(), envelope("LaunchRequest", fixedNow), headers),
				tc.want,
			)
			{
				got := calls.Load()
				require.EqualValues(t, 0, got, "fetches = %d, want zero", got)
			}
		})
	}
}
