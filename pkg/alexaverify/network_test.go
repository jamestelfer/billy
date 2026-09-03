//go:build network

package alexaverify

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

const networkCaptureBodyEnv = "BILLY_ALEXA_CAPTURE_BODY"

// TestRealCapturedRequest verifies one out-of-tree Alexa capture against the
// live S3 object and the host's system root pool. It is intentionally excluded
// from CI: Amazon certificate rotation is expected to make an old capture fail.
// Point BILLY_ALEXA_CAPTURE_BODY at a .body file whose .json sidecar sits next
// to it; neither file is copied, printed or committed by this test.
func TestRealCapturedRequest(t *testing.T) {
	bodyPath := os.Getenv(networkCaptureBodyEnv)
	if bodyPath == "" {
		t.Skipf("set %s to an out-of-tree capture", networkCaptureBodyEnv)
	}
	if !strings.HasSuffix(bodyPath, ".body") {
		t.Fatalf("%s must name a .body file", networkCaptureBodyEnv)
	}

	body, err := os.ReadFile(bodyPath)
	if err != nil {
		t.Fatalf("reading capture body: %v", err)
	}
	sidecar, err := os.ReadFile(strings.TrimSuffix(bodyPath, ".body") + ".json")
	if err != nil {
		t.Fatalf("reading capture sidecar: %v", err)
	}
	var metadata struct {
		Headers http.Header `json:"headers"`
	}
	if err := json.Unmarshal(sidecar, &metadata); err != nil {
		t.Fatalf("decoding capture sidecar: %v", err)
	}
	var envelope struct {
		Request struct {
			Timestamp time.Time `json:"timestamp"`
		} `json:"request"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decoding capture timestamp: %v", err)
	}
	if envelope.Request.Timestamp.IsZero() {
		t.Fatal("capture has no request timestamp")
	}

	verifier, err := New(WithClock(at(envelope.Request.Timestamp)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := verifier.Verify(t.Context(), body, metadata.Headers); err != nil {
		t.Fatalf("Verify live capture: %v", err)
	}
}
