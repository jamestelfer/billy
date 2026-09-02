// Package alexaverify verifies that an inbound HTTP request really was sent
// by Alexa.
//
// Amazon signs every request it delivers to a custom skill hosted as a web
// service. The signature covers the exact bytes of the request body, and the
// public key needed to check it is published as a PEM certificate chain at a
// URL carried in the SignatureCertChainUrl header. This package implements the
// checks Amazon's own SDKs perform, in the order that is cheapest to defend:
//
//  1. the request timestamp is fresh, and
//  2. the Signature-256 header is a valid RSA PKCS#1 v1.5 SHA-256 signature
//     over the body, made by a certificate that chains to a trusted root and
//     is issued for echo-api.amazon.com.
//
// The timestamp is checked first. It is a local, free computation, whereas the
// signature check may make an outbound HTTPS request to a URL supplied by the
// caller. Checking freshness first stops an unauthenticated caller driving
// egress. Both reference SDKs order these differently; this is a deliberate
// divergence.
//
// # Byte fidelity
//
// Verify takes the request body as a []byte and checks the signature over
// exactly those bytes. Callers must read the body once, verify that buffer,
// and then decode the request envelope from the same buffer. Decoding and
// re-encoding the JSON before verification — even through a middleware that
// means well — produces different bytes and every signature will fail.
//
// # There is no way to disable verification
//
// This package provides no switch, environment variable, build tag or option
// that turns verification off. The Java SDK has such a system property; it is
// not ported. Tests get the three seams below instead — the trusted root pool,
// the clock and the HTTP client.
//
// # Certificate revocation is not checked
//
// This is deliberate, not an oversight. Neither reference SDK checks
// revocation either (the Node adapter carries an explicit TODO), so an
// implementation that did would be rejecting requests that every other Alexa
// skill on the internet accepts. Chain validity is bounded instead by the
// NotBefore and NotAfter dates on the leaf, which are re-checked on every
// verification including cache hits.
package alexaverify
