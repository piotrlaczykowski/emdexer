package cache

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	lru "github.com/hashicorp/golang-lru/v2/expirable"
)

// MemoryCache is an in-process LRU+TTL cache backend.
//
// NOTE: generations are per-process. With the memory backend and
// multiple gateway replicas, a namespace bump on one replica does not
// propagate. Use the Redis backend for multi-replica deployments.
type MemoryCache struct {
	lru         *lru.LRU[string, *CachedResponse]
	generations sync.Map // key: namespace (string), value: *int64
	ttl         time.Duration
}

// NewMemoryCache constructs a MemoryCache.
func NewMemoryCache(maxEntries int, ttl time.Duration) (*MemoryCache, error) {
	if maxEntries <= 0 {
		return nil, fmt.Errorf("maxEntries must be > 0, got %d", maxEntries)
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("ttl must be > 0, got %v", ttl)
	}
	return &MemoryCache{
		lru: lru.NewLRU[string, *CachedResponse](maxEntries, nil, ttl),
		ttl: ttl,
	}, nil
}

func (m *MemoryCache) Get(_ context.Context, key string) (*CachedResponse, bool) {
	return m.lru.Get(key)
}

func (m *MemoryCache) Set(_ context.Context, key string, value *CachedResponse, ttl time.Duration) error {
	_ = ttl // expirable LRU uses the constructor TTL; per-entry TTL not supported.
	m.lru.Add(key, value)
	return nil
}

func (m *MemoryCache) IncrGeneration(_ context.Context, namespace string) (int64, error) {
	v, _ := m.generations.LoadOrStore(namespace, new(int64))
	return atomic.AddInt64(v.(*int64), 1), nil
}

func (m *MemoryCache) GetGeneration(_ context.Context, namespace string) int64 {
	v, ok := m.generations.Load(namespace)
	if !ok {
		return 0
	}
	return atomic.LoadInt64(v.(*int64))
}

func (m *MemoryCache) Close() error { m.lru.Purge(); return nil }
