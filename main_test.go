package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	code := m.Run()
	dirty, err := snaps.Clean(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cleaning snapshots:", err)
		os.Exit(1)
	}
	if dirty {
		code = 1
	}
	os.Exit(code)
}

// R4: the binary invoked with --version prints a non-empty version string and
// exits successfully.
func TestRunVersionFlagPrintsVersionAndSucceeds(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"billy", "--version"}, &stdout, &stderr)

	require.Equal(t, 0, code, "run(--version) exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	assert.NotEmpty(t, strings.TrimSpace(stdout.String()), "run(--version) printed nothing to stdout")
	assert.Contains(t, stdout.String(), buildVersion(), "run(--version) printed %q, want it to contain %q", stdout.String(), buildVersion())
}

// --help must not start the service, and must go to stdout so it can be piped.
func TestRunHelpFlagPrintsUsageAndSucceeds(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"billy", "--help"}, &stdout, &stderr)

	require.Equal(t, 0, code, "run(--help) exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	assert.Contains(t, stdout.String(), "billy", "run(--help) stdout = %q, want the command name in it", stdout.String())
}

func TestRunFailsBeforeServingWhenBookDescriptorSettingIsMissing(t *testing.T) {
	t.Setenv(envBookDescriptor, "")
	var stdout, stderr bytes.Buffer

	code := run([]string{"billy"}, &stdout, &stderr)

	require.Equal(t, exitFailure, code)
	assert.Contains(t, stderr.String(), envBookDescriptor)
}

func TestRunFailsBeforeServingWhenBookMediaIsMissing(t *testing.T) {
	descriptorName := filepath.Join(t.TempDir(), "book.json")
	require.NoError(t, os.WriteFile(descriptorName, []byte(`{"title":"Story","mp3":"missing.mp3"}`), 0o600))
	t.Setenv(envBookDescriptor, descriptorName)
	var stdout, stderr bytes.Buffer

	code := run([]string{"billy"}, &stdout, &stderr)

	require.Equal(t, exitFailure, code)
	assert.Contains(t, stderr.String(), "book media")
}

func TestRunUnknownFlagFailsWithDiagnostic(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"billy", "--nope"}, &stdout, &stderr)

	require.NotEqual(t, 0, code, "run(--nope) exit code = 0, want non-zero")
	assert.NotEqual(t, 0, stderr.Len(), "run(--nope) wrote no diagnostic to stderr")
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
			code := run(args, &stdout, &stderr)
			require.Equal(t, exitUsage, code, "run(%q) exit code = %d, want %d", args[1:], code, exitUsage)
		})
	}
}
