package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jamestelfer/billy/pkg/alexaverify"
)

// R7: /healthz is a liveness probe — 200, no auth, and nothing sensitive in
// the body. It must not leak the version, the hostname, the tailnet name or
// any filesystem path.
func TestHealthzReturns200WithNonSensitiveBody(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)

	newRouter(testLogger(), mustCaptureStore(t), mustVerifier(t)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	if strings.TrimSpace(body) == "" {
		t.Error("GET /healthz returned an empty body; want a fixed non-empty one")
	}
	for _, secret := range []string{buildVersion(), "ts.net", "/", "\\"} {
		if strings.Contains(body, secret) {
			t.Errorf("GET /healthz body %q leaks %q", body, secret)
		}
	}
}

func TestUnknownPathReturns404(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/not-a-route", nil)

	newRouter(testLogger(), mustCaptureStore(t), mustVerifier(t)).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /not-a-route status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func mustCaptureStore(t *testing.T) *captureStore {
	t.Helper()
	store, err := newCaptureStore(t.TempDir())
	if err != nil {
		t.Fatalf("newCaptureStore() error = %v", err)
	}
	return store
}

// mustVerifier builds a verifier with production defaults for tests that care
// about routing rather than verification.
func mustVerifier(t *testing.T) *alexaverify.Verifier {
	t.Helper()
	verifier, err := alexaverify.New(alexaverify.WithLogger(testLogger()))
	if err != nil {
		t.Fatalf("alexaverify.New() error = %v", err)
	}
	return verifier
}
