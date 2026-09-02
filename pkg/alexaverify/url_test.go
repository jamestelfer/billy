package alexaverify

import (
	"errors"
	"testing"
)

// The table below is ported from
// SkillRequestSignatureVerifierTest.java (alexa/alexa-skills-kit-sdk-for-java
// @ e7f16b0, Apache-2.0) and tst/verifier/index.spec.ts
// (alexa/alexa-skills-kit-sdk-for-nodejs @ ea88cef, Apache-2.0), plus the
// extras the "Host a Custom Skill as a Web Service" documentation mandates and
// the two cases where this package is deliberately stricter than both.
//
// Every row states the expected canonical output, not merely "accepted".
// Normalization is the check here rather than a tidy-up step, so a row that
// accepts the wrong canonical form is as much a failure as one that accepts a
// URL it should reject.
func TestNormalizeCertChainURL(t *testing.T) {
	t.Parallel()

	const canonical = "https://s3.amazonaws.com/echo.api/cert"

	tests := []struct {
		name    string
		in      string
		want    string // expected canonical form; empty when a rejection is expected
		wantErr error  // sentinel the failure must wrap
		why     string
	}{
		// --- accepted -------------------------------------------------------
		{name: "plain", in: canonical, want: canonical,
			why: "the literal form Amazon actually emits"},
		{name: "explicit default port", in: "https://s3.amazonaws.com:443/echo.api/cert", want: canonical,
			why: "443 is the scheme default and is dropped from the canonical form"},
		{name: "dot segment that returns to the prefix", in: "https://s3.amazonaws.com/echo.api/../echo.api/cert", want: canonical,
			why: "URI.normalize() collapses it to the plain form; so must we"},
		{name: "uppercase host", in: "https://S3.AMAZONAWS.COM/echo.api/cert", want: canonical,
			why: "Java compares equalsIgnoreCase; the canonical form case-folds"},
		{name: "uppercase scheme", in: "HTTPS://s3.amazonaws.com/echo.api/cert", want: canonical,
			why: "scheme comparison is case-insensitive per RFC 3986"},
		{name: "duplicate leading slash", in: "https://s3.amazonaws.com//echo.api/cert", want: canonical,
			why: "URI.normalize() collapses duplicate slashes, verified on JDK 21"},
		{name: "duplicate internal slash", in: "https://s3.amazonaws.com/echo.api//cert", want: canonical,
			why: "same collapse, mid-path"},
		{name: "fragment", in: "https://s3.amazonaws.com/echo.api/cert#x", want: canonical,
			why: "both SDKs strip the fragment and neither puts it on the wire"},
		{name: "trailing dot segment", in: "https://s3.amazonaws.com/echo.api/./cert", want: canonical,
			why: "a single dot segment is a no-op once normalized"},
		{name: "real world cert name", in: "https://s3.amazonaws.com/echo.api/echo-api-cert-7.pem",
			want: "https://s3.amazonaws.com/echo.api/echo-api-cert-7.pem",
			why:  "the shape of a genuine SignatureCertChainUrl"},
		{name: "query string is preserved", in: "https://s3.amazonaws.com/echo.api/cert?v=1",
			want: "https://s3.amazonaws.com/echo.api/cert?v=1",
			why:  "both SDKs fetch with the query; dropping it would diverge from both"},

		// --- rejected: path -------------------------------------------------
		{name: "escapes the prefix", in: "https://s3.amazonaws.com/echo.api/../cert", wantErr: ErrCertURLTraversal,
			why: "normalizes to /cert; the case Node's un-normalized prefix check misses"},
		{name: "percent encoded traversal", in: "https://s3.amazonaws.com/echo.api/%2e%2e/cert", wantErr: ErrCertURLTraversal,
			why: "risk register: both SDKs accept and fetch this verbatim, we do not"},
		{name: "percent encoded traversal uppercase", in: "https://s3.amazonaws.com/echo.api/%2E%2E/cert", wantErr: ErrCertURLTraversal,
			why: "percent-encoding is case-insensitive, so the uppercase form must not slip past"},
		{name: "prefix is case sensitive", in: "https://s3.amazonaws.com/ECHO.API/cert", wantErr: ErrCertURLInvalid,
			why: "both SDKs compare the path prefix case-sensitively"},
		{name: "no prefix", in: "https://s3.amazonaws.com/cert", wantErr: ErrCertURLInvalid,
			why: "outside /echo.api/ entirely"},
		{name: "prefix as a substring, not a prefix", in: "https://s3.amazonaws.com/not/echo.api/cert", wantErr: ErrCertURLInvalid,
			why: "startsWith, not contains"},
		{name: "empty path", in: "https://s3.amazonaws.com", wantErr: ErrCertURLInvalid,
			why: "no path at all cannot start with the prefix"},
		{name: "prefix with no object", in: "https://s3.amazonaws.com/echo.api/", wantErr: ErrCertURLInvalid,
			why: "a bare directory URL names no certificate; both SDKs accept it and fail at the fetch instead"},
		{name: "prefix with a dot segment leaving no object", in: "https://s3.amazonaws.com/echo.api/x/..", wantErr: ErrCertURLTraversal,
			why: "the dot segment is what makes this a traversal rather than a plain bad path"},

		// --- rejected: scheme, host, port -----------------------------------
		{name: "http", in: "http://s3.amazonaws.com/echo.api/cert", wantErr: ErrCertURLInvalid,
			why: "protocol must be https"},
		{name: "file scheme", in: "file:///echo.api/cert", wantErr: ErrCertURLInvalid,
			why: "a non-http scheme must not reach the fetcher"},
		{name: "wrong host", in: "https://very.bad/echo.api/cert", wantErr: ErrCertURLInvalid,
			why: "the host is the whole point of the check"},
		{name: "host as a subdomain of the valid one", in: "https://evil.s3.amazonaws.com/echo.api/cert", wantErr: ErrCertURLInvalid,
			why: "equality, not suffix matching"},
		{name: "valid host as a subdomain of an attacker domain", in: "https://s3.amazonaws.com.evil.test/echo.api/cert", wantErr: ErrCertURLInvalid,
			why: "equality, not prefix matching"},
		{name: "non default port", in: "https://s3.amazonaws.com:563/echo.api/cert", wantErr: ErrCertURLInvalid,
			why: "Java rejects any explicit non-default port"},
		{name: "port zero", in: "https://s3.amazonaws.com:0/echo.api/cert", wantErr: ErrCertURLInvalid,
			why: "the case Node's Number(port) truthiness check misses"},

		// --- rejected: malformed and userinfo -------------------------------
		{name: "not a url", in: "badUrl", wantErr: ErrCertURLInvalid,
			why: "url.Parse accepts this as a relative reference; requiring a scheme rejects it"},
		{name: "empty", in: "", wantErr: ErrCertURLInvalid,
			why: "an empty header value must not be treated as a relative path"},
		{name: "scheme relative", in: "//s3.amazonaws.com/echo.api/cert", wantErr: ErrCertURLInvalid,
			why: "no scheme means no guarantee of https"},
		{name: "control character", in: "https://s3.amazonaws.com/echo.api/ce\x7frt", wantErr: ErrCertURLInvalid,
			why: "url.Parse rejects control characters outright"},
		{name: "userinfo", in: "https://user:pw@s3.amazonaws.com/echo.api/cert", wantErr: ErrCertURLUserinfo,
			why: "risk register: both SDKs accept, we do not"},
		{name: "username only", in: "https://user@s3.amazonaws.com/echo.api/cert", wantErr: ErrCertURLUserinfo,
			why: "userinfo without a password is still userinfo"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := normalizeCertChainURL(tc.in)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("normalizeCertChainURL(%q) error = %v, want %v\n  because: %s",
						tc.in, err, tc.wantErr, tc.why)
				}
				if got != "" {
					t.Errorf("normalizeCertChainURL(%q) returned %q alongside an error; "+
						"a rejected URL must yield nothing a caller could fetch", tc.in, got)
				}
				return
			}

			if err != nil {
				t.Fatalf("normalizeCertChainURL(%q) error = %v, want it accepted\n  because: %s",
					tc.in, err, tc.why)
			}
			if got != tc.want {
				t.Errorf("normalizeCertChainURL(%q) = %q, want %q\n  because: %s",
					tc.in, got, tc.want, tc.why)
			}
		})
	}
}

// Normalization must be idempotent, or the cache key is not stable: two
// requests carrying different spellings of the same URL have to collapse to
// one entry, and re-normalizing the canonical form must not move it again.
func TestNormalizeCertChainURLIsIdempotent(t *testing.T) {
	t.Parallel()

	spellings := []string{
		"https://s3.amazonaws.com/echo.api/cert",
		"https://s3.amazonaws.com:443/echo.api/cert",
		"https://S3.AMAZONAWS.COM//echo.api/cert",
		"https://s3.amazonaws.com/echo.api/../echo.api/cert",
		"https://s3.amazonaws.com/echo.api/./cert#fragment",
	}

	for _, in := range spellings {
		first, err := normalizeCertChainURL(in)
		if err != nil {
			t.Fatalf("normalizeCertChainURL(%q) error = %v", in, err)
		}
		second, err := normalizeCertChainURL(first)
		if err != nil {
			t.Fatalf("re-normalizing %q error = %v", first, err)
		}
		if first != second {
			t.Errorf("normalizeCertChainURL is not idempotent for %q: %q then %q", in, first, second)
		}
	}
}

// The two risk-register rejections must stay distinguishable from the generic
// invalid-URL failure. If a refactor ever collapsed them, a future breakage
// caused by our own strictness would be indistinguishable from Amazon sending
// a plainly bad URL — which is the entire reason they have their own
// sentinels.
func TestRiskRegisterErrorsAreDistinctFromTheGenericOne(t *testing.T) {
	t.Parallel()

	cases := map[string]error{
		"https://user:pw@s3.amazonaws.com/echo.api/cert": ErrCertURLUserinfo,
		"https://s3.amazonaws.com/echo.api/%2e%2e/cert":  ErrCertURLTraversal,
	}

	for in, want := range cases {
		_, err := normalizeCertChainURL(in)
		if !errors.Is(err, want) {
			t.Fatalf("normalizeCertChainURL(%q) error = %v, want %v", in, err, want)
		}
		if errors.Is(err, ErrCertURLInvalid) {
			t.Errorf("normalizeCertChainURL(%q) also wraps the generic ErrCertURLInvalid, "+
				"which would hide the divergence in a log search", in)
		}
	}
}
