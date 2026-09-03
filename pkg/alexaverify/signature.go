package alexaverify

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
)

// verifyBodySignature checks the Signature-256 value as RSA PKCS#1 v1.5 over
// SHA-256, which is the Go equivalent of Java's SHA256withRSA and Node's
// RSA-SHA256 verifier.
func verifyBodySignature(body []byte, encodedSignature string, leaf *x509.Certificate) error {
	publicKey, ok := leaf.PublicKey.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("%w: got %T", ErrUnsupportedKey, leaf.PublicKey)
	}

	signature, err := base64.StdEncoding.DecodeString(encodedSignature)
	if err != nil {
		return fmt.Errorf("%w: decoding base64: %v", ErrBadSignature, err)
	}

	digest := sha256.Sum256(body)
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSignature, err)
	}
	return nil
}
