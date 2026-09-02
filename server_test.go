package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// Structural: never ship a bare http.Server. A public listener with no header
// or idle timeout is a slowloris target.
func TestNewHTTPServerSetsTimeouts(t *testing.T) {
	srv := newHTTPServer(newRouter(testLogger(), mustCaptureStore(t), mustVerifier(t)))

	if srv.ReadHeaderTimeout == 0 {
		t.Error("ReadHeaderTimeout is unset")
	}
	if srv.IdleTimeout == 0 {
		t.Error("IdleTimeout is unset")
	}
}

// Integration: the real serve loop, over a real listener, answering a real
// request — and returning cleanly when its context is cancelled. tsnet
// supplies the listener in production; here a loopback listener stands in, so
// the wiring is exercised without a tailnet.
func TestServeAnswersHealthzAndShutsDownOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	served := make(chan error, 1)
	go func() {
		served <- serve(ctx, ln, newRouter(testLogger(), mustCaptureStore(t), mustVerifier(t)), testLogger())
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+ln.Addr().String()+"/healthz", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /healthz error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /healthz status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	cancel()
	select {
	case err := <-served:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("serve() error = %v, want nil or ErrServerClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve() did not return within 5s of context cancellation")
	}
}
