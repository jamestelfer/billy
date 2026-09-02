package alexaverify

import (
	"encoding/json"
	"fmt"
	"time"
)

// timestampEnvelope is the smallest decode of an Alexa request envelope that
// the freshness check needs. It is decoded from the same byte slice the
// signature is verified over — never from a re-encoding of it.
type timestampEnvelope struct {
	Request struct {
		Type      string `json:"type"`
		RequestID string `json:"requestId"`
		Timestamp string `json:"timestamp"`
	} `json:"request"`
}

// verifyTimestamp checks that the request envelope in body carries a timestamp
// within tolerance of now, in either direction.
//
// The absolute delta is Java's behaviour
// (SkillRequestTimestampVerifier.java:103). Node only checks the past
// direction, so a far-future timestamp passes there; under the intersection
// principle rejecting it costs nothing, because Java already does.
//
// The comparison is inclusive at the boundary, matching both SDKs.
func verifyTimestamp(body []byte, now time.Time, tolerance time.Duration) error {
	var env timestampEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("%w: request envelope did not decode: %w", ErrStaleTimestamp, err)
	}
	if env.Request.Timestamp == "" {
		return fmt.Errorf("%w: request envelope carried no timestamp", ErrStaleTimestamp)
	}

	ts, err := time.Parse(time.RFC3339, env.Request.Timestamp)
	if err != nil {
		return fmt.Errorf("%w: request timestamp did not parse: %w", ErrStaleTimestamp, err)
	}

	allowed := tolerance
	if skillEventRequestTypes[env.Request.Type] {
		// Skill events are delivered asynchronously and are documented as
		// arriving up to an hour late.
		allowed = skillEventTolerance
	}

	delta := now.Sub(ts)
	if delta < 0 {
		delta = -delta
	}
	if delta > allowed {
		return fmt.Errorf("%w: request %q of type %q is %s away from now, tolerance %s",
			ErrStaleTimestamp, env.Request.RequestID, env.Request.Type, delta, allowed)
	}
	return nil
}
