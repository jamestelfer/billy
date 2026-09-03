package alexaverify

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// certCache is a small FIFO cache of already parsed and trusted certificate
// chains. It is process-local by design: certificates are never persisted.
type certCache struct {
	mu      sync.Mutex
	entries map[string]certCacheEntry
	next    uint64
}

type certCacheEntry struct {
	chain *parsedChain
	order uint64
}

func newCertCache() *certCache {
	return &certCache{entries: make(map[string]certCacheEntry, maxCertificateCacheEntries)}
}

func (v *Verifier) certificateCache() *certCache {
	v.cacheOnce.Do(func() {
		v.cache = newCertCache()
	})
	return v.cache
}

// loadCertificateChain returns a parsed, trusted chain from the in-memory
// cache or fetches and validates one on a miss. The normalized URL is the key.
func (v *Verifier) loadCertificateChain(
	ctx context.Context,
	canonicalURL string,
	now time.Time,
) (*parsedChain, error) {
	cache := v.certificateCache()
	if chain, ok := cache.get(canonicalURL); ok {
		if err := validateCachedLeafTime(chain, now); err != nil {
			cache.remove(canonicalURL, chain)
			return nil, err
		}
		return chain, nil
	}

	v.logger().Info("fetching alexa signing certificate chain",
		slog.String("url", canonicalURL))
	bundle, err := v.fetchCertificateChain(ctx, canonicalURL)
	if err != nil {
		return nil, err
	}
	chain, err := parsePEMChain(bundle)
	if err != nil {
		return nil, err
	}
	if err := v.validateChain(chain, now); err != nil {
		return nil, err
	}
	if err := verifyCertificateHostname(chain.leaf); err != nil {
		return nil, err
	}

	cache.put(canonicalURL, chain)
	return chain, nil
}

func (c *certCache) get(key string) (*parsedChain, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	return entry.chain, ok
}

func (c *certCache) put(key string, chain *parsedChain) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, ok := c.entries[key]; ok {
		// Replacing a chain for an existing URL does not make it newest. Keeping
		// its original order makes eviction deterministic and prevents a hot key
		// from retaining itself forever merely through replacement.
		entry.chain = chain
		c.entries[key] = entry
		return
	}

	if len(c.entries) == maxCertificateCacheEntries {
		var oldestKey string
		var oldestOrder uint64
		first := true
		for candidateKey, candidate := range c.entries {
			if first || candidate.order < oldestOrder {
				oldestKey = candidateKey
				oldestOrder = candidate.order
				first = false
			}
		}
		delete(c.entries, oldestKey)
	}

	c.entries[key] = certCacheEntry{chain: chain, order: c.next}
	c.next++
}

// remove deletes key only if it still names chain. The identity check prevents
// a failed reader from deleting a replacement installed concurrently.
func (c *certCache) remove(key string, chain *parsedChain) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, ok := c.entries[key]; ok && entry.chain == chain {
		delete(c.entries, key)
	}
}

// validateCachedLeafTime rechecks both bounds on every cache hit. This is kept
// separate from full path validation because trust and SANs are immutable for
// a parsed chain, whereas the validity verdict changes as the clock advances.
func validateCachedLeafTime(chain *parsedChain, now time.Time) error {
	if now.Before(chain.leaf.NotBefore) {
		return fmt.Errorf("%w: cached signing certificate is not valid before %s",
			ErrUntrustedChain, chain.leaf.NotBefore)
	}
	if now.After(chain.leaf.NotAfter) {
		return fmt.Errorf("%w: cached signing certificate expired at %s",
			ErrUntrustedChain, chain.leaf.NotAfter)
	}
	return nil
}
