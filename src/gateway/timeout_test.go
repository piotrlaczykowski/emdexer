package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"
)

// --- Mock registry ---

type MockRegistry struct {
	nodes map[string]NodeInfo
}

func NewMockRegistry() *MockRegistry {
	return &MockRegistry{nodes: make(map[string]NodeInfo)}
}

func (m *MockRegistry) Register(n NodeInfo) {
	n.LastSeen = time.Now()
	if n.RegisteredAt.IsZero() {
		n.RegisteredAt = time.Now()
	}
	m.nodes[n.ID] = n
}
func (m *MockRegistry) Deregister(id string) { delete(m.nodes, id) }
func (m *MockRegistry) List() []NodeInfo {
	out := make([]NodeInfo, 0, len(m.nodes))
	for _, n := range m.nodes {
		out = append(out, n)
	}
	return out
}

// --- Mock embedder ---

type MockEmbedder struct {
	Vector []float32
	Err    error
}

func (m *MockEmbedder) Embed(text string) ([]float32, error) {
	return m.Vector, m.Err
}

func (m *MockEmbedder) Name() string { return "mock" }

// --- Tests ---

func TestHandleSearchNamespaceAuth(t *testing.T) {
	srv := &Server{
		registry: NewMockRegistry(),
		embedder: &MockEmbedder{Vector: []float32{0.1, 0.2}},
	}

	// No AllowedNamespaces in context → 403
	req := httptest.NewRequest("GET", "/v1/search?q=hello&namespace=default", nil)
	rr := httptest.NewRecorder()
	srv.handleSearch(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 without AllowedNamespaces, got %d", rr.Code)
	}

	// Wrong namespace → 403
	req = httptest.NewRequest("GET", "/v1/search?q=hello&namespace=secret", nil)
	ctx := context.WithValue(req.Context(), "AllowedNamespaces", []string{"public"})
	req = req.WithContext(ctx)
	rr = httptest.NewRecorder()
	srv.handleSearch(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 for unauthorized namespace, got %d", rr.Code)
	}
}

func TestHandleSearchMissingQuery(t *testing.T) {
	srv := &Server{
		registry: NewMockRegistry(),
		embedder: &MockEmbedder{Vector: []float32{0.1}},
	}
	req := httptest.NewRequest("GET", "/v1/search?namespace=default", nil)
	ctx := context.WithValue(req.Context(), "AllowedNamespaces", []string{"*"})
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	srv.handleSearch(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing q, got %d", rr.Code)
	}
}

func TestHandleSearchMethodNotAllowed(t *testing.T) {
	srv := &Server{
		registry: NewMockRegistry(),
	}
	req := httptest.NewRequest("POST", "/v1/search?q=hello&namespace=default", nil)
	ctx := context.WithValue(req.Context(), "AllowedNamespaces", []string{"*"})
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	srv.handleSearch(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandleListNamespaces(t *testing.T) {
	reg := NewMockRegistry()
	reg.Register(NodeInfo{ID: "node-alpha", URL: "http://alpha:8080", Collections: []string{"docs", "code"}})
	reg.Register(NodeInfo{ID: "node-beta", URL: "http://beta:8080", Collections: []string{"code", "images"}})
	reg.Register(NodeInfo{ID: "node-gamma", URL: "http://gamma:8080", Collections: []string{"docs"}})

	srv := &Server{registry: reg}

	req := httptest.NewRequest("GET", "/v1/namespaces", nil)
	rr := httptest.NewRecorder()
	srv.handleListNamespaces(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp struct {
		Namespaces []string `json:"namespaces"`
		Nodes      []struct {
			ID         string   `json:"id"`
			URL        string   `json:"url"`
			Namespaces []string `json:"namespaces"`
			LastSeen   string   `json:"last_seen"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	sort.Strings(resp.Namespaces)
	expected := []string{"code", "docs", "images"}
	if len(resp.Namespaces) != len(expected) {
		t.Fatalf("expected %d namespaces, got %d: %v", len(expected), len(resp.Namespaces), resp.Namespaces)
	}
	for i, ns := range expected {
		if resp.Namespaces[i] != ns {
			t.Errorf("namespace[%d]: expected %q, got %q", i, ns, resp.Namespaces[i])
		}
	}

	if len(resp.Nodes) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(resp.Nodes))
	}
}

func TestRegistryLastSeenAndExpiry(t *testing.T) {
	reg := NewFileNodeRegistry(t.TempDir() + "/nodes.json")

	reg.Register(NodeInfo{ID: "alive", URL: "http://alive:8080", Collections: []string{"ns1"}})
	if nodes := reg.List(); len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}

	// Simulate a stale node by manually setting LastSeen in the past.
	reg.mu.Lock()
	stale := reg.nodes["alive"]
	stale.LastSeen = time.Now().Add(-4 * time.Minute) // > 180s
	reg.nodes["alive"] = stale
	reg.mu.Unlock()

	if nodes := reg.List(); len(nodes) != 0 {
		t.Errorf("expected stale node to be filtered out, got %d", len(nodes))
	}

	// Re-register (heartbeat) should make it visible again.
	reg.Register(NodeInfo{ID: "alive", URL: "http://alive:8080", Collections: []string{"ns1"}})
	if nodes := reg.List(); len(nodes) != 1 {
		t.Errorf("expected refreshed node to reappear, got %d", len(nodes))
	}
}

func TestRegistryPreservesRegisteredAt(t *testing.T) {
	reg := NewFileNodeRegistry(t.TempDir() + "/nodes.json")

	reg.Register(NodeInfo{ID: "n1", URL: "http://n1:8080", Collections: []string{"a"}})
	first := reg.List()[0].RegisteredAt

	time.Sleep(5 * time.Millisecond)
	reg.Register(NodeInfo{ID: "n1", URL: "http://n1:8080", Collections: []string{"a", "b"}})
	second := reg.List()[0].RegisteredAt

	if !first.Equal(second) {
		t.Errorf("RegisteredAt should be preserved on re-register: got %v then %v", first, second)
	}
	if len(reg.List()[0].Collections) != 2 {
		t.Errorf("expected Collections updated to 2, got %d", len(reg.List()[0].Collections))
	}
}

func TestNamespacesEmptyRegistry(t *testing.T) {
	srv := &Server{registry: NewMockRegistry()}

	req := httptest.NewRequest("GET", "/v1/namespaces", nil)
	rr := httptest.NewRecorder()
	srv.handleListNamespaces(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp struct {
		Namespaces []string      `json:"namespaces"`
		Nodes      []interface{} `json:"nodes"`
	}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Namespaces) != 0 {
		t.Errorf("expected 0 namespaces, got %d", len(resp.Namespaces))
	}
}

