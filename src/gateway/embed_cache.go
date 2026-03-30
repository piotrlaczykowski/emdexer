package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/piotrlaczykowski/emdexer/embed"
)

// cachedEmbedProvider wraps an EmbedProvider with an LRU+TTL in-memory cache.
// Thread-safe. Evicts the least-recently-inserted entry when maxEntries is reached.
type cachedEmbedProvider struct {
	inner      embed.EmbedProvider
	mu         sync.Mutex
	entries    map[string]*cacheEntry
	order      []string // insertion-order eviction queue
	maxEntries int
	ttl        time.Duration
}

type cacheEntry struct {
	vector    []float32
	expiresAt time.Time
}

// newCachedEmbedProvider returns a caching EmbedProvider.
// maxEntries <= 0 disables caching and returns inner unchanged.
func newCachedEmbedProvider(inner embed.EmbedProvider, maxEntries int, ttl time.Duration) embed.EmbedProvider {
	if maxEntries <= 0 {
		return inner
	}
	return &cachedEmbedProvider{
		inner:      inner,
		entries:    make(map[string]*cacheEntry),
		maxEntries: maxEntries,
		ttl:        ttl,
	}
}

func (c *cachedEmbedProvider) Name() string { return "cached:" + c.inner.Name() }

func (c *cachedEmbedProvider) cacheKey(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:16])
}

// get looks up a key. Must be called with c.mu held.
func (c *cachedEmbedProvider) get(key string) ([]float32, bool) {
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expiresAt) {
		return nil, false
	}
	return e.vector, true
}

// set stores a vector. Must be called with c.mu held.
func (c *cachedEmbedProvider) set(key string, vector []float32) {
	if _, exists := c.entries[key]; !exists {
		// Only evict when adding a new key.
		if len(c.entries) >= c.maxEntries && len(c.order) > 0 {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, oldest)
		}
		c.order = append(c.order, key)
	}
	c.entries[key] = &cacheEntry{vector: vector, expiresAt: time.Now().Add(c.ttl)}
}

func (c *cachedEmbedProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	key := c.cacheKey(text)

	c.mu.Lock()
	if v, ok := c.get(key); ok {
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()

	vector, err := c.inner.Embed(ctx, text)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.set(key, vector)
	c.mu.Unlock()
	return vector, nil
}

func (c *cachedEmbedProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	var missIdxs []int
	var missTexts []string

	c.mu.Lock()
	for i, text := range texts {
		key := c.cacheKey(text)
		if v, ok := c.get(key); ok {
			results[i] = v
		} else {
			missIdxs = append(missIdxs, i)
			missTexts = append(missTexts, text)
		}
	}
	c.mu.Unlock()

	if len(missTexts) == 0 {
		return results, nil
	}

	vectors, err := c.inner.EmbedBatch(ctx, missTexts)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	for j, i := range missIdxs {
		if j < len(vectors) && vectors[j] != nil {
			results[i] = vectors[j]
			c.set(c.cacheKey(texts[i]), vectors[j])
		}
	}
	c.mu.Unlock()
	return results, nil
}
