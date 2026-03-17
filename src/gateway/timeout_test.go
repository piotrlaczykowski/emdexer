package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"sync"

	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
)

// MockPointsClient implements a subset of qdrant.PointsClient for testing
type MockPointsClient struct {
	qdrant.PointsClient
	Delay time.Duration
}

func (m *MockPointsClient) Search(ctx context.Context, in *qdrant.SearchPoints, opts ...grpc.CallOption) (*qdrant.SearchResponse, error) {
	select {
	case <-time.After(m.Delay):
		return &qdrant.SearchResponse{
			Result: []*qdrant.ScoredPoint{
				{Id: &qdrant.PointId{PointIdOptions: &qdrant.PointId_Num{Num: 1}}, Score: 0.9},
			},
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// MockEmbedder implements embed.EmbedProvider
type MockEmbedder struct{}

func (m *MockEmbedder) Embed(text string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}
func (m *MockEmbedder) Name() string { return "mock" }

type MockRegistry struct {
	Nodes []NodeInfo
}

func (m *MockRegistry) Register(n NodeInfo) {}
func (m *MockRegistry) Deregister(id string) {}
func (m *MockRegistry) List() []NodeInfo     { return m.Nodes }

func TestSearchTimeoutAndAggregation(t *testing.T) {
	registry := &MockRegistry{
		Nodes: []NodeInfo{
			{ID: "node-1", URL: "localhost:1"},
			{ID: "node-2", URL: "localhost:2"},
		},
	}

	// This is a bit tricky because searchQdrant is a global function in main.go
	// and it's not a method of Server. However, Server.pointsClient is used.
	
	srv := &Server{
		registry:     registry,
		pointsClient: &MockPointsClient{Delay: 4 * time.Second}, // Longer than 3s timeout
		embedder:     &MockEmbedder{},
		collection:   "test",
	}

	req := httptest.NewRequest("GET", "/v1/search?q=test&namespace=default", nil)
	// Add allowed namespaces to context
	ctx := context.WithValue(req.Context(), "AllowedNamespaces", []string{"*"})
	req = req.WithContext(ctx)
	
	rr := httptest.NewRecorder()

	start := time.Now()
	srv.handleSearch(rr, req)
	duration := time.Since(start)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Query   string         `json:"query"`
		Results []SearchResult `json:"results"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v, body: %s", err, rr.Body.String())
	}

	if len(resp.Results) != 0 {
		t.Errorf("expected 0 results due to timeouts, got %d", len(resp.Results))
	}

	// The total time should be around 3s (parallel timeouts), not 6s (serial) or 4s (mock delay)
	if duration > 3500*time.Millisecond {
		t.Errorf("search took too long: %v (should be ~3s)", duration)
	}
}

func TestSearchPartialAggregation(t *testing.T) {
	registry := &MockRegistry{
		Nodes: []NodeInfo{
			{ID: "fast-node", URL: "localhost:1"},
			{ID: "slow-node", URL: "localhost:2"},
		},
	}

	// We need a conditional delay. Let's wrap the mock client.
	srv := &Server{
		registry: registry,
		pointsClient: &ConditionalMockPointsClient{
			Delay: 4 * time.Second,
		},
		embedder:   &MockEmbedder{},
		collection: "test",
	}

	req := httptest.NewRequest("GET", "/v1/search?q=test&namespace=default", nil)
	ctx := context.WithValue(req.Context(), "AllowedNamespaces", []string{"*"})
	req = req.WithContext(ctx)
	
	rr := httptest.NewRecorder()

	srv.handleSearch(rr, req)

	var resp struct {
		Results []SearchResult `json:"results"`
	}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	// Should have exactly 1 result (from the fast node)
	if len(resp.Results) != 1 {
		t.Errorf("expected 1 result from aggregation, got %d", len(resp.Results))
	}
}

type ConditionalMockPointsClient struct {
	qdrant.PointsClient
	Delay     time.Duration
	mu        sync.Mutex
	callCount int
}

func (m *ConditionalMockPointsClient) Search(ctx context.Context, in *qdrant.SearchPoints, opts ...grpc.CallOption) (*qdrant.SearchResponse, error) {
	m.mu.Lock()
	m.callCount++
	count := m.callCount
	m.mu.Unlock()

	if count%2 == 0 { // Simulate slow node for even calls
		select {
		case <-time.After(m.Delay):
			return &qdrant.SearchResponse{
				Result: []*qdrant.ScoredPoint{
					{Id: &qdrant.PointId{PointIdOptions: &qdrant.PointId_Num{Num: 1}}, Score: 0.9},
				},
			}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return &qdrant.SearchResponse{
		Result: []*qdrant.ScoredPoint{
			{Id: &qdrant.PointId{PointIdOptions: &qdrant.PointId_Num{Num: 1}}, Score: 0.9},
		},
	}, nil
}
