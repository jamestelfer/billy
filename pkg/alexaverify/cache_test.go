package alexaverify

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
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

	if err := verifier.Warm(t.Context(), testCertURL); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	if err := verifier.Verify(t.Context(), body, headersFor(signature)); err != nil {
		t.Fatalf("Verify after Warm: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("certificate fetches = %d, want 1 shared by Warm and Verify", got)
	}
}

func TestCertificateCacheFetchesOnceForSuccessiveVerifications(t *testing.T) {
	pki := generateTestPKI(t, fixedNow)
	leaf := pki.issueLeaf(t, validLeafSpec(fixedNow))
	body := envelope("LaunchRequest", fixedNow)
	signature := signBody(t, leaf.key, body)
	verifier, calls := verifierServingBundle(t, pki.roots, fixedNow, leaf.bundle)

	for attempt := 1; attempt <= 2; attempt++ {
		if err := verifier.Verify(t.Context(), body, headersFor(signature)); err != nil {
			t.Fatalf("Verify attempt %d: %v", attempt, err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("certificate fetches = %d, want 1", got)
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
		if err := verifier.Verify(t.Context(), body, headersForURL(signature, certURL)); err != nil {
			t.Fatalf("Verify with %q: %v", certURL, err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("certificate fetches = %d, want 1 for equivalent normalized URLs", got)
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

	if err := verifier.Verify(t.Context(), body, headersFor(signature)); err != nil {
		t.Fatalf("priming Verify: %v", err)
	}
	current = fixedNow.Add(2 * time.Minute)
	assertOnlySentinel(t, verifier.Verify(t.Context(), body, headersFor(signature)), ErrUntrustedChain)
	if got := calls.Load(); got != 1 {
		t.Fatalf("certificate fetches = %d, want 1; expired hit must be rejected locally", got)
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
		if err := verifier.Verify(t.Context(), body, headersForURL(signature, certURL)); err != nil {
			t.Fatalf("Verify cache key %d: %v", index, err)
		}
	}
	if got := calls.Load(); got != maxCertificateCacheEntries+1 {
		t.Fatalf("certificate fetches after filling = %d, want %d",
			got, maxCertificateCacheEntries+1)
	}

	if err := verifier.Verify(t.Context(), body, headersForURL(signature, testCertURL+"?slot=0")); err != nil {
		t.Fatalf("Verify evicted key: %v", err)
	}
	if got := calls.Load(); got != maxCertificateCacheEntries+2 {
		t.Fatalf("certificate fetches after revisiting oldest = %d, want %d",
			got, maxCertificateCacheEntries+2)
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
		if err != nil {
			t.Errorf("concurrent Verify: %v", err)
		}
	}
}
