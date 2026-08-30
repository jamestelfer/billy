package main

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// maxBodyBytes bounds the request body read.
//
// The endpoint is publicly reachable and, for the duration of this phase,
// unauthenticated: an unbounded read is a trivial memory-exhaustion vector.
// Alexa's own envelopes are a few kilobytes at most, so this is generous.
const maxBodyBytes = 1 << 20 // 1 MiB

// spokenConfirmation is what the Echo says back. Capture is the only
// behaviour in this phase, so the response exists purely to close the
// request cleanly and confirm the round trip out loud.
const spokenConfirmation = "Captured."

// alexaEnvelope is the minimal structurally valid response Alexa accepts.
// Nothing here is skill behaviour — it is the smallest envelope that keeps
// the Echo from reporting a problem.
type alexaEnvelope struct {
	Version  string        `json:"version"`
	Response alexaResponse `json:"response"`
}

type alexaResponse struct {
	OutputSpeech     alexaOutputSpeech `json:"outputSpeech"`
	ShouldEndSession bool              `json:"shouldEndSession"`
}

type alexaOutputSpeech struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func newAlexaEnvelope(text string) alexaEnvelope {
	return alexaEnvelope{
		Version: "1.0",
		Response: alexaResponse{
			OutputSpeech:     alexaOutputSpeech{Type: "PlainText", Text: text},
			ShouldEndSession: true,
		},
	}
}

// handleAlexa captures the request and answers with a valid envelope.
//
// There is deliberately no signature verification here (R12). The endpoint is
// unauthenticated for the duration of this phase, which is acceptable only
// because the skill stays in Development status and the phase is short-lived
// — verification is the very next piece of work and has its own design.
func handleAlexa(log *slog.Logger, store *captureStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		receivedAt := time.Now().UTC()

		// io.ReadAll returns what it managed to read alongside the error, so
		// a body that trips the limit is still captured — truncated, and
		// recorded as such, rather than lost.
		limited := http.MaxBytesReader(w, r.Body, maxBodyBytes)
		body, readErr := io.ReadAll(limited)

		var maxBytes *http.MaxBytesError
		truncated := errors.As(readErr, &maxBytes)
		if readErr != nil && !truncated {
			log.Error("reading the request body", slog.Any("error", readErr))
		}

		meta := captureMetadata{
			ReceivedAt:    receivedAt,
			Method:        r.Method,
			RequestURI:    r.URL.RequestURI(),
			Proto:         r.Proto,
			RemoteAddr:    r.RemoteAddr,
			Host:          r.Host,
			ContentLength: r.ContentLength,
			BodyLength:    len(body),
			Truncated:     truncated,
			Headers:       r.Header.Clone(),
		}

		// R11: a capture failure must never fail the request. A dropped
		// capture costs one corpus entry; an error response to Alexa costs
		// the user an audible failure and teaches nothing.
		if stem, err := store.Save(meta, body); err != nil {
			log.Error("capturing the request", slog.Any("error", err))
		} else {
			log.Info("captured request",
				slog.String("stem", stem),
				slog.Int("body_length", len(body)),
				slog.Bool("truncated", truncated))
		}

		writeAlexaResponse(w, log, newAlexaEnvelope(spokenConfirmation))
	}
}
