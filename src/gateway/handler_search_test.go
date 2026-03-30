package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/piotrlaczykowski/emdexer/auth"
	"github.com/piotrlaczykowski/emdexer/rerank"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
)

// mockPointsClient implements qdrant.PointsClient with no-op Search and Query.
type mockPointsClient struct {
	qdrant.PointsClient
}

func (m *mockPointsClient) Search(_ context.Context, _ *qdrant.SearchPoints, _ ...grpc.CallOption) (*qdrant.SearchResponse, error) {
	return &qdrant.SearchResponse{}, nil
}

func (m *mockPointsClient) Query(_ context.Context, _ *qdrant.QueryPoints, _ ...grpc.CallOption) (*qdrant.QueryResponse, error) {
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
