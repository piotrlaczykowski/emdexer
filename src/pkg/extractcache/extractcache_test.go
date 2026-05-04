package extractcache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestNoop_AlwaysMisses(t *testing.T) {
	c := Noop{}
	got, ok := c.Get(context.Background(), "any-key")
	if ok {
		t.Fatalf("Noop.Get returned hit; got=%+v", got)
	}
	if err := c.Set(context.Background(), "any-key", &Result{Text: "x"}); err != nil {
		t.Fatalf("Noop.Set: %v", err)
	}
}

func newMini(t *testing.T) (*RedisCache, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	c, err := NewRedisCache("redis://"+mr.Addr(), 5*time.Minute)
	if err != nil {
		t.Fatalf("redis cache: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, mr
}

func TestRedisCache_RoundTrip(t *testing.T) {
	c, _ := newMini(t)
	ctx := context.Background()
	key := BuildKey("deadbeefdeadbeef", "extractous")

	if _, ok := c.Get(ctx, key); ok {
		t.Fatalf("unexpected hit on empty cache")
	}
	in := &Result{Text: "hello", Meta: map[string]string{"method": "tika"}, Extractor: "extractous"}
	if err := c.Set(ctx, key, in); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, ok := c.Get(ctx, key)
	if !ok {
		t.Fatalf("miss after set")
	}
	if got.Text != "hello" || got.Meta["method"] != "tika" {
		t.Fatalf("payload mismatch: %+v", got)
	}
	if got.SchemaV != schemaVersion {
		t.Fatalf("schema version not stamped: %d", got.SchemaV)
	}
	if got.ExtractAt.IsZero() {
		t.Fatalf("ExtractAt not stamped after Set")
	}
}

func TestRedisCache_TTLExpires(t *testing.T) {
	c, mr := newMini(t)
	ctx := context.Background()
	key := BuildKey("aaaa", "extractous")
	if err := c.Set(ctx, key, &Result{Text: "x"}); err != nil {
		t.Fatal(err)
	}
	mr.FastForward(6 * time.Minute)
	if _, ok := c.Get(ctx, key); ok {
		t.Fatalf("expected miss after TTL")
	}
}

func TestRedisCache_PingFailsWhenServerDown(t *testing.T) {
	mr, _ := miniredis.Run()
	addr := mr.Addr() // capture before Close tears down the internal server
	mr.Close()
	if _, err := NewRedisCache("redis://"+addr, time.Minute); err == nil {
		t.Fatalf("expected ping failure")
	}
}
