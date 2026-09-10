package alexaverify

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testPKI is generated at test time rather than committed. The intermediate is
// intentional: a leaf signed directly by a test root would not exercise chain
// assembly at all.
type testPKI struct {
	intermediate    *x509.Certificate
	intermediateKey crypto.Signer
	roots           *x509.CertPool
}

type testLeaf struct {
	certificate *x509.Certificate
	key         crypto.Signer
	bundle      []byte
}

type leafSpec struct {
	dnsNames  []string
	notBefore time.Time
	notAfter  time.Time
	key       crypto.Signer
}

func generateTestPKI(t *testing.T, now time.Time) *testPKI {
	t.Helper()

	rootKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err, "generating root key: %v", err)
	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "alexaverify test root"},
		NotBefore:             now.Add(-24 * time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, rootKey.Public(), rootKey)
	require.NoError(t, err, "creating root certificate: %v", err)
	root, err := x509.ParseCertificate(rootDER)
	require.NoError(t, err, "parsing root certificate: %v", err)

	intermediateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err, "generating intermediate key: %v", err)
	intermediateTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "alexaverify test intermediate"},
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

	roots := x509.NewCertPool()
	roots.AddCert(root)
	return &testPKI{
		intermediate:    intermediate,
		intermediateKey: intermediateKey,
		roots:           roots,
	}
}

func (pki *testPKI) issueLeaf(t *testing.T, spec leafSpec) testLeaf {
	t.Helper()

	key := spec.key
	if key == nil {
		generated, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err, "generating leaf key: %v", err)
		key = generated
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(spec.notBefore.UnixNano() + spec.notAfter.UnixNano()),
		Subject:      pkix.Name{CommonName: "alexaverify test leaf"},
		DNSNames:     spec.dnsNames,
		NotBefore:    spec.notBefore,
		NotAfter:     spec.notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(
		rand.Reader, template, pki.intermediate, key.Public(), pki.intermediateKey,
	)
	require.NoError(t, err, "creating leaf certificate: %v", err)
	certificate, err := x509.ParseCertificate(der)
	require.NoError(t, err, "parsing leaf certificate: %v", err)

	bundle := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	bundle = append(bundle, pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: pki.intermediate.Raw,
	})...)
	return testLeaf{certificate: certificate, key: key, bundle: bundle}
}

func validLeafSpec(now time.Time) leafSpec {
	return leafSpec{
		dnsNames:  []string{echoAPIDomain},
		notBefore: now.Add(-time.Hour),
		notAfter:  now.Add(time.Hour),
	}
}
