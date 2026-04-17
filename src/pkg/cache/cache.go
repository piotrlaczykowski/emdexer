// Package cache provides response caching for chat completions.
//
// Two backends live behind the Cache interface: an in-process LRU+TTL
// (MemoryCache) and a Redis-backed distributed cache (RedisCache).
// Invalidation uses a per-namespace generation counter: the cache key
// embeds the current generation, so a counter bump makes every prior
// key unreachable without explicit deletes.
package cache

import (
	"context"
	"time"
)

// CachedResponse is the value stored in the cache for a given key.
type CachedResponse struct {
	Answer    string    `json:"answer"`
	Model     string    `json:"model"`
	Namespace string    `json:"namespace"`
	CachedAt  time.Time `json:"cached_at"`
}

// Cache is the interface every response-cache backend implements.
type Cache interface {
	// Get returns a cached response and whether it was found.
	Get(ctx context.Context, key string) (*CachedResponse, bool)
	// Set stores a response. ttl=0 means "use the backend's default TTL".
	Set(ctx context.Context, key string, value *CachedResponse, ttl time.Duration) error
	// IncrGeneration bumps the namespace generation counter and returns
	// the new value. After a bump, every previously-built key for this
	// namespace is unreachable (because BuildKey embeds the generation).
	IncrGeneration(ctx context.Context, namespace string) (int64, error)
	// GetGeneration returns the current generation counter for a
	// namespace. Returns 0 if the namespace has never been invalidated.
	GetGeneration(ctx context.Context, namespace string) int64
	// Close releases underlying resources.
	Close() error
}
