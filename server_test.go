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
	srv := newHTTPServer(newRouter(testLogger(), mustCaptureStore(t), mustVerifier(t), mustBook(t)))

	assert.NotEqual(t, 0, srv.ReadHeaderTimeout, "ReadHeaderTimeout is unset")
	assert.NotEqual(t, 0, srv.IdleTimeout, "IdleTimeout is unset")
}

// Integration: the real serve loop, over a real listener, answering a real
// request — and returning cleanly when its context is cancelled. tsnet
// supplies the listener in production; here a loopback listener stands in, so
// the wiring is exercised without a tailnet.
func TestServeAnswersHealthzAndMediaAndShutsDownOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err, "Listen() error = %v", err)

	configuredBook := bookWithMedia(t, []byte("0123456789"))
	router := newRouter(testLogger(), mustCaptureStore(t), mustVerifier(t), configuredBook)
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

	mediaReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+ln.Addr().String()+"/media/book.mp3", nil)
	require.NoError(t, err)
	mediaReq.Header.Set("Range", "bytes=3-6")
	mediaResp, err := http.DefaultClient.Do(mediaReq)
	require.NoError(t, err)
	defer func() { _ = mediaResp.Body.Close() }()
	mediaBody, err := io.ReadAll(mediaResp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusPartialContent, mediaResp.StatusCode)
	assert.Equal(t, "audio/mpeg", mediaResp.Header.Get("Content-Type"))
	assert.Equal(t, "3456", string(mediaBody))

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
