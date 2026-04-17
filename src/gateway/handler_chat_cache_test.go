package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
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

func TestCache_HitSetsExpectedHeader(t *testing.T) {
	rr := httptest.NewRecorder()
	rr.Header().Set("X-Emdexer-Cache", "hit")
	if got := rr.Header().Get("X-Emdexer-Cache"); got != "hit" {
		t.Fatalf("header write failed: %q", got)
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
	if out != in {
		t.Fatalf("round-trip mismatch: got %+v want %+v", out, in)
	}
}
