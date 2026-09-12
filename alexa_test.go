package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/jamestelfer/billy/pkg/alexaverify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testNow is the clock the handler tests pin the verifier to, so a fixture
// timestamp does not go stale as the repository ages.
var testNow = time.Date(2026, time.August, 30, 1, 2, 3, 0, time.UTC)

const sampleLaunchRequest = `{"version":"1.0","session":{"new":true,"sessionId":"amzn1.echo-api.session.1",` +
	`"application":{"applicationId":"amzn1.ask.skill.1"},"user":{"userId":"amzn1.ask.account.1"}},` +
	`"request":{"type":"LaunchRequest","requestId":"amzn1.echo-api.request.1","timestamp":"2026-08-30T01:02:03Z","locale":"en-AU"}}`

const samplePlayRequest = `{"version":"1.0","context":{"System":{"device":{"supportedInterfaces":{"AudioPlayer":{}}}}},` +
	`"request":{"type":"IntentRequest","requestId":"amzn1.echo-api.request.2","timestamp":"2026-08-30T01:02:03Z",` +
	`"locale":"en-US","intent":{"name":"PlayBookIntent","slots":{"title":{"name":"title","value":"Test Book"}}}}}`

const samplePromptedTitleRequest = `{"version":"1.0","session":{"attributes":{"awaitingTitle":true}},` +
	`"context":{"System":{"device":{"supportedInterfaces":{"AudioPlayer":{}}}}},` +
	`"request":{"type":"IntentRequest","requestId":"amzn1.echo-api.request.3","timestamp":"2026-08-30T01:02:03Z",` +
	`"locale":"en-US","intent":{"name":"SelectBookIntent","slots":{"title":{"name":"title","value":"Test Book"}}}}}`

const samplePauseRequest = `{"version":"1.0","context":{"AudioPlayer":{"playerActivity":"PLAYING",` +
	`"token":"` + staticBookToken + `","offsetInMilliseconds":42000}},` +
	`"request":{"type":"IntentRequest","requestId":"amzn1.echo-api.request.4","timestamp":"2026-08-30T01:02:03Z",` +
	`"locale":"en-US","intent":{"name":"AMAZON.PauseIntent"}}}`

// signatureHeaders are the two headers every Alexa request carries. The values
// are structurally plausible but not genuine: these tests exercise the gate's
// routing and logging, not the cryptography.
func signatureHeaders() map[string]string {
	return map[string]string{
		"Signature-256":         "c2lnbmF0dXJl",
		"SignatureCertChainUrl": "https://s3.amazonaws.com/echo.api/echo-api-cert-1.pem",
		"Content-Type":          "application/json",
	}
}

// launchRequestAt renders the sample body with a different timestamp, so the
// freshness gate can be driven from the outside.
func launchRequestAt(ts time.Time) string {
	return strings.Replace(sampleLaunchRequest,
		"2026-08-30T01:02:03Z", ts.UTC().Format(time.RFC3339), 1)
}

func newTestCapture(t *testing.T) (*captureStore, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "capture")
	store, err := newCaptureStore(dir)
	require.NoError(t, err, "newCaptureStore() error = %v", err)
	return store, dir
}

func postAlexa(t *testing.T, router http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// captureVerifiedRequest drives the post-verification half of the handler
// directly. Capture-only tests use it to isolate filesystem and response
// behaviour from the comparatively expensive generated-certificate fixture;
// the complete verified route is covered in alexa_signature_test.go.
func captureVerifiedRequest(t *testing.T, store *captureStore, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	captureAndRespond(rec, req, testLogger(), store, []byte(body), testNow, mustBook(t))
	return rec
}

func onlyCapturedStem(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "reading the capture directory: %v", err)
	var stems []string
	for _, e := range entries {
		if stem, ok := strings.CutSuffix(e.Name(), ".body"); ok {
			stems = append(stems, stem)
		}
	}
	require.Len(t, stems, 1, "capture directory holds %d body files, want exactly 1", len(stems))
	return stems[0]
}

func assertCaptureDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "reading the capture directory: %v", err)
	require.Empty(t, entries, "capture directory holds %d files, want 0", len(entries))
}

// R10: the response must be a structurally valid Alexa envelope.
func TestAlexaRespondsWithAValidEnvelope(t *testing.T) {
	store, _ := newTestCapture(t)

	rec := captureVerifiedRequest(t, store, sampleLaunchRequest, signatureHeaders())

	require.Equal(t, http.StatusOK, rec.Code, "status = %d, want %d", rec.Code, http.StatusOK)
	assert.Regexp(t, `^application/json(;|$)`, rec.Header().Get("Content-Type"))

	snaps.MatchJSON(t, rec.Body.Bytes())
}

func TestAlexaDirectPlayReturnsTheConfiguredStream(t *testing.T) {
	store, _ := newTestCapture(t)
	configuredBook := mustBook(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", strings.NewReader(samplePlayRequest))
	req.Host = "billy.example.test"
	rec := httptest.NewRecorder()

	captureAndRespond(rec, req, testLogger(), store, []byte(samplePlayRequest), testNow, configuredBook)

	require.Equal(t, http.StatusOK, rec.Code)
	snaps.MatchJSON(t, rec.Body.Bytes())
	assert.Contains(t, rec.Body.String(), `"url":"https://billy.example.test/media/book.mp3"`)
	var response alexaEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.Len(t, response.Response.Directives, 1)
	token := response.Response.Directives[0].AudioItem.Stream.Token
	assert.NotContains(t, token, configuredBook.Title)
	assert.NotContains(t, token, configuredBook.mediaName)
}

func TestAlexaAcknowledgesAudioPlayerLifecycleEventsWithoutInteraction(t *testing.T) {
	for _, eventType := range []string{
		"AudioPlayer.PlaybackStarted",
		"AudioPlayer.PlaybackStopped",
		"AudioPlayer.PlaybackFinished",
		"AudioPlayer.PlaybackNearlyFinished",
	} {
		t.Run(eventType, func(t *testing.T) {
			body := `{"version":"1.0","request":{"type":"` + eventType + `","requestId":"request-id",` +
				`"timestamp":"2026-08-30T01:02:03Z","token":"` + staticBookToken + `","offsetInMilliseconds":42000}}`
			store, captureDir := newTestCapture(t)
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", strings.NewReader(body))
			rec := httptest.NewRecorder()

			captureAndRespond(rec, req, testLogger(), store, []byte(body), testNow, mustBook(t))

			var response alexaEnvelope
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			assert.Empty(t, response.Response.Directives)
			assert.Nil(t, response.Response.OutputSpeech)
			assert.NotEmpty(t, onlyCapturedStem(t, captureDir))
		})
	}
}

func TestAlexaPlaybackFailureIsDiagnosticAndDoesNotStopService(t *testing.T) {
	body := `{"version":"1.0","request":{"type":"AudioPlayer.PlaybackFailed","requestId":"request-id",` +
		`"timestamp":"2026-08-30T01:02:03Z","token":"` + staticBookToken + `",` +
		`"error":{"type":"MEDIA_ERROR_UNKNOWN","message":"private failure detail"}}}`
	store, _ := newTestCapture(t)
	configuredBook := mustBook(t)
	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, nil))
	failedReq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", strings.NewReader(body))
	failedRec := httptest.NewRecorder()

	captureAndRespond(failedRec, failedReq, log, store, []byte(body), testNow, configuredBook)

	require.Equal(t, http.StatusOK, failedRec.Code)
	assert.Contains(t, logged.String(), "audio playback failed")
	assert.Contains(t, logged.String(), "MEDIA_ERROR_UNKNOWN")
	assert.Contains(t, logged.String(), staticBookToken)
	assert.NotContains(t, logged.String(), "private failure detail")

	playReq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", strings.NewReader(samplePlayRequest))
	playRec := httptest.NewRecorder()
	captureAndRespond(playRec, playReq, log, store, []byte(samplePlayRequest), testNow, configuredBook)
	var playResponse alexaEnvelope
	require.NoError(t, json.Unmarshal(playRec.Body.Bytes(), &playResponse))
	assert.Len(t, playResponse.Response.Directives, 1)

	healthRec := httptest.NewRecorder()
	handleHealthz(healthRec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))
	assert.Equal(t, http.StatusOK, healthRec.Code)
}

func TestAlexaPauseAndStopIssueStopForTheConfiguredStream(t *testing.T) {
	for _, intent := range []string{"AMAZON.PauseIntent", "AMAZON.StopIntent"} {
		t.Run(intent, func(t *testing.T) {
			body := strings.Replace(samplePauseRequest, "AMAZON.PauseIntent", intent, 1)
			store, _ := newTestCapture(t)
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", strings.NewReader(body))
			rec := httptest.NewRecorder()

			captureAndRespond(rec, req, testLogger(), store, []byte(body), testNow, mustBook(t))

			var response alexaEnvelope
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			require.Len(t, response.Response.Directives, 1)
			assert.Equal(t, "AudioPlayer.Stop", response.Response.Directives[0].Type)
			assert.NotEqual(t, "AudioPlayer.Play", response.Response.Directives[0].Type)
			assert.Nil(t, response.Response.OutputSpeech)
		})
	}
}

func TestAlexaTransportIgnoresAnotherStream(t *testing.T) {
	for _, intent := range []string{"AMAZON.PauseIntent", "AMAZON.StopIntent", "AMAZON.ResumeIntent"} {
		t.Run(intent, func(t *testing.T) {
			body := strings.Replace(samplePauseRequest, "AMAZON.PauseIntent", intent, 1)
			body = strings.Replace(body, staticBookToken, "another-stream", 1)
			store, _ := newTestCapture(t)
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", strings.NewReader(body))
			rec := httptest.NewRecorder()

			captureAndRespond(rec, req, testLogger(), store, []byte(body), testNow, mustBook(t))

			var response alexaEnvelope
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			assert.Empty(t, response.Response.Directives)
			assert.Nil(t, response.Response.OutputSpeech)
		})
	}
}

func TestAlexaResumeRequiresAPausedConfiguredStream(t *testing.T) {
	body := strings.Replace(samplePauseRequest, "AMAZON.PauseIntent", "AMAZON.ResumeIntent", 1)
	store, _ := newTestCapture(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", strings.NewReader(body))
	rec := httptest.NewRecorder()

	captureAndRespond(rec, req, testLogger(), store, []byte(body), testNow, mustBook(t))

	var response alexaEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Empty(t, response.Response.Directives)
}

func TestAlexaResumeContinuesTheConfiguredStreamAtReportedOffset(t *testing.T) {
	body := strings.Replace(samplePauseRequest, "AMAZON.PauseIntent", "AMAZON.ResumeIntent", 1)
	body = strings.Replace(body, `"playerActivity":"PLAYING"`, `"playerActivity":"PAUSED"`, 1)
	store, _ := newTestCapture(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", strings.NewReader(body))
	req.Host = "billy.example.test"
	rec := httptest.NewRecorder()

	captureAndRespond(rec, req, testLogger(), store, []byte(body), testNow, mustBook(t))

	var response alexaEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.Len(t, response.Response.Directives, 1)
	directive := response.Response.Directives[0]
	assert.Equal(t, "AudioPlayer.Play", directive.Type)
	assert.Equal(t, "REPLACE_ALL", directive.PlayBehavior)
	require.NotNil(t, directive.AudioItem)
	assert.Equal(t, staticBookToken, directive.AudioItem.Stream.Token)
	assert.Equal(t, int64(42000), directive.AudioItem.Stream.OffsetInMilliseconds)
	assert.Equal(t, "https://billy.example.test/media/book.mp3", directive.AudioItem.Stream.URL)
	assert.Nil(t, response.Response.OutputSpeech)
}

func TestAlexaLaunchPromptsForATitleAndKeepsTheSessionOpen(t *testing.T) {
	store, _ := newTestCapture(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", strings.NewReader(sampleLaunchRequest))
	rec := httptest.NewRecorder()

	captureAndRespond(rec, req, testLogger(), store, []byte(sampleLaunchRequest), testNow, mustBook(t))

	var response alexaEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Empty(t, response.Response.Directives)
	require.NotNil(t, response.Response.ShouldEndSession)
	assert.False(t, *response.Response.ShouldEndSession)
	assert.Equal(t, true, response.SessionAttributes["awaitingTitle"])
	require.NotNil(t, response.Response.OutputSpeech)
	assert.Contains(t, response.Response.OutputSpeech.Text, "play")
}

func TestAlexaPromptedTitleStartsOnlyInThePromptCreatedState(t *testing.T) {
	for name, body := range map[string]string{
		"prompted":     samplePromptedTitleRequest,
		"out of state": strings.Replace(samplePromptedTitleRequest, `"attributes":{"awaitingTitle":true}`, `"attributes":{}`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			store, _ := newTestCapture(t)
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", strings.NewReader(body))
			rec := httptest.NewRecorder()

			captureAndRespond(rec, req, testLogger(), store, []byte(body), testNow, mustBook(t))

			var response alexaEnvelope
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			if name == "prompted" {
				assert.Len(t, response.Response.Directives, 1)
			} else {
				assert.Empty(t, response.Response.Directives)
			}
		})
	}
}

func TestAlexaTitleMatchingIgnoresOnlyCasePunctuationAndWhitespace(t *testing.T) {
	tests := map[string]struct {
		title string
		match bool
	}{
		"exact":              {title: "Test Book", match: true},
		"case":               {title: "test book", match: true},
		"punctuation spaces": {title: "  Test,   Book!  ", match: true},
		"substring":          {title: "Test", match: false},
		"near match":         {title: "Test Books", match: false},
	}

	paths := map[string]string{"direct": samplePlayRequest, "prompted": samplePromptedTitleRequest}
	for name, tc := range tests {
		for pathName, template := range paths {
			t.Run(name+"/"+pathName, func(t *testing.T) {
				body := strings.Replace(template, `"value":"Test Book"`, `"value":"`+tc.title+`"`, 1)
				store, _ := newTestCapture(t)
				req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", strings.NewReader(body))
				rec := httptest.NewRecorder()

				captureAndRespond(rec, req, testLogger(), store, []byte(body), testNow, mustBook(t))

				var response alexaEnvelope
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
				if tc.match {
					assert.Len(t, response.Response.Directives, 1)
				} else {
					assert.Empty(t, response.Response.Directives)
				}
			})
		}
	}
}

func TestAlexaUnknownTitleNamesTheOnlyAvailableBook(t *testing.T) {
	body := strings.Replace(samplePlayRequest, `"value":"Test Book"`, `"value":"Another Book"`, 1)
	store, _ := newTestCapture(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", strings.NewReader(body))
	rec := httptest.NewRecorder()

	captureAndRespond(rec, req, testLogger(), store, []byte(body), testNow, mustBook(t))

	var response alexaEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Empty(t, response.Response.Directives)
	require.NotNil(t, response.Response.OutputSpeech)
	assert.Equal(t, "I only know Test Book.", response.Response.OutputSpeech.Text)
}

func TestAlexaDoesNotStartAudioWithoutAnExplicitMatchingTitle(t *testing.T) {
	tests := map[string]string{
		"launch":           sampleLaunchRequest,
		"unrelated intent": strings.Replace(samplePlayRequest, "PlayBookIntent", "AMAZON.HelpIntent", 1),
		"unknown title":    strings.Replace(samplePlayRequest, `"value":"Test Book"`, `"value":"Another Book"`, 1),
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			store, _ := newTestCapture(t)
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", strings.NewReader(body))
			rec := httptest.NewRecorder()

			captureAndRespond(rec, req, testLogger(), store, []byte(body), testNow, mustBook(t))

			var response alexaEnvelope
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			assert.Empty(t, response.Response.Directives)
		})
	}
}

func TestAlexaMatchingTitleOnUnsupportedDeviceDoesNotStartAudio(t *testing.T) {
	store, _ := newTestCapture(t)
	configuredBook := mustBook(t)
	body := strings.Replace(samplePlayRequest, `"supportedInterfaces":{"AudioPlayer":{}}`, `"supportedInterfaces":{}`, 1)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", strings.NewReader(body))
	rec := httptest.NewRecorder()

	captureAndRespond(rec, req, testLogger(), store, []byte(body), testNow, configuredBook)

	var response alexaEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Empty(t, response.Response.Directives)
	require.NotNil(t, response.Response.OutputSpeech)
	assert.Contains(t, response.Response.OutputSpeech.Text, "does not support")
}

func TestAlexaPlaybackAnnouncementIncludesTheConfiguredAuthor(t *testing.T) {
	store, _ := newTestCapture(t)
	configuredBook := bookWithMetadata(t, "Test Book", "A. Writer", []byte("audio"))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alexa", strings.NewReader(samplePlayRequest))
	req.Host = "billy.example.test"
	rec := httptest.NewRecorder()

	captureAndRespond(rec, req, testLogger(), store, []byte(samplePlayRequest), testNow, configuredBook)

	var response alexaEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.NotNil(t, response.Response.OutputSpeech)
	assert.Equal(t, "Playing Test Book by A. Writer.", response.Response.OutputSpeech.Text)
}

// R8: the exact bytes Alexa sent land on disk.
func TestAlexaPersistsTheBodyByteForByte(t *testing.T) {
	store, dir := newTestCapture(t)

	captureVerifiedRequest(t, store, sampleLaunchRequest, signatureHeaders())

	stem := onlyCapturedStem(t, dir)
	body, err := os.ReadFile(filepath.Join(dir, stem+".body"))
	require.NoError(t, err, "reading the captured body: %v", err)
	// JSON equivalence is insufficient here: the signature covers these exact wire bytes.
	//nolint:testifylint // JSONEq would hide whitespace or encoding changes.
	assert.Equal(t, []byte(sampleLaunchRequest), body, "captured body = %q, want %q", body, sampleLaunchRequest)
}

// R9: the sidecar records the signature headers and the request context. The
// signature headers are the entire reason this corpus is being collected.
func TestAlexaPersistsRequestMetadata(t *testing.T) {
	store, dir := newTestCapture(t)

	captureVerifiedRequest(t, store, sampleLaunchRequest, signatureHeaders())

	stem := onlyCapturedStem(t, dir)
	_, meta := readCapturePair(t, dir, stem)

	assert.Equal(t, http.MethodPost, meta["method"], "method = %v, want %v", meta["method"], http.MethodPost)
	assert.Equal(t, "/alexa", meta["request_uri"], "request_uri = %v, want /alexa", meta["request_uri"])
	assert.NotEmpty(t, meta["received_at"], "received_at is missing")
	assert.NotEmpty(t, meta["remote_addr"], "remote_addr is missing")
	assert.NotEmpty(t, meta["proto"], "proto is missing")
	assert.EqualValues(t, len(sampleLaunchRequest), meta["body_length"], "body_length")

	require.IsType(t, map[string]any{}, meta["headers"])
	recorded := meta["headers"].(map[string]any)
	for _, name := range []string{"Signature-256", "Signaturecertchainurl", "Content-Type"} {
		assert.Contains(t, recorded, name, "headers is missing %q; got keys %v", name, keysOf(recorded))
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// R11: a capture failure must not fail the request. Losing a corpus entry is
// cheap; an error response makes the Echo say "there was a problem" and
// teaches nothing.
//
// The failure is induced by replacing the capture directory with a regular
// file rather than by removing write permission: chmod is largely inert on
// Windows, and this phase treats Windows as a first-class host.
func TestAlexaStillRespondsWhenCaptureFails(t *testing.T) {
	store, dir := newTestCapture(t)

	require.NoError(t, os.RemoveAll(dir), "removing the capture directory")
	require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0o600), "blocking the capture directory")

	rec := captureVerifiedRequest(t, store, samplePlayRequest, signatureHeaders())

	require.Equal(t, http.StatusOK, rec.Code, "status = %d, want %d even though the capture failed", rec.Code, http.StatusOK)
	var envelope alexaEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "response is not valid JSON")
	assert.Equal(t, "1.0", envelope.Version)
	assert.Len(t, envelope.Response.Directives, 1)
}

// R14/R21: a request with no verification headers is refused with 400, and
// skill handling — of which capture is the observable part — never runs.
func TestAlexaRejectsAMissingSignatureHeader(t *testing.T) {
	store, dir := newTestCapture(t)

	headers := signatureHeaders()
	delete(headers, "Signature-256")

	rec := postAlexa(t, newTestRouter(t, store), sampleLaunchRequest, headers)

	require.Equal(t, http.StatusBadRequest, rec.Code, "POST /alexa status = %d, want %d", rec.Code, http.StatusBadRequest)
	assertCaptureDirEmpty(t, dir)
}

func TestAlexaRejectsAMissingCertChainURLHeader(t *testing.T) {
	store, dir := newTestCapture(t)

	headers := signatureHeaders()
	delete(headers, "SignatureCertChainUrl")

	rec := postAlexa(t, newTestRouter(t, store), sampleLaunchRequest, headers)

	require.Equal(t, http.StatusBadRequest, rec.Code, "POST /alexa status = %d, want %d", rec.Code, http.StatusBadRequest)
	assertCaptureDirEmpty(t, dir)
}

// R27: freshness is checked in both directions. The future case is the one the
// Node adapter misses entirely.
func TestAlexaRejectsATimestampOutsideTolerance(t *testing.T) {
	offsets := map[string]time.Duration{
		"ten minutes in the past":   -10 * time.Minute,
		"ten minutes in the future": 10 * time.Minute,
	}

	for name, offset := range offsets {
		t.Run(name, func(t *testing.T) {
			store, dir := newTestCapture(t)

			body := launchRequestAt(testNow.Add(offset))
			rec := postAlexa(t, newTestRouter(t, store), body, signatureHeaders())

			require.Equal(t, http.StatusBadRequest, rec.Code, "POST /alexa status = %d, want %d", rec.Code, http.StatusBadRequest)
			assertCaptureDirEmpty(t, dir)
		})
	}
}

// The gate fails closed: a fresh request with merely plausible signature
// headers is still refused when its certificate cannot be fetched and checked.
func TestAlexaFailsClosedForAFreshUnsignedRequest(t *testing.T) {
	store, dir := newTestCapture(t)

	rec := postAlexa(t, newTestRouter(t, store), sampleLaunchRequest, signatureHeaders())

	require.Equal(t, http.StatusBadRequest, rec.Code, "POST /alexa status = %d, want %d", rec.Code, http.StatusBadRequest)
	assertCaptureDirEmpty(t, dir)
}

// A rejection must disclose nothing about which check failed: telling a caller
// that the timestamp was fine but the signature was not is free intelligence.
func TestAlexaRejectionsAreIndistinguishableToTheCaller(t *testing.T) {
	store, _ := newTestCapture(t)
	router := newTestRouter(t, store)

	noHeaders := signatureHeaders()
	delete(noHeaders, "Signature-256")

	missing := postAlexa(t, router, sampleLaunchRequest, noHeaders)
	stale := postAlexa(t, router, launchRequestAt(testNow.Add(-time.Hour)), signatureHeaders())

	assert.Equal(t, stale.Code, missing.Code, "statuses differ: missing header %d, stale timestamp %d", missing.Code, stale.Code)
	assert.Equal(t, stale.Body.String(), missing.Body.String(), "bodies differ:\n missing header: %q\n stale timestamp: %q", missing.Body.String(), stale.Body.String())
}

// The endpoint is public, so an unbounded read is a memory-exhaustion vector.
// Over the limit the request is refused outright: a truncated body can never
// verify, and must never be allowed to verify a prefix of itself.
func TestAlexaRejectsAnOversizedBodyAndCapturesNothing(t *testing.T) {
	store, dir := newTestCapture(t)

	oversized := "{\"pad\":\"" + strings.Repeat("x", maxBodyBytes) + "\"}"
	rec := postAlexa(t, newTestRouter(t, store), oversized, signatureHeaders())

	require.Equal(t, http.StatusBadRequest, rec.Code, "POST /alexa status = %d, want %d", rec.Code, http.StatusBadRequest)
	assertCaptureDirEmpty(t, dir)
}

// Regression watchpoint: adding /alexa must not shadow /healthz.
func TestHealthzStillAnswersAlongsideAlexa(t *testing.T) {
	store, _ := newTestCapture(t)
	router := newTestRouter(t, store)

	postAlexa(t, router, sampleLaunchRequest, signatureHeaders())

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "GET /healthz status = %d, want %d", rec.Code, http.StatusOK)
}

// R22: Alexa only ever POSTs. Anything else is a probe: it gets a 405 from the
// mux, never reaches verification, and must not create a capture file.
func TestNonPostAlexaIsRejectedAndCapturesNothing(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			store, dir := newTestCapture(t)

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), method, "/alexa", nil)
			newTestRouter(t, store).ServeHTTP(rec, req)

			assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "%s /alexa status = %d, want %d", method, rec.Code, http.StatusMethodNotAllowed)
			assertCaptureDirEmpty(t, dir)
		})
	}
}

// R22: verification is wired to /alexa and nowhere else, so a probe with no
// signature to offer can still reach the liveness endpoint.
func TestVerificationIsNotAppliedToHealthz(t *testing.T) {
	store, _ := newTestCapture(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	newTestRouter(t, store).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "GET /healthz status = %d, want %d with no signature headers", rec.Code, http.StatusOK)
}

// R18/R19: the two rejections where this service is stricter than both
// reference SDKs must be logged under their own message, never folded into the
// generic invalid-URL line. If Amazon ever changes what it emits, these are
// the only two places the verifier can break in a way the SDKs would not, so
// they have to be findable in one search.
func TestAlexaLogsHostileCertChainURLsDistinctly(t *testing.T) {
	tests := []struct {
		name   string
		url    string
		wantIn string
	}{
		{
			name:   "wrong host",
			url:    "https://very.bad/echo.api/cert",
			wantIn: "invalid certificate chain URL",
		},
		{
			name:   "userinfo",
			url:    "https://user:pw@s3.amazonaws.com/echo.api/cert",
			wantIn: "certificate chain URL contains userinfo",
		},
		{
			name:   "percent encoded traversal",
			url:    "https://s3.amazonaws.com/echo.api/%2e%2e/cert",
			wantIn: "certificate chain URL escapes the permitted path",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, dir := newTestCapture(t)
			var logged bytes.Buffer
			router := newTestRouterWithLog(t, store, &logged)

			headers := signatureHeaders()
			headers["SignatureCertChainUrl"] = tc.url

			rec := postAlexa(t, router, sampleLaunchRequest, headers)

			require.Equal(t, http.StatusBadRequest, rec.Code, "POST /alexa status = %d, want %d", rec.Code, http.StatusBadRequest)
			assert.Contains(t, logged.String(), tc.wantIn, "log does not mention %q; log was:\n%s", tc.wantIn, logged.String())
			assert.Contains(t, logged.String(), "cert_chain_url", "log does not carry the offending URL; log was:\n%s", logged.String())
			assertCaptureDirEmpty(t, dir)
		})
	}
}

// Regression watchpoint from Phase 2: the header-missing path must keep its own
// log line rather than being swallowed by the new URL errors.
func TestAlexaStillLogsAMissingHeaderDistinctlyFromABadURL(t *testing.T) {
	store, _ := newTestCapture(t)

	var missingLog bytes.Buffer
	headers := signatureHeaders()
	delete(headers, "SignatureCertChainUrl")
	postAlexa(t, newTestRouterWithLog(t, store, &missingLog), sampleLaunchRequest, headers)

	var badURLLog bytes.Buffer
	badHeaders := signatureHeaders()
	badHeaders["SignatureCertChainUrl"] = "https://very.bad/echo.api/cert"
	postAlexa(t, newTestRouterWithLog(t, store, &badURLLog), sampleLaunchRequest, badHeaders)

	assert.Contains(t, missingLog.String(), "missing verification header", "a missing header was not logged as one; log was:\n%s", missingLog.String())
	assert.NotContains(t, missingLog.String(), "certificate chain URL", "a missing header was logged as a URL failure; log was:\n%s", missingLog.String())
	assert.Contains(t, badURLLog.String(), "invalid certificate chain URL", "a bad URL was not logged as one; log was:\n%s", badURLLog.String())
}

// R14: the cert URL is checked only after the timestamp. A stale request with
// a hostile URL must be refused on freshness, so an unauthenticated caller
// cannot use a replayed body to make this process look at a URL of their
// choosing at all.
func TestAlexaChecksTheTimestampBeforeTheCertChainURL(t *testing.T) {
	store, _ := newTestCapture(t)
	var logged bytes.Buffer

	headers := signatureHeaders()
	headers["SignatureCertChainUrl"] = "https://very.bad/echo.api/cert"
	postAlexa(t, newTestRouterWithLog(t, store, &logged),
		launchRequestAt(testNow.Add(-time.Hour)), headers)

	assert.Contains(t, logged.String(), "timestamp outside tolerance", "a stale request was not rejected on freshness first; log was:\n%s", logged.String())
	assert.NotContains(t, logged.String(), "certificate chain URL", "the cert URL was inspected before the timestamp gate closed; log was:\n%s", logged.String())
}

// newTestRouter builds the production router with the verifier's clock pinned
// and certificate egress blocked. Tests that need a valid chain inject one
// explicitly; ordinary handler tests must stay offline and fail closed.
func newTestRouter(t *testing.T, store *captureStore) http.Handler {
	t.Helper()
	return newTestRouterWithLog(t, store, io.Discard)
}

// newTestRouterWithLog is newTestRouter with the service log captured, for the
// tests that assert on which rejection was recorded.
func newTestRouterWithLog(t *testing.T, store *captureStore, out io.Writer) http.Handler {
	t.Helper()
	log := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelWarn}))
	client := &http.Client{Transport: handlerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, http.ErrServerClosed
	})}
	verifier, err := alexaverify.New(
		alexaverify.WithClock(func() time.Time { return testNow }),
		alexaverify.WithHTTPClient(client),
		alexaverify.WithLogger(log),
	)
	require.NoError(t, err, "alexaverify.New() error = %v", err)
	return newRouter(log, store, verifier, mustBook(t))
}
