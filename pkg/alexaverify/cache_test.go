package alexaverify

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func headersForURL(signature, certURL string) http.Header {
	headers := headersFor(signature)
	headers.Set(certChainURLHeader, certURL)
	return headers
}

func TestWarmPopulatesTheCertificateCache(t *testing.T) {
	pki := generateTestPKI(t, fixedNow)
	leaf := pki.issueLeaf(t, validLeafSpec(fixedNow))
	body := envelope("LaunchRequest", fixedNow)
	signature := signBody(t, leaf.key, body)
	verifier, calls := verifierServingBundle(t, pki.roots, fixedNow, leaf.bundle)

	{
		err := verifier.Warm(t.Context(), testCertURL)
		require.NoError(t, err, "Warm: %v", err)
	}
	{
		err := verifier.Verify(t.Context(), body, headersFor(signature))
		require.NoError(t, err, "Verify after Warm: %v", err)
	}
	{
		got := calls.Load()
		require.EqualValues(t, 1, got, "certificate fetches = %d, want 1 shared by Warm and Verify", got)
	}
}

func TestCertificateCacheFetchesOnceForSuccessiveVerifications(t *testing.T) {
	pki := generateTestPKI(t, fixedNow)
	leaf := pki.issueLeaf(t, validLeafSpec(fixedNow))
	body := envelope("LaunchRequest", fixedNow)
	signature := signBody(t, leaf.key, body)
	verifier, calls := verifierServingBundle(t, pki.roots, fixedNow, leaf.bundle)

	for attempt := 1; attempt <= 2; attempt++ {
		{
			err := verifier.Verify(t.Context(), body, headersFor(signature))
			require.NoError(t, err, "Verify attempt %d: %v", attempt, err)
		}
	}
	{
		got := calls.Load()
		require.EqualValues(t, 1, got, "certificate fetches = %d, want 1", got)
	}
}

func TestCertificateCacheUsesTheNormalizedURLAsItsKey(t *testing.T) {
	pki := generateTestPKI(t, fixedNow)
	leaf := pki.issueLeaf(t, validLeafSpec(fixedNow))
	body := envelope("LaunchRequest", fixedNow)
	signature := signBody(t, leaf.key, body)
	verifier, calls := verifierServingBundle(t, pki.roots, fixedNow, leaf.bundle)

	urls := []string{
		"https://S3.AMAZONAWS.COM/echo.api//cert",
		"https://s3.amazonaws.com/echo.api/cert#ignored",
	}
	for _, certURL := range urls {
		{
			err := verifier.Verify(t.Context(), body, headersForURL(signature, certURL))
			require.NoError(t, err, "Verify with %q: %v", certURL, err)
		}
	}
	{
		got := calls.Load()
		require.EqualValues(t, 1, got, "certificate fetches = %d, want 1 for equivalent normalized URLs", got)
	}
}

func TestCertificateCacheRejectsAnExpiredHit(t *testing.T) {
	pki := generateTestPKI(t, fixedNow)
	spec := validLeafSpec(fixedNow)
	spec.notAfter = fixedNow.Add(time.Minute)
	leaf := pki.issueLeaf(t, spec)
	body := envelope("LaunchRequest", fixedNow)
	signature := signBody(t, leaf.key, body)

	current := fixedNow
	verifier, calls := verifierServingBundle(t, pki.roots, fixedNow, leaf.bundle)
	verifier.Now = func() time.Time { return current }

	{
		err := verifier.Verify(t.Context(), body, headersFor(signature))
		require.NoError(t, err, "priming Verify: %v", err)
	}
	current = fixedNow.Add(2 * time.Minute)
	assertOnlySentinel(t, verifier.Verify(t.Context(), body, headersFor(signature)), ErrUntrustedChain)
	{
		got := calls.Load()
		require.EqualValues(t, 1, got, "certificate fetches = %d, want 1; expired hit must be rejected locally", got)
	}
}

func TestCertificateCacheEvictsTheOldestEntryAtItsBound(t *testing.T) {
	pki := generateTestPKI(t, fixedNow)
	leaf := pki.issueLeaf(t, validLeafSpec(fixedNow))
	body := envelope("LaunchRequest", fixedNow)
	signature := signBody(t, leaf.key, body)
	verifier, calls := verifierServingBundle(t, pki.roots, fixedNow, leaf.bundle)

	for index := range maxCertificateCacheEntries + 1 {
		certURL := fmt.Sprintf("%s?slot=%d", testCertURL, index)
		{
			err := verifier.Verify(t.Context(), body, headersForURL(signature, certURL))
			require.NoError(t, err, "Verify cache key %d: %v", index, err)
		}
	}
	{
		got := calls.Load()
		require.EqualValues(t, maxCertificateCacheEntries+1, got, "certificate fetches after filling = %d, want %d", got, maxCertificateCacheEntries+1)
	}

	{
		err := verifier.Verify(t.Context(), body, headersForURL(signature, testCertURL+"?slot=0"))
		require.NoError(t, err, "Verify evicted key: %v", err)
	}
	{
		got := calls.Load()
		require.EqualValues(t, maxCertificateCacheEntries+2, got, "certificate fetches after revisiting oldest = %d, want %d", got, maxCertificateCacheEntries+2)
	}
}

func TestCertificateCacheIsSafeForConcurrentVerification(t *testing.T) {
	pki := generateTestPKI(t, fixedNow)
	leaf := pki.issueLeaf(t, validLeafSpec(fixedNow))
	body := envelope("LaunchRequest", fixedNow)
	signature := signBody(t, leaf.key, body)
	verifier, _ := verifierServingBundle(t, pki.roots, fixedNow, leaf.bundle)

	const goroutines = 16
	start := make(chan struct{})
	errs := make(chan error, goroutines)
	var group sync.WaitGroup
	for range goroutines {
		group.Go(func() {
			<-start
			errs <- verifier.Verify(t.Context(), body, headersFor(signature))
		})
	}
	close(start)
	group.Wait()
	close(errs)

	for err := range errs {
		assert.NoError(t, err, "concurrent Verify: %v", err)
	}
}
