package main

import (
	"bytes"
	"strings"
	"testing"
)

// R4: the binary invoked with --version prints a non-empty version string and
// exits successfully.
func TestRunVersionFlagPrintsVersionAndSucceeds(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"billy", "--version"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run(--version) exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) == "" {
		t.Error("run(--version) printed nothing to stdout")
	}
	if !strings.Contains(stdout.String(), buildVersion()) {
		t.Errorf("run(--version) printed %q, want it to contain %q", stdout.String(), buildVersion())
	}
}

// --help must not start the service, and must go to stdout so it can be piped.
func TestRunHelpFlagPrintsUsageAndSucceeds(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"billy", "--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run(--help) exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "billy") {
		t.Errorf("run(--help) stdout = %q, want the command name in it", stdout.String())
	}
}

func TestRunUnknownFlagFailsWithDiagnostic(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"billy", "--nope"}, &stdout, &stderr)

	if code == 0 {
		t.Fatal("run(--nope) exit code = 0, want non-zero")
	}
	if stderr.Len() == 0 {
		t.Error("run(--nope) wrote no diagnostic to stderr")
	}
}

// A usage mistake is the operator's, not a crash: it gets its own exit code so
// a supervisor does not treat it as something a restart could fix.
func TestRunUsageErrorsExitWithTheUsageCode(t *testing.T) {
	for _, args := range [][]string{
		{"billy", "--nope"},
		{"billy", "unexpected-argument"},
	} {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(args, &stdout, &stderr); code != exitUsage {
				t.Fatalf("run(%q) exit code = %d, want %d", args[1:], code, exitUsage)
			}
		})
	}
}
