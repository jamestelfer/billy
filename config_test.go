package main

import (
	"path/filepath"
	"strings"
	"testing"
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
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	if cfg.Hostname != "billy" {
		t.Errorf("Hostname = %q, want %q", cfg.Hostname, "billy")
	}
	if cfg.AuthKey != "tskey-auth-secret" {
		t.Errorf("AuthKey = %q, want the value from TS_AUTHKEY", cfg.AuthKey)
	}
	if want := filepath.Join(userConfig, "billy", "tsnet"); cfg.StateDir != want {
		t.Errorf("StateDir = %q, want %q", cfg.StateDir, want)
	}
	if want := filepath.Join(userConfig, "billy", "capture"); cfg.CaptureDir != want {
		t.Errorf("CaptureDir = %q, want %q", cfg.CaptureDir, want)
	}
	if cfg.Addr != ":443" {
		t.Errorf("Addr = %q, want %q — Funnel and Alexa both require 443", cfg.Addr, ":443")
	}
}

func TestLoadConfigOverridesEveryDefault(t *testing.T) {
	stateDir := t.TempDir()
	captureDir := t.TempDir()

	cfg, err := loadConfig(env(map[string]string{
		"TS_AUTHKEY":        "tskey-auth-secret",
		"BILLY_HOSTNAME":    "echo-capture",
		"BILLY_STATE_DIR":   stateDir,
		"BILLY_CAPTURE_DIR": captureDir,
	}), t.TempDir())
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	if cfg.Hostname != "echo-capture" {
		t.Errorf("Hostname = %q, want %q", cfg.Hostname, "echo-capture")
	}
	if cfg.StateDir != stateDir {
		t.Errorf("StateDir = %q, want %q", cfg.StateDir, stateDir)
	}
	if cfg.CaptureDir != captureDir {
		t.Errorf("CaptureDir = %q, want %q", cfg.CaptureDir, captureDir)
	}
}

// An auth key already persisted in the state directory is enough: TS_AUTHKEY
// is only needed to register the node the first time. Requiring it on every
// start would push operators towards leaving the key in the environment
// permanently.
func TestLoadConfigAllowsAbsentAuthKey(t *testing.T) {
	cfg, err := loadConfig(env(nil), t.TempDir())
	if err != nil {
		t.Fatalf("loadConfig() with no TS_AUTHKEY error = %v, want nil", err)
	}
	if cfg.AuthKey != "" {
		t.Errorf("AuthKey = %q, want empty", cfg.AuthKey)
	}
}

// The auth key is a credential: it must never reach a log line, and slog
// resolves LogValuer on anything it is handed.
func TestConfigLogValueRedactsAuthKey(t *testing.T) {
	cfg, err := loadConfig(env(map[string]string{"TS_AUTHKEY": "tskey-auth-secret"}), t.TempDir())
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	rendered := cfg.LogValue().String()
	if strings.Contains(rendered, "tskey-auth-secret") {
		t.Errorf("config log value %q contains the auth key", rendered)
	}
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
			if err == nil {
				t.Fatalf("loadConfig() with %s hostname %q error = nil, want an error", name, hostname)
			}
		})
	}
}
