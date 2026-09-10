package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func env(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

// R5: the auth key is supplied via the environment. Everything else has a
// portable default derived from the user config directory, and every default
// is overridable.
func TestLoadConfigDefaults(t *testing.T) {
	userConfig := t.TempDir()

	cfg, err := loadConfig(env(map[string]string{"TS_AUTHKEY": "tskey-auth-secret"}), userConfig)
	require.NoError(t, err, "loadConfig() error = %v", err)

	assert.Equal(t, "billy", cfg.Hostname, "Hostname = %q, want %q", cfg.Hostname, "billy")
	assert.Equal(t, "tskey-auth-secret", cfg.AuthKey, "AuthKey = %q, want the value from TS_AUTHKEY", cfg.AuthKey)
	assert.Equal(t, filepath.Join(userConfig, "billy", "tsnet"), cfg.StateDir)
	assert.Equal(t, filepath.Join(userConfig, "billy", "capture"), cfg.CaptureDir)
	assert.Equal(t, defaultCertChainURL, cfg.CertChainURL, "CertChainURL = %q, want the observed Alexa URL %q", cfg.CertChainURL, defaultCertChainURL)
	assert.Equal(t, ":443", cfg.Addr, "Addr = %q, want %q — Funnel and Alexa both require 443", cfg.Addr, ":443")
}

func TestLoadConfigOverridesEveryDefault(t *testing.T) {
	stateDir := t.TempDir()
	captureDir := t.TempDir()
	certChainURL := "https://s3.amazonaws.com/echo.api/override.pem"

	cfg, err := loadConfig(env(map[string]string{
		"TS_AUTHKEY":           "tskey-auth-secret",
		"BILLY_HOSTNAME":       "echo-capture",
		"BILLY_STATE_DIR":      stateDir,
		"BILLY_CAPTURE_DIR":    captureDir,
		"BILLY_CERT_CHAIN_URL": certChainURL,
	}), t.TempDir())
	require.NoError(t, err, "loadConfig() error = %v", err)

	assert.Equal(t, "echo-capture", cfg.Hostname, "Hostname = %q, want %q", cfg.Hostname, "echo-capture")
	assert.Equal(t, stateDir, cfg.StateDir, "StateDir = %q, want %q", cfg.StateDir, stateDir)
	assert.Equal(t, captureDir, cfg.CaptureDir, "CaptureDir = %q, want %q", cfg.CaptureDir, captureDir)
	assert.Equal(t, certChainURL, cfg.CertChainURL, "CertChainURL = %q, want %q", cfg.CertChainURL, certChainURL)
}

// An auth key already persisted in the state directory is enough: TS_AUTHKEY
// is only needed to register the node the first time. Requiring it on every
// start would push operators towards leaving the key in the environment
// permanently.
func TestLoadConfigAllowsAbsentAuthKey(t *testing.T) {
	cfg, err := loadConfig(env(nil), t.TempDir())
	require.NoError(t, err, "loadConfig() with no TS_AUTHKEY error = %v, want nil", err)
	assert.Empty(t, cfg.AuthKey, "AuthKey = %q, want empty", cfg.AuthKey)
}

// The auth key is a credential: it must never reach a log line, and slog
// resolves LogValuer on anything it is handed.
func TestConfigLogValueRedactsAuthKey(t *testing.T) {
	cfg, err := loadConfig(env(map[string]string{"TS_AUTHKEY": "tskey-auth-secret"}), t.TempDir())
	require.NoError(t, err, "loadConfig() error = %v", err)

	rendered := cfg.LogValue().String()
	assert.NotContains(t, rendered, "tskey-auth-secret", "config log value %q contains the auth key", rendered)
}

// The hostname becomes a DNS label in the Funnel URL. A bad value fails
// deep inside tsnet at listen time, long after the operator has stopped
// looking at the terminal; reject it at startup instead.
func TestLoadConfigRejectsInvalidHostname(t *testing.T) {
	for name, hostname := range map[string]string{
		"leading hyphen":  "-billy",
		"trailing hyphen": "billy-",
		"underscore":      "billy_capture",
		"dot":             "billy.example",
		"too long":        strings.Repeat("b", 64),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadConfig(env(map[string]string{"BILLY_HOSTNAME": hostname}), t.TempDir())
			require.Error(t, err, "loadConfig() with %s hostname %q error = nil, want an error", name, hostname)
		})
	}
}
