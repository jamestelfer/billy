package main

import (
	"encoding/json/v2"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jamestelfer/billy/pkg/alexaverify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R7: /healthz is a liveness probe — 200, no auth, and nothing sensitive in
// the body. It must not leak the version, the hostname, the tailnet name or
// any filesystem path.
func TestHealthzReturns200WithNonSensitiveBody(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)

	newRouter(testLogger(), mustCaptureStore(t), mustVerifier(t), mustBook(t)).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "GET /healthz status = %d, want %d", rec.Code, http.StatusOK)

	body := rec.Body.String()
	assert.NotEmpty(t, strings.TrimSpace(body), "GET /healthz returned an empty body; want a fixed non-empty one")
	for _, secret := range []string{buildVersion(), "ts.net", "/", "\\"} {
		assert.NotContains(t, body, secret, "GET /healthz body %q leaks %q", body, secret)
	}
}

func TestAlexaResponseEscapesHTMLAndPreservesSpeech(t *testing.T) {
	const speech = `<script>alert("hello")</script> & goodbye`
	rec := httptest.NewRecorder()

	writeAlexaResponse(rec, testLogger(), newAlexaEnvelope(speech))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.NotContains(t, rec.Body.String(), "<")
	assert.NotContains(t, rec.Body.String(), ">")
	assert.NotContains(t, rec.Body.String(), "&")
	var envelope alexaEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	require.NotNil(t, envelope.Response.OutputSpeech)
	assert.Equal(t, speech, envelope.Response.OutputSpeech.Text)
}

func TestMediaRouteServesTheConfiguredFileAsMPEG(t *testing.T) {
	configuredBook := bookWithMedia(t, []byte("complete media bytes"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/media/book.mp3", nil)

	newRouter(testLogger(), mustCaptureStore(t), mustVerifier(t), configuredBook).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "audio/mpeg", rec.Header().Get("Content-Type"))
	assert.Equal(t, []byte("complete media bytes"), rec.Body.Bytes())
}

func TestMediaRouteSupportsHeadAndByteRanges(t *testing.T) {
	configuredBook := bookWithMedia(t, []byte("0123456789"))
	router := newRouter(testLogger(), mustCaptureStore(t), mustVerifier(t), configuredBook)

	tests := map[string]struct {
		method           string
		rangeHeader      string
		wantStatus       int
		wantBody         string
		wantLength       string
		wantRange        string
		wantAcceptRanges string
	}{
		"head": {
			method: http.MethodHead, wantStatus: http.StatusOK, wantLength: "10", wantAcceptRanges: "bytes",
		},
		"partial": {
			method: http.MethodGet, rangeHeader: "bytes=2-5", wantStatus: http.StatusPartialContent,
			wantBody: "2345", wantLength: "4", wantRange: "bytes 2-5/10", wantAcceptRanges: "bytes",
		},
		"unsatisfiable": {
			method: http.MethodGet, rangeHeader: "bytes=20-30", wantStatus: http.StatusRequestedRangeNotSatisfiable,
			wantBody: "invalid range", wantRange: "bytes */10",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), tc.method, "/media/book.mp3", nil)
			if tc.rangeHeader != "" {
				req.Header.Set("Range", tc.rangeHeader)
			}

			router.ServeHTTP(rec, req)

			require.Equal(t, tc.wantStatus, rec.Code)
			assert.Contains(t, rec.Body.String(), tc.wantBody)
			assert.Equal(t, tc.wantLength, rec.Header().Get("Content-Length"))
			assert.Equal(t, tc.wantRange, rec.Header().Get("Content-Range"))
			assert.Equal(t, tc.wantAcceptRanges, rec.Header().Get("Accept-Ranges"))
		})
	}
}

func TestMediaRouteRemainsConfinedAfterStartupValidation(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "book")
	require.NoError(t, os.Mkdir(dir, 0o700))
	mediaName := filepath.Join(dir, "book.mp3")
	require.NoError(t, os.WriteFile(mediaName, []byte("configured media"), 0o600))
	descriptorName := filepath.Join(dir, "book.json")
	require.NoError(t, os.WriteFile(descriptorName, []byte(`{"title":"Story","mp3":"book.mp3"}`), 0o600))
	configuredBook, err := loadBook(descriptorName)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, configuredBook.Close()) })

	require.NoError(t, os.Remove(mediaName))
	outside := filepath.Join(parent, "outside.mp3")
	require.NoError(t, os.WriteFile(outside, []byte("must not escape"), 0o600))
	if err := os.Symlink(outside, mediaName); err != nil {
		t.Skipf("symbolic links are unavailable on this test host: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/media/book.mp3", nil)
	newRouter(testLogger(), mustCaptureStore(t), mustVerifier(t), configuredBook).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "must not escape")
}

func TestMediaRouteCannotSelectAnotherFile(t *testing.T) {
	configuredBook := bookWithMedia(t, []byte("configured media"))
	router := newRouter(testLogger(), mustCaptureStore(t), mustVerifier(t), configuredBook)

	queryRec := httptest.NewRecorder()
	queryReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/media/book.mp3?file=other.mp3", nil)
	queryReq.Header.Set("X-Book-Path", "other.mp3")
	router.ServeHTTP(queryRec, queryReq)
	require.Equal(t, http.StatusOK, queryRec.Code)
	assert.Equal(t, "configured media", queryRec.Body.String())

	otherRec := httptest.NewRecorder()
	otherReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/media/other.mp3", nil)
	router.ServeHTTP(otherRec, otherReq)
	assert.Equal(t, http.StatusNotFound, otherRec.Code)
}

func TestUnknownPathReturns404(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/not-a-route", nil)

	newRouter(testLogger(), mustCaptureStore(t), mustVerifier(t), mustBook(t)).ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code, "GET /not-a-route status = %d, want %d", rec.Code, http.StatusNotFound)
}

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func mustCaptureStore(t *testing.T) *captureStore {
	t.Helper()
	store, err := newCaptureStore(t.TempDir())
	require.NoError(t, err, "newCaptureStore() error = %v", err)
	return store
}

// mustVerifier builds a verifier with production defaults for tests that care
// about routing rather than verification.
func mustBook(t *testing.T) *book {
	t.Helper()
	return bookWithMedia(t, []byte("test audio"))
}

func bookWithMedia(t *testing.T, media []byte) *book {
	t.Helper()
	return bookWithMetadata(t, "Test Book", "", media)
}

func bookWithMetadata(t *testing.T, title, author string, media []byte) *book {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "book.mp3"), media, 0o600))
	descriptor := map[string]string{"title": title, "mp3": "book.mp3"}
	if author != "" {
		descriptor["author"] = author
	}
	descriptorJSON, err := json.Marshal(descriptor)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "book.json"), descriptorJSON, 0o600))
	configuredBook, err := loadBook(filepath.Join(dir, "book.json"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, configuredBook.Close()) })
	return configuredBook
}

func mustVerifier(t *testing.T) *alexaverify.Verifier {
	t.Helper()
	verifier, err := alexaverify.New(alexaverify.WithLogger(testLogger()))
	require.NoError(t, err, "alexaverify.New() error = %v", err)
	return verifier
}
