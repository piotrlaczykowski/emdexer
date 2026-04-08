package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/piotrlaczykowski/emdexer/audit"
	"github.com/piotrlaczykowski/emdexer/auth"
	"github.com/piotrlaczykowski/emdexer/search"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
)

// GraphEdge represents a directed edge between two files in the knowledge graph.
type GraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// parseGraphSearchRequest reads query/depth/namespace from a JSON body (POST)
// or URL query parameters (GET), whichever is present.
// Defaults: depth=1, namespace="default".
func parseGraphSearchRequest(r *http.Request) (query, namespace string, depth int, err error) {
	depth = 1
	namespace = "default"

	if r.Method == http.MethodPost && strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Query     string `json:"query"`
			Q         string `json:"q"` // alias
			Depth     int    `json:"depth"`
			Namespace string `json:"namespace"`
		}
		if err = json.NewDecoder(r.Body).Decode(&body); err != nil {
			return
		}
		query = body.Query
		if query == "" {
			query = body.Q
		}
		if body.Depth > 0 {
			depth = body.Depth
		}
		if body.Namespace != "" {
			namespace = body.Namespace
		}
	} else {
		query = r.URL.Query().Get("q")
		if d := r.URL.Query().Get("depth"); d != "" {
			if n, atoiErr := strconv.Atoi(d); atoiErr == nil {
				depth = n
			}
		}
		if ns := r.URL.Query().Get("namespace"); ns != "" {
			namespace = ns
		}
	}

	if depth < 1 {
		depth = 1
	}
	if depth > 3 {
		depth = 3
	}

	if query == "" {
		err = fmt.Errorf("missing query — use ?q= or JSON body {\"query\":\"...\"}")
	}
	return
}

// handleGraphSearch implements GET and POST /v1/search/graph.
//
// Accepts query via URL param (?q=) or JSON body ({"query":"..."}).
//
// Response:
//
//	{
//	  "query":          "...",
//	  "results":        [...],
//	  "graph_nodes":    ["file.go", ...],
//	  "graph_edges":    [{"source": "a.go", "target": "b.go"}, ...],
//	  "query_time_ms":  42
//	}
func (s *Server) handleGraphSearch(w http.ResponseWriter, r *http.Request) {
	ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.graph.search")
	defer span.End()
	r = r.WithContext(ctx)

	start := time.Now()

	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	allowedNamespaces, ok := auth.GetAllowedNamespaces(r)
	if !ok {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	rawQuery, namespace, depth, parseErr := parseGraphSearchRequest(r)
	if parseErr != nil {
		http.Error(w, parseErr.Error(), http.StatusBadRequest)
		return
	}
	query := logSafe(rawQuery)
	namespace = logSafe(strings.TrimSpace(namespace))

	isAllowed := false
	for _, ns := range allowedNamespaces {
		if ns == "*" || ns == namespace {
			isAllowed = true
			break
		}
	}
	if !isAllowed {
		http.Error(w, "Forbidden: Namespace not authorized", http.StatusForbidden)
		return
	}

	if !s.graphCfg.Enabled {
		http.Error(w, "graph RAG is disabled (EMDEX_GRAPH_ENABLED=false)", http.StatusServiceUnavailable)
		return
	}

	span.SetAttributes(
		attribute.String("graph.namespace", namespace),
		attribute.Int("graph.depth", depth),
	)

	embedCtx, embedCancel := context.WithTimeout(r.Context(), s.embedTimeout)
	defer embedCancel()
	vector, err := s.embedder.Embed(embedCtx, query)
	if err != nil {
		http.Error(w, fmt.Sprintf("embedding error: %v", err), http.StatusInternalServerError)
		return
	}

	// Initial search — results that seed graph expansion.
	searchCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var results []search.Result
	if s.bm25Enabled {
		results, err = search.HybridSearch(searchCtx, s.pointsClient, s.collection, query, vector, 10, namespace)
	} else {
		results, err = search.SearchQdrant(searchCtx, s.pointsClient, s.collection, vector, 10, namespace)
	}
	if err != nil {
		http.Error(w, fmt.Sprintf("search error: %v", err), http.StatusInternalServerError)
		return
	}
	for i := range results {
		results[i].Payload["source_namespace"] = namespace
	}

	// Graph expansion — collect nodes and edges alongside expanded results.
	graphNodes, graphEdges := s.collectGraphStructure(r.Context(), results, namespace, depth)

	if len(results) == 0 {
		graphSearchEmptyResults.WithLabelValues(namespace).Inc()
	}

	// Merge neighbour results using graph expansion (reuses existing RRF logic).
	initialCount := len(results)
	if len(results) > 0 {
		results = s.graphExpandResultsWithDepth(r.Context(), results, query, vector, namespace, 10, depth)
	}

	// namespace and depth are already captured in the OTel span attributes above.
	log.Printf("[graph-search] initial=%d expanded=%d nodes=%d edges=%d",
		initialCount, len(results), len(graphNodes), len(graphEdges))

	resp := map[string]interface{}{
		"query":         query,
		"results":       results,
		"graph_nodes":   graphNodes,
		"graph_edges":   graphEdges,
		"query_time_ms": time.Since(start).Milliseconds(),
	}
	s.writeJSON(w, http.StatusOK, resp)

	auditEntry := audit.Entry{
		Action:    "graph_search",
		Query:     query,
		Namespace: namespace,
		Results:   len(results),
		LatencyMS: time.Since(start).Milliseconds(),
		Status:    http.StatusOK,
		Metadata:  map[string]interface{}{"depth": depth, "graph_nodes": len(graphNodes)},
	}
	if claims, ok := auth.GetUserClaims(r); ok {
		auditEntry.User = claims.Subject
	}
	audit.Log(auditEntry)
}

// collectGraphStructure walks the knowledge graph from each source file in
// results and returns the deduplicated set of visited nodes and edges.
func (s *Server) collectGraphStructure(ctx context.Context, results []search.Result, namespace string, depth int) ([]string, []GraphEdge) {
	sourceFiles := uniquePaths(results)
	nodeSet := make(map[string]bool)
	for _, f := range sourceFiles {
		nodeSet[f] = true
	}

	var edges []GraphEdge
	seenEdges := make(map[string]bool)

	for _, src := range sourceFiles {
		neighbors := s.knowledgeGraph.Neighbors(ctx, s.pointsClient, s.collection, namespace, src, depth)
		for _, nb := range neighbors {
			nodeSet[nb] = true
			key := src + "\x00" + nb
			if !seenEdges[key] {
				seenEdges[key] = true
				edges = append(edges, GraphEdge{Source: src, Target: nb})
			}
		}
	}

	nodes := make([]string, 0, len(nodeSet))
	for n := range nodeSet {
		nodes = append(nodes, n)
	}
	return nodes, edges
}

// graphExpandResultsWithDepth is like graphExpandResults but accepts an
// explicit depth parameter, overriding the server-wide graphCfg.Depth.
func (s *Server) graphExpandResultsWithDepth(ctx context.Context, results []search.Result, query string, vector []float32, namespace string, limit, depth int) []search.Result {
	if len(results) == 0 {
		return results
	}

	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.graph.expand")
	defer span.End()

	sourceFiles := uniquePaths(results)
	neighborSet := make(map[string]bool)
	for _, file := range sourceFiles {
		for _, nb := range s.knowledgeGraph.Neighbors(ctx, s.pointsClient, s.collection, namespace, file, depth) {
			neighborSet[nb] = true
		}
	}
	for _, f := range sourceFiles {
		delete(neighborSet, f)
	}
	if len(neighborSet) == 0 {
		return results
	}

	neighbors := make([]string, 0, len(neighborSet))
	for f := range neighborSet {
		neighbors = append(neighbors, f)
	}

	neighborResults, err := search.HybridSearchByPaths(ctx, s.pointsClient, s.collection, query, vector, uint64(limit), namespace, neighbors)
	if err != nil {
		log.Printf("[graph-search] neighbor search failed namespace=%q: %v", namespace, err)
		return results
	}
	if len(neighborResults) == 0 {
		return results
	}

	return search.MergeRRFWeighted(results, neighborResults, 0.7, limit)
}
