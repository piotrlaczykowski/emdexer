package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/piotrlaczykowski/emdexer/registry"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
)

// stubRegistry is an in-memory registry.NodeRegistry for tests.
type stubRegistry struct {
	nodes []registry.NodeInfo
}

func (s *stubRegistry) Register(_ context.Context, n registry.NodeInfo) error {
	s.nodes = append(s.nodes, n)
	return nil
}
func (s *stubRegistry) Deregister(_ context.Context, _ string) error { return nil }
func (s *stubRegistry) List(_ context.Context) ([]registry.NodeInfo, error) {
	out := make([]registry.NodeInfo, len(s.nodes))
	copy(out, s.nodes)
	return out, nil
}

// countingPointsClient extends mockPointsClient with a Count implementation
// that returns per-namespace counts from a static map, keyed by the namespace
// keyword in the first Must filter condition.
type countingPointsClient struct {
	mockPointsClient
	counts map[string]uint64
}

func (c *countingPointsClient) Count(_ context.Context, req *qdrant.CountPoints, _ ...grpc.CallOption) (*qdrant.CountResponse, error) {
	var ns string
	if req.Filter != nil {
		for _, cond := range req.Filter.Must {
			if f := cond.GetField(); f != nil && f.Key == "namespace" {
				if m := f.Match; m != nil {
					if kw, ok := m.MatchValue.(*qdrant.Match_Keyword); ok {
						ns = kw.Keyword
						break
					}
				}
			}
		}
	}
	return &qdrant.CountResponse{
		Result: &qdrant.CountResult{Count: c.counts[ns]},
	}, nil
}

func TestHandleNamespaceStats_OK(t *testing.T) {
	reg := &stubRegistry{nodes: []registry.NodeInfo{
		{ID: "node-a", Namespaces: []string{"shared", "alpha"}, LastHeartbeat: time.Now()},
		{ID: "node-b", Namespaces: []string{"shared", "beta"}, LastHeartbeat: time.Now()},
	}}
	s := &Server{
		reg:          reg,
		pointsClient: &countingPointsClient{counts: map[string]uint64{"shared": 100, "alpha": 40, "beta": 20}},
		collection:   "test",
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/namespaces/stats", nil)
	s.handleNamespaceStats(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var stats []NamespaceStat
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if len(stats) != 3 {
		t.Fatalf("expected 3 namespaces, got %d: %+v", len(stats), stats)
	}

	// Response is sorted by namespace: alpha, beta, shared.
	want := []struct {
		ns    string
		count uint64
		ids   []string
	}{
		{"alpha", 40, []string{"node-a"}},
		{"beta", 20, []string{"node-b"}},
		{"shared", 100, []string{"node-a", "node-b"}},
	}
	for i, exp := range want {
		got := stats[i]
		if got.Namespace != exp.ns {
			t.Errorf("stats[%d].namespace=%q want %q", i, got.Namespace, exp.ns)
		}
		if got.VectorCount != exp.count {
			t.Errorf("stats[%d].vector_count=%d want %d", i, got.VectorCount, exp.count)
		}
		if len(got.NodeIDs) != len(exp.ids) {
			t.Errorf("stats[%d].node_ids=%v want %v", i, got.NodeIDs, exp.ids)
			continue
		}
		for j, id := range exp.ids {
			if got.NodeIDs[j] != id {
				t.Errorf("stats[%d].node_ids[%d]=%q want %q", i, j, got.NodeIDs[j], id)
			}
		}
		// Non-DB registry: last_indexed_at must be absent.
		if got.LastIndexedAt != nil {
			t.Errorf("stats[%d].last_indexed_at expected nil for stub registry, got %v", i, got.LastIndexedAt)
		}
	}
}

func TestHandleNamespaceStats_MethodNotAllowed(t *testing.T) {
	s := &Server{reg: &stubRegistry{}, pointsClient: &countingPointsClient{}, collection: "test"}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/namespaces/stats", nil)
	s.handleNamespaceStats(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandleNamespaceStats_EmptyRegistry(t *testing.T) {
	s := &Server{reg: &stubRegistry{}, pointsClient: &countingPointsClient{}, collection: "test"}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/namespaces/stats", nil)
	s.handleNamespaceStats(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	var stats []NamespaceStat
	if err := json.Unmarshal([]byte(body), &stats); err != nil {
		t.Fatalf("failed to parse JSON: %v — body=%s", err, body)
	}
	if len(stats) != 0 {
		t.Errorf("expected empty array, got %d entries", len(stats))
	}
	if body == "null" {
		t.Errorf("expected JSON array [], got null body")
	}
}
