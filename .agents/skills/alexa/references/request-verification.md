# HTTPS request verification

## Preserve the wire bytes

The signature covers the exact request-body bytes, not semantically equivalent JSON. Read the
body once into a bounded slice, verify that slice, and decode or capture only that same slice.
A truncated or partially read body fails closed — never verify a prefix.

Order the checks so cheap rejections happen first, and so stale unauthenticated traffic cannot
trigger outbound certificate fetches:

1. Read the bounded body completely.
2. Check timestamp freshness.
3. Validate the certificate URL, then fetch the chain.
4. Validate the chain and certificate hostname.
5. Verify the signature.
6. Only then capture and dispatch.

## Headers and cryptography

Alexa supplies `Signature-256` and `SignatureCertChainUrl`. The modern path is RSA PKCS#1 v1.5
with SHA-256. Missing headers, malformed certificate URLs, untrusted chains, wrong certificate
hostnames, unsupported key types, and bad signatures all reject before any skill behavior runs.

## Certificate URL policy

Caller input selects an outbound fetch here, so the URL check is a security boundary:

- Require HTTPS.
- Require host `s3.amazonaws.com`, case-insensitively.
- Allow no explicit port other than `443`.
- Normalize the decoded path before checking it.
- Require the normalized path to begin with `/echo.api/`, case-sensitively.
- Strip fragments and reject userinfo.
- Reject encoded or ordinary traversal escaping the allowed prefix.

Validate the leaf through supplied intermediates to system roots, then require DNS SAN
`echo-api.amazon.com`. Re-check validity dates on every cache hit, not just on first fetch —
a cached chain expires in place. Bound fetches by timeout, retry count, and response size.

## Freshness

Default tolerance is 150 seconds in both directions. `AlexaSkillEvent.*` events get a separate
one-hour allowance; that allowance does not extend to `AudioPlayer.*` events.

Keep verification unconditional. Injecting roots, clock, and HTTP client gives testability
without a production bypass.

## Rejection and logging

Return one indistinguishable client response for every verification failure while logging
stable internal categories. Telling a caller "timestamp valid but signature bad" hands them an
oracle.

Never log request bodies, `Signature-256` values, `apiAccessToken`, user IDs, or device IDs.
Real captured envelopes contain live bearer tokens and identifiers, and editing a capture to
sanitize it invalidates its signature — so committed tests need generated certificates and
synthetic signed envelopes.
