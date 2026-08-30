package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// R7: /healthz is a liveness probe — 200, no auth, and nothing sensitive in
// the body. It must not leak the version, the hostname, the tailnet name or
// any filesystem path.
func TestHealthzReturns200WithNonSensitiveBody(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)

	newRouter(testLogger()).ServeHTTP(rec, req)

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

	newRouter(testLogger()).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /not-a-route status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}
