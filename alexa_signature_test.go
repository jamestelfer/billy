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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type handlerRoundTripFunc func(*http.Request) (*http.Response, error)

func (f handlerRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// TestAlexaAcceptsAValidSignedRequest exercises the complete verified playback
// path: a generated certificate chain is fetched over TLS, the exact request
// bytes verify and are captured, and skill dispatch returns a Play directive.
func TestAlexaAcceptsAValidSignedRequest(t *testing.T) {
	verifier, leafKey, fetches := newHandlerTestVerifier(t)
	store, captureDir := newTestCapture(t)

	body := []byte(samplePlayRequest)
	headers := signAlexaBody(t, leafKey, body)

	configuredBook := mustBook(t)
	recorder := postAlexa(t, newRouter(testLogger(), store, verifier, configuredBook), string(body), headers)
	require.Equal(t, http.StatusOK, recorder.Code, "POST /alexa status = %d, want %d; body = %q", recorder.Code, http.StatusOK, recorder.Body.String())
	require.EqualValues(t, 1, fetches.Load(), "certificate fetches")

	var response alexaEnvelope
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response), "decoding Alexa response")
	require.NotNil(t, response.Response.OutputSpeech)
	require.Equal(t, "Playing Test Book.", response.Response.OutputSpeech.Text)
	require.Len(t, response.Response.Directives, 1)
	require.Equal(t, "AudioPlayer.Play", response.Response.Directives[0].Type)
	require.Equal(t, staticBookToken, response.Response.Directives[0].AudioItem.Stream.Token)

	stem := onlyCapturedStem(t, captureDir)
	captured, err := os.ReadFile(filepath.Join(captureDir, stem+".body"))
	require.NoError(t, err, "reading captured body: %v", err)
	require.Equal(t, string(body), string(captured), "captured body differs from the signed wire bytes")
}

func TestAlexaAcceptsSignedAudioPlayerLifecycleEvents(t *testing.T) {
	verifier, leafKey, fetches := newHandlerTestVerifier(t)
	store, _ := newTestCapture(t)
	router := newRouter(testLogger(), store, verifier, mustBook(t))

	for _, eventType := range []string{
		"AudioPlayer.PlaybackStarted",
		"AudioPlayer.PlaybackStopped",
		"AudioPlayer.PlaybackFinished",
		"AudioPlayer.PlaybackNearlyFinished",
		"AudioPlayer.PlaybackFailed",
	} {
		t.Run(eventType, func(t *testing.T) {
			body := []byte(`{"version":"1.0","request":{"type":"` + eventType + `","requestId":"request-id",` +
				`"timestamp":"2026-08-30T01:02:03Z","token":"` + staticBookToken + `","offsetInMilliseconds":42000,` +
				`"error":{"type":"MEDIA_ERROR_UNKNOWN","message":"detail"}}}`)

			recorder := postAlexa(t, router, string(body), signAlexaBody(t, leafKey, body))

			require.Equal(t, http.StatusOK, recorder.Code)
			var response alexaEnvelope
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Empty(t, response.Response.Directives)
			assert.Nil(t, response.Response.OutputSpeech)
		})
	}
	assert.EqualValues(t, 1, fetches.Load(), "the verified lifecycle requests should reuse the certificate cache")
}

func newHandlerTestVerifier(t *testing.T) (*alexaverify.Verifier, *rsa.PrivateKey, *atomic.Int64) {
	t.Helper()
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
	return verifier, leafKey, &fetches
}

func signAlexaBody(t *testing.T, leafKey *rsa.PrivateKey, body []byte) map[string]string {
	t.Helper()
	digest := sha256.Sum256(body)
	signature, err := rsa.SignPKCS1v15(rand.Reader, leafKey, crypto.SHA256, digest[:])
	require.NoError(t, err, "signing request: %v", err)
	return map[string]string{
		"Signature-256":         base64.StdEncoding.EncodeToString(signature),
		"SignatureCertChainUrl": "https://s3.amazonaws.com/echo.api/echo-api-cert-test.pem",
		"Content-Type":          "application/json",
	}
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
