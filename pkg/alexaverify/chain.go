package alexaverify

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"
)

// parsedChain is a certificate bundle split into the signing leaf and the
// intermediates needed to build a path to a trusted root.
type parsedChain struct {
	leaf          *x509.Certificate
	intermediates *x509.CertPool
}

// parsePEMChain splits the fetched PEM bundle in wire order: Alexa's signing
// leaf is first and every following certificate is an intermediate candidate.
// This follows generateCertificatesArray in the pinned Node SDK's helper.ts.
func parsePEMChain(bundle []byte) (*parsedChain, error) {
	remaining := bytes.TrimSpace(bundle)
	certificates := make([]*x509.Certificate, 0, 3)

	for len(remaining) > 0 {
		block, rest := pem.Decode(remaining)
		if block == nil {
			return nil, fmt.Errorf("%w: certificate bundle contains invalid PEM", ErrUntrustedChain)
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("%w: unexpected PEM block %q", ErrUntrustedChain, block.Type)
		}

		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%w: parsing certificate: %v", ErrUntrustedChain, err)
		}
		certificates = append(certificates, certificate)
		remaining = bytes.TrimSpace(rest)
	}

	if len(certificates) == 0 {
		return nil, fmt.Errorf("%w: certificate bundle is empty", ErrUntrustedChain)
	}

	intermediates := x509.NewCertPool()
	for _, certificate := range certificates[1:] {
		intermediates.AddCert(certificate)
	}
	return &parsedChain{leaf: certificates[0], intermediates: intermediates}, nil
}

// validateChain verifies the fetched leaf at now against either the injected
// roots or the host's system roots, using the rest of the bundle as
// intermediates. Revocation is deliberately not checked: neither pinned Alexa
// SDK checks it, and the package documents that omission as an explicit scope
// decision rather than an oversight.
func (v *Verifier) validateChain(chain *parsedChain, now time.Time) error {
	roots := v.Roots
	if roots == nil {
		var err error
		roots, err = x509.SystemCertPool()
		if err != nil {
			return fmt.Errorf("%w: loading system roots: %v", ErrUntrustedChain, err)
		}
	}

	_, err := chain.leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: chain.intermediates,
		CurrentTime:   now,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUntrustedChain, err)
	}
	return nil
}

// verifyCertificateHostname applies the Java SDK's SAN rule using Go's X.509
// primitive. VerifyHostname consults DNS SANs and therefore cannot accept an
// unrelated SAN type whose value happens to contain the expected text.
func verifyCertificateHostname(leaf *x509.Certificate) error {
	if err := leaf.VerifyHostname(echoAPIDomain); err != nil {
		return fmt.Errorf("%w: %v", ErrCertHostname, err)
	}
	return nil
}
