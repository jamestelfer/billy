package alexaverify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"testing"
	"time"
)

// fixedNow is the clock every test pins to, so nothing here depends on when it
// runs.
var fixedNow = time.Date(2025, time.March, 1, 12, 0, 0, 0, time.UTC)

func at(t time.Time) func() time.Time { return func() time.Time { return t } }

// envelope builds a minimal Alexa request envelope with the given type and
// timestamp. The bytes it returns are what gets verified, exactly as they are.
func envelope(requestType string, ts time.Time) []byte {
	return fmt.Appendf(nil,
		`{"version":"1.0","request":{"type":%q,"requestId":"amzn1.echo-api.request.test","timestamp":%q,"locale":"en-AU"}}`,
		requestType, ts.UTC().Format(time.RFC3339))
}

// signedHeaders returns a header set carrying both required headers, so a test
// exercising the timestamp gate is not stopped short by ErrMissingHeader.
func signedHeaders() http.Header {
	h := http.Header{}
	h.Set("Signature-256", "not-checked-in-this-test")
	h.Set("SignatureCertChainUrl", "https://s3.amazonaws.com/echo.api/echo-api-cert.pem")
	return h
}

func TestVerifyRequiresBothHeaders(t *testing.T) {
	t.Parallel()

	body := envelope("LaunchRequest", fixedNow)

	tests := map[string]func() http.Header{
		"no headers at all": func() http.Header { return http.Header{} },
		"signature only": func() http.Header {
			h := signedHeaders()
			h.Del("SignatureCertChainUrl")
			return h
		},
		"cert chain url only": func() http.Header {
			h := signedHeaders()
			h.Del("Signature-256")
			return h
		},
		"signature present but empty": func() http.Header {
			h := signedHeaders()
			h.Set("Signature-256", "")
			return h
		},
	}

	for name, headers := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			v, err := New(WithClock(at(fixedNow)))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			err = v.Verify(t.Context(), body, headers())
			if !errors.Is(err, ErrMissingHeader) {
				t.Fatalf("got %v, want ErrMissingHeader", err)
			}
		})
	}
}

// The header lookup must be case-insensitive. Go's http.Header.Get gives this
// for free, but the Node adapter implements it by hand, so the behaviour is
// clearly load-bearing and deserves a test that would notice a regression to a
// raw map lookup.
func TestVerifyHeaderLookupIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("signature-256", "not-checked-in-this-test")
	h.Set("signaturecertchainurl", "https://s3.amazonaws.com/echo.api/echo-api-cert.pem")

	v, err := New(WithClock(at(fixedNow)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = v.Verify(t.Context(), envelope("LaunchRequest", fixedNow), h)
	if errors.Is(err, ErrMissingHeader) {
		t.Fatalf("headers were not found case-insensitively: %v", err)
	}
}

func TestVerifyTimestampGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		requestType string
		offset      time.Duration // request timestamp relative to now
		wantStale   bool
	}{
		{"fresh", "LaunchRequest", 0, false},
		{"just inside tolerance in the past", "IntentRequest", -150 * time.Second, false},
		{"just outside tolerance in the past", "IntentRequest", -151 * time.Second, true},
		{"ten minutes in the past", "IntentRequest", -10 * time.Minute, true},
		// The future cases are the ones the Node adapter misses entirely.
		{"just inside tolerance in the future", "IntentRequest", 150 * time.Second, false},
		{"just outside tolerance in the future", "IntentRequest", 151 * time.Second, true},
		{"ten minutes in the future", "IntentRequest", 10 * time.Minute, true},
		// Skill events are delivered late by design and get an hour.
		{"skill event at 59 minutes", "AlexaSkillEvent.SkillDisabled", -59 * time.Minute, false},
		{"skill event at exactly one hour", "AlexaSkillEvent.SkillDisabled", -time.Hour, false},
		{"skill event at one hour and a second", "AlexaSkillEvent.SkillDisabled", -time.Hour - time.Second, true},
		{"skill event in the future beyond an hour", "AlexaSkillEvent.SkillEnabled", time.Hour + time.Second, true},
		// A type that merely looks like a skill event gets the standard
		// window: the set is explicit, not a prefix match.
		{"unlisted AlexaSkillEvent type gets the standard window", "AlexaSkillEvent.NotAThing", -10 * time.Minute, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v, err := New(WithClock(at(fixedNow)))
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			body := envelope(tc.requestType, fixedNow.Add(tc.offset))
			err = v.Verify(t.Context(), body, signedHeaders())

			switch {
			case tc.wantStale && !errors.Is(err, ErrStaleTimestamp):
				t.Fatalf("got %v, want ErrStaleTimestamp", err)
			case !tc.wantStale && errors.Is(err, ErrStaleTimestamp):
				t.Fatalf("passed the timestamp gate expected, got %v", err)
			}

			// Anything that clears the timestamp gate must still be refused:
			// the gate fails closed while the signature step is unbuilt.
			if !tc.wantStale && !errors.Is(err, errNotImplemented) {
				t.Fatalf("got %v, want the gate to fail closed at the signature step", err)
			}
		})
	}
}

// A body that cannot yield a timestamp cannot be proved fresh, so it is
// rejected rather than waved through.
func TestVerifyUndecodableTimestampIsStale(t *testing.T) {
	t.Parallel()

	tests := map[string][]byte{
		"not json":          []byte("this is not json"),
		"empty":             nil,
		"no request member": []byte(`{"version":"1.0"}`),
		"no timestamp":      []byte(`{"request":{"type":"LaunchRequest"}}`),
		"unparseable timestamp": []byte(
			`{"request":{"type":"LaunchRequest","timestamp":"yesterday afternoon"}}`),
		"truncated mid-envelope": envelope("LaunchRequest", fixedNow)[:40],
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			v, err := New(WithClock(at(fixedNow)))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := v.Verify(t.Context(), body, signedHeaders()); !errors.Is(err, ErrStaleTimestamp) {
				t.Fatalf("got %v, want ErrStaleTimestamp", err)
			}
		})
	}
}

func TestNewRejectsNegativeTolerance(t *testing.T) {
	t.Parallel()

	if _, err := New(WithTolerance(-time.Second)); err == nil {
		t.Fatal("New accepted a negative tolerance")
	}
}

func TestNewClampsExcessiveTolerance(t *testing.T) {
	t.Parallel()

	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))

	v, err := New(WithTolerance(300*time.Second), WithLogger(log))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if v.Tolerance != maxTolerance {
		t.Fatalf("tolerance = %s, want %s", v.Tolerance, maxTolerance)
	}
	if !bytes.Contains(logged.Bytes(), []byte("clamping")) {
		t.Fatalf("no warning was logged; log was %q", logged.String())
	}

	// The clamp has to bite in practice, not just in the field: a request 200
	// seconds old must still be rejected.
	body := envelope("IntentRequest", fixedNow.Add(-200*time.Second))
	v.Now = at(fixedNow)
	if err := v.Verify(t.Context(), body, signedHeaders()); !errors.Is(err, ErrStaleTimestamp) {
		t.Fatalf("got %v, want ErrStaleTimestamp", err)
	}
}

func TestNewDefaultsTolerance(t *testing.T) {
	t.Parallel()

	v, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if v.Tolerance != defaultTolerance {
		t.Fatalf("tolerance = %s, want %s", v.Tolerance, defaultTolerance)
	}
}

// A directly constructed Verifier must not be more permissive than one built
// by New: the exported fields are a documented surface, and a zero Tolerance
// there means "the default", not "unbounded".
func TestZeroValueVerifierIsNotPermissive(t *testing.T) {
	t.Parallel()

	v := &Verifier{Now: at(fixedNow)}
	body := envelope("IntentRequest", fixedNow.Add(-time.Hour))
	if err := v.Verify(t.Context(), body, signedHeaders()); !errors.Is(err, ErrStaleTimestamp) {
		t.Fatalf("got %v, want ErrStaleTimestamp", err)
	}
}

// The signature step is not built yet, so nothing may be admitted. This test
// is the explicit statement of "fails closed", and it is expected to be
// rewritten — not deleted — when the signature work lands.
func TestSignatureStepFailsClosed(t *testing.T) {
	t.Parallel()

	v, err := New(WithClock(at(fixedNow)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := v.Verify(t.Context(), envelope("LaunchRequest", fixedNow), signedHeaders()); err == nil {
		t.Fatal("Verify admitted a request with no signature check implemented")
	}
}

func TestWarmIsCallableAndFailsClosed(t *testing.T) {
	t.Parallel()

	v, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Warm is part of the public surface from the outset so embedders can rely
	// on it; it must not pretend to have succeeded before it does anything.
	if err := v.Warm(context.Background(), "https://s3.amazonaws.com/echo.api/echo-api-cert.pem"); err == nil {
		t.Fatal("Warm reported success without populating anything")
	}
}
