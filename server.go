package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

const (
	// Alexa's own request timeout is around 8 seconds, so nothing legitimate
	// needs longer than these. They exist to bound what an anonymous caller
	// on the public Funnel URL can hold open.
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second

	// How long in-flight requests get to finish once shutdown starts.
	shutdownGrace = 10 * time.Second
)

func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}

// serve runs the HTTP server on ln until ctx is cancelled, then drains
// in-flight requests and returns. It does not close ln itself — the caller
// owns the listener, which in production belongs to the tsnet server.
func serve(ctx context.Context, ln net.Listener, handler http.Handler, log *slog.Logger) error {
	srv := newHTTPServer(handler)

	errs := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errs <- err
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down", slog.Duration("grace", shutdownGrace))

	// Shutdown gets its own context: ctx is already cancelled, and draining
	// needs a live deadline of its own.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		// A drain that times out still needs the server torn down, and the
		// listener closed, before we return.
		_ = srv.Close()
		return err
	}
	return <-errs
}
