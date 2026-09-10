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

	require.NoError(t, verifier.Warm(t.Context(), testCertURL))
	require.NoError(t, verifier.Verify(t.Context(), body, headersFor(signature)))
	require.EqualValues(t, 1, calls.Load(), "Warm and Verify should share one certificate fetch")
}

func TestCertificateCacheFetchesOnceForSuccessiveVerifications(t *testing.T) {
	pki := generateTestPKI(t, fixedNow)
	leaf := pki.issueLeaf(t, validLeafSpec(fixedNow))
	body := envelope("LaunchRequest", fixedNow)
	signature := signBody(t, leaf.key, body)
	verifier, calls := verifierServingBundle(t, pki.roots, fixedNow, leaf.bundle)

	for attempt := 1; attempt <= 2; attempt++ {
		require.NoError(t, verifier.Verify(t.Context(), body, headersFor(signature)), "Verify attempt %d", attempt)
	}
	require.EqualValues(t, 1, calls.Load(), "successive verifications should share one certificate fetch")
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
		require.NoError(t, verifier.Verify(t.Context(), body, headersForURL(signature, certURL)),
			"Verify with %q", certURL)
	}
	require.EqualValues(t, 1, calls.Load(), "equivalent normalized URLs should share one certificate fetch")
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

	require.NoError(t, verifier.Verify(t.Context(), body, headersFor(signature)), "priming Verify")
	current = fixedNow.Add(2 * time.Minute)
	assertOnlySentinel(t, verifier.Verify(t.Context(), body, headersFor(signature)), ErrUntrustedChain)
	require.EqualValues(t, 1, calls.Load(), "expired hit must be rejected locally")
}

func TestCertificateCacheEvictsTheOldestEntryAtItsBound(t *testing.T) {
	pki := generateTestPKI(t, fixedNow)
	leaf := pki.issueLeaf(t, validLeafSpec(fixedNow))
	body := envelope("LaunchRequest", fixedNow)
	signature := signBody(t, leaf.key, body)
	verifier, calls := verifierServingBundle(t, pki.roots, fixedNow, leaf.bundle)

	for index := range maxCertificateCacheEntries + 1 {
		certURL := fmt.Sprintf("%s?slot=%d", testCertURL, index)
		require.NoError(t, verifier.Verify(t.Context(), body, headersForURL(signature, certURL)),
			"Verify cache key %d", index)
	}
	require.EqualValues(t, maxCertificateCacheEntries+1, calls.Load(), "certificate fetches after filling")

	require.NoError(t, verifier.Verify(t.Context(), body, headersForURL(signature, testCertURL+"?slot=0")),
		"Verify evicted key")
	require.EqualValues(t, maxCertificateCacheEntries+2, calls.Load(), "certificate fetches after revisiting oldest")
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
