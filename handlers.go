package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// newRouter builds the service's HTTP routes.
//
// The route set is deliberately tiny and locked: /healthz for liveness and
// /alexa for the skill endpoint. Anything else is a 404 from the mux.
func newRouter(log *slog.Logger, store *captureStore) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.Handle("POST /alexa", handleAlexa(log, store))
	return mux
}

// writeAlexaResponse marshals the envelope before writing anything, so a
// marshalling failure cannot leave a half-written body behind a 200.
func writeAlexaResponse(w http.ResponseWriter, log *slog.Logger, envelope alexaEnvelope) {
	encoded, err := json.Marshal(envelope)
	if err != nil {
		log.Error("encoding the alexa response", slog.Any("error", err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(encoded); err != nil {
		log.Error("writing the alexa response", slog.Any("error", err))
	}
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
