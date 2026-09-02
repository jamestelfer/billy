package alexaverify

import (
	"fmt"
	"net/url"
	"path"
	"slices"
	"strings"
)

// normalizeCertChainURL validates the SignatureCertChainUrl header value and
// returns the canonical form to fetch, which is also the cache key.
//
// Ported from the checks in:
//
//	Java: SkillRequestSignatureVerifier.getAndVerifySigningCertificateChainUrl
//	Node: SkillRequestSignatureVerifier.validateCertUrl
//
// The order is normalize, then validate. Java does this via URI.normalize()
// before touching the path; Node does not normalize at all and passes its
// prefix check by accident on some inputs. Prefix-checking a raw path is the
// bug that lets /echo.api/../cert through, so normalization is not an
// optimisation here — it is the check.
//
// This is a pure function. It performs no I/O, which is the point: a URL that
// fails here must never cause an outbound connection.
func normalizeCertChainURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %q did not parse: %w", ErrCertURLInvalid, raw, err)
	}

	// url.Parse is famously accommodating: "badUrl" parses successfully as a
	// relative reference with an empty scheme and host. Requiring both to be
	// present is what turns that into a rejection.
	if !strings.EqualFold(u.Scheme, validCertScheme) {
		return "", fmt.Errorf("%w: %q has scheme %q, want %q",
			ErrCertURLInvalid, raw, u.Scheme, validCertScheme)
	}

	// Outside the intersection of the two SDKs: both inspect only the host and
	// silently accept credentials. Amazon emits a fixed literal URL that has
	// never carried any, and Go's http client would turn them into an
	// Authorization header on a request to S3. Its own sentinel, so a future
	// breakage here is diagnosable in one grep.
	if u.User != nil {
		return "", fmt.Errorf("%w: %q", ErrCertURLUserinfo, raw)
	}

	if err := validateCertChainHostPort(raw, u); err != nil {
		return "", err
	}

	// u.Path is already percent-decoded, so path.Clean sees %2e%2e as "..".
	// That is how the percent-encoded traversal both SDKs miss gets caught:
	// there is nothing clever here, it falls out of Go decoding first.
	//
	// path.Clean, not filepath.Clean: this is a URL path, and filepath.Clean
	// would rewrite the separators to backslashes on Windows.
	cleaned := path.Clean(u.Path)
	if !strings.HasPrefix(cleaned, "/") {
		// path.Clean("") is ".", and a relative result can never satisfy the
		// prefix check. Normalising it to "/" keeps the error message honest.
		cleaned = "/"
	}

	if !strings.HasPrefix(cleaned, validCertPathPrefix) {
		return "", certPathError(raw, u.Path, cleaned)
	}

	// The fragment is dropped, matching both SDKs: Java's URL.getPath() omits
	// it and Node's url.parse().pathname does too, so neither ever puts one on
	// the wire. The query is kept, because both SDKs do fetch with it; Amazon
	// never sends one, and a caller who adds one can only ever steer the fetch
	// to another object under s3.amazonaws.com/echo.api/, which varying the
	// path already allows. Rebuilding from the parsed parts rather than editing
	// the raw string is what makes the canonical form stable enough to key a
	// cache on.
	canonical := url.URL{
		Scheme:   validCertScheme,
		Host:     validCertHost,
		Path:     cleaned,
		RawQuery: u.RawQuery,
	}
	return canonical.String(), nil
}

// validateCertChainHostPort checks the host and, if one is given explicitly,
// the port.
func validateCertChainHostPort(raw string, u *url.URL) error {
	// u.Hostname() strips the port and the brackets around an IPv6 literal.
	// Java compares with equalsIgnoreCase and Node relies on url.parse having
	// already lowercased; case-folding explicitly matches both.
	if !strings.EqualFold(u.Hostname(), validCertHost) {
		return fmt.Errorf("%w: %q has host %q, want %q",
			ErrCertURLInvalid, raw, u.Hostname(), validCertHost)
	}

	// An absent port is fine — it means 443 for https. Any port that is
	// present must be 443. Java compares against the scheme's default port;
	// Node uses Number(port) as a truthiness test, so ":0" slips through
	// there. Java already rejects ":0", so rejecting it costs no
	// compatibility.
	if port := u.Port(); port != "" && port != validCertPort {
		return fmt.Errorf("%w: %q has port %q, want %q or none",
			ErrCertURLInvalid, raw, port, validCertPort)
	}
	return nil
}

// certPathError decides which sentinel a failed path prefix check deserves.
//
// A path carrying a ".." segment, however it was spelled on the wire, is a
// traversal attempt and gets its own sentinel and log line: that rejection is
// one of the two places this package is stricter than both reference SDKs, and
// it has to be findable in one grep. Anything else — /cert, /ECHO.API/cert, or
// a bare /echo.api/ with no object — is simply not a URL Amazon emits, and
// gets the generic error.
//
// A bare /echo.api/ is worth a note: both SDKs accept it and then fail at the
// fetch, whereas this rejects it at validation. The request is refused either
// way, so no caller either SDK would admit is turned away, and it is not a
// residual-risk entry.
func certPathError(raw, original, cleaned string) error {
	if slices.Contains(strings.Split(original, "/"), "..") {
		return fmt.Errorf("%w: %q normalizes to path %q, which escapes %q",
			ErrCertURLTraversal, raw, cleaned, validCertPathPrefix)
	}
	return fmt.Errorf("%w: %q has path %q, want a path under %q",
		ErrCertURLInvalid, raw, cleaned, validCertPathPrefix)
}
