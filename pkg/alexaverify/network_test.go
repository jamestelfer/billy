//go:build network

package alexaverify

import (
	"encoding/json/v2"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
	require.Regexp(t, `\.body$`, bodyPath, "%s must name a .body file", networkCaptureBodyEnv)

	body, err := os.ReadFile(bodyPath)
	require.NoError(t, err, "reading capture body: %v", err)
	sidecar, err := os.ReadFile(strings.TrimSuffix(bodyPath, ".body") + ".json")
	require.NoError(t, err, "reading capture sidecar: %v", err)
	var metadata struct {
		Headers http.Header `json:"headers"`
	}
	require.NoError(t, json.Unmarshal(sidecar, &metadata), "decoding capture sidecar")
	var envelope struct {
		Request struct {
			Timestamp time.Time `json:"timestamp"`
		} `json:"request"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope), "decoding capture timestamp")
	require.NotZero(t, envelope.Request.Timestamp, "capture has no request timestamp")

	verifier, err := New(WithClock(at(envelope.Request.Timestamp)))
	require.NoError(t, err, "New: %v", err)
	require.NoError(t, verifier.Verify(t.Context(), body, metadata.Headers), "Verify live capture")
}
