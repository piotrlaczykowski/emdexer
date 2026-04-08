package indexer

import (
	"math"
	"strings"
	"testing"
)

func TestFixedChunker_Basic(t *testing.T) {
	words := strings.Repeat("word ", 100)
	chunks := FixedChunker{Size: 30, Overlap: 5}.Chunk(words)
	if len(chunks) == 0 {
		t.Fatal("expected chunks")
	}
	for _, c := range chunks {
		if w := len(strings.Fields(c)); w > 30 {
			t.Errorf("chunk has %d words, max 30", w)
		}
	}
}

func TestFixedChunker_DefaultsWhenZero(t *testing.T) {
	words := strings.Repeat("word ", 600)
	chunks := FixedChunker{}.Chunk(words)
	if len(chunks) == 0 {
		t.Fatal("expected chunks with default size")
	}
}

func TestSemanticChunker_NilEmbedder_FallsBack(t *testing.T) {
	text := strings.Repeat("This is a sentence about topic A. ", 20)
	chunks := SemanticChunker{MaxChunkWords: 50}.Chunk(text)
	if len(chunks) == 0 {
		t.Fatal("expected fallback chunks")
	}
}

func TestSemanticChunker_ShortText_FallsBack(t *testing.T) {
	// 2 sentences — below the 3-sentence threshold
	text := "First sentence. Second sentence."
	called := false
	chunker := SemanticChunker{
		MaxChunkWords: 50,
		Embedder: func(s string) ([]float32, error) {
			called = true
			return []float32{1, 0}, nil
		},
	}
	chunks := chunker.Chunk(text)
	if called {
		t.Error("embedder should not be called for short text")
	}
	if len(chunks) == 0 {
		t.Fatal("expected fallback chunks")
	}
}

func TestSemanticChunker_GroupsSimilarSentences(t *testing.T) {
	// Build 10 sentences. Sentences 0-4 embed to vector A, 5-9 to vector B.
	// With threshold 0.5, they should form 2 groups.
	vecA := []float32{1, 0, 0}
	vecB := []float32{0, 1, 0}

	sentences := make([]string, 10)
	for i := 0; i < 5; i++ {
		sentences[i] = "Topic A sentence number one here."
	}
	for i := 5; i < 10; i++ {
		sentences[i] = "Topic B sentence number two here."
	}
	text := strings.Join(sentences, " ")

	chunker := SemanticChunker{
		MaxChunkWords: 200,
		Threshold:     0.5,
		Embedder: func(s string) ([]float32, error) {
			if strings.Contains(s, "Topic A") {
				return vecA, nil
			}
			return vecB, nil
		},
	}
	chunks := chunker.Chunk(text)
	if len(chunks) < 2 {
		t.Errorf("expected at least 2 semantic groups, got %d", len(chunks))
	}
}

func TestCosineSimilarity_KnownVectors(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{0, 1}
	c := []float32{1, 0}

	if sim := CosineSimilarity(a, b); sim > 0.01 {
		t.Errorf("orthogonal vectors: expected ~0, got %f", sim)
	}
	if sim := CosineSimilarity(a, c); math.Abs(float64(sim-1.0)) > 0.001 {
		t.Errorf("identical vectors: expected ~1.0, got %f", sim)
	}
}

func TestCosineSimilarity_ZeroVector(t *testing.T) {
	a := []float32{0, 0}
	b := []float32{1, 0}
	if sim := CosineSimilarity(a, b); sim != 0 {
		t.Errorf("zero vector: expected 0, got %f", sim)
	}
}

func TestSplitSentences_Basic(t *testing.T) {
	text := "First sentence. Second sentence? Third one! And a fourth."
	sents := splitSentences(text)
	if len(sents) < 3 {
		t.Errorf("expected at least 3 sentences, got %d: %v", len(sents), sents)
	}
}
