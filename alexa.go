package main

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/jamestelfer/billy/pkg/alexaverify"
)

// maxBodyBytes bounds the request body read.
//
// The endpoint is publicly reachable, so an unbounded read is a trivial
// memory-exhaustion vector — and the read happens before verification, because
// verification needs the bytes. Alexa's own envelopes are a few kilobytes at
// most, so this is generous.
const maxBodyBytes = 1 << 20 // 1 MiB

// staticBookToken is stable across requests and restarts, but deliberately
// carries no title, author, or local filesystem information.
const staticBookToken = "20d5b92f-69ff-4cb4-9236-3fae8c8f36e1" //nolint:gosec // public opaque stream identity, not a credential

// verificationFailureBody is what a rejected caller is told: nothing. Which
// check failed is recorded in the log for the operator, never disclosed on the
// wire, because that would tell an attacker exactly which knob to turn next.
// The status matches the Node adapter's "Request verification failed".
const verificationFailureBody = "request verification failed"

// alexaEnvelope is the minimal structurally valid response Alexa accepts.
// Nothing here is skill behaviour — it is the smallest envelope that keeps
// the Echo from reporting a problem.
type alexaEnvelope struct {
	Version           string         `json:"version"`
	SessionAttributes map[string]any `json:"sessionAttributes,omitempty"`
	Response          alexaResponse  `json:"response"`
}

type alexaResponse struct {
	OutputSpeech     *alexaOutputSpeech `json:"outputSpeech,omitempty"`
	Reprompt         *alexaReprompt     `json:"reprompt,omitempty"`
	Directives       []alexaDirective   `json:"directives,omitempty"`
	ShouldEndSession *bool              `json:"shouldEndSession,omitempty"`
}

type alexaOutputSpeech struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type alexaReprompt struct {
	OutputSpeech alexaOutputSpeech `json:"outputSpeech"`
}

type alexaPlayerContext struct {
	PlayerActivity       string `json:"playerActivity"`
	Token                string `json:"token"`
	OffsetInMilliseconds int64  `json:"offsetInMilliseconds"`
}

type alexaDirective struct {
	Type         string          `json:"type"`
	PlayBehavior string          `json:"playBehavior,omitempty"`
	AudioItem    *alexaAudioItem `json:"audioItem,omitempty"`
}

type alexaAudioItem struct {
	Stream alexaStream `json:"stream"`
}

type alexaStream struct {
	Token                string `json:"token"`
	URL                  string `json:"url"`
	OffsetInMilliseconds int64  `json:"offsetInMilliseconds"`
}

type alexaRequestEnvelope struct {
	Session struct {
		Attributes map[string]json.RawMessage `json:"attributes"`
	} `json:"session"`
	Context struct {
		AudioPlayer alexaPlayerContext `json:"AudioPlayer"`
		System      struct {
			Device struct {
				SupportedInterfaces map[string]json.RawMessage `json:"supportedInterfaces"`
			} `json:"device"`
		} `json:"System"`
	} `json:"context"`
	Request struct {
		Type                 string `json:"type"`
		Token                string `json:"token"`
		OffsetInMilliseconds int64  `json:"offsetInMilliseconds"`
		Error                struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		Intent struct {
			Name  string `json:"name"`
			Slots map[string]struct {
				Value string `json:"value"`
			} `json:"slots"`
		} `json:"intent"`
	} `json:"request"`
}

func newAlexaEnvelope(text string) alexaEnvelope {
	shouldEnd := true
	return alexaEnvelope{
		Version: "1.0",
		Response: alexaResponse{
			OutputSpeech:     &alexaOutputSpeech{Type: "PlainText", Text: text},
			ShouldEndSession: &shouldEnd,
		},
	}
}

// handleAlexa verifies the request came from Alexa, then captures it and
// answers with a valid envelope.
//
// Capture happens only after a request verifies. Capturing rejected requests
// would help diagnose a verification bug, but it hands any unauthenticated
// caller a way to fill the disk, and it would poison the signed-request corpus
// with bodies that are not signed requests. Rejections are counted and logged
// instead.
func handleAlexa(log *slog.Logger, store *captureStore, verifier *alexaverify.Verifier, configuredBook *book) http.HandlerFunc {
	var rejected atomic.Uint64

	return func(w http.ResponseWriter, r *http.Request) {
		receivedAt := time.Now().UTC()

		// The body is read exactly once, into one buffer. That buffer is what
		// the signature is checked over and what is later decoded and
		// captured. Nothing between here and verification may re-encode it:
		// the signature covers these bytes and no equivalent rendering of
		// them.
		limited := http.MaxBytesReader(w, r.Body, maxBodyBytes)
		body, readErr := io.ReadAll(limited)
		if readErr != nil {
			// A truncated or unreadable body can never verify, and must not be
			// allowed to verify a prefix of itself. Fail closed here rather
			// than handing a partial buffer to the verifier.
			_, overLimit := errors.AsType[*http.MaxBytesError](readErr)
			log.Warn("rejecting request: the body could not be read in full",
				slog.Bool("over_limit", overLimit),
				slog.Int("bytes_read", len(body)),
				slog.Uint64("rejected_total", rejected.Add(1)),
				slog.Any("error", readErr))
			rejectUnverified(w)
			return
		}

		if err := verifier.Verify(r.Context(), body, r.Header); err != nil {
			logVerificationFailure(log, r, err, rejected.Add(1))
			rejectUnverified(w)
			return
		}

		captureAndRespond(w, r, log, store, body, receivedAt, configuredBook)
	}
}

// captureAndRespond records a verified request and answers it. It runs only
// for requests that have already passed verification.
func captureAndRespond(
	w http.ResponseWriter,
	r *http.Request,
	log *slog.Logger,
	store *captureStore,
	body []byte,
	receivedAt time.Time,
	configuredBook *book,
) {
	meta := captureMetadata{
		ReceivedAt:    receivedAt,
		Method:        r.Method,
		RequestURI:    r.URL.RequestURI(),
		Proto:         r.Proto,
		RemoteAddr:    r.RemoteAddr,
		Host:          r.Host,
		ContentLength: r.ContentLength,
		BodyLength:    len(body),
		Headers:       r.Header.Clone(),
	}

	// R11: a capture failure must never fail the request. A dropped capture
	// costs one corpus entry; an error response to Alexa costs the user an
	// audible failure and teaches nothing.
	if stem, err := store.Save(meta, body); err != nil {
		log.Error("capturing the request", slog.Any("error", err))
	} else {
		log.Info("captured verified request",
			slog.String("stem", stem),
			slog.Int("body_length", len(body)))
	}

	writeAlexaResponse(w, log, dispatchAlexaRequest(log, r, body, configuredBook))
}

func emptyAlexaEnvelope() alexaEnvelope {
	return alexaEnvelope{Version: "1.0", Response: alexaResponse{}}
}

func newTitlePromptEnvelope() alexaEnvelope {
	const prompt = "What would you like to play?"
	shouldEnd := false
	return alexaEnvelope{
		Version:           "1.0",
		SessionAttributes: map[string]any{"awaitingTitle": true},
		Response: alexaResponse{
			OutputSpeech:     &alexaOutputSpeech{Type: "PlainText", Text: prompt},
			Reprompt:         &alexaReprompt{OutputSpeech: alexaOutputSpeech{Type: "PlainText", Text: prompt}},
			ShouldEndSession: &shouldEnd,
		},
	}
}

func dispatchAlexaRequest(log *slog.Logger, r *http.Request, body []byte, configuredBook *book) alexaEnvelope {
	var request alexaRequestEnvelope
	if err := json.Unmarshal(body, &request); err != nil {
		log.Error("decoding a verified alexa request", slog.Any("error", err))
		return newAlexaEnvelope("I couldn't understand that request.")
	}

	if response, handled := dispatchLifecycle(log, request); handled {
		return response
	}
	if request.Request.Type == "LaunchRequest" {
		return newTitlePromptEnvelope()
	}
	if request.Request.Type != "IntentRequest" {
		return newAlexaEnvelope("Ask me to play " + configuredBook.Title + ".")
	}
	return dispatchIntent(r, request, configuredBook)
}

func dispatchLifecycle(log *slog.Logger, request alexaRequestEnvelope) (alexaEnvelope, bool) {
	switch request.Request.Type {
	case "AudioPlayer.PlaybackStarted",
		"AudioPlayer.PlaybackStopped",
		"AudioPlayer.PlaybackFinished",
		"AudioPlayer.PlaybackNearlyFinished":
		return emptyAlexaEnvelope(), true
	case "AudioPlayer.PlaybackFailed":
		log.Error("audio playback failed",
			slog.String("token", request.Request.Token),
			slog.String("error_type", request.Request.Error.Type))
		return emptyAlexaEnvelope(), true
	default:
		return alexaEnvelope{}, false
	}
}

func dispatchIntent(r *http.Request, request alexaRequestEnvelope, configuredBook *book) alexaEnvelope {
	switch request.Request.Intent.Name {
	case "AMAZON.PauseIntent", "AMAZON.StopIntent":
		return stopConfiguredStream(request.Context.AudioPlayer.Token)
	case "AMAZON.ResumeIntent":
		return resumeConfiguredStream(r.Host, request.Context.AudioPlayer)
	case "PlayBookIntent":
	case "SelectBookIntent":
		var awaitingTitle bool
		if err := json.Unmarshal(request.Session.Attributes["awaitingTitle"], &awaitingTitle); err != nil || !awaitingTitle {
			return newAlexaEnvelope("Ask me to play " + configuredBook.Title + ".")
		}
	default:
		return newAlexaEnvelope("Ask me to play " + configuredBook.Title + ".")
	}

	if normalizeTitle(request.Request.Intent.Slots["title"].Value) != normalizeTitle(configuredBook.Title) {
		return newAlexaEnvelope("I only know " + configuredBook.Title + ".")
	}
	if raw, ok := request.Context.System.Device.SupportedInterfaces["AudioPlayer"]; !ok || string(raw) == "null" {
		return newAlexaEnvelope("This device does not support audiobook playback.")
	}

	announcement := "Playing " + configuredBook.Title + "."
	if configuredBook.Author != "" {
		announcement = "Playing " + configuredBook.Title + " by " + configuredBook.Author + "."
	}
	envelope := newAlexaEnvelope(announcement)
	envelope.Response.Directives = []alexaDirective{newPlayDirective(r.Host, 0)}
	return envelope
}

func stopConfiguredStream(token string) alexaEnvelope {
	envelope := emptyAlexaEnvelope()
	if token == staticBookToken {
		envelope.Response.Directives = []alexaDirective{{Type: "AudioPlayer.Stop"}}
	}
	return envelope
}

func resumeConfiguredStream(host string, player alexaPlayerContext) alexaEnvelope {
	envelope := emptyAlexaEnvelope()
	if player.Token == staticBookToken && player.PlayerActivity == "PAUSED" {
		envelope.Response.Directives = []alexaDirective{newPlayDirective(host, player.OffsetInMilliseconds)}
	}
	return envelope
}

func newPlayDirective(host string, offset int64) alexaDirective {
	streamURL := (&url.URL{
		Scheme: "https",
		Host:   host,
		Path:   "/media/book.mp3",
	}).String()
	return alexaDirective{
		Type:         "AudioPlayer.Play",
		PlayBehavior: "REPLACE_ALL",
		AudioItem: &alexaAudioItem{Stream: alexaStream{
			Token:                staticBookToken,
			URL:                  streamURL,
			OffsetInMilliseconds: offset,
		}},
	}
}

func rejectUnverified(w http.ResponseWriter) {
	http.Error(w, verificationFailureBody, http.StatusBadRequest)
}

// logVerificationFailure records why a request was refused.
//
// Each sentinel gets its own stable message so an operator can tell a missing
// header from a bad signature at a glance — the distinction both reference
// SDKs make, and which the caller is deliberately not told about.
//
// The order of the cases matters: the two risk-register errors are tested
// before the general ErrCertURLInvalid so they can never be swallowed by it.
func logVerificationFailure(log *slog.Logger, r *http.Request, err error, total uint64) {
	attrs := []any{
		slog.String("remote_addr", r.RemoteAddr),
		slog.Uint64("rejected_total", total),
		slog.Any("error", err),
	}

	// The offending URL is the whole diagnostic value of a URL rejection, and
	// it is attacker-supplied rather than secret. The signature header and the
	// body are never logged.
	withURL := func() []any {
		return append(attrs, slog.String("cert_chain_url", r.Header.Get("SignatureCertChainUrl")))
	}

	switch {
	case errors.Is(err, alexaverify.ErrMissingHeader):
		log.Warn("rejecting request: missing verification header", attrs...)
	case errors.Is(err, alexaverify.ErrStaleTimestamp):
		log.Warn("rejecting request: timestamp outside tolerance", attrs...)
	case errors.Is(err, alexaverify.ErrCertURLUserinfo):
		log.Warn("rejecting request: certificate chain URL contains userinfo", withURL()...)
	case errors.Is(err, alexaverify.ErrCertURLTraversal):
		log.Warn("rejecting request: certificate chain URL escapes the permitted path", withURL()...)
	case errors.Is(err, alexaverify.ErrCertURLInvalid):
		log.Warn("rejecting request: invalid certificate chain URL", withURL()...)
	case errors.Is(err, alexaverify.ErrCertFetch):
		log.Warn("rejecting request: could not fetch the certificate chain", withURL()...)
	case errors.Is(err, alexaverify.ErrUntrustedChain):
		log.Warn("rejecting request: certificate chain is not trusted", withURL()...)
	case errors.Is(err, alexaverify.ErrCertHostname):
		log.Warn("rejecting request: certificate is not valid for the Alexa API domain", withURL()...)
	case errors.Is(err, alexaverify.ErrUnsupportedKey):
		log.Warn("rejecting request: unsupported certificate public key", withURL()...)
	case errors.Is(err, alexaverify.ErrBadSignature):
		log.Warn("rejecting request: invalid request signature", attrs...)
	default:
		log.Warn("rejecting request: verification failed", attrs...)
	}
}
