package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type MockRegistry struct {
	Nodes []NodeInfo
}

func (m *MockRegistry) Register(n NodeInfo) {}
func (m *MockRegistry) Deregister(id string) {}
func (m *MockRegistry) List() []NodeInfo     { return m.Nodes }

func TestSearchTimeoutAndAggregation(t *testing.T) {
	// Start mock nodes
	node1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(4 * time.Second) // Longer than 3s timeout
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []SearchResult{{Path: "file1", Score: 0.9}},
		})
	}))
	defer node1.Close()

	node2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(4 * time.Second)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []SearchResult{{Path: "file2", Score: 0.8}},
		})
	}))
	defer node2.Close()

	registry := &MockRegistry{
		Nodes: []NodeInfo{
			{ID: "node-1", URL: node1.URL},
			{ID: "node-2", URL: node2.URL},
		},
	}

	srv := &Server{
		registry: registry,
	}

	req := httptest.NewRequest("GET", "/v1/search?q=test&namespace=default", nil)
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

	if duration > 3500*time.Millisecond {
		t.Errorf("search took too long: %v (should be ~3s)", duration)
	}
}

func TestSearchPartialAggregation(t *testing.T) {
	nodeFast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []SearchResult{{Path: "fast", Score: 1.0}},
		})
	}))
	defer nodeFast.Close()

	nodeSlow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(4 * time.Second)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []SearchResult{{Path: "slow", Score: 0.5}},
		})
	}))
	defer nodeSlow.Close()

	registry := &MockRegistry{
		Nodes: []NodeInfo{
			{ID: "fast-node", URL: nodeFast.URL},
			{ID: "slow-node", URL: nodeSlow.URL},
		},
	}

	srv := &Server{
		registry: registry,
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

	if len(resp.Results) != 1 {
		t.Errorf("expected 1 result from aggregation, got %d", len(resp.Results))
	}
	if resp.Results[0].Path != "fast" {
		t.Errorf("expected result from fast node, got %s", resp.Results[0].Path)
	}
}

