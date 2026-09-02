// Command billy is a self-hosted Alexa skill endpoint for Audiobookshelf.
//
// At this stage it does one thing: reach the public internet over an embedded
// Tailscale Funnel listener and capture the raw bytes of every Alexa request,
// so those bytes can serve as the corpus for building signature verification.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jamestelfer/billy/pkg/alexaverify"
	"tailscale.com/tsnet"
)

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

// run is the whole program, parameterised on its arguments and output streams
// so it can be exercised from tests without a process boundary.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print the version and exit")

	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	if *showVersion {
		if _, err := fmt.Fprintln(stdout, buildVersion()); err != nil {
			return 1
		}
		return 0
	}

	log := slog.New(slog.NewTextHandler(stderr, nil))

	// signal.NotifyContext is portable: os.Interrupt and syscall.SIGTERM are
	// both defined on Windows, so no build-tagged signal handling is needed.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := serveFunnel(ctx, log); err != nil {
		log.Error("service failed", slog.Any("error", err))
		return 1
	}
	return 0
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
