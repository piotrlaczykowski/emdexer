package indexer

import (
	"strings"
	"testing"
)

func TestSmartChunk_CustomSizeOverlap(t *testing.T) {
	words := make([]string, 100)
	for i := range words {
		words[i] = "word"
	}
	text := strings.Join(words, " ")

	chunks := SmartChunk(text, 30, 5)
	if len(chunks) == 0 {
		t.Fatal("expected chunks, got none")
	}
	for _, c := range chunks {
		w := strings.Fields(c)
		if len(w) > 30 {
			t.Errorf("chunk has %d words, max is 30", len(w))
		}
	}
}

func TestSmartChunk_OverlapClamp(t *testing.T) {
	// overlap >= size should not panic or infinite loop
	words := make([]string, 50)
	for i := range words {
		words[i] = "word"
	}
	text := strings.Join(words, " ")
	chunks := SmartChunk(text, 10, 10) // overlap == size → step=1
	if len(chunks) == 0 {
		t.Fatal("expected chunks with clamped overlap")
	}
}
