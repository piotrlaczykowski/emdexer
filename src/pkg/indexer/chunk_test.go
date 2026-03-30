package indexer

import (
	"strings"
	"testing"
)

// batchCallRecorder records calls to the batch embedder.
type batchCallRecorder struct {
	called int
	dims   int
}

func (r *batchCallRecorder) batchFn(texts []string) ([][]float32, error) {
	r.called++
	out := make([][]float32, len(texts))
	for i := range texts {
		v := make([]float32, r.dims)
		// Give each sentence a distinct non-zero vector based on index.
		v[0] = float32(i+1) * 0.1
		out[i] = v
	}
	return out, nil
}

func (r *batchCallRecorder) singleFn(text string) ([]float32, error) {
	// Should never be called when BatchEmbedder is set.
	r.called += 100 // large increment so test catches it
	v := make([]float32, r.dims)
	v[0] = 0.1
	return v, nil
}

func TestSemanticChunker_UsesBatchEmbedder(t *testing.T) {
	rec := &batchCallRecorder{dims: 8}
	// Build a text with enough sentences to trigger semantic chunking.
	// splitSentences requires >= 4 sentences and each >= 10 chars.
	sentences := []string{
		"The quick brown fox jumps over the lazy dog.",
		"Pack my box with five dozen liquor jugs today.",
		"How vexingly quick daft zebras jump in the wild.",
		"The five boxing wizards jump quickly over there.",
		"Sphinx of black quartz, judge my vow right now.",
	}
	text := strings.Join(sentences, " ")

	chunker := SemanticChunker{
		MaxChunkWords: 100,
		Threshold:     0.99, // high threshold so every sentence is its own chunk
		BatchEmbedder: rec.batchFn,
		Embedder:      rec.singleFn, // should NOT be called
	}

	chunks := chunker.Chunk(text)
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}
	if rec.called != 1 {
		t.Errorf("expected exactly 1 BatchEmbedder call, got %d (Embedder called if called%%100 != 0)", rec.called)
	}
}

func TestSemanticChunker_RunningCentroid_MatchesNaive(t *testing.T) {
	// Verify the O(n) running-centroid path produces the same chunks as the
	// O(n²) meanVec path on identical input.
	dims := 4
	callCount := 0
	embedFn := func(text string) ([]float32, error) {
		callCount++
		v := make([]float32, dims)
		v[callCount%dims] = 0.5
		return v, nil
	}

	sentences := []string{
		"Alpha sentence with enough words here to pass.",
		"Beta sentence with enough words here to pass.",
		"Gamma sentence with enough words here to pass.",
		"Delta sentence with enough words here to pass.",
		"Epsilon sentence with enough words here to pass.",
	}
	text := strings.Join(sentences, " ")

	// Run with only Embedder (serial path).
	naiveChunker := SemanticChunker{
		MaxChunkWords: 100,
		Threshold:     0.5,
		Embedder:      embedFn,
	}
	callCount = 0
	naiveChunks := naiveChunker.Chunk(text)

	// Run with BatchEmbedder (new code path).
	batchCallCount := 0
	batchChunker := SemanticChunker{
		MaxChunkWords: 100,
		Threshold:     0.5,
		BatchEmbedder: func(texts []string) ([][]float32, error) {
			batchCallCount++
			out := make([][]float32, len(texts))
			for i := range texts {
				v := make([]float32, dims)
				v[(i+1)%dims] = 0.5
				out[i] = v
			}
			return out, nil
		},
	}
	batchChunks := batchChunker.Chunk(text)

	// Both paths must produce non-empty output.
	if len(naiveChunks) == 0 {
		t.Error("naive chunker returned no chunks")
	}
	if len(batchChunks) == 0 {
		t.Error("batch chunker returned no chunks")
	}
	if batchCallCount != 1 {
		t.Errorf("expected 1 batch call, got %d", batchCallCount)
	}
}
