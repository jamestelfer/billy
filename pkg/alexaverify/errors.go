package alexaverify

import "errors"

// Every error returned by [Verifier.Verify] wraps exactly one of these
// sentinels, so a caller can classify a failure with a single errors.Is chain
// and log it distinctly. They are part of the package's public contract:
// removing one, or changing which condition maps to which, is a breaking
// change.
//
// Callers must return the same HTTP status for all of them. The distinction
// exists for the operator reading logs, not for the caller on the wire —
// telling a prospective attacker which of the checks they failed is free
// intelligence.
var (
	// ErrMissingHeader reports that a required verification header —
	// Signature-256 or SignatureCertChainUrl — was absent or empty.
	ErrMissingHeader = errors.New("alexaverify: missing verification header")

	// ErrCertURLInvalid reports that the certificate chain URL failed scheme,
	// host, path prefix or port validation.
	ErrCertURLInvalid = errors.New("alexaverify: invalid certificate chain URL")

	// ErrCertURLUserinfo reports that the certificate chain URL carried
	// userinfo. Both reference SDKs accept this; we do not. See the residual
	// risk register in the implementation plan: Amazon emits a fixed literal
	// URL with no credentials, and accepting one would have Go attach a
	// pointless Authorization header to the S3 fetch.
	ErrCertURLUserinfo = errors.New("alexaverify: certificate chain URL contains userinfo")

	// ErrCertURLTraversal reports that the certificate chain URL's normalized
	// path escaped the required prefix, including via percent-encoded dot
	// segments. Both reference SDKs accept the percent-encoded form; we do
	// not, for the same reason as ErrCertURLUserinfo.
	ErrCertURLTraversal = errors.New("alexaverify: certificate chain URL escapes the permitted path")

	// ErrCertFetch reports that the certificate chain could not be retrieved:
	// a transport failure, a non-200 response, or a body over the size cap.
	ErrCertFetch = errors.New("alexaverify: fetching the certificate chain")

	// ErrUntrustedChain reports that the certificate chain did not parse, or
	// did not validate to a trusted root at the current time.
	ErrUntrustedChain = errors.New("alexaverify: certificate chain is not trusted")

	// ErrCertHostname reports that the leaf certificate does not carry the
	// required DNS subject alternative name.
	ErrCertHostname = errors.New("alexaverify: certificate is not valid for the Alexa API domain")

	// ErrUnsupportedKey reports that the leaf certificate's public key is not
	// RSA, and so cannot have produced an RSA PKCS#1 v1.5 signature.
	ErrUnsupportedKey = errors.New("alexaverify: unsupported certificate public key type")

	// ErrBadSignature reports that the signature header was not valid base64,
	// or did not verify over the supplied body bytes.
	ErrBadSignature = errors.New("alexaverify: signature verification failed")

	// ErrStaleTimestamp reports that the request's freshness could not be
	// established: either the envelope carried no usable timestamp, or the
	// timestamp differs from now by more than the tolerance in either
	// direction. A future-dated request fails this check, which the Node
	// adapter does not catch.
	ErrStaleTimestamp = errors.New("alexaverify: request timestamp outside tolerance")
)
