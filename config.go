package main

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
)

// config is the service's runtime configuration, resolved entirely from the
// environment. There are no configuration files and no flags beyond
// --version: everything a host needs to set is an environment variable, which
// is the one mechanism that works identically under systemd, launchd and the
// Windows Service Manager.
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

	// Addr is the Funnel listen address. Funnel permits only 443, 8443 and
	// 10000, and Alexa requires 443.
	Addr string
}

// Environment variable names. TS_AUTHKEY follows Tailscale's own convention
// so an operator who already has a key exported does not have to rename it.
const (
	envAuthKey    = "TS_AUTHKEY"
	envHostname   = "BILLY_HOSTNAME"
	envStateDir   = "BILLY_STATE_DIR"
	envCaptureDir = "BILLY_CAPTURE_DIR"
)

const defaultHostname = "billy"

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
		Hostname:   orDefault(getenv(envHostname), defaultHostname),
		AuthKey:    getenv(envAuthKey),
		StateDir:   orDefault(getenv(envStateDir), filepath.Join(userConfigDir, "billy", "tsnet")),
		CaptureDir: orDefault(getenv(envCaptureDir), filepath.Join(userConfigDir, "billy", "capture")),
		Addr:       ":443",
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
		slog.String("addr", c.Addr),
	)
}
