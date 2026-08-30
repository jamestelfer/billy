package main

import (
	"log/slog"
	"net/http"
)

// newRouter builds the service's HTTP routes.
//
// The route set is deliberately tiny and locked: /healthz for liveness and
// /alexa for the skill endpoint. Anything else is a 404 from the mux.
func newRouter(log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	return mux
}

// handleHealthz answers a liveness probe. The body is fixed and carries
// nothing about the deployment: this endpoint is reachable by anyone who can
// reach the Funnel URL, so it must not disclose the version, the tailnet
// name, the hostname or any path.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}
