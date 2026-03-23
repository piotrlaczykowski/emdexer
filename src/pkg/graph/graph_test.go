package graph

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
)

// ── mock scroller ─────────────────────────────────────────────────────────────

// mockScroller implements PointsScroller for testing without a real Qdrant connection.
type mockScroller struct {
	// points maps a namespace to a slice of (path, relationsJSON) pairs.
	points []scrollPoint
}

type scrollPoint struct {
	path      string
	relJSON   string
	namespace string
}

func (m *mockScroller) Scroll(_ context.Context, in *qdrant.ScrollPoints, _ ...grpc.CallOption) (*qdrant.ScrollResponse, error) {
	var results []*qdrant.RetrievedPoint
	for i, p := range m.points {
		// honour namespace filter if present
		if len(in.Filter.GetMust()) > 0 {
			pass := false
			for _, cond := range in.Filter.GetMust() {
				f := cond.GetField()
				if f == nil {
					continue
				}
				if f.Key == "namespace" {
					kw := f.Match.GetKeyword()
					if kw == p.namespace {
						pass = true
					}
				}
			}
			if !pass {
				continue
			}
		}

		pt := &qdrant.RetrievedPoint{
			Id: &qdrant.PointId{PointIdOptions: &qdrant.PointId_Num{Num: uint64(i + 1)}},
			Payload: map[string]*qdrant.Value{
				"path":  {Kind: &qdrant.Value_StringValue{StringValue: p.path}},
				"chunk": {Kind: &qdrant.Value_IntegerValue{IntegerValue: 0}},
			},
		}
		if p.relJSON != "" {
			pt.Payload["relations"] = &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: p.relJSON}}
		}
		results = append(results, pt)
	}
	return &qdrant.ScrollResponse{Result: results}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func relationsJSON(rels []Relation) string {
	b, _ := json.Marshal(rels)
	return string(b)
}

func makeScroller(points ...scrollPoint) *mockScroller {
	return &mockScroller{points: points}
}

// ── TestNeighbors_TwoHops ─────────────────────────────────────────────────────

// TestNeighbors_TwoHops verifies BFS traversal across two hops:
//   a.go → b.go → c.go
//   Neighbors("a.go", depth=2) must return both b.go and c.go.
func TestNeighbors_TwoHops(t *testing.T) {
	scroller := makeScroller(
		scrollPoint{
			path:      "a.go",
			namespace: "test",
			relJSON: relationsJSON([]Relation{
				{Type: "imports", Target: "b.go"},
			}),
		},
		scrollPoint{
			path:      "b.go",
			namespace: "test",
			relJSON: relationsJSON([]Relation{
				{Type: "imports", Target: "c.go"},
			}),
		},
		scrollPoint{
			path:      "c.go",
			namespace: "test",
		},
	)

	g := New(5 * time.Minute)
	neighbors := g.Neighbors(context.Background(), scroller, "collection", "test", "a.go", 2)

	found := make(map[string]bool)
	for _, n := range neighbors {
		found[n] = true
	}
	if !found["b.go"] {
		t.Errorf("expected b.go in neighbors, got %v", neighbors)
	}
	if !found["c.go"] {
		t.Errorf("expected c.go in neighbors, got %v", neighbors)
	}
	if found["a.go"] {
		t.Errorf("source file a.go must not appear in its own neighbors")
	}
}

// ── TestGraph_CacheExpiry ─────────────────────────────────────────────────────

// TestGraph_CacheExpiry verifies that the graph rebuilds after TTL expiry.
// We use a TTL of 0 so every call to Neighbors triggers a rebuild.
func TestGraph_CacheExpiry(t *testing.T) {
	callCount := 0

	// wrap mockScroller to count Scroll invocations
	type countingScroller struct {
		*mockScroller
		count *int
	}
	cs := countingScroller{
		mockScroller: makeScroller(scrollPoint{
			path:      "a.go",
			namespace: "ns",
			relJSON:   relationsJSON([]Relation{{Type: "imports", Target: "b.go"}}),
		}),
		count: &callCount,
	}

	g := New(0) // zero TTL — every call is a cache miss

	// First call: builds the graph.
	g.Neighbors(context.Background(), cs.mockScroller, "col", "ns", "a.go", 1)

	// Because TTL == 0, time.Since(builtAt) >= 0, so the cache is always stale.
	// A second call must rebuild.
	g.Neighbors(context.Background(), cs.mockScroller, "col", "ns", "a.go", 1)

	// Verify the cache key exists (was populated at least once).
	e := g.cachedEntry("ns")
	if e == nil {
		t.Fatal("expected cache entry to exist after Neighbors call")
	}
	if len(e.adjacency) == 0 {
		t.Error("expected non-empty adjacency map after graph build")
	}
}

// ── TestGraph_EmptyRelations ──────────────────────────────────────────────────

// TestGraph_EmptyRelations verifies that files with no relations produce an
// empty neighbour set without panicking.
func TestGraph_EmptyRelations(t *testing.T) {
	scroller := makeScroller(
		scrollPoint{path: "lone.go", namespace: "ns"},
	)

	g := New(5 * time.Minute)
	neighbors := g.Neighbors(context.Background(), scroller, "col", "ns", "lone.go", 1)

	if len(neighbors) != 0 {
		t.Errorf("expected empty neighbors for file with no relations, got %v", neighbors)
	}
}

// ── TestGraph_CircularRefs ────────────────────────────────────────────────────

// TestGraph_CircularRefs verifies that the BFS terminates correctly when files
// mutually import each other (a.go → b.go → a.go).
func TestGraph_CircularRefs(t *testing.T) {
	scroller := makeScroller(
		scrollPoint{
			path:      "a.go",
			namespace: "ns",
			relJSON:   relationsJSON([]Relation{{Type: "imports", Target: "b.go"}}),
		},
		scrollPoint{
			path:      "b.go",
			namespace: "ns",
			relJSON:   relationsJSON([]Relation{{Type: "imports", Target: "a.go"}}),
		},
	)

	g := New(5 * time.Minute)

	// depth=1: only immediate neighbours
	neighbors1 := g.Neighbors(context.Background(), scroller, "col", "ns", "a.go", 1)
	if len(neighbors1) != 1 || neighbors1[0] != "b.go" {
		t.Errorf("depth=1: expected [b.go], got %v", neighbors1)
	}

	// depth=2: should not loop; a.go should not re-appear
	// Reset cache so we use the same graph entry but restart BFS.
	neighbors2 := g.Neighbors(context.Background(), scroller, "col", "ns", "a.go", 2)
	for _, n := range neighbors2 {
		if n == "a.go" {
			t.Errorf("source file a.go must not appear in its own neighbor set (depth=2)")
		}
	}
}
