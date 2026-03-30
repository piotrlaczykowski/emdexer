package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// mockEmbedProvider records call counts and returns predictable vectors.
type mockEmbedProvider struct {
	calls atomic.Int32
	dims  int
}

func (m *mockEmbedProvider) Name() string { return "mock" }

func (m *mockEmbedProvider) Embed(_ context.Context, text string) ([]float32, error) {
	m.calls.Add(1)
	v := make([]float32, m.dims)
	if len(text) > 0 {
		v[0] = float32(text[0]) / 255.0
	}
	return v, nil
}

func (m *mockEmbedProvider) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	m.calls.Add(int32(len(texts)))
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, m.dims)
		if len(t) > 0 {
			v[0] = float32(t[0]) / 255.0
		}
		out[i] = v
	}
	return out, nil
}

func TestCachedEmbedProvider_CachesResult(t *testing.T) {
	inner := &mockEmbedProvider{dims: 4}
	cached := newCachedEmbedProvider(inner, 100, 5*time.Minute).(*cachedEmbedProvider)

	ctx := context.Background()
	v1, err := cached.Embed(ctx, "hello")
	if err != nil {
		t.Fatalf("first Embed: %v", err)
	}
	v2, _ := cached.Embed(ctx, "hello")

	if inner.calls.Load() != 1 {
		t.Errorf("expected 1 inner call, got %d", inner.calls.Load())
	}
	if len(v1) != len(v2) {
		t.Errorf("cached vector length differs: %d vs %d", len(v1), len(v2))
	}
}

func TestCachedEmbedProvider_TTLExpiry(t *testing.T) {
	inner := &mockEmbedProvider{dims: 4}
	cached := newCachedEmbedProvider(inner, 100, 20*time.Millisecond)

	ctx := context.Background()
	_, _ = cached.Embed(ctx, "ttl-test")
	time.Sleep(40 * time.Millisecond) // > TTL
	_, _ = cached.Embed(ctx, "ttl-test")

	if inner.calls.Load() < 2 {
		t.Errorf("expected ≥2 inner calls after TTL expiry, got %d", inner.calls.Load())
	}
}

func TestCachedEmbedProvider_LRUEviction(t *testing.T) {
	inner := &mockEmbedProvider{dims: 4}
	cached := newCachedEmbedProvider(inner, 2, 5*time.Minute)

	ctx := context.Background()
	_, _ = cached.Embed(ctx, "a") // fills slot 1
	_, _ = cached.Embed(ctx, "b") // fills slot 2 (oldest = "a")
	_, _ = cached.Embed(ctx, "c") // evicts "a", fills slot with "c"

	before := inner.calls.Load()
	_, _ = cached.Embed(ctx, "a") // "a" was evicted — must call inner again
	if inner.calls.Load() == before {
		t.Error("expected inner call for evicted key 'a', but inner was not called")
	}

	before2 := inner.calls.Load()
	_, _ = cached.Embed(ctx, "b") // "b" was evicted when "c" inserted — inner called
	if inner.calls.Load() == before2 {
		t.Error("expected inner call for evicted key 'b', but inner was not called")
	}
}

func TestCachedEmbedProvider_BatchPartialHit(t *testing.T) {
	inner := &mockEmbedProvider{dims: 4}
	cached := newCachedEmbedProvider(inner, 100, 5*time.Minute)

	ctx := context.Background()
	// Prime cache with "text1".
	_, _ = cached.Embed(ctx, "text1")
	before := inner.calls.Load()

	// EmbedBatch with text1 (cached) + text2 (miss) — inner should only be called for text2.
	results, err := cached.EmbedBatch(ctx, []string{"text1", "text2"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0] == nil || results[1] == nil {
		t.Fatal("got nil result in batch")
	}
	// inner should have been called for text2 only (1 miss = 1 inner call via EmbedBatch).
	added := inner.calls.Load() - before
	if added != 1 {
		t.Errorf("expected 1 new inner call for batch miss, got %d", added)
	}
}
