package alexaverify

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// errWarmNotImplemented remains until the in-memory cache lands. Warm cannot
// report success before there is somewhere to retain the fetched chain.
var errWarmNotImplemented = errors.New("alexaverify: cache warming not implemented")

// Verifier checks that a request was signed by Alexa.
//
// The zero value is usable but unvalidated; prefer [New], which rejects
// nonsensical configuration up front. The four exported fields are the
// package's only configuration, and three of them exist solely as test seams.
// None of them can disable verification: see the package documentation.
//
// A Verifier is safe for concurrent use.
type Verifier struct {
	// Roots is the pool of trusted certificate authorities the chain must
	// validate to. A nil Roots means the host's system pool, which is what
	// production always uses; tests inject a pool containing a generated test
	// root so the crypto path can be exercised without the network.
	Roots *x509.CertPool

	// Now supplies the current time. A nil Now means time.Now. Nothing else in
	// this package reads the clock directly, so a test can pin it.
	Now func() time.Time

	// HTTPClient fetches the certificate chain. A nil HTTPClient means a
	// bounded default. Tests point this at an httptest server.
	HTTPClient *http.Client

	// Tolerance is the freshness window for standard requests, applied in both
	// directions. Values above the 150s ceiling are clamped; a non-positive
	// value means the default. Skill event request types always use their own
	// one hour window regardless of this setting.
	Tolerance time.Duration

	log *slog.Logger
}

// Option configures a [Verifier] passed to [New].
type Option func(*Verifier) error

// New builds a Verifier, applying opts and validating the result.
//
// A negative tolerance is an error. A tolerance above the 150 second ceiling
// is clamped to it and logged at warn, matching the Java SDK's constructor
// behaviour rather than failing an otherwise workable configuration.
func New(opts ...Option) (*Verifier, error) {
	v := &Verifier{Tolerance: defaultTolerance}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(v); err != nil {
			return nil, err
		}
	}

	if v.log == nil {
		v.log = slog.Default()
	}
	if v.Tolerance < 0 {
		return nil, fmt.Errorf("alexaverify: a negative tolerance (%s) is not supported", v.Tolerance)
	}
	if v.Tolerance > maxTolerance {
		v.log.Warn("clamping the alexa timestamp tolerance to the maximum allowed",
			slog.Duration("requested", v.Tolerance),
			slog.Duration("maximum", maxTolerance))
		v.Tolerance = maxTolerance
	}
	return v, nil
}

// WithRoots sets the trusted root pool. This is a test seam; production leaves
// it nil so the host's system pool is used.
func WithRoots(roots *x509.CertPool) Option {
	return func(v *Verifier) error {
		v.Roots = roots
		return nil
	}
}

// WithClock sets the time source. This is a test seam.
func WithClock(now func() time.Time) Option {
	return func(v *Verifier) error {
		v.Now = now
		return nil
	}
}

// WithHTTPClient sets the client used to fetch certificate chains. This is a
// test seam.
func WithHTTPClient(client *http.Client) Option {
	return func(v *Verifier) error {
		v.HTTPClient = client
		return nil
	}
}

// WithTolerance sets the freshness window for standard requests.
func WithTolerance(d time.Duration) Option {
	return func(v *Verifier) error {
		v.Tolerance = d
		return nil
	}
}

// WithLogger sets the logger used for operational warnings, such as a clamped
// tolerance. It does not affect any verification verdict.
func WithLogger(log *slog.Logger) Option {
	return func(v *Verifier) error {
		v.log = log
		return nil
	}
}

// Verify reports whether body, as received on the wire, was signed by Alexa.
//
// body must be the exact bytes read from the request: the signature is
// computed over them, so a decoded-and-re-encoded copy will not verify. hdr is
// the request's header set; both Signature-256 and SignatureCertChainUrl must
// be present.
//
// Checks run in the order timestamp then signature. The timestamp check is
// local; the signature check may reach out to S3 for the certificate chain, so
// running it second means a stale or replayed request cannot make this process
// open an outbound connection.
//
// A nil return means the request is authentic. Every non-nil return wraps
// exactly one of the sentinels in this package.
func (v *Verifier) Verify(ctx context.Context, body []byte, hdr http.Header) error {
	// http.Header.Get canonicalises the key, which gives us the
	// case-insensitive lookup the Node adapter implements by hand.
	signature := hdr.Get(signatureHeader)
	if signature == "" {
		return fmt.Errorf("%w: %s", ErrMissingHeader, signatureHeader)
	}
	certChainURL := hdr.Get(certChainURLHeader)
	if certChainURL == "" {
		return fmt.Errorf("%w: %s", ErrMissingHeader, certChainURLHeader)
	}

	if err := verifyTimestamp(body, v.now(), v.tolerance()); err != nil {
		return err
	}

	return v.verifySignature(ctx, body, signature, certChainURL)
}

// verifySignature checks the Signature-256 header against the certificate
// chain published at certChainURL.
func (v *Verifier) verifySignature(ctx context.Context, body []byte, signature, certChainURL string) error {
	// The URL decides where this process makes an outbound HTTPS request, so it
	// is validated before anything touches the network. A rejected URL must
	// never open a connection.
	canonicalURL, err := normalizeCertChainURL(certChainURL)
	if err != nil {
		return err
	}

	bundle, err := v.fetchCertificateChain(ctx, canonicalURL)
	if err != nil {
		return err
	}
	chain, err := parsePEMChain(bundle)
	if err != nil {
		return err
	}
	if err := v.validateChain(chain, v.now()); err != nil {
		return err
	}
	if err := verifyCertificateHostname(chain.leaf); err != nil {
		return err
	}
	return verifyBodySignature(body, signature, chain.leaf)
}

// Warm populates the in-memory certificate cache from seedURL, so the first
// real request after a restart does not pay for a cold fetch inside Alexa's
// response budget.
//
// It is best effort by contract. Callers should log a failure and carry on:
// egress being unavailable at boot is not a reason to refuse to start.
func (v *Verifier) Warm(_ context.Context, _ string) error {
	return errWarmNotImplemented
}

// now reads the clock through the seam, defaulting to time.Now.
func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

// tolerance resolves the freshness window, defending a directly constructed
// Verifier against a nonsensical value. New performs the strict check.
func (v *Verifier) tolerance() time.Duration {
	switch {
	case v.Tolerance <= 0:
		return defaultTolerance
	case v.Tolerance > maxTolerance:
		return maxTolerance
	default:
		return v.Tolerance
	}
}
