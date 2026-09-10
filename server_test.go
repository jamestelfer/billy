package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Structural: never ship a bare http.Server. A public listener with no header
// or idle timeout is a slowloris target.
func TestNewHTTPServerSetsTimeouts(t *testing.T) {
	srv := newHTTPServer(newRouter(testLogger(), mustCaptureStore(t), mustVerifier(t)))

	assert.NotEqual(t, 0, srv.ReadHeaderTimeout, "ReadHeaderTimeout is unset")
	assert.NotEqual(t, 0, srv.IdleTimeout, "IdleTimeout is unset")
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
	require.NoError(t, err, "Listen() error = %v", err)

	router := newRouter(testLogger(), mustCaptureStore(t), mustVerifier(t))
	served := make(chan error, 1)
	go func() {
		served <- serve(ctx, ln, router, testLogger())
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+ln.Addr().String()+"/healthz", nil)
	require.NoError(t, err, "NewRequestWithContext() error = %v", err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "GET /healthz error = %v", err)
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	assert.Equal(t, http.StatusOK, resp.StatusCode, "GET /healthz status = %d, want %d", resp.StatusCode, http.StatusOK)

	cancel()
	select {
	case err := <-served:
		if err != nil {
			require.ErrorIs(t, err, http.ErrServerClosed)
		}
	case <-time.After(5 * time.Second):
		require.FailNow(t, "serve() did not return within 5s of context cancellation")
	}
}
