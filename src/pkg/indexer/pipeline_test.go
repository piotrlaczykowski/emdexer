package indexer

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/piotrlaczykowski/emdexer/extractcache"
	"github.com/zeebo/xxh3"
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

// fakeECache is a test double for extractcache.Cache.
type fakeECache struct {
	store map[string]*extractcache.Result
	gets  int32
	sets  int32
}

func (f *fakeECache) Get(_ context.Context, k string) (*extractcache.Result, bool) {
	atomic.AddInt32(&f.gets, 1)
	if f.store == nil {
		return nil, false
	}
	v, ok := f.store[k]
	return v, ok
}

func (f *fakeECache) Set(_ context.Context, k string, v *extractcache.Result) error {
	atomic.AddInt32(&f.sets, 1)
	if f.store == nil {
		f.store = map[string]*extractcache.Result{}
	}
	f.store[k] = v
	return nil
}

func (f *fakeECache) Close() error { return nil }

func TestIndexDataToPoints_ExtractCacheHit_SkipsExtractor(t *testing.T) {
	var extractCalls int32
	extractor := func(path string, content []byte, host string) (string, map[string]string, error) {
		atomic.AddInt32(&extractCalls, 1)
		return "fresh extracted text long enough to pass the ten char minimum check", nil, nil
	}
	content := []byte("hello world content for cache test")
	hashHex := extractcache.HexXXH3(xxh3.Hash(content))
	key := extractcache.BuildKey(hashHex, "extractous")
	cache := &fakeECache{store: map[string]*extractcache.Result{
		key: {Text: "cached text long enough to pass minimum check ok yes", Extractor: "extractous"},
	}}
	cfg := PipelineConfig{
		Namespace:    "ns",
		Embedder:     &batchRecordingEmbedder{dims: 4},
		Extract:      extractor,
		ExtractCache: cache,
		ChunkSize:    16,
		ChunkOverlap: 0,
	}
	pts := IndexDataToPoints("/some/path.txt", content, cfg)
	if len(pts) == 0 {
		t.Fatalf("no points produced")
	}
	if extractCalls != 0 {
		t.Fatalf("extractor called %d times; expected 0 on cache hit", extractCalls)
	}
}

func TestIndexDataToPoints_ExtractCacheMiss_StoresAfterExtract(t *testing.T) {
	var extractCalls int32
	extractor := func(path string, content []byte, host string) (string, map[string]string, error) {
		atomic.AddInt32(&extractCalls, 1)
		return "fresh extracted text long enough to pass minimum check yes", nil, nil
	}
	cache := &fakeECache{}
	cfg := PipelineConfig{
		Namespace:    "ns",
		Embedder:     &batchRecordingEmbedder{dims: 4},
		Extract:      extractor,
		ExtractCache: cache,
		ChunkSize:    16,
		ChunkOverlap: 0,
	}
	_ = IndexDataToPoints("/p.txt", []byte("body content"), cfg)
	if extractCalls != 1 {
		t.Fatalf("extractCalls=%d want 1", extractCalls)
	}
	if cache.sets != 1 {
		t.Fatalf("cache.sets=%d want 1", cache.sets)
	}
}
