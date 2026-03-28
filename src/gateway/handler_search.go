package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/piotrlaczykowski/emdexer/audit"
	"github.com/piotrlaczykowski/emdexer/auth"
	"github.com/piotrlaczykowski/emdexer/rerank"
	"github.com/piotrlaczykowski/emdexer/search"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// uniquePaths returns deduplicated file paths from the payload of a result set.
func uniquePaths(results []search.Result) []string {
	seen := make(map[string]bool, len(results))
	var paths []string
	for _, r := range results {
		if p, ok := r.Payload["path"].(string); ok && p != "" && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	return paths
}

// graphExpandResults augments results by finding structurally adjacent files in the
// knowledge graph and issuing a follow-up search restricted to those files.
// Neighbour results are merged using RRF with a 0.7 weight so direct matches
// always rank higher than graph-expanded ones.
// On any error the original results are returned unchanged.
func (s *Server) graphExpandResults(ctx context.Context, results []search.Result, query string, vector []float32, namespace string, limit int) []search.Result {
	if !s.graphCfg.Enabled || len(results) == 0 {
		return results
	}

	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.graph.expand")
	defer span.End()

	sourceFiles := uniquePaths(results)
	neighborSet := make(map[string]bool)
	for _, file := range sourceFiles {
		for _, nb := range s.knowledgeGraph.Neighbors(ctx, s.pointsClient, s.collection, namespace, file, s.graphCfg.Depth) {
			neighborSet[nb] = true
		}
	}
	// Remove files already in initial results to avoid re-fetching them.
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
		log.Printf("[graph] neighbor search failed namespace=%q: %v", namespace, err)
		return results
	}
	if len(neighborResults) == 0 {
		return results
	}

	// Merge: primary (weight=1.0) and neighbour (weight=0.7).
	return search.MergeRRFWeighted(results, neighborResults, 0.7, limit)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	// Extract W3C Trace Context from incoming headers and create root span.
	ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.search")
	defer span.End()
	r = r.WithContext(ctx)

	start := time.Now()
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	allowedNamespaces, ok := auth.GetAllowedNamespaces(r)
	if !ok {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	requestedNamespace := logSafe(strings.TrimSpace(r.URL.Query().Get("namespace")))
	if requestedNamespace == "" {
		requestedNamespace = "default"
	}

	// For global search (namespace=* or __global__), resolve to authorized namespaces.
	// For single namespace, validate against allowedNamespaces as before.
	isGlobal := requestedNamespace == "*" || requestedNamespace == "__global__"
	if !isGlobal {
		isAllowed := false
		for _, ns := range allowedNamespaces {
			if ns == "*" || ns == requestedNamespace {
				isAllowed = true
				break
			}
		}
		if !isAllowed {
			http.Error(w, "Forbidden: Namespace not authorized", http.StatusForbidden)
			return
		}
	}

	query := logSafe(r.URL.Query().Get("q"))
	if query == "" {
		http.Error(w, "missing ?q=", http.StatusBadRequest)
		return
	}

	vector, err := s.embedder.Embed(r.Context(), query)
	if err != nil {
		http.Error(w, fmt.Sprintf("embedding error: %v", err), http.StatusInternalServerError)
		return
	}

	namespaces := search.ResolveNamespaces(requestedNamespace, allowedNamespaces, s.knownNamespaces())

	var results []search.Result
	var fanoutFailedNS []string
	if len(namespaces) <= 1 {
		// Single namespace — fast path (hybrid or vector-only).
		ns := ""
		if len(namespaces) == 1 {
			ns = namespaces[0]
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if s.bm25Enabled {
			results, err = search.HybridSearch(ctx, s.pointsClient, s.collection, query, vector, 10, ns)
		} else {
			results, err = search.SearchQdrant(ctx, s.pointsClient, s.collection, vector, 10, ns)
		}
		if err != nil {
			http.Error(w, fmt.Sprintf("search error: %v", err), http.StatusInternalServerError)
			return
		}
		// Inject source_namespace for consistent buildContext behavior.
		for i := range results {
			results[i].Payload["source_namespace"] = ns
		}
	} else {
		// Multi-namespace fan-out with RRF merge.
		// Partial failures are surfaced in the response so clients can detect degraded results;
		// a complete failure returns 200 with empty results rather than a 504.
		if s.bm25Enabled {
			results, fanoutFailedNS, err = search.FanOutHybridSearch(r.Context(), s.pointsClient, s.collection, query, vector, namespaces, 10, s.globalSearchTimeout)
		} else {
			results, fanoutFailedNS, err = search.FanOutSearch(r.Context(), s.pointsClient, s.collection, vector, namespaces, 10, s.globalSearchTimeout)
		}
		if err != nil {
			http.Error(w, fmt.Sprintf("search error: %v", err), http.StatusInternalServerError)
			return
		}
		if len(fanoutFailedNS) > 0 {
			log.Printf("[search] fan-out partial failure: %d/%d namespaces errored: %v",
				len(fanoutFailedNS), len(namespaces), fanoutFailedNS)
		}
	}

	if len(results) == 0 {
		mode := "vector"
		if s.bm25Enabled {
			mode = "hybrid"
		}
		searchEmptyResults.WithLabelValues(requestedNamespace, mode).Inc()
	}

	// ── Phase 30: Late-interaction reranking ──────────────────────────────────
	// Apply only when results are available and a real Reranker is wired in.
	if _, isNoop := s.reranker.(rerank.NoOpReranker); !isNoop && len(results) > 0 {
		texts := make([]string, len(results))
		for i, r := range results {
			if t, ok := r.Payload["text"].(string); ok {
				texts[i] = t
			}
		}
		ranked, rerr := rerank.Rank(r.Context(), s.reranker, query, texts, s.rerankTopK, requestedNamespace)
		if rerr != nil {
			log.Printf("[rerank] error for namespace %q — skipping rerank: %v", requestedNamespace, rerr)
		} else {
			// Log rank delta for research audit.
			changed := 0
			for newPos, sc := range ranked {
				if sc.Index != newPos {
					changed++
				}
				score := sc.Score
				results[sc.Index].RerankScore = &score
			}
			log.Printf("[rerank] namespace=%q candidates=%d changed_rank=%d", requestedNamespace, len(ranked), changed)

			// Rebuild results in reranked order, applying threshold filter.
			reranked := make([]search.Result, 0, len(ranked))
			for _, sc := range ranked {
				if float64(sc.Score) >= s.rerankThreshold {
					reranked = append(reranked, results[sc.Index])
				}
			}
			if len(reranked) > 0 {
				results = reranked
			}
		}
	}
	// ─────────────────────────────────────────────────────────────────────────

	resp := map[string]interface{}{
		"query":   query,
		"results": results,
	}
	if isGlobal {
		resp["namespaces_searched"] = namespaces
		if len(fanoutFailedNS) > 0 {
			resp["partial_failures"] = fanoutFailedNS
		}
	}
	s.writeJSON(w, http.StatusOK, resp)

	auditEntry := audit.Entry{
		Action:    "search",
		Query:     query,
		Namespace: requestedNamespace,
		Results:   len(results),
		LatencyMS: time.Since(start).Milliseconds(),
		Status:    http.StatusOK,
	}
	if claims, ok := auth.GetUserClaims(r); ok {
		auditEntry.User = claims.Subject
	}
	if isGlobal {
		auditEntry.Metadata = map[string]interface{}{"namespaces_searched": namespaces}
	}
	audit.Log(auditEntry)
}
