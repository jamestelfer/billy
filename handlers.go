package main

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/jamestelfer/billy/pkg/alexaverify"
)

// newRouter builds the service's HTTP routes.
//
// The route set is deliberately tiny and locked: /healthz for liveness,
// /alexa for the skill endpoint, and one fixed public media representation.
// Anything else is a 404 from the mux.
//
// The method is part of each pattern, so unsupported methods never reach a
// handler. Request verification is wired to /alexa and nowhere else: health
// probes and Alexa's media fetches have no request signature to offer.
func newRouter(log *slog.Logger, store *captureStore, verifier *alexaverify.Verifier, configuredBook *book) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.Handle("GET /media/book.mp3", handleMedia(log, configuredBook))
	mux.Handle("POST /alexa", handleAlexa(log, store, verifier, configuredBook))
	return mux
}

func handleMedia(log *slog.Logger, configuredBook *book) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		media, err := configuredBook.openMedia()
		if err != nil {
			log.Error("opening the configured book media", slog.Any("error", err))
			http.Error(w, "media unavailable", http.StatusInternalServerError)
			return
		}
		defer func() {
			if err := media.Close(); err != nil {
				log.Error("closing the configured book media", slog.Any("error", err))
			}
		}()

		info, err := media.Stat()
		if err != nil {
			log.Error("inspecting the configured book media", slog.Any("error", err))
			http.Error(w, "media unavailable", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "audio/mpeg")
		http.ServeContent(w, r, "book.mp3", info.ModTime(), media)
	}
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
