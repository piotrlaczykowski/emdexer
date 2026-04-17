package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisKeyPrefix = "emdex:"

// RedisCache is a Redis-backed distributed cache. Generations live at
// keys of the form "emdex:gen:{namespace}" and entries at
// "emdex:cache:{sha256hex}".
type RedisCache struct {
	client *redis.Client
	ttl    time.Duration
}

// NewRedisCache parses redisURL, pings the server, and returns a ready
// backend. The ping uses a 2s timeout so misconfigured Redis fails fast
// at gateway startup.
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

func (r *RedisCache) cacheKey(key string) string { return redisKeyPrefix + "cache:" + key }
func (r *RedisCache) genKey(ns string) string    { return redisKeyPrefix + "gen:" + ns }

func (r *RedisCache) Get(ctx context.Context, key string) (*CachedResponse, bool) {
	raw, err := r.client.Get(ctx, r.cacheKey(key)).Bytes()
	if err != nil {
		return nil, false
	}
	var v CachedResponse
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, false
	}
	return &v, true
}

func (r *RedisCache) Set(ctx context.Context, key string, value *CachedResponse, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = r.ttl
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return r.client.Set(ctx, r.cacheKey(key), raw, ttl).Err()
}

func (r *RedisCache) IncrGeneration(ctx context.Context, namespace string) (int64, error) {
	return r.client.Incr(ctx, r.genKey(namespace)).Result()
}

func (r *RedisCache) GetGeneration(ctx context.Context, namespace string) int64 {
	s, err := r.client.Get(ctx, r.genKey(namespace)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0
		}
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func (r *RedisCache) Close() error { return r.client.Close() }
