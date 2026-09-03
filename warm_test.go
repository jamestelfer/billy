package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jamestelfer/billy/pkg/alexaverify"
)

func TestCertificateCacheWarmFailureIsLoggedAndNonFatal(t *testing.T) {
	var calls atomic.Int64
	client := &http.Client{Transport: handlerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("egress unavailable")
	})}
	verifier, err := alexaverify.New(
		alexaverify.WithClock(func() time.Time { return testNow }),
		alexaverify.WithHTTPClient(client),
	)
	if err != nil {
		t.Fatalf("alexaverify.New: %v", err)
	}
	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, nil))

	done := startCertificateCacheWarm(
		t.Context(), verifier,
		"https://s3.amazonaws.com/echo.api/unreachable.pem", log,
	)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("failed warm did not finish within the fetch bound")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("warm attempts = %d, want 2", got)
	}
	if !strings.Contains(logged.String(), "continuing") {
		t.Fatalf("warm failure was not logged as non-fatal; log was:\n%s", logged.String())
	}
}

func TestHostileCertificateWarmSeedNeverFetches(t *testing.T) {
	var calls atomic.Int64
	client := &http.Client{Transport: handlerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("unexpected fetch")
	})}
	verifier, err := alexaverify.New(
		alexaverify.WithClock(func() time.Time { return testNow }),
		alexaverify.WithHTTPClient(client),
	)
	if err != nil {
		t.Fatalf("alexaverify.New: %v", err)
	}

	done := startCertificateCacheWarm(
		t.Context(), verifier, "https://very.bad/echo.api/cert", testLogger(),
	)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("hostile seed warm did not finish")
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("fetches = %d, want zero for hostile seed", got)
	}
}

func TestHealthzDoesNotWaitForCertificateWarm(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	started := make(chan struct{})
	var once sync.Once
	client := &http.Client{Transport: handlerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		once.Do(func() { close(started) })
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	verifier, err := alexaverify.New(
		alexaverify.WithClock(func() time.Time { return testNow }),
		alexaverify.WithHTTPClient(client),
	)
	if err != nil {
		t.Fatalf("alexaverify.New: %v", err)
	}

	done := startCertificateCacheWarm(
		ctx, verifier, "https://s3.amazonaws.com/echo.api/slow.pem", testLogger(),
	)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("warm fetch did not start")
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	handleHealthz(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want %d while warm is blocked",
			recorder.Code, http.StatusOK)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("warm did not stop after context cancellation")
	}
}
