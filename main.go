// Command billy is a self-hosted Alexa skill endpoint for Audiobookshelf.
//
// At this stage it does one thing: reach the public internet over an embedded
// Tailscale Funnel listener and capture the raw bytes of every Alexa request,
// so those bytes can serve as the corpus for building signature verification.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jamestelfer/billy/pkg/alexaverify"
	"github.com/urfave/cli/v3"
	"tailscale.com/tsnet"
)

// Exit codes. Anything a supervisor might branch on lives here rather than
// being scattered as literals.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

// run is the whole program, parameterised on its arguments and output streams
// so it can be exercised from tests without a process boundary.
//
// There is exactly one flag, and there will not be many more: configuration is
// resolved from the environment (see config.go), which is the one mechanism
// that behaves identically under systemd, launchd and the Windows Service
// Manager. The CLI layer exists to give the binary a usable --help and a
// machine-readable --version, not to become a second configuration system.
func run(args []string, stdout, stderr io.Writer) int {
	log := slog.New(slog.NewTextHandler(stderr, nil))

	cmd := &cli.Command{
		Name:      "billy",
		Usage:     "a self-hosted Alexa skill endpoint for Audiobookshelf",
		Version:   buildVersion(),
		ArgsUsage: " ",
		Writer:    stdout,
		ErrWriter: stderr,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			// billy takes no positional arguments. Silently ignoring them, as
			// the standard library's flag package does, hides a typo in a unit
			// file until someone wonders why a setting had no effect.
			if cmd.Args().Present() {
				return cli.Exit(
					fmt.Sprintf("billy takes no arguments, got %q", cmd.Args().Slice()),
					exitUsage)
			}
			return serveFunnel(ctx, log)
		},
		// A usage error is the operator's mistake, not a runtime failure, and
		// gets its own exit code so a supervisor does not treat it as a crash
		// worth restarting.
		OnUsageError: func(_ context.Context, _ *cli.Command, err error, _ bool) error {
			return cli.Exit(err.Error(), exitUsage)
		},
		// The default handler writes to a package-global writer and calls
		// os.Exit. Both are unacceptable here: run is deliberately a pure
		// function of its arguments and streams so it can be tested without a
		// process boundary, and a library reaching for os.Exit takes that away.
		// Reporting and exit-code mapping happen below instead.
		ExitErrHandler: func(_ context.Context, _ *cli.Command, _ error) {},
	}

	// signal.NotifyContext is portable: os.Interrupt and syscall.SIGTERM are
	// both defined on Windows, so no build-tagged signal handling is needed.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := cmd.Run(ctx, args)
	if err == nil {
		return exitOK
	}

	if exit, ok := errors.AsType[cli.ExitCoder](err); ok {
		// A usage diagnostic is for a human at a terminal, so it goes out
		// plainly rather than as a structured log line.
		if msg := exit.Error(); msg != "" {
			fmt.Fprintln(stderr, msg) //nolint:errcheck // nothing useful to do if stderr is gone
		}
		return exit.ExitCode()
	}
	log.Error("service failed", slog.Any("error", err))
	return exitFailure
}

// serveFunnel resolves configuration, joins the tailnet and serves the routes
// over Funnel until ctx is cancelled.
func serveFunnel(ctx context.Context, log *slog.Logger) error {
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("resolving the user config directory: %w", err)
	}

	cfg, err := loadConfig(os.Getenv, userConfigDir)
	if err != nil {
		return err
	}
	log.Info("starting", slog.Any("config", cfg), slog.String("version", buildVersion()))

	// The state directory holds the node identity: if it is lost the Funnel
	// URL changes, which breaks the endpoint pinned in the Alexa console.
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return fmt.Errorf("creating the state directory: %w", err)
	}

	ts := &tsnet.Server{
		Dir:      cfg.StateDir,
		Hostname: cfg.Hostname,
		AuthKey:  cfg.AuthKey,
		// Ephemeral nodes are deregistered on shutdown and come back with a
		// new identity. The Funnel URL has to survive a restart.
		Ephemeral: false,
		Logf:      func(format string, args ...any) { log.Debug(fmt.Sprintf(format, args...)) },
		UserLogf:  func(format string, args ...any) { log.Info(fmt.Sprintf(format, args...)) },
	}
	defer func() {
		if err := ts.Close(); err != nil {
			log.Error("closing the tailnet node", slog.Any("error", err))
		}
	}()

	capture, err := newCaptureStore(cfg.CaptureDir)
	if err != nil {
		return err
	}

	// The verifier is built from the service's own public package using only
	// its exported API: no test seam is set here, so it uses the host's system
	// root pool, the real clock and the package's bounded HTTP client.
	verifier, err := alexaverify.New(alexaverify.WithLogger(log))
	if err != nil {
		return fmt.Errorf("building the alexa request verifier: %w", err)
	}

	// First-run Funnel setup provisions a Let's Encrypt certificate for the
	// node, which can take several seconds. ListenFunnel blocks for it rather
	// than failing, so there is no startup deadline to tune here.
	log.Info("requesting the funnel listener; first run provisions a certificate and may take a while",
		slog.String("addr", cfg.Addr))
	ln, err := ts.ListenFunnel("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listening on funnel %s: %w", cfg.Addr, err)
	}
	defer func() { _ = ln.Close() }()

	log.Info("serving", slog.String("url", "https://"+cfg.Hostname+".<tailnet>.ts.net"))

	return serve(ctx, ln, newRouter(log, capture, verifier), log)
}
