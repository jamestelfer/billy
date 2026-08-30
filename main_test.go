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
