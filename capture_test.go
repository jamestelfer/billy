package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readCapturePair(t *testing.T, dir, stem string) ([]byte, map[string]any) {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(dir, stem+".body"))
	if err != nil {
		t.Fatalf("reading the body file: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, stem+".json"))
	if err != nil {
		t.Fatalf("reading the sidecar: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("sidecar is not valid JSON: %v", err)
	}
	return body, meta
}

// R8: the body is persisted byte-for-byte. The Alexa signature is computed
// over the raw bytes, so anything that reformats, re-marshals or normalises
// the body makes the whole corpus worthless.
func TestSaveWritesTheBodyByteForByte(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "capture")
	store, err := newCaptureStore(dir)
	if err != nil {
		t.Fatalf("newCaptureStore() error = %v", err)
	}

	// Deliberately ugly: trailing whitespace, CRLF, duplicate keys and a
	// non-ASCII escape all survive a byte-exact write and none survive a
	// round trip through encoding/json.
	raw := []byte("{\r\n  \"version\" : \"1.0\",\r\n  \"a\": 1, \"a\": 2,\r\n  \"t\": \"caf\\u00e9\"  \r\n}\r\n")

	stem, err := store.Save(captureMetadata{ReceivedAt: time.Now().UTC()}, raw)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, _ := readCapturePair(t, dir, stem)
	if string(got) != string(raw) {
		t.Errorf("persisted body = %q, want the exact bytes %q", got, raw)
	}
}

// The stem has to be legal on every target platform. Windows is the strictest
// and the most likely host: it rejects colons outright, which rules out a
// bare RFC3339 timestamp.
func TestSaveUsesFilenamesLegalOnWindows(t *testing.T) {
	dir := t.TempDir()
	store, err := newCaptureStore(dir)
	if err != nil {
		t.Fatalf("newCaptureStore() error = %v", err)
	}

	stem, err := store.Save(captureMetadata{ReceivedAt: time.Now().UTC()}, []byte("{}"))
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if strings.ContainsAny(stem, `<>:"/\|?*`) {
		t.Errorf("stem %q contains a character Windows rejects", stem)
	}
	if strings.HasSuffix(stem, ".") || strings.HasSuffix(stem, " ") {
		t.Errorf("stem %q ends with a dot or space, which Windows rejects", stem)
	}
	reserved := map[string]bool{
		"CON": true, "PRN": true, "AUX": true, "NUL": true,
		"COM1": true, "COM2": true, "LPT1": true, "LPT2": true,
	}
	if reserved[strings.ToUpper(stem)] {
		t.Errorf("stem %q is a reserved Windows device name", stem)
	}
}

// Alexa can send bursts, and the whole point is a corpus: one capture must
// never overwrite another, even within the same nanosecond tick.
func TestSaveNeverCollides(t *testing.T) {
	dir := t.TempDir()
	store, err := newCaptureStore(dir)
	if err != nil {
		t.Fatalf("newCaptureStore() error = %v", err)
	}

	at := time.Date(2026, 8, 30, 1, 2, 3, 456, time.UTC)
	seen := map[string]bool{}
	for i := range 50 {
		stem, err := store.Save(captureMetadata{ReceivedAt: at}, []byte("{}"))
		if err != nil {
			t.Fatalf("Save() #%d error = %v", i, err)
		}
		if seen[stem] {
			t.Fatalf("Save() #%d reused stem %q", i, stem)
		}
		seen[stem] = true
	}
}

// Stems sort in receipt order, so a plain directory listing reads as a
// timeline.
func TestSaveStemsSortInReceiptOrder(t *testing.T) {
	dir := t.TempDir()
	store, err := newCaptureStore(dir)
	if err != nil {
		t.Fatalf("newCaptureStore() error = %v", err)
	}

	earlier, err := store.Save(captureMetadata{ReceivedAt: time.Date(2026, 8, 30, 1, 0, 0, 0, time.UTC)}, []byte("{}"))
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	later, err := store.Save(captureMetadata{ReceivedAt: time.Date(2026, 8, 30, 2, 0, 0, 0, time.UTC)}, []byte("{}"))
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if earlier >= later {
		t.Errorf("stems do not sort in receipt order: %q >= %q", earlier, later)
	}
}
