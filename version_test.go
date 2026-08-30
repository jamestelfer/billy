package main

import (
	"strings"
	"testing"
)

// R4: `--version` must print a non-empty string, including for a plain
// `go build` where no ldflags are injected.
func TestBuildVersionIsNeverEmpty(t *testing.T) {
	t.Cleanup(restoreBuildVars(version, commit, date))
	version, commit, date = "", "", ""

	if got := buildVersion(); got == "" {
		t.Fatal("buildVersion() returned an empty string with no build vars set")
	}
}

func TestBuildVersionReportsInjectedBuildMetadata(t *testing.T) {
	t.Cleanup(restoreBuildVars(version, commit, date))
	version, commit, date = "1.2.3", "abc1234", "2026-08-30T00:00:00Z"

	got := buildVersion()
	for _, want := range []string{"1.2.3", "abc1234", "2026-08-30T00:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("buildVersion() = %q, want it to contain %q", got, want)
		}
	}
}

func restoreBuildVars(v, c, d string) func() {
	return func() { version, commit, date = v, c, d }
}
