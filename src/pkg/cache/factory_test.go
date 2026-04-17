package cache

import (
	"testing"
)

func TestFactory_DisabledReturnsNilNil(t *testing.T) {
	t.Setenv("EMDEX_CACHE_ENABLED", "false")
	c, err := NewFromEnv()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if c != nil {
		t.Fatalf("expected nil cache when disabled, got %T", c)
	}
}

func TestFactory_DefaultBackendMemory(t *testing.T) {
	t.Setenv("EMDEX_CACHE_ENABLED", "true")
	t.Setenv("EMDEX_CACHE_BACKEND", "")
	t.Setenv("EMDEX_CACHE_TTL", "1m")
	t.Setenv("EMDEX_CACHE_MAX_ENTRIES", "8")
	c, err := NewFromEnv()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if _, ok := c.(*MemoryCache); !ok {
		t.Fatalf("expected MemoryCache, got %T", c)
	}
	_ = c.Close()
}

func TestFactory_InvalidTTLErrors(t *testing.T) {
	t.Setenv("EMDEX_CACHE_ENABLED", "true")
	t.Setenv("EMDEX_CACHE_TTL", "not-a-duration")
	if _, err := NewFromEnv(); err == nil {
		t.Fatal("expected error for invalid TTL")
	}
}

func TestFactory_UnknownBackendErrors(t *testing.T) {
	t.Setenv("EMDEX_CACHE_ENABLED", "true")
	t.Setenv("EMDEX_CACHE_BACKEND", "memcached")
	t.Setenv("EMDEX_CACHE_TTL", "1m")
	if _, err := NewFromEnv(); err == nil {
		t.Fatal("expected error for unknown backend")
	}
}
