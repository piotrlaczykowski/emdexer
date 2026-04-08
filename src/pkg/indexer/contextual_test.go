package indexer

import (
	"fmt"
	"strings"
	"testing"
)

func TestBuildDocContext_ShortDocSkipped(t *testing.T) {
	called := false
	llm := func(p string) (string, error) { called = true; return "summary", nil }
	result := BuildDocContext("short", llm)
	if called || result != "" {
		t.Error("expected LLM to be skipped for short doc")
	}
}

func TestBuildDocContext_ReturnsContext(t *testing.T) {
	longDoc := strings.Repeat("word ", 300)
	llm := func(p string) (string, error) {
		if !strings.Contains(p, "word") {
			return "", fmt.Errorf("prompt missing doc content")
		}
		return "  A document about words.  ", nil
	}
	result := BuildDocContext(longDoc, llm)
	if result != "A document about words." {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestEnrichChunkWithContext_Empty(t *testing.T) {
	result := EnrichChunkWithContext("chunk text", "")
	if result != "chunk text" {
		t.Errorf("expected passthrough, got %q", result)
	}
}

func TestEnrichChunkWithContext_Prepends(t *testing.T) {
	result := EnrichChunkWithContext("chunk text", "doc summary")
	if !strings.HasPrefix(result, "[Context: doc summary]") {
		t.Errorf("expected context prefix, got %q", result)
	}
	if !strings.Contains(result, "chunk text") {
		t.Error("expected original chunk to be present")
	}
}

func TestBuildDocContext_LLMError(t *testing.T) {
	longDoc := strings.Repeat("word ", 300)
	llm := func(p string) (string, error) { return "", fmt.Errorf("LLM error") }
	result := BuildDocContext(longDoc, llm)
	if result != "" {
		t.Errorf("expected empty on LLM error, got %q", result)
	}
}
