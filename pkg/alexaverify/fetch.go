package alexaverify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

var defaultHTTPClient = &http.Client{
	Timeout: certificateFetchTimeout,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// fetchCertificateChain retrieves a previously normalized chain URL within one
// deadline, at most certificateFetchAttempts times, and never accepts a
// redirect or an unbounded response.
func (v *Verifier) fetchCertificateChain(ctx context.Context, canonicalURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, certificateFetchTimeout)
	defer cancel()

	client := v.fetchHTTPClient()
	var lastErr error
	for attempt := 1; attempt <= certificateFetchAttempts; attempt++ {
		bundle, err := fetchCertificateChainOnce(ctx, client, canonicalURL)
		if err == nil {
			return bundle, nil
		}
		lastErr = err

		if attempt == certificateFetchAttempts {
			break
		}
		timer := time.NewTimer(certificateFetchRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("%w: %v", ErrCertFetch, ctx.Err())
		case <-timer.C:
		}
	}

	return nil, fmt.Errorf("%w: %v", ErrCertFetch, lastErr)
}

func (v *Verifier) fetchHTTPClient() *http.Client {
	if v.HTTPClient == nil {
		return defaultHTTPClient
	}

	// Copy the injected client so redirect hardening cannot mutate caller-owned
	// state. Redirects are rejected after URL validation: otherwise a valid S3
	// URL could steer the actual fetch to an unvalidated host.
	client := *v.HTTPClient
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &client
}

func fetchCertificateChainOnce(ctx context.Context, client *http.Client, canonicalURL string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, canonicalURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %s", response.Status)
	}

	bundle, err := io.ReadAll(io.LimitReader(response.Body, maxCertificateChainBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if len(bundle) > maxCertificateChainBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxCertificateChainBytes)
	}
	return bundle, nil
}
