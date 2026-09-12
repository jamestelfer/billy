package main

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
)

// config is the service's runtime configuration, resolved entirely from the
// environment. Service settings are not read from a configuration file and
// there are no flags beyond --version: everything a host needs to set is an
// environment variable. The external book descriptor is content named by one
// of those settings, not a second source of service configuration.
type config struct {
	// Hostname is the tailnet node name, and so decides the Funnel URL. It
	// must be stable across restarts: the URL is pinned in the Alexa
	// developer console.
	Hostname string

	// AuthKey registers the node with the tailnet. It is only required on
	// first run — afterwards the node identity lives in StateDir. It is a
	// credential and must never be logged.
	AuthKey string

	// StateDir holds the tsnet node state. Losing it means losing the node
	// identity, and with it the Funnel URL.
	StateDir string

	// CaptureDir holds the captured Alexa requests. Its contents are secret.
	CaptureDir string

	// CertChainURL seeds the in-memory Alexa signing certificate cache at
	// startup. Warming is best effort and never delays readiness.
	CertChainURL string

	// BookDescriptor names the external JSON descriptor for the one book this
	// process serves. It is required whenever the service starts.
	BookDescriptor string

	// Addr is the Funnel listen address. Funnel permits only 443, 8443 and
	// 10000, and Alexa requires 443.
	Addr string
}

// Environment variable names. TS_AUTHKEY follows Tailscale's own convention
// so an operator who already has a key exported does not have to rename it.
const (
	envAuthKey        = "TS_AUTHKEY"
	envHostname       = "BILLY_HOSTNAME"
	envStateDir       = "BILLY_STATE_DIR"
	envCaptureDir     = "BILLY_CAPTURE_DIR"
	envCertChainURL   = "BILLY_CERT_CHAIN_URL"
	envBookDescriptor = "BILLY_BOOK_DESCRIPTOR"
)

const (
	defaultHostname = "billy"

	// This is a public S3 object URL observed on genuine Alexa requests. A
	// rotation merely makes the best-effort warm fail or seed an unused key;
	// the first request then fetches the URL carried in that request as usual.
	defaultCertChainURL = "https://s3.amazonaws.com/echo.api/echo-api-cert-USAmazon-prod-30584302.pem"
)

// loadConfig resolves the configuration from getenv, defaulting directories
// under userConfigDir.
//
// getenv and userConfigDir are parameters rather than direct calls to
// os.Getenv and os.UserConfigDir so the resolution is testable without
// mutating process state.
//
// Defaulting under the user config directory is what makes the capture
// directory private on every platform: %AppData% on Windows is already
// ACL-restricted to the user, which the 0700 mode used at creation time is
// not — Go's permission bits are largely inert there.
func loadConfig(getenv func(string) string, userConfigDir string) (config, error) {
	cfg := config{
		Hostname:       orDefault(getenv(envHostname), defaultHostname),
		AuthKey:        getenv(envAuthKey),
		StateDir:       orDefault(getenv(envStateDir), filepath.Join(userConfigDir, "billy", "tsnet")),
		CaptureDir:     orDefault(getenv(envCaptureDir), filepath.Join(userConfigDir, "billy", "capture")),
		CertChainURL:   orDefault(getenv(envCertChainURL), defaultCertChainURL),
		BookDescriptor: getenv(envBookDescriptor),
		Addr:           ":443",
	}
	if strings.TrimSpace(cfg.BookDescriptor) == "" {
		return config{}, fmt.Errorf("%s is required and must name a book descriptor", envBookDescriptor)
	}
	if err := validHostname(cfg.Hostname); err != nil {
		return config{}, fmt.Errorf("%s: %w", envHostname, err)
	}
	return cfg, nil
}

// validHostname checks the tailnet node name is usable as a DNS label. A bad
// value otherwise fails deep inside tsnet at listen time, well after the
// operator has stopped watching the terminal.
func validHostname(h string) error {
	if h == "" || len(h) > 63 {
		return fmt.Errorf("hostname %q must be 1-63 characters", h)
	}
	if strings.HasPrefix(h, "-") || strings.HasSuffix(h, "-") {
		return fmt.Errorf("hostname %q must not start or end with a hyphen", h)
	}
	for _, r := range h {
		isAllowed := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-'
		if !isAllowed {
			return fmt.Errorf("hostname %q must contain only letters, digits and hyphens", h)
		}
	}
	return nil
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// LogValue implements slog.LogValuer so that logging the config can never
// disclose the auth key, however carelessly it is logged.
func (c config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("hostname", c.Hostname),
		slog.Bool("auth_key_present", c.AuthKey != ""),
		slog.String("state_dir", c.StateDir),
		slog.String("capture_dir", c.CaptureDir),
		slog.String("cert_chain_url", c.CertChainURL),
		slog.String("book_descriptor", c.BookDescriptor),
		slog.String("addr", c.Addr),
	)
}
