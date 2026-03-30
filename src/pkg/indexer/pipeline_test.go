package indexer

import (
	"context"
	"testing"
)

// batchRecordingEmbedder tracks whether EmbedBatch was called and with how many texts.
type batchRecordingEmbedder struct {
	batchCalled bool
	batchCount  int
	dims        int
}

func (m *batchRecordingEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	v := make([]float32, m.dims)
	v[0] = 0.1 // non-zero so IsZeroVector returns false
	return v, nil
}

func (m *batchRecordingEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	m.batchCalled = true
	m.batchCount = len(texts)
	results := make([][]float32, len(texts))
	for i := range texts {
		v := make([]float32, m.dims)
		v[0] = 0.1
		results[i] = v
	}
	return results, nil
}

func (m *batchRecordingEmbedder) Name() string { return "mock" }

func TestIndexDataToPoints_UsesBatchEmbed(t *testing.T) {
	embedder := &batchRecordingEmbedder{dims: 4}

	cfg := PipelineConfig{
		Namespace:  "test",
		ChunkSize:  10, // small chunks to produce multiple from 30-word content
		ChunkOverlap: 0,
		Embedder:   embedder,
		Extract: func(path string, content []byte, host string) (string, map[string]string, error) {
			// Return 30 words so the fixed chunker at size=10 produces 3 chunks.
			return "word1 word2 word3 word4 word5 word6 word7 word8 word9 word10 " +
				"word11 word12 word13 word14 word15 word16 word17 word18 word19 word20 " +
				"word21 word22 word23 word24 word25 word26 word27 word28 word29 word30", nil, nil
		},
	}

	points := IndexDataToPoints("test/file.txt", []byte("content"), cfg)
	if len(points) == 0 {
		t.Fatal("expected at least one point, got none")
	}
	if !embedder.batchCalled {
		t.Error("expected EmbedBatch to be called, but it was not")
	}
	if embedder.batchCount < 2 {
		t.Errorf("expected EmbedBatch called with ≥2 texts (multiple chunks), got %d", embedder.batchCount)
	}
}

// ctxAwareEmbedder returns ctx.Err() from EmbedBatch when the context is done.
type ctxAwareEmbedder struct {
	dims int
}

func (e *ctxAwareEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	v := make([]float32, e.dims)
	v[0] = 0.1
	return v, nil
}

func (e *ctxAwareEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	results := make([][]float32, len(texts))
	for i := range texts {
		v := make([]float32, e.dims)
		v[0] = 0.1
		results[i] = v
	}
	return results, nil
}

func (e *ctxAwareEmbedder) Name() string { return "ctx-aware-mock" }

func TestIndexDataToPoints_RespectsCtxCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	embedder := &ctxAwareEmbedder{dims: 4}
	cfg := PipelineConfig{
		Namespace:    "test",
		ChunkSize:    10,
		ChunkOverlap: 0,
		Embedder:     embedder,
		Ctx:          ctx,
		Extract: func(path string, content []byte, host string) (string, map[string]string, error) {
			return "word1 word2 word3 word4 word5 word6 word7 word8 word9 word10 " +
				"word11 word12 word13 word14 word15", nil, nil
		},
	}

	// With a pre-cancelled context, EmbedBatch returns ctx.Err(), so no points are produced.
	points := IndexDataToPoints("test/file.txt", []byte("content"), cfg)
	if len(points) != 0 {
		t.Errorf("expected empty points with cancelled context, got %d", len(points))
	}
}
