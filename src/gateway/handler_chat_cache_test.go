package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/piotrlaczykowski/emdexer/cache"
)

func newTestServerWithCache(t *testing.T) *Server {
	t.Helper()
	mem, err := cache.NewMemoryCache(16, time.Minute)
	if err != nil {
		t.Fatalf("NewMemoryCache: %v", err)
	}
	t.Cleanup(func() { _ = mem.Close() })
	return &Server{cache: mem}
}

func TestCache_GenerationBumpMakesNextLookupMiss(t *testing.T) {
	s := newTestServerWithCache(t)
	ctx := context.Background()

	gen0 := s.cache.GetGeneration(ctx, "ns")
	k0 := cache.BuildKey("ns", gen0, "m", "q")
	_ = s.cache.Set(ctx, k0, &cache.CachedResponse{Answer: "old"}, 0)

	if _, ok := s.cache.Get(ctx, k0); !ok {
		t.Fatal("precondition: expected hit")
	}

	if _, err := s.cache.IncrGeneration(ctx, "ns"); err != nil {
		t.Fatalf("IncrGeneration: %v", err)
	}

	gen1 := s.cache.GetGeneration(ctx, "ns")
	k1 := cache.BuildKey("ns", gen1, "m", "q")
	if _, ok := s.cache.Get(ctx, k1); ok {
		t.Fatal("post-bump key must miss")
	}
}

func TestCache_HitKeyIsReachableBeforeBump(t *testing.T) {
	s := newTestServerWithCache(t)
	ctx := context.Background()

	gen := s.cache.GetGeneration(ctx, "myns")
	key := cache.BuildKey("myns", gen, "gemini-2.0", "what is go?")
	want := &cache.CachedResponse{Answer: "a language", Model: "gemini-2.0", Namespace: "myns"}

	if err := s.cache.Set(ctx, key, want, 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, ok := s.cache.Get(ctx, key)
	if !ok {
		t.Fatal("expected hit — this key would trigger X-Emdexer-Cache: hit")
	}
	if got.Answer != want.Answer {
		t.Fatalf("cached answer mismatch: got %q want %q", got.Answer, want.Answer)
	}

	// After bump the same logical key is now unreachable — handler would set "miss".
	if _, err := s.cache.IncrGeneration(ctx, "myns"); err != nil {
		t.Fatalf("IncrGeneration: %v", err)
	}
	gen2 := s.cache.GetGeneration(ctx, "myns")
	key2 := cache.BuildKey("myns", gen2, "gemini-2.0", "what is go?")
	if _, ok := s.cache.Get(ctx, key2); ok {
		t.Fatal("post-bump key must miss — handler would set X-Emdexer-Cache: miss")
	}
}

func TestCache_CachedResponseJSONRoundTrip(t *testing.T) {
	in := cache.CachedResponse{
		Answer:    "hi",
		Model:     "gemini-2.0",
		Namespace: "ns",
		CachedAt:  time.Now().UTC().Truncate(time.Second),
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out cache.CachedResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Answer != in.Answer || out.Model != in.Model || out.Namespace != in.Namespace {
		t.Fatalf("round-trip field mismatch: got %+v want %+v", out, in)
	}
	if !out.CachedAt.Equal(in.CachedAt) {
		t.Fatalf("CachedAt mismatch: got %v want %v", out.CachedAt, in.CachedAt)
	}
}
