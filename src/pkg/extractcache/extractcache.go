// Package extractcache provides a Redis-backed deduplication cache for
// expensive extraction results (extractous, whisper, vision).
// OFF by default — extracted text may be sensitive, operators must opt in.
package extractcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	schemaVersion = 1
	keyPrefix     = "emdex:extract:"
)

// Result is the cached extraction output for a single file.
type Result struct {
	Text      string            `json:"text"`
	Meta      map[string]string `json:"meta,omitempty"`
	Extractor string            `json:"extractor"`
	ExtractAt time.Time         `json:"extracted_at"`
	SchemaV   int               `json:"schema_v"`
}

// Cache is the backend interface.
type Cache interface {
	Get(ctx context.Context, key string) (*Result, bool)
	Set(ctx context.Context, key string, r *Result) error
	Close() error
}

// BuildKey derives the cache key from content XXH3 hex and extractor tag.
func BuildKey(xxh3Hex, extractor string) string {
	if strings.ContainsAny(extractor, ":/") {
		extractor = strings.NewReplacer(":", "_", "/", "_").Replace(extractor)
	}
	return fmt.Sprintf("%sv%d:%s:%s", keyPrefix, schemaVersion, xxh3Hex, extractor)
}

// HexXXH3 returns the canonical 16-char lowercase hex of a uint64 xxh3 sum.
func HexXXH3(sum uint64) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 15; i >= 0; i-- {
		out[i] = hexdigits[sum&0xf]
		sum >>= 4
	}
	return string(out)
}

// Noop always misses. Used when the feature is disabled.
type Noop struct{}

func (Noop) Get(context.Context, string) (*Result, bool) { return nil, false }
func (Noop) Set(context.Context, string, *Result) error  { return nil }
func (Noop) Close() error                                { return nil }

// RedisCache stores results in Redis with TTL.
type RedisCache struct {
	client *redis.Client
	ttl    time.Duration
}

// NewRedisCache pings the server at construction so misconfigured Redis
// fails loudly at startup rather than silently on every Get.
func NewRedisCache(redisURL string, ttl time.Duration) (*RedisCache, error) {
	if ttl <= 0 {
		return nil, fmt.Errorf("ttl must be > 0, got %v", ttl)
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &RedisCache{client: client, ttl: ttl}, nil
}

func (r *RedisCache) Get(ctx context.Context, key string) (*Result, bool) {
	raw, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		return nil, false
	}
	var v Result
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, false
	}
	if v.SchemaV != schemaVersion {
		return nil, false
	}
	return &v, true
}

func (r *RedisCache) Set(ctx context.Context, key string, v *Result) error {
	if v == nil {
		return errors.New("nil result")
	}
	cp := *v  // copy so we don't mutate caller's struct
	cp.SchemaV = schemaVersion
	if cp.ExtractAt.IsZero() {
		cp.ExtractAt = time.Now().UTC()
	}
	raw, err := json.Marshal(&cp)
	if err != nil {
		return err
	}
	return r.client.Set(ctx, key, raw, r.ttl).Err()
}

func (r *RedisCache) Close() error { return r.client.Close() }

// NewFromEnv reads EMDEX_EXTRACT_CACHE_* and returns Noop when disabled.
// Returns (nil, err) when enabled but misconfigured — callers must nil-check
// when err != nil.
func NewFromEnv() (Cache, error) {
	if strings.ToLower(os.Getenv("EMDEX_EXTRACT_CACHE_ENABLED")) != "true" {
		return Noop{}, nil
	}
	url := os.Getenv("EMDEX_EXTRACT_CACHE_REDIS_URL")
	if url == "" {
		return nil, errors.New("EMDEX_EXTRACT_CACHE_REDIS_URL required when EMDEX_EXTRACT_CACHE_ENABLED=true")
	}
	ttlStr := os.Getenv("EMDEX_EXTRACT_CACHE_TTL")
	if ttlStr == "" {
		ttlStr = "720h"
	}
	ttl, err := time.ParseDuration(ttlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid EMDEX_EXTRACT_CACHE_TTL %q: %w", ttlStr, err)
	}
	return NewRedisCache(url, ttl)
}
