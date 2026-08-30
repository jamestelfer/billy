package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleLaunchRequest = `{"version":"1.0","session":{"new":true,"sessionId":"amzn1.echo-api.session.1",` +
	`"application":{"applicationId":"amzn1.ask.skill.1"},"user":{"userId":"amzn1.ask.account.1"}},` +
	`"request":{"type":"LaunchRequest","requestId":"amzn1.echo-api.request.1","timestamp":"2026-08-30T01:02:03Z","locale":"en-AU"}}`

func newTestCapture(t *testing.T) (*captureStore, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "capture")
	store, err := newCaptureStore(dir)
	if err != nil {
		t.Fatalf("newCaptureStore() error = %v", err)
	}
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

func onlyCapturedStem(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the capture directory: %v", err)
	}
	var stems []string
	for _, e := range entries {
		if stem, ok := strings.CutSuffix(e.Name(), ".body"); ok {
			stems = append(stems, stem)
		}
	}
	if len(stems) != 1 {
		t.Fatalf("capture directory holds %d body files, want exactly 1", len(stems))
	}
	return stems[0]
}

// R10: the response must be a structurally valid Alexa envelope.
func TestAlexaRespondsWithAValidEnvelope(t *testing.T) {
	store, _ := newTestCapture(t)

	rec := postAlexa(t, newRouter(testLogger(), store), sampleLaunchRequest, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /alexa status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var envelope struct {
		Version  string `json:"version"`
		Response struct {
			OutputSpeech struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"outputSpeech"`
			ShouldEndSession bool `json:"shouldEndSession"`
		} `json:"response"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response is not valid JSON: %v (body: %s)", err, rec.Body.String())
	}
	if envelope.Version != "1.0" {
		t.Errorf("version = %q, want %q", envelope.Version, "1.0")
	}
	if envelope.Response.OutputSpeech.Type != "PlainText" {
		t.Errorf("outputSpeech.type = %q, want %q", envelope.Response.OutputSpeech.Type, "PlainText")
	}
	if envelope.Response.OutputSpeech.Text == "" {
		t.Error("outputSpeech.text is empty; the Echo would say nothing")
	}
	if !envelope.Response.ShouldEndSession {
		t.Error("shouldEndSession = false, want true")
	}
}

// R8: the exact bytes Alexa sent land on disk.
func TestAlexaPersistsTheBodyByteForByte(t *testing.T) {
	store, dir := newTestCapture(t)

	postAlexa(t, newRouter(testLogger(), store), sampleLaunchRequest, nil)

	stem := onlyCapturedStem(t, dir)
	body, err := os.ReadFile(filepath.Join(dir, stem+".body"))
	if err != nil {
		t.Fatalf("reading the captured body: %v", err)
	}
	if string(body) != sampleLaunchRequest {
		t.Errorf("captured body = %q, want %q", body, sampleLaunchRequest)
	}
}

// R9: the sidecar records the signature headers and the request context. The
// signature headers are the entire reason this corpus is being collected.
func TestAlexaPersistsRequestMetadata(t *testing.T) {
	store, dir := newTestCapture(t)

	headers := map[string]string{
		"Signature-256":         "c2lnbmF0dXJl",
		"SignatureCertChainUrl": "https://s3.amazonaws.com/echo.api/echo-api-cert-1.pem",
		"Content-Type":          "application/json",
	}
	postAlexa(t, newRouter(testLogger(), store), sampleLaunchRequest, headers)

	stem := onlyCapturedStem(t, dir)
	_, meta := readCapturePair(t, dir, stem)

	if meta["method"] != http.MethodPost {
		t.Errorf("method = %v, want %v", meta["method"], http.MethodPost)
	}
	if meta["request_uri"] != "/alexa" {
		t.Errorf("request_uri = %v, want /alexa", meta["request_uri"])
	}
	if meta["received_at"] == nil || meta["received_at"] == "" {
		t.Error("received_at is missing")
	}
	if meta["remote_addr"] == nil || meta["remote_addr"] == "" {
		t.Error("remote_addr is missing")
	}
	if meta["proto"] == nil || meta["proto"] == "" {
		t.Error("proto is missing")
	}
	if got := meta["body_length"]; got != float64(len(sampleLaunchRequest)) {
		t.Errorf("body_length = %v, want %d", got, len(sampleLaunchRequest))
	}
	if meta["truncated"] != false {
		t.Errorf("truncated = %v, want false", meta["truncated"])
	}

	recorded, ok := meta["headers"].(map[string]any)
	if !ok {
		t.Fatalf("headers = %v, want an object", meta["headers"])
	}
	for _, name := range []string{"Signature-256", "Signaturecertchainurl", "Content-Type"} {
		if _, present := recorded[name]; !present {
			t.Errorf("headers is missing %q; got keys %v", name, keysOf(recorded))
		}
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

	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("removing the capture directory: %v", err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("blocking the capture directory: %v", err)
	}

	rec := postAlexa(t, newRouter(testLogger(), store), sampleLaunchRequest, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /alexa status = %d, want %d even though the capture failed", rec.Code, http.StatusOK)
	}
	var envelope map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if envelope["version"] != "1.0" {
		t.Errorf("version = %v, want 1.0", envelope["version"])
	}
}

// The endpoint is public and unauthenticated for this phase, so an unbounded
// read is a memory-exhaustion vector. Over the limit, what was read is still
// captured and the sidecar says so.
func TestAlexaBoundsTheBodyAndRecordsTruncation(t *testing.T) {
	store, dir := newTestCapture(t)

	oversized := "{\"pad\":\"" + strings.Repeat("x", maxBodyBytes) + "\"}"
	rec := postAlexa(t, newRouter(testLogger(), store), oversized, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /alexa status = %d, want %d", rec.Code, http.StatusOK)
	}

	stem := onlyCapturedStem(t, dir)
	body, meta := readCapturePair(t, dir, stem)

	if len(body) > maxBodyBytes {
		t.Errorf("captured %d bytes, want at most %d", len(body), maxBodyBytes)
	}
	if meta["truncated"] != true {
		t.Errorf("truncated = %v, want true", meta["truncated"])
	}
	if got := meta["body_length"]; got != float64(len(body)) {
		t.Errorf("body_length = %v, want %d", got, len(body))
	}
}

// Regression watchpoint: adding /alexa must not shadow /healthz.
func TestHealthzStillAnswersAlongsideAlexa(t *testing.T) {
	store, _ := newTestCapture(t)
	router := newRouter(testLogger(), store)

	postAlexa(t, router, sampleLaunchRequest, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET /healthz status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// Alexa only ever POSTs. Anything else is a probe, and must not create a
// capture file: the disk is the scarce resource on an open endpoint.
func TestGetAlexaIsRejectedAndCapturesNothing(t *testing.T) {
	store, dir := newTestCapture(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/alexa", nil)
	newRouter(testLogger(), store).ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Errorf("GET /alexa status = %d, want a rejection", rec.Code)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the capture directory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("GET /alexa left %d files in the capture directory, want 0", len(entries))
	}
}
