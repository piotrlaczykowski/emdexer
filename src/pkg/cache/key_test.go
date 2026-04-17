package cache

import "testing"

func TestBuildKey_Deterministic(t *testing.T) {
	k1 := BuildKey("ns", 0, "gemini-2.0", "hello")
	k2 := BuildKey("ns", 0, "gemini-2.0", "hello")
	if k1 != k2 {
		t.Fatalf("expected deterministic output, got %q vs %q", k1, k2)
	}
	if len(k1) != 64 {
		t.Fatalf("expected 64-char hex SHA-256, got len=%d (%q)", len(k1), k1)
	}
}

func TestBuildKey_Normalization(t *testing.T) {
	base := BuildKey("ns", 0, "m", "hello world")
	if got := BuildKey("ns", 0, "m", "  Hello World  "); got != base {
		t.Fatalf("whitespace/case normalization failed: %q != %q", got, base)
	}
}

func TestBuildKey_GenerationChangesKey(t *testing.T) {
	a := BuildKey("ns", 0, "m", "q")
	b := BuildKey("ns", 1, "m", "q")
	if a == b {
		t.Fatalf("different generations must produce different keys")
	}
}

func TestBuildKey_NamespaceChangesKey(t *testing.T) {
	a := BuildKey("nsA", 7, "m", "q")
	b := BuildKey("nsB", 7, "m", "q")
	if a == b {
		t.Fatalf("different namespaces must produce different keys")
	}
}

func TestBuildKey_ModelChangesKey(t *testing.T) {
	a := BuildKey("ns", 7, "gemini-2.0", "q")
	b := BuildKey("ns", 7, "gemini-2.5", "q")
	if a == b {
		t.Fatalf("different models must produce different keys")
	}
}
