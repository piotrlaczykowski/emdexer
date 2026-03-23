// Package graph provides a lightweight in-memory knowledge graph built lazily
// from Qdrant payload metadata. It tracks structural relations between indexed
// files (imports, links) and exposes BFS-based neighbour lookup for graph-
// augmented RAG retrieval.
package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
)

// ── Prometheus metrics ────────────────────────────────────────────────────────

var graphExpansionsTotal = promauto.NewCounter(prometheus.CounterOpts{
	Name: "emdexer_gateway_graph_expansions_total",
	Help: "Total number of graph expansions triggered after initial hybrid search",
})

var graphNeighborsFound = promauto.NewHistogram(prometheus.HistogramOpts{
	Name:    "emdexer_gateway_graph_neighbors_found",
	Help:    "Number of neighbour files discovered per graph expansion",
	Buckets: []float64{0, 1, 2, 5, 10, 20, 50, 100},
})

var graphCacheHitsTotal = promauto.NewCounter(prometheus.CounterOpts{
	Name: "emdexer_gateway_graph_cache_hits_total",
	Help: "Number of times the graph was served from the in-memory cache without rebuilding",
})

// ── Types ─────────────────────────────────────────────────────────────────────

// Relation is the JSON-decoded form of one entry in the `relations` Qdrant payload field.
// Only "imports" and "links_to" relations contribute graph edges; "defines" is stored
// for future cross-file identifier lookup but does not create adjacency entries.
type Relation struct {
	Type   string `json:"type"`
	Target string `json:"target,omitempty"` // imports, links_to
	Name   string `json:"name,omitempty"`   // defines
}

// PointsScroller is the subset of qdrant.PointsClient required by BuildGraph.
// Using an interface allows test doubles without a live Qdrant instance.
type PointsScroller interface {
	Scroll(ctx context.Context, in *qdrant.ScrollPoints, opts ...grpc.CallOption) (*qdrant.ScrollResponse, error)
}

// entry is a single cached adjacency list snapshot for one namespace.
type entry struct {
	adjacency map[string][]string // file path → []reachable file paths
	builtAt   time.Time
}

// Graph is a concurrency-safe, lazily-built in-memory directed knowledge graph.
// One Graph instance is shared across all requests; entries are cached per namespace
// and refreshed after TTL expiry.
type Graph struct {
	mu    sync.RWMutex
	cache map[string]*entry
	ttl   time.Duration
}

// New creates a Graph with the given cache TTL.  5*time.Minute is the recommended default.
func New(ttl time.Duration) *Graph {
	return &Graph{
		cache: make(map[string]*entry),
		ttl:   ttl,
	}
}

// BuildGraph scrolls all chunk-0 points in the namespace, parses the `relations` payload field,
// and builds a fresh adjacency map.  The result replaces any previous cached entry.
// Callers should check the returned error before calling Neighbours.
func (g *Graph) BuildGraph(ctx context.Context, pc PointsScroller, collection, namespace string) error {
	adj := make(map[string][]string)

	// Filter: namespace match + chunk == 0 (relations are only stored on the first chunk).
	var mustConds []*qdrant.Condition
	if namespace != "" {
		mustConds = append(mustConds, &qdrant.Condition{
			ConditionOneOf: &qdrant.Condition_Field{
				Field: &qdrant.FieldCondition{
					Key: "namespace",
					Match: &qdrant.Match{
						MatchValue: &qdrant.Match_Keyword{Keyword: namespace},
					},
				},
			},
		})
	}
	mustConds = append(mustConds, &qdrant.Condition{
		ConditionOneOf: &qdrant.Condition_Field{
			Field: &qdrant.FieldCondition{
				Key: "chunk",
				Match: &qdrant.Match{
					MatchValue: &qdrant.Match_Integer{Integer: 0},
				},
			},
		},
	})
	filter := &qdrant.Filter{Must: mustConds}

	limit := uint32(250)
	var offset *qdrant.PointId

	for {
		req := &qdrant.ScrollPoints{
			CollectionName: collection,
			Filter:         filter,
			Limit:          &limit,
			WithPayload: &qdrant.WithPayloadSelector{
				SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: true},
			},
		}
		if offset != nil {
			req.Offset = offset
		}

		resp, err := pc.Scroll(ctx, req)
		if err != nil {
			return fmt.Errorf("graph scroll namespace=%q: %w", namespace, err)
		}

		for _, pt := range resp.GetResult() {
			path := stringPayload(pt.Payload, "path")
			if path == "" {
				continue
			}
			relJSON := stringPayload(pt.Payload, "relations")
			if relJSON == "" {
				continue
			}
			var rels []Relation
			if err := json.Unmarshal([]byte(relJSON), &rels); err != nil {
				log.Printf("[graph] bad relations JSON for %q: %v", path, err)
				continue
			}
			for _, r := range rels {
				if (r.Type == "imports" || r.Type == "links_to") && r.Target != "" {
					adj[path] = append(adj[path], r.Target)
				}
			}
		}

		next := resp.GetNextPageOffset()
		if next == nil {
			break
		}
		offset = next
	}

	g.mu.Lock()
	g.cache[namespace] = &entry{adjacency: adj, builtAt: time.Now()}
	g.mu.Unlock()

	log.Printf("[graph] built namespace=%q: %d files with outgoing edges", namespace, len(adj))
	return nil
}

// Neighbors returns files reachable from file within depth hops (clamped to [1, 3]).
// It rebuilds the graph from Qdrant on a cold start or after TTL expiry.
// On any error the function returns nil so callers can skip graph expansion silently.
func (g *Graph) Neighbors(ctx context.Context, pc PointsScroller, collection, namespace, file string, depth int) []string {
	if depth < 1 {
		depth = 1
	}
	if depth > 3 {
		depth = 3
	}

	e := g.cachedEntry(namespace)
	if e != nil && time.Since(e.builtAt) < g.ttl {
		graphCacheHitsTotal.Inc()
	} else {
		if err := g.BuildGraph(ctx, pc, collection, namespace); err != nil {
			log.Printf("[graph] BuildGraph failed namespace=%q: %v — skipping expansion", namespace, err)
			return nil
		}
		e = g.cachedEntry(namespace)
	}

	if e == nil {
		return nil
	}

	graphExpansionsTotal.Inc()

	// BFS — visited set prevents revisiting nodes and handles circular references.
	visited := map[string]bool{file: true}
	frontier := []string{file}

	for hop := 0; hop < depth; hop++ {
		var next []string
		for _, f := range frontier {
			for _, nb := range e.adjacency[f] {
				if !visited[nb] {
					visited[nb] = true
					next = append(next, nb)
				}
			}
		}
		frontier = next
		if len(frontier) == 0 {
			break
		}
	}

	out := make([]string, 0, len(visited)-1)
	for f := range visited {
		if f != file {
			out = append(out, f)
		}
	}
	graphNeighborsFound.Observe(float64(len(out)))
	return out
}

// cachedEntry returns the cached entry for the namespace under a read lock.
func (g *Graph) cachedEntry(namespace string) *entry {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.cache[namespace]
}

// stringPayload extracts a string value from a Qdrant point payload map.
func stringPayload(payload map[string]*qdrant.Value, key string) string {
	v, ok := payload[key]
	if !ok {
		return ""
	}
	if sv, ok := v.Kind.(*qdrant.Value_StringValue); ok {
		return sv.StringValue
	}
	return ""
}
