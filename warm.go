package main

import (
	"context"
	"log/slog"

	"github.com/jamestelfer/billy/pkg/alexaverify"
)

// startCertificateCacheWarm begins a best-effort cache seed without delaying
// listener readiness. The returned channel closes when the attempt finishes;
// production does not wait for it, while tests use it to avoid leaked work.
func startCertificateCacheWarm(
	ctx context.Context,
	verifier *alexaverify.Verifier,
	seedURL string,
	log *slog.Logger,
) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := verifier.Warm(ctx, seedURL); err != nil {
			log.Warn("could not warm the alexa certificate cache; continuing",
				slog.Any("error", err),
				slog.String("cert_chain_url", seedURL))
			return
		}
		log.Info("warmed the alexa certificate cache",
			slog.String("cert_chain_url", seedURL))
	}()
	return done
}
