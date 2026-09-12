package main

import (
	"crypto/rand"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// captureFilenameLayout is a UTC timestamp layout chosen so the resulting
// filename is legal on every target platform. RFC3339 cannot be used: it
// contains colons, which Windows rejects outright. Nanosecond precision and
// leading zeros keep stems sorting in receipt order lexically.
const captureFilenameLayout = "20060102T150405.000000000Z"

// captureMetadata is the sidecar written alongside every captured body. It
// records everything needed to reason about the request later without having
// to trust the body itself — in particular the signature headers, which are
// the whole reason this corpus exists.
//
// There is no "truncated" field. Only verified requests are captured, and a
// truncated body cannot verify: the signature covers bytes that are no longer
// all present. A truncated capture is therefore unreachable rather than merely
// unusual, so recording the possibility would be misleading.
type captureMetadata struct {
	ReceivedAt    time.Time   `json:"received_at"`
	Method        string      `json:"method"`
	RequestURI    string      `json:"request_uri"`
	Proto         string      `json:"proto"`
	RemoteAddr    string      `json:"remote_addr"`
	Host          string      `json:"host"`
	ContentLength int64       `json:"content_length"`
	BodyLength    int         `json:"body_length"`
	Headers       http.Header `json:"headers"`
}

// captureStore writes captured requests to a directory, one raw body file and
// one JSON sidecar per request, sharing a stem.
type captureStore struct {
	dir string
}

// newCaptureStore prepares dir to receive captures.
//
// The 0700 mode is requested but is not what actually protects the contents:
// Go's permission bits are largely inert on Windows. The real protection is
// the default location — under os.UserConfigDir, which is already
// ACL-restricted to the user.
func newCaptureStore(dir string) (*captureStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating the capture directory: %w", err)
	}
	return &captureStore{dir: dir}, nil
}

// Save writes body verbatim to <stem>.body and meta to <stem>.json, returning
// the shared stem.
//
// body is written with no intermediate decode: the Alexa signature is
// computed over the exact bytes on the wire, so a round-tripped body is
// useless as a corpus.
func (s *captureStore) Save(meta captureMetadata, body []byte) (string, error) {
	stem, err := captureStem(meta.ReceivedAt)
	if err != nil {
		return "", err
	}

	// Encode before writing either file. JSON v2 rejects invalid UTF-8, which
	// can occur in otherwise valid HTTP header values; an encoding failure must
	// not leave an orphaned body in the capture corpus.
	sidecar, err := json.Marshal(meta, jsontext.WithIndent("  "))
	if err != nil {
		return "", fmt.Errorf("encoding the capture metadata: %w", err)
	}

	bodyPath := filepath.Join(s.dir, stem+".body")
	sidecarPath := filepath.Join(s.dir, stem+".json")
	if err := os.WriteFile(bodyPath, body, 0o600); err != nil {
		return "", errors.Join(
			fmt.Errorf("writing the captured body: %w", err),
			removeCaptureFiles(bodyPath),
		)
	}
	if err := os.WriteFile(sidecarPath, sidecar, 0o600); err != nil {
		return "", errors.Join(
			fmt.Errorf("writing the capture metadata: %w", err),
			removeCaptureFiles(bodyPath, sidecarPath),
		)
	}

	return stem, nil
}

func removeCaptureFiles(paths ...string) error {
	var cleanupErr error
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("removing partial capture %s: %w", path, err))
		}
	}
	return cleanupErr
}

// captureStem builds a time-ordered, collision-free, cross-platform-legal
// filename stem. The random suffix is what makes it collision-free: two
// requests can share a timestamp, and losing one to an overwrite would lose a
// corpus entry silently.
func captureStem(at time.Time) (string, error) {
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("generating a capture filename suffix: %w", err)
	}
	return fmt.Sprintf("%s-%x", at.UTC().Format(captureFilenameLayout), suffix), nil
}
