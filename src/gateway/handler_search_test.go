package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/piotrlaczykowski/emdexer/auth"
	"github.com/piotrlaczykowski/emdexer/rerank"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
)

// mockPointsClient implements qdrant.PointsClient with no-op Search and Query.
// It captures args so tests can assert on which method was called and with what.
type mockPointsClient struct {
	qdrant.PointsClient
	lastQuery     *qdrant.QueryPoints
	lastSearchPts *qdrant.SearchPoints
	searchCalled  int
	queryCalled   int
}

func (m *mockPointsClient) Search(_ context.Context, in *qdrant.SearchPoints, _ ...grpc.CallOption) (*qdrant.SearchResponse, error) {
	m.searchCalled++
	m.lastSearchPts = in
	return &qdrant.SearchResponse{}, nil
}

func (m *mockPointsClient) Query(_ context.Context, in *qdrant.QueryPoints, _ ...grpc.CallOption) (*qdrant.QueryResponse, error) {
	m.queryCalled++
	m.lastQuery = in
	return &qdrant.QueryResponse{}, nil
}

// mockEmbedder returns a fixed zero vector.
type mockEmbedder struct{}

func (m *mockEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}

func (m *mockEmbedder) Name() string { return "mock" }

func (m *mockEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{0.1, 0.2, 0.3}
	}
	return out, nil
}

// newTestServer creates a minimal Server wired with mock dependencies.
func newTestServer() *Server {
	return &Server{
		pointsClient: &mockPointsClient{},
		embedder:     &mockEmbedder{},
		collection:   "test",
		reranker:     rerank.NoOpReranker{},
		bm25Enabled:  false,
	}
}

// requestWithNamespace builds a GET request with namespace auth injected into context.
func requestWithNamespace(target string, namespaces []string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := context.WithValue(r.Context(), auth.AllowedNamespacesKey, namespaces)
	return r.WithContext(ctx)
}

func TestHandleSearch_Returns200(t *testing.T) {
	s := newTestServer()
	w := httptest.NewRecorder()
	r := requestWithNamespace("/v1/search?q=hello&namespace=default", []string{"*"})

	s.handleSearch(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleSearch_DebugIncludesRRFConfig(t *testing.T) {
	s := newTestServer()
	w := httptest.NewRecorder()
	r := requestWithNamespace("/v1/search?q=hello&namespace=default&debug=true", []string{"*"})

	s.handleSearch(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	cfg, ok := resp["rrf_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected rrf_config in response, got: %v", resp)
	}
	for _, field := range []string{"k", "vector_weight", "bm25_weight"} {
		if _, exists := cfg[field]; !exists {
			t.Errorf("expected rrf_config.%s to be present", field)
		}
	}
}

func TestHandleSearch_MissingQuery_Returns400(t *testing.T) {
	s := newTestServer()
	w := httptest.NewRecorder()
	r := requestWithNamespace("/v1/search?namespace=default", []string{"*"})

	s.handleSearch(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", w.Code)
	}
}

func TestHandleSearch_BM25FallbackCounterIncrements(t *testing.T) {
	s := newTestServer()
	s.bm25Enabled = true // trigger hybrid path; mockPointsClient returns 0 results

	before := testutil.ToFloat64(bm25FallbackTotal.WithLabelValues("default"))

	w := httptest.NewRecorder()
	r := requestWithNamespace("/v1/search?q=hello&namespace=default", []string{"*"})
	s.handleSearch(w, r)

	after := testutil.ToFloat64(bm25FallbackTotal.WithLabelValues("default"))
	if after-before != 1 {
		t.Errorf("expected bm25FallbackTotal to increment by 1, got delta=%.0f", after-before)
	}
}

func TestHandleSearch_ModeSemantic_CallsSearchOnly(t *testing.T) {
	s := newTestServer()
	mock := s.pointsClient.(*mockPointsClient)

	w := httptest.NewRecorder()
	r := requestWithNamespace("/v1/search?q=hello&namespace=default&mode=semantic", []string{"*"})
	s.handleSearch(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if mock.searchCalled != 1 {
		t.Errorf("expected Search called once, got %d", mock.searchCalled)
	}
	if mock.queryCalled != 0 {
		t.Errorf("expected Query NOT called for semantic mode, got %d", mock.queryCalled)
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["mode"] != "semantic" {
		t.Errorf("expected response mode=semantic, got %v", resp["mode"])
	}
}

func TestHandleSearch_ModeKeyword_CallsQueryWithSinglePrefetch(t *testing.T) {
	s := newTestServer()
	mock := s.pointsClient.(*mockPointsClient)

	w := httptest.NewRecorder()
	r := requestWithNamespace("/v1/search?q=hello&namespace=default&mode=keyword", []string{"*"})
	s.handleSearch(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if mock.queryCalled != 1 {
		t.Fatalf("expected Query called once, got %d", mock.queryCalled)
	}
	if got := len(mock.lastQuery.Prefetch); got != 1 {
		t.Errorf("expected exactly 1 prefetch (text-only) for keyword mode, got %d", got)
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["mode"] != "keyword" {
		t.Errorf("expected response mode=keyword, got %v", resp["mode"])
	}
}

func TestHandleSearch_ModeHybrid_CallsQueryWithTwoPrefetches(t *testing.T) {
	s := newTestServer()
	s.bm25Enabled = false // explicit mode=hybrid should call HybridSearch regardless
	mock := s.pointsClient.(*mockPointsClient)

	w := httptest.NewRecorder()
	r := requestWithNamespace("/v1/search?q=hello&namespace=default&mode=hybrid", []string{"*"})
	s.handleSearch(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if mock.queryCalled != 1 {
		t.Fatalf("expected Query called once, got %d", mock.queryCalled)
	}
	if got := len(mock.lastQuery.Prefetch); got != 2 {
		t.Errorf("expected 2 prefetches (vector + text) for hybrid mode, got %d", got)
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["mode"] != "hybrid" {
		t.Errorf("expected response mode=hybrid, got %v", resp["mode"])
	}
}

func TestHandleSearch_ModeInvalid_Returns400(t *testing.T) {
	s := newTestServer()
	w := httptest.NewRecorder()
	r := requestWithNamespace("/v1/search?q=hello&namespace=default&mode=quantum", []string{"*"})
	s.handleSearch(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid mode, got %d: %s", w.Code, w.Body.String())
	}
}
