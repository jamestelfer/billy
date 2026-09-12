package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json/v2"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jamestelfer/billy/pkg/alexaverify"
	"github.com/stretchr/testify/require"
)

type handlerRoundTripFunc func(*http.Request) (*http.Response, error)

func (f handlerRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// TestAlexaAcceptsAValidSignedRequest is the full Phase 3 tracer bullet: a
// generated root -> intermediate -> leaf chain is fetched over TLS, the exact
// request bytes verify, skill handling runs, and those same bytes are captured.
func TestAlexaAcceptsAValidSignedRequest(t *testing.T) {
	roots, leafKey, bundle := generateHandlerTestChain(t, testNow)

	var fetches atomic.Int64
	certificateServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bundle)
	}))
	t.Cleanup(certificateServer.Close)

	target, err := url.Parse(certificateServer.URL)
	require.NoError(t, err, "parsing certificate server URL: %v", err)
	baseTransport := certificateServer.Client().Transport
	client := *certificateServer.Client()
	client.Transport = handlerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		clone := request.Clone(request.Context())
		cloneURL := *request.URL
		cloneURL.Scheme = target.Scheme
		cloneURL.Host = target.Host
		clone.URL = &cloneURL
		clone.Host = target.Host
		return baseTransport.RoundTrip(clone)
	})

	verifier, err := alexaverify.New(
		alexaverify.WithRoots(roots),
		alexaverify.WithClock(func() time.Time { return testNow }),
		alexaverify.WithHTTPClient(&client),
	)
	require.NoError(t, err, "alexaverify.New: %v", err)
	store, captureDir := newTestCapture(t)

	body := []byte(sampleLaunchRequest)
	digest := sha256.Sum256(body)
	signature, err := rsa.SignPKCS1v15(rand.Reader, leafKey, crypto.SHA256, digest[:])
	require.NoError(t, err, "signing request: %v", err)
	headers := map[string]string{
		"Signature-256":         base64.StdEncoding.EncodeToString(signature),
		"SignatureCertChainUrl": "https://s3.amazonaws.com/echo.api/echo-api-cert-test.pem",
		"Content-Type":          "application/json",
	}

	recorder := postAlexa(t, newRouter(testLogger(), store, verifier), string(body), headers)
	require.Equal(t, http.StatusOK, recorder.Code, "POST /alexa status = %d, want %d; body = %q", recorder.Code, http.StatusOK, recorder.Body.String())
	require.EqualValues(t, 1, fetches.Load(), "certificate fetches")

	var response alexaEnvelope
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response), "decoding Alexa response")
	require.Equal(t, spokenConfirmation, response.Response.OutputSpeech.Text, "spoken response = %q, want %q", response.Response.OutputSpeech.Text, spokenConfirmation)

	stem := onlyCapturedStem(t, captureDir)
	captured, err := os.ReadFile(filepath.Join(captureDir, stem+".body"))
	require.NoError(t, err, "reading captured body: %v", err)
	require.Equal(t, string(body), string(captured), "captured body differs from the signed wire bytes")
}

func generateHandlerTestChain(t *testing.T, now time.Time) (*x509.CertPool, *rsa.PrivateKey, []byte) {
	t.Helper()

	rootKey := generateHandlerRSAKey(t, "root")
	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "billy handler test root"},
		NotBefore:             now.Add(-24 * time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}
	rootDER, err := x509.CreateCertificate(
		rand.Reader, rootTemplate, rootTemplate, rootKey.Public(), rootKey,
	)
	require.NoError(t, err, "creating root certificate: %v", err)
	root, err := x509.ParseCertificate(rootDER)
	require.NoError(t, err, "parsing root certificate: %v", err)

	intermediateKey := generateHandlerRSAKey(t, "intermediate")
	intermediateTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "billy handler test intermediate"},
		NotBefore:             now.Add(-12 * time.Hour),
		NotAfter:              now.Add(12 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	intermediateDER, err := x509.CreateCertificate(
		rand.Reader, intermediateTemplate, root, intermediateKey.Public(), rootKey,
	)
	require.NoError(t, err, "creating intermediate certificate: %v", err)
	intermediate, err := x509.ParseCertificate(intermediateDER)
	require.NoError(t, err, "parsing intermediate certificate: %v", err)

	leafKey := generateHandlerRSAKey(t, "leaf")
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "billy handler test leaf"},
		DNSNames:     []string{"echo-api.amazon.com"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(
		rand.Reader, leafTemplate, intermediate, leafKey.Public(), intermediateKey,
	)
	require.NoError(t, err, "creating leaf certificate: %v", err)

	roots := x509.NewCertPool()
	roots.AddCert(root)
	bundle := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	bundle = append(bundle, pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: intermediateDER,
	})...)
	return roots, leafKey, bundle
}

func generateHandlerRSAKey(t *testing.T, name string) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err, "generating %s key: %v", name, err)
	return key
}
