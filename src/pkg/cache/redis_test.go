package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func newMini(t *testing.T) (*RedisCache, *miniredis.Miniredis) {
	t.Helper()
	s := miniredis.RunT(t)
	c, err := NewRedisCache("redis://"+s.Addr(), time.Minute)
	if err != nil {
		t.Fatalf("NewRedisCache: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, s
}

func TestRedisCache_GetSet(t *testing.T) {
	c, _ := newMini(t)
	ctx := context.Background()
	want := &CachedResponse{Answer: "hi", Model: "m", Namespace: "ns", CachedAt: time.Unix(1, 0).UTC()}
	if err := c.Set(ctx, "k", want, 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok := c.Get(ctx, "k")
	if !ok {
		t.Fatal("expected hit")
	}
	if got.Answer != "hi" || got.Model != "m" || got.Namespace != "ns" {
		t.Fatalf("unexpected round-trip: %+v", got)
	}
}

func TestRedisCache_Miss(t *testing.T) {
	c, _ := newMini(t)
	if _, ok := c.Get(context.Background(), "missing"); ok {
		t.Fatal("expected miss, got hit")
	}
}

func TestRedisCache_IncrGeneration(t *testing.T) {
	c, _ := newMini(t)
	ctx := context.Background()
	if g := c.GetGeneration(ctx, "ns"); g != 0 {
		t.Fatalf("expected 0, got %d", g)
	}
	g1, _ := c.IncrGeneration(ctx, "ns")
	g2, _ := c.IncrGeneration(ctx, "ns")
	if g1 != 1 || g2 != 2 {
		t.Fatalf("expected 1,2; got %d,%d", g1, g2)
	}
	if got := c.GetGeneration(ctx, "ns"); got != 2 {
		t.Fatalf("expected 2, got %d", got)
	}
}

func TestRedisCache_TTLApplied(t *testing.T) {
	c, s := newMini(t)
	_ = c.Set(context.Background(), "k", &CachedResponse{Answer: "x"}, 0)
	s.FastForward(2 * time.Minute) // exceeds 1m constructor TTL
	if _, ok := c.Get(context.Background(), "k"); ok {
		t.Fatal("expected miss after TTL")
	}
}
