package alexaverify

import "time"

// Every magic value in this package lives here and is named after its
// counterpart in the reference SDKs, so a reviewer can diff the two side by
// side. The sources are pinned:
//
//	Java: alexa/alexa-skills-kit-sdk-for-java @ e7f16b0523b24a34a7971d4df7ecbb48b4539639
//	      ask-sdk-servlet-support/src/com/amazon/ask/servlet/ServletConstants.java
//	Node: alexa/alexa-skills-kit-sdk-for-nodejs @ ea88cef4a72a44abf1aff6083472ba08a665862a
//	      ask-sdk-express-adapter/lib/verifier/index.ts
//
// Both are Apache-2.0; see NOTICE at the repository root.
const (
	// signatureHeader carries the base64 signature over the raw body.
	// ServletConstants.SIGNATURE_REQUEST_HEADER. Only the SHA-256 header is
	// honoured; the legacy SHA-1 "Signature" header is not supported.
	signatureHeader = "Signature-256"

	// certChainURLHeader carries the URL of the PEM certificate chain.
	// ServletConstants.SIGNATURE_CERTIFICATE_CHAIN_URL_REQUEST_HEADER.
	certChainURLHeader = "SignatureCertChainUrl"

	// validCertScheme, validCertHost and validCertPathPrefix bound where a
	// certificate chain may be fetched from. The host comparison case-folds;
	// the path prefix is case-sensitive. Both SDKs agree on all three.
	validCertScheme     = "https"
	validCertHost       = "s3.amazonaws.com"
	validCertPathPrefix = "/echo.api/"

	// validCertPort is the only explicit port accepted on a chain URL. An
	// absent port is also accepted and means the same thing.
	validCertPort = "443"
)

const (
	// defaultTolerance is the freshness window applied when a caller does not
	// choose one. Java defaults to 30s and caps at 150s; Node uses 150s. The
	// looser of the two defaults is chosen deliberately: this service runs on
	// domestic hardware whose clock may be worse than a datacentre's, and 150s
	// is still inside what Amazon itself considers acceptable.
	defaultTolerance = 150 * time.Second

	// maxTolerance is the hard ceiling for standard requests.
	// ServletConstants.MAXIMUM_TOLERANCE_MILLIS.
	maxTolerance = 150 * time.Second

	// skillEventTolerance applies to the skill event request types below,
	// which Amazon may deliver up to an hour late by design.
	// ServletConstants.TOLERANCE_SKILL_EVENTS_MILLIS.
	skillEventTolerance = time.Hour
)

// skillEventRequestTypes is ServletConstants.SKILL_EVENT_REQUESTS, and the
// identical set in the Node adapter. It is an explicit set rather than an
// "AlexaSkillEvent." prefix match on purpose: a prefix would grant the hour-long
// window to request types neither SDK grants it to, which is looser than both
// references rather than stricter.
var skillEventRequestTypes = map[string]bool{
	"AlexaSkillEvent.SkillEnabled":            true,
	"AlexaSkillEvent.SkillDisabled":           true,
	"AlexaSkillEvent.SkillPermissionChanged":  true,
	"AlexaSkillEvent.SkillPermissionAccepted": true,
	"AlexaSkillEvent.SkillAccountLinked":      true,
}
