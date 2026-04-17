package cache

import (
	"context"
	"testing"
	"time"
)

func TestMemoryCache_GetSet(t *testing.T) {
	c, err := NewMemoryCache(16, time.Minute)
	if err != nil {
		t.Fatalf("NewMemoryCache: %v", err)
	}
	defer c.Close()

	want := &CachedResponse{Answer: "hi", Model: "m", Namespace: "ns", CachedAt: time.Now()}
	if err := c.Set(context.Background(), "k", want, 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok := c.Get(context.Background(), "k")
	if !ok {
		t.Fatal("expected hit, got miss")
	}
	if got.Answer != "hi" || got.Model != "m" {
		t.Fatalf("unexpected value: %+v", got)
	}
}

func TestMemoryCache_TTLExpiry(t *testing.T) {
	c, err := NewMemoryCache(16, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("NewMemoryCache: %v", err)
	}
	defer c.Close()

	_ = c.Set(context.Background(), "k", &CachedResponse{Answer: "x"}, 0)
	time.Sleep(120 * time.Millisecond)
	if _, ok := c.Get(context.Background(), "k"); ok {
		t.Fatal("expected miss after TTL, got hit")
	}
}

func TestMemoryCache_GenerationInvalidation(t *testing.T) {
	c, err := NewMemoryCache(16, time.Minute)
	if err != nil {
		t.Fatalf("NewMemoryCache: %v", err)
	}
	defer c.Close()
	ctx := context.Background()

	gen0 := c.GetGeneration(ctx, "ns")
	if gen0 != 0 {
		t.Fatalf("expected initial generation 0, got %d", gen0)
	}
	k0 := BuildKey("ns", gen0, "m", "q")
	_ = c.Set(ctx, k0, &CachedResponse{Answer: "stale"}, 0)

	gen1, err := c.IncrGeneration(ctx, "ns")
	if err != nil || gen1 != 1 {
		t.Fatalf("IncrGeneration: got %d err=%v", gen1, err)
	}
	k1 := BuildKey("ns", gen1, "m", "q")
	if k0 == k1 {
		t.Fatal("key must change when generation bumps")
	}
	if _, ok := c.Get(ctx, k1); ok {
		t.Fatal("post-bump key should miss")
	}
}

func TestMemoryCache_IncrGenerationConcurrent(t *testing.T) {
	c, _ := NewMemoryCache(16, time.Minute)
	defer c.Close()
	const n = 100
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func() { _, _ = c.IncrGeneration(context.Background(), "ns"); done <- struct{}{} }()
	}
	for i := 0; i < n; i++ {
		<-done
	}
	if got := c.GetGeneration(context.Background(), "ns"); got != int64(n) {
		t.Fatalf("expected generation %d, got %d", n, got)
	}
}
