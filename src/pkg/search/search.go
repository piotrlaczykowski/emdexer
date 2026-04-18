package search

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/qdrant/go-client/qdrant"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

// vectorSearchDuration tracks latency of the vector-only SearchQdrant path.
var vectorSearchDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "emdexer_gateway_search_vector_duration_ms",
	Help:    "Latency of Qdrant vector search in milliseconds (vector-only path)",
	Buckets: []float64{10, 50, 100, 200, 500, 1000, 2000, 5000},
}, []string{"collection", "namespace"})

// hybridTotalDuration tracks the end-to-end latency of HybridSearch (server-side
// RRF via Qdrant Universal Query API) in milliseconds.
var hybridTotalDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "emdexer_gateway_search_hybrid_total_ms",
	Help:    "End-to-end latency of HybridSearch (server-side RRF via Qdrant Universal Query API) in milliseconds",
	Buckets: []float64{10, 50, 100, 200, 500, 1000, 2000, 5000},
}, []string{"collection", "namespace"})

// keywordTotalDuration tracks the end-to-end latency of KeywordSearch
// (single text-filter prefetch via Qdrant Universal Query API) in milliseconds.
var keywordTotalDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "emdexer_gateway_search_keyword_total_ms",
	Help:    "End-to-end latency of KeywordSearch (text-filter via Qdrant Universal Query API) in milliseconds",
	Buckets: []float64{10, 50, 100, 200, 500, 1000, 2000, 5000},
}, []string{"collection", "namespace"})

// unifiedQueryDuration tracks the latency of the single pc.Query call (server-side RRF).
var unifiedQueryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "emdexer_gateway_search_unified_query_duration_ms",
	Help:    "Latency of the Qdrant Universal Query API call (vector+text prefetches, server-side RRF) in milliseconds",
	Buckets: []float64{10, 50, 100, 200, 500, 1000, 2000, 5000},
}, []string{"collection", "namespace"})

// bm25Fallbacks counts how many times HybridSearch fell back to vector-only because
// the Qdrant Universal Query API failed (e.g., Qdrant version < 1.10.0).
var bm25Fallbacks = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_bm25_fallback_total",
	Help: "Number of times HybridSearch fell back to vector-only because the Qdrant Universal Query API failed",
}, []string{"collection", "namespace"})

// bm25ZeroResults counts how many times unified hybrid search returned 0 results,
// which may indicate a missing full-text index or an overly specific query.
var bm25ZeroResults = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_bm25_zero_results_total",
	Help: "Number of times unified hybrid search returned 0 results (possible missing text index or narrow query)",
}, []string{"collection", "namespace"})

// Result represents a single search hit returned to callers.
type Result struct {
	ID          uint64                 `json:"id"`
	Score       float32                `json:"score"`                   // RRF or vector score from Qdrant
	RerankScore *float32               `json:"rerank_score,omitempty"`  // set by Phase 30 reranking; nil when disabled
	Payload     map[string]interface{} `json:"payload"`
}

// SearchQdrant performs a vector similarity search filtered to the given namespace.
// Used for the vector-only path (EMDEX_BM25_ENABLED=false) and as the fallback
// when the Universal Query API is unavailable.
func SearchQdrant(ctx context.Context, pc qdrant.PointsClient, collection string, vector []float32, limit uint64, namespace string) ([]Result, error) {
	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.search.vector")
	span.SetAttributes(attribute.String("search.collection", collection), attribute.String("search.namespace", namespace))
	defer span.End()

	start := time.Now()
	defer func() {
		vectorSearchDuration.WithLabelValues(collection, namespace).Observe(float64(time.Since(start).Milliseconds()))
	}()

	resp, err := pc.Search(ctx, &qdrant.SearchPoints{
		CollectionName: collection,
		Vector:         vector,
		Limit:          limit,
		Filter:         buildNamespaceFilter(namespace),
		WithPayload: &qdrant.WithPayloadSelector{
			SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: true},
		},
	})
	if err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(resp.GetResult()))
	for _, pt := range resp.GetResult() {
		results = append(results, pointToResult(pt))
	}
	return results, nil
}

// HybridSearch issues a single Qdrant Universal Query API call with two server-side
// prefetches (dense vector + text-match filter) fused via Reciprocal Rank Fusion (RRF).
//
// Both prefetch legs enforce the namespace filter to prevent cross-tenant data leaks.
//
// Fallback: if the Query API fails (e.g., Qdrant < 1.10.0) the function transparently
// falls back to vector-only search via SearchQdrant so search continues without
// operator intervention.
func HybridSearch(ctx context.Context, pc qdrant.PointsClient, collection string, query string, vector []float32, limit uint64, namespace string) ([]Result, error) {
	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.search.hybrid")
	span.SetAttributes(
		attribute.String("search.collection", collection),
		attribute.String("search.namespace", namespace),
		attribute.String("search.mode", "server_rrf"),
	)
	defer span.End()

	start := time.Now()
	defer func() {
		hybridTotalDuration.WithLabelValues(collection, namespace).Observe(float64(time.Since(start).Milliseconds()))
	}()

	prefetchLim := limit * 2
	resp, err := pc.Query(ctx, &qdrant.QueryPoints{
		CollectionName: collection,
		Prefetch: []*qdrant.PrefetchQuery{
			{
				// Dense vector similarity leg.
				Query:  qdrant.NewQueryDense(vector),
				Filter: buildNamespaceFilter(namespace),
				Limit:  &prefetchLim,
			},
			{
				// Text-match (BM25) leg: filter-only, unranked candidates fed into RRF.
				Filter: buildTextFilter(namespace, query),
				Limit:  &prefetchLim,
			},
		},
		Query: qdrant.NewQueryFusion(qdrant.Fusion_RRF),
		Limit: &limit,
		WithPayload: &qdrant.WithPayloadSelector{
			SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: true},
		},
	})
	if err != nil {
		log.Printf("[search] unified query failed for collection %q namespace %q — falling back to vector-only: %v", collection, namespace, err)
		bm25Fallbacks.WithLabelValues(collection, namespace).Inc()
		return SearchQdrant(ctx, pc, collection, vector, limit, namespace)
	}
	unifiedQueryDuration.WithLabelValues(collection, namespace).Observe(float64(time.Since(start).Milliseconds()))

	points := resp.GetResult()
	if len(points) == 0 {
		log.Printf("[search] unified query returned 0 results for collection %q namespace %q query %q", collection, namespace, query)
		bm25ZeroResults.WithLabelValues(collection, namespace).Inc()
	}

	results := make([]Result, 0, len(points))
	for _, pt := range points {
		results = append(results, pointToResult(pt))
	}
	return results, nil
}

// KeywordSearch issues a single Qdrant Universal Query API call with one server-side
// prefetch (text-match filter) wrapped in RRF fusion. Use this when the caller wants
// keyword/identifier matching without vector similarity contribution.
//
// On Query API error it falls back to vector-only search via SearchQdrant for symmetry
// with HybridSearch — callers always get results when Qdrant is reachable.
func KeywordSearch(ctx context.Context, pc qdrant.PointsClient, collection string, query string, vector []float32, limit uint64, namespace string) ([]Result, error) {
	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.search.keyword")
	span.SetAttributes(
		attribute.String("search.collection", collection),
		attribute.String("search.namespace", namespace),
		attribute.String("search.mode", "keyword"),
	)
	defer span.End()

	start := time.Now()
	defer func() {
		keywordTotalDuration.WithLabelValues(collection, namespace).Observe(float64(time.Since(start).Milliseconds()))
	}()

	prefetchLim := limit * 2
	resp, err := pc.Query(ctx, &qdrant.QueryPoints{
		CollectionName: collection,
		Prefetch: []*qdrant.PrefetchQuery{
			{
				// Text-match (BM25) leg only — no vector prefetch.
				Filter: buildTextFilter(namespace, query),
				Limit:  &prefetchLim,
			},
		},
		Query: qdrant.NewQueryFusion(qdrant.Fusion_RRF),
		Limit: &limit,
		WithPayload: &qdrant.WithPayloadSelector{
			SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: true},
		},
	})
	if err != nil {
		log.Printf("[search] keyword query failed for collection %q namespace %q — falling back to vector-only: %v", collection, namespace, err)
		bm25Fallbacks.WithLabelValues(collection, namespace).Inc()
		return SearchQdrant(ctx, pc, collection, vector, limit, namespace)
	}

	points := resp.GetResult()
	if len(points) == 0 {
		log.Printf("[search] keyword query returned 0 results for collection %q namespace %q query %q", collection, namespace, query)
		bm25ZeroResults.WithLabelValues(collection, namespace).Inc()
	}

	results := make([]Result, 0, len(points))
	for _, pt := range points {
		results = append(results, pointToResult(pt))
	}
	return results, nil
}

// HybridSearchByPaths is like HybridSearch but restricts both prefetch legs to the
// given file paths. Used by graph-augmented retrieval to search only within files
// that are structurally adjacent to the initial results (neighbours in the knowledge graph).
// Returns nil immediately when paths is empty.
func HybridSearchByPaths(ctx context.Context, pc qdrant.PointsClient, collection string, query string, vector []float32, limit uint64, namespace string, paths []string) ([]Result, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	pathFilter := buildPathsFilter(namespace, paths)
	prefetchLim := limit * 2

	resp, err := pc.Query(ctx, &qdrant.QueryPoints{
		CollectionName: collection,
		Prefetch: []*qdrant.PrefetchQuery{
			{
				Query:  qdrant.NewQueryDense(vector),
				Filter: pathFilter,
				Limit:  &prefetchLim,
			},
			{
				Filter: buildTextFilterWithPaths(namespace, query, paths),
				Limit:  &prefetchLim,
			},
		},
		Query: qdrant.NewQueryFusion(qdrant.Fusion_RRF),
		Limit: &limit,
		WithPayload: &qdrant.WithPayloadSelector{
			SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: true},
		},
	})
	if err != nil {
		log.Printf("primary search failed, falling back to HybridSearchByPaths vector-only: %v", err)
		// Fall back to vector-only constrained by the path filter.
		vecResp, vecErr := pc.Search(ctx, &qdrant.SearchPoints{
			CollectionName: collection,
			Vector:         vector,
			Limit:          limit,
			Filter:         pathFilter,
			WithPayload:    &qdrant.WithPayloadSelector{SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: true}},
		})
		if vecErr != nil {
			return nil, vecErr
		}
		results := make([]Result, 0, len(vecResp.GetResult()))
		for _, pt := range vecResp.GetResult() {
			results = append(results, pointToResult(pt))
		}
		return results, nil
	}

	results := make([]Result, 0, len(resp.GetResult()))
	for _, pt := range resp.GetResult() {
		results = append(results, pointToResult(pt))
	}
	return results, nil
}

// ── Filter helpers ────────────────────────────────────────────────────────────

// buildNamespaceFilter returns a Must filter on namespace=<ns>, or nil for empty ns.
func buildNamespaceFilter(namespace string) *qdrant.Filter {
	if namespace == "" {
		return nil
	}
	return &qdrant.Filter{Must: []*qdrant.Condition{nsCondition(namespace)}}
}

// buildTextFilter returns a Must filter on [namespace?, text=<query>].
func buildTextFilter(namespace, query string) *qdrant.Filter {
	must := make([]*qdrant.Condition, 0, 2)
	if namespace != "" {
		must = append(must, nsCondition(namespace))
	}
	must = append(must, textCondition(query))
	return &qdrant.Filter{Must: must}
}

// buildPathsFilter returns a Must filter on [namespace?, path in paths].
func buildPathsFilter(namespace string, paths []string) *qdrant.Filter {
	must := make([]*qdrant.Condition, 0, 2)
	if namespace != "" {
		must = append(must, nsCondition(namespace))
	}
	must = append(must, pathsCondition(paths))
	return &qdrant.Filter{Must: must}
}

// buildTextFilterWithPaths returns a Must filter on [namespace?, path in paths, text=<query>].
func buildTextFilterWithPaths(namespace, query string, paths []string) *qdrant.Filter {
	must := make([]*qdrant.Condition, 0, 3)
	if namespace != "" {
		must = append(must, nsCondition(namespace))
	}
	must = append(must, pathsCondition(paths))
	must = append(must, textCondition(query))
	return &qdrant.Filter{Must: must}
}

// nsCondition returns a keyword match condition on the "namespace" payload field.
func nsCondition(namespace string) *qdrant.Condition {
	return &qdrant.Condition{
		ConditionOneOf: &qdrant.Condition_Field{
			Field: &qdrant.FieldCondition{
				Key: "namespace",
				Match: &qdrant.Match{
					MatchValue: &qdrant.Match_Keyword{Keyword: namespace},
				},
			},
		},
	}
}

// textCondition returns a full-text match condition on the "text" payload field.
func textCondition(query string) *qdrant.Condition {
	return &qdrant.Condition{
		ConditionOneOf: &qdrant.Condition_Field{
			Field: &qdrant.FieldCondition{
				Key: "text",
				Match: &qdrant.Match{
					MatchValue: &qdrant.Match_Text{Text: query},
				},
			},
		},
	}
}

// pathsCondition returns a keywords-in condition on the "path" payload field.
func pathsCondition(paths []string) *qdrant.Condition {
	return &qdrant.Condition{
		ConditionOneOf: &qdrant.Condition_Field{
			Field: &qdrant.FieldCondition{
				Key: "path",
				Match: &qdrant.Match{
					MatchValue: &qdrant.Match_Keywords{
						Keywords: &qdrant.RepeatedStrings{Strings: paths},
					},
				},
			},
		},
	}
}

// pointToResult converts a Qdrant ScoredPoint to a Result.
func pointToResult(pt *qdrant.ScoredPoint) Result {
	if pt == nil {
		return Result{}
	}
	payload := make(map[string]interface{})
	for k, v := range pt.Payload {
		switch val := v.Kind.(type) {
		case *qdrant.Value_StringValue:
			payload[k] = val.StringValue
		case *qdrant.Value_IntegerValue:
			payload[k] = val.IntegerValue
		case *qdrant.Value_DoubleValue:
			payload[k] = val.DoubleValue
		case *qdrant.Value_BoolValue:
			payload[k] = val.BoolValue
		default:
			payload[k] = fmt.Sprintf("%v", v)
		}
	}
	var id uint64
	if pt.Id != nil {
		if numID, ok := pt.Id.PointIdOptions.(*qdrant.PointId_Num); ok {
			id = numID.Num
		}
	}
	return Result{ID: id, Score: pt.Score, Payload: payload}
}
