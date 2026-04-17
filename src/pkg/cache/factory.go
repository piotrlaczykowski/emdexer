package cache

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// NewFromEnv reads EMDEX_CACHE_* and returns the configured backend.
// Returns (nil, nil) when EMDEX_CACHE_ENABLED != "true" — callers
// should nil-check on every code path.
//
// Env vars:
//
//	EMDEX_CACHE_ENABLED      "true" to enable; any other value disables.
//	EMDEX_CACHE_BACKEND      "memory" (default) or "redis".
//	EMDEX_CACHE_REDIS_URL    e.g. "redis://localhost:6379" (redis only).
//	EMDEX_CACHE_TTL          time.Duration, e.g. "5m".
//	EMDEX_CACHE_MAX_ENTRIES  int, memory backend only.
func NewFromEnv() (Cache, error) {
	if strings.ToLower(os.Getenv("EMDEX_CACHE_ENABLED")) != "true" {
		return nil, nil
	}
	backend := strings.ToLower(os.Getenv("EMDEX_CACHE_BACKEND"))
	if backend == "" {
		backend = "memory"
	}
	ttlStr := os.Getenv("EMDEX_CACHE_TTL")
	if ttlStr == "" {
		ttlStr = "5m"
	}
	ttl, err := time.ParseDuration(ttlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid EMDEX_CACHE_TTL %q: %w", ttlStr, err)
	}

	switch backend {
	case "memory":
		max := 1000
		if s := os.Getenv("EMDEX_CACHE_MAX_ENTRIES"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("invalid EMDEX_CACHE_MAX_ENTRIES %q", s)
			}
			max = n
		}
		return NewMemoryCache(max, ttl)
	case "redis":
		url := os.Getenv("EMDEX_CACHE_REDIS_URL")
		if url == "" {
			return nil, fmt.Errorf("EMDEX_CACHE_REDIS_URL required when backend=redis")
		}
		return NewRedisCache(url, ttl)
	default:
		return nil, fmt.Errorf("unknown EMDEX_CACHE_BACKEND %q (expected memory|redis)", backend)
	}
}
