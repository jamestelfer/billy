// Command corpusgen regenerates alexaverify's synthetic signed request corpus.
//
// It deliberately writes no private key. Each run creates a new test-only PKI
// and replaces the root, chain and signatures together.
package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	fixtureTime = "2026-01-02T03:04:05Z"
	certURL     = "https://s3.amazonaws.com/echo.api/echo-api-cert-synthetic.pem"
)

type fixture struct {
	directory   string
	requestType string
	extra       string
}

type headers struct {
	Signature256        string `json:"signature_256"`
	CertificateChainURL string `json:"certificate_chain_url"`
}

func main() {
	if err := generate("testdata/corpus"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(out string) error {
	now, err := time.Parse(time.RFC3339, fixtureTime)
	if err != nil {
		return err
	}
	root, rootKey, err := createRoot(now)
	if err != nil {
		return err
	}
	intermediate, intermediateKey, err := createIntermediate(now, root, rootKey)
	if err != nil {
		return err
	}
	leafDER, leafKey, err := createLeaf(now, intermediate, intermediateKey)
	if err != nil {
		return err
	}

	if err := os.RemoveAll(out); err != nil {
		return fmt.Errorf("removing old corpus: %w", err)
	}
	if err := os.MkdirAll(out, 0o750); err != nil {
		return fmt.Errorf("creating corpus directory: %w", err)
	}
	if err := writePEM(filepath.Join(out, "root.pem"), root.Raw); err != nil {
		return err
	}
	chain := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	chain = append(chain, pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: intermediate.Raw,
	})...)
	if err := os.WriteFile(filepath.Join(out, "chain.pem"), chain, 0o600); err != nil {
		return fmt.Errorf("writing chain: %w", err)
	}

	fixtures := []fixture{
		{directory: "LaunchRequest", requestType: "LaunchRequest"},
		{
			directory:   "IntentRequest",
			requestType: "IntentRequest",
			extra:       `,"intent":{"name":"SyntheticIntent","confirmationStatus":"NONE","slots":{}}`,
		},
		{
			directory:   "AudioPlayer.PlaybackStarted",
			requestType: "AudioPlayer.PlaybackStarted",
			extra:       `,"token":"synthetic-audio-token","offsetInMilliseconds":0`,
		},
		{
			directory:   "AudioPlayer.PlaybackNearlyFinished",
			requestType: "AudioPlayer.PlaybackNearlyFinished",
			extra:       `,"token":"synthetic-audio-token","offsetInMilliseconds":59000`,
		},
		{
			directory:   "AudioPlayer.PlaybackStopped",
			requestType: "AudioPlayer.PlaybackStopped",
			extra:       `,"token":"synthetic-audio-token","offsetInMilliseconds":32000`,
		},
		{
			directory:   "AudioPlayer.PlaybackFinished",
			requestType: "AudioPlayer.PlaybackFinished",
			extra:       `,"token":"synthetic-audio-token","offsetInMilliseconds":60000`,
		},
		{
			directory:   "AudioPlayer.PlaybackFailed",
			requestType: "AudioPlayer.PlaybackFailed",
			extra: `,"token":"synthetic-audio-token","offsetInMilliseconds":12000,` +
				`"error":{"type":"MEDIA_ERROR_UNKNOWN","message":"synthetic failure"}`,
		},
	}
	for _, item := range fixtures {
		if err := writeFixture(out, item, now, leafKey); err != nil {
			return err
		}
	}
	return nil
}

func writeFixture(out string, item fixture, now time.Time, key *rsa.PrivateKey) error {
	dir := filepath.Join(out, item.directory)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", item.directory, err)
	}

	// Deliberately pretty and newline-terminated. Any decode/re-encode before
	// verification changes these bytes and invalidates every signature.
	body := fmt.Appendf(nil, `{
  "version": "1.0",
  "context": {
    "System": {
      "application": {"applicationId": "amzn1.ask.skill.synthetic"},
      "user": {"userId": "synthetic-user-id"},
      "device": {"deviceId": "synthetic-device-id"},
      "apiEndpoint": "https://api.amazonalexa.com",
      "apiAccessToken": "synthetic-api-access-token"
    }
  },
  "request": {
    "type": %q,
    "requestId": %q,
    "timestamp": %q,
    "locale": "en-US"%s
  }
}
`, item.requestType, "synthetic-"+strings.ToLower(item.directory), now.Format(time.RFC3339), item.extra)

	digest := sha256.Sum256(body)
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return fmt.Errorf("signing %s: %w", item.directory, err)
	}
	encodedHeaders, err := json.Marshal(headers{
		Signature256:        base64.StdEncoding.EncodeToString(signature),
		CertificateChainURL: certURL,
	}, jsontext.WithIndent("  "))
	if err != nil {
		return fmt.Errorf("encoding headers for %s: %w", item.directory, err)
	}
	encodedHeaders = append(encodedHeaders, '\n')

	if err := os.WriteFile(filepath.Join(dir, "body.json"), body, 0o600); err != nil {
		return fmt.Errorf("writing body for %s: %w", item.directory, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "headers.json"), encodedHeaders, 0o600); err != nil {
		return fmt.Errorf("writing headers for %s: %w", item.directory, err)
	}
	return nil
}

func createRoot(now time.Time) (*x509.Certificate, *rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("generating root key: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1001),
		Subject:               pkix.Name{CommonName: "alexaverify synthetic corpus root"},
		NotBefore:             now.Add(-365 * 24 * time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return nil, nil, fmt.Errorf("creating root certificate: %w", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing root certificate: %w", err)
	}
	return certificate, key, nil
}

func createIntermediate(
	now time.Time,
	root *x509.Certificate,
	rootKey *rsa.PrivateKey,
) (*x509.Certificate, *rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("generating intermediate key: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1002),
		Subject:               pkix.Name{CommonName: "alexaverify synthetic corpus intermediate"},
		NotBefore:             now.Add(-180 * 24 * time.Hour),
		NotAfter:              now.Add(5 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, root, key.Public(), rootKey)
	if err != nil {
		return nil, nil, fmt.Errorf("creating intermediate certificate: %w", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing intermediate certificate: %w", err)
	}
	return certificate, key, nil
}

func createLeaf(
	now time.Time,
	intermediate *x509.Certificate,
	intermediateKey *rsa.PrivateKey,
) ([]byte, *rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("generating leaf key: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1003),
		Subject:      pkix.Name{CommonName: "alexaverify synthetic corpus leaf"},
		DNSNames:     []string{"echo-api.amazon.com"},
		NotBefore:    now.Add(-24 * time.Hour),
		NotAfter:     now.Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, intermediate, key.Public(), intermediateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("creating leaf certificate: %w", err)
	}
	return der, key, nil
}

func writePEM(path string, der []byte) error {
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
