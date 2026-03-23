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

var vectorSearchDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "emdexer_gateway_search_vector_duration_ms",
	Help:    "Latency of Qdrant vector search in milliseconds",
	Buckets: []float64{10, 50, 100, 200, 500, 1000, 2000, 5000},
}, []string{"collection", "namespace"})

var bm25SearchDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "emdexer_gateway_search_bm25_duration_ms",
	Help:    "Latency of BM25 keyword scroll search in milliseconds",
	Buckets: []float64{10, 50, 100, 200, 500, 1000, 2000, 5000},
}, []string{"collection", "namespace"})

var hybridTotalDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "emdexer_gateway_search_hybrid_total_ms",
	Help:    "End-to-end latency of HybridSearch (both legs + RRF merge) in milliseconds",
	Buckets: []float64{10, 50, 100, 200, 500, 1000, 2000, 5000},
}, []string{"collection", "namespace"})

// RRF hit-distribution counters: how many of the returned top-N results came
// from the vector leg only, the BM25 leg only, or both legs (overlap).
var rrfVectorHits = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_rrf_top_vector_hits_total",
	Help: "Number of returned results sourced exclusively from the vector leg",
}, []string{"collection", "namespace"})

var rrfBM25Hits = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_rrf_top_bm25_hits_total",
	Help: "Number of returned results sourced exclusively from the BM25 leg",
}, []string{"collection", "namespace"})

var rrfBothLegsHits = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_rrf_top_both_legs_hits_total",
	Help: "Number of returned results that appeared in both vector and BM25 legs (overlap)",
}, []string{"collection", "namespace"})

var bm25Fallbacks = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_bm25_fallback_total",
	Help: "Number of times HybridSearch fell back to vector-only due to a BM25 failure",
}, []string{"collection", "namespace"})

var bm25ZeroResults = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_bm25_zero_results_total",
	Help: "Number of times BM25 search returned 0 results (index may be empty or query too specific)",
}, []string{"collection", "namespace"})

type Result struct {
	ID      uint64                 `json:"id"`
	Score   float32                `json:"score"`
	Payload map[string]interface{} `json:"payload"`
}

func SearchQdrant(ctx context.Context, pc qdrant.PointsClient, collection string, vector []float32, limit uint64, namespace string) ([]Result, error) {
	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.search.vector")
	span.SetAttributes(attribute.String("search.collection", collection), attribute.String("search.namespace", namespace))
	defer span.End()

	start := time.Now()
	defer func() {
		vectorSearchDuration.WithLabelValues(collection, namespace).Observe(float64(time.Since(start).Milliseconds()))
	}()
	var filter *qdrant.Filter
	if namespace != "" {
		filter = &qdrant.Filter{
			Must: []*qdrant.Condition{
				{
					ConditionOneOf: &qdrant.Condition_Field{
						Field: &qdrant.FieldCondition{
							Key: "namespace",
							Match: &qdrant.Match{
								MatchValue: &qdrant.Match_Keyword{
									Keyword: namespace,
								},
							},
						},
					},
				},
			},
		}
	}

	resp, err := pc.Search(ctx, &qdrant.SearchPoints{
		CollectionName: collection,
		Vector:         vector,
		Limit:          limit,
		Filter:         filter,
		WithPayload: &qdrant.WithPayloadSelector{
			SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: true},
		},
	})
	if err != nil {
		return nil, err
	}

	var results []Result
	for _, pt := range resp.GetResult() {
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
		if numID, ok := pt.Id.PointIdOptions.(*qdrant.PointId_Num); ok {
			id = numID.Num
		}
		results = append(results, Result{
			ID:      id,
			Score:   pt.Score,
			Payload: payload,
		})
	}
	return results, nil
}

// BM25SearchQdrant performs a keyword-based scroll search using Qdrant's full-text payload index.
// Both namespace and text filters are applied so results never cross tenant boundaries.
// Scroll order is arbitrary; callers use rank position (not score) for RRF fusion.
func BM25SearchQdrant(ctx context.Context, pc qdrant.PointsClient, collection string, query string, limit uint64, namespace string) ([]Result, error) {
	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.search.bm25")
	span.SetAttributes(attribute.String("search.collection", collection), attribute.String("search.namespace", namespace))
	defer span.End()

	start := time.Now()
	defer func() {
		bm25SearchDuration.WithLabelValues(collection, namespace).Observe(float64(time.Since(start).Milliseconds()))
	}()

	// Namespace keyword filter is placed first so Qdrant can prune candidates
	// with the cheaper keyword index before evaluating the full-text scan.
	var must []*qdrant.Condition
	if namespace != "" {
		must = []*qdrant.Condition{
			{
				ConditionOneOf: &qdrant.Condition_Field{
					Field: &qdrant.FieldCondition{
						Key: "namespace",
						Match: &qdrant.Match{
							MatchValue: &qdrant.Match_Keyword{
								Keyword: namespace,
							},
						},
					},
				},
			},
		}
	}
	must = append(must, &qdrant.Condition{
		ConditionOneOf: &qdrant.Condition_Field{
			Field: &qdrant.FieldCondition{
				Key: "text",
				Match: &qdrant.Match{
					MatchValue: &qdrant.Match_Text{
						Text: query,
					},
				},
			},
		},
	})

	lim := uint32(limit)
	resp, err := pc.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: collection,
		Filter:         &qdrant.Filter{Must: must},
		Limit:          &lim,
		WithPayload: &qdrant.WithPayloadSelector{
			SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: true},
		},
	})
	if err != nil {
		return nil, err
	}

	if len(resp.GetResult()) == 0 {
		log.Printf("[search] BM25 returned 0 results for collection %q namespace %q query %q", collection, namespace, query)
		bm25ZeroResults.WithLabelValues(collection, namespace).Inc()
	}

	var results []Result
	for _, pt := range resp.GetResult() {
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
		if numID, ok := pt.Id.PointIdOptions.(*qdrant.PointId_Num); ok {
			id = numID.Num
		}
		results = append(results, Result{ID: id, Score: 0, Payload: payload})
	}
	return results, nil
}

// HybridSearch runs vector and BM25 searches concurrently and merges the results
// via Reciprocal Rank Fusion (k=60). Both legs enforce the namespace filter to
// prevent cross-tenant data leaks.
//
// If BM25 fails (e.g., the full-text index has not been created yet) the function
// falls back to the vector-only results so search continues to work without
// operator intervention.
func HybridSearch(ctx context.Context, pc qdrant.PointsClient, collection string, query string, vector []float32, limit uint64, namespace string) ([]Result, error) {
	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.search.hybrid")
	span.SetAttributes(attribute.String("search.collection", collection), attribute.String("search.namespace", namespace))
	defer span.End()

	type leg struct {
		results []Result
		err     error
	}

	start := time.Now()
	vectorCh := make(chan leg, 1)
	bm25Ch := make(chan leg, 1)

	go func() {
		r, err := SearchQdrant(ctx, pc, collection, vector, limit*2, namespace)
		vectorCh <- leg{r, err}
	}()
	go func() {
		r, err := BM25SearchQdrant(ctx, pc, collection, query, limit*2, namespace)
		bm25Ch <- leg{r, err}
	}()

	vRes := <-vectorCh
	bRes := <-bm25Ch

	if vRes.err != nil {
		return nil, vRes.err
	}

	if bRes.err != nil {
		log.Printf("[search] BM25 failed for collection %q — falling back to vector-only: %v", collection, bRes.err)
		bm25Fallbacks.WithLabelValues(collection, namespace).Inc()
		hybridTotalDuration.WithLabelValues(collection, namespace).Observe(float64(time.Since(start).Milliseconds()))
		return vRes.results, nil
	}

	merged := MergeRRFHybrid(vRes.results, bRes.results, int(limit))
	hybridTotalDuration.WithLabelValues(collection, namespace).Observe(float64(time.Since(start).Milliseconds()))

	// Track which top-N results came from each leg to monitor RRF balance.
	vectorKeys := resultKeySet(vRes.results)
	bm25Keys := resultKeySet(bRes.results)
	var vectorHits, bm25Hits, bothHits int
	for _, r := range merged {
		path, _ := r.Payload["path"].(string)
		chunk := fmt.Sprintf("%v", r.Payload["chunk"])
		key := path + ":" + chunk
		inV, inB := vectorKeys[key], bm25Keys[key]
		switch {
		case inV && inB:
			bothHits++
		case inV:
			vectorHits++
		default:
			bm25Hits++
		}
	}
	rrfVectorHits.WithLabelValues(collection, namespace).Add(float64(vectorHits))
	rrfBM25Hits.WithLabelValues(collection, namespace).Add(float64(bm25Hits))
	rrfBothLegsHits.WithLabelValues(collection, namespace).Add(float64(bothHits))

	return merged, nil
}

// HybridSearchByPaths is like HybridSearch but restricts results to the given file paths.
// It is used by graph-augmented retrieval to search only within files that are structurally
// adjacent to the initial results (neighbours in the knowledge graph).
// If paths is empty the function returns nil immediately.
func HybridSearchByPaths(ctx context.Context, pc qdrant.PointsClient, collection string, query string, vector []float32, limit uint64, namespace string, paths []string) ([]Result, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	// Build a filter that matches namespace AND (path == paths[0] OR path == paths[1] …).
	var must []*qdrant.Condition
	if namespace != "" {
		must = append(must, &qdrant.Condition{
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
	// Use Match_Keywords for an efficient "path in {paths}" filter.
	must = append(must, &qdrant.Condition{
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
	})
	filter := &qdrant.Filter{Must: must}

	type leg struct {
		results []Result
		err     error
	}

	vectorCh := make(chan leg, 1)
	bm25Ch := make(chan leg, 1)

	go func() {
		resp, err := pc.Search(ctx, &qdrant.SearchPoints{
			CollectionName: collection,
			Vector:         vector,
			Limit:          limit * 2,
			Filter:         filter,
			WithPayload:    &qdrant.WithPayloadSelector{SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: true}},
		})
		if err != nil {
			vectorCh <- leg{nil, err}
			return
		}
		var res []Result
		for _, pt := range resp.GetResult() {
			res = append(res, pointToResult(pt))
		}
		vectorCh <- leg{res, nil}
	}()

	go func() {
		lim := uint32(limit * 2)
		bm25Must := append(must, &qdrant.Condition{
			ConditionOneOf: &qdrant.Condition_Field{
				Field: &qdrant.FieldCondition{
					Key: "text",
					Match: &qdrant.Match{
						MatchValue: &qdrant.Match_Text{Text: query},
					},
				},
			},
		})
		resp, err := pc.Scroll(ctx, &qdrant.ScrollPoints{
			CollectionName: collection,
			Filter:         &qdrant.Filter{Must: bm25Must},
			Limit:          &lim,
			WithPayload:    &qdrant.WithPayloadSelector{SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: true}},
		})
		if err != nil {
			bm25Ch <- leg{nil, err}
			return
		}
		var res []Result
		for _, pt := range resp.GetResult() {
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
			if numID, ok := pt.Id.PointIdOptions.(*qdrant.PointId_Num); ok {
				id = numID.Num
			}
			res = append(res, Result{ID: id, Score: 0, Payload: payload})
		}
		bm25Ch <- leg{res, nil}
	}()

	vRes := <-vectorCh
	bRes := <-bm25Ch

	if vRes.err != nil {
		return nil, vRes.err
	}
	if bRes.err != nil || len(bRes.results) == 0 {
		return vRes.results, nil
	}
	return MergeRRFHybrid(vRes.results, bRes.results, int(limit)), nil
}

// pointToResult converts a Qdrant ScoredPoint to a Result.
func pointToResult(pt *qdrant.ScoredPoint) Result {
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
	if numID, ok := pt.Id.PointIdOptions.(*qdrant.PointId_Num); ok {
		id = numID.Num
	}
	return Result{ID: id, Score: pt.Score, Payload: payload}
}

// resultKeySet builds a set of path:chunk keys from a result slice for O(1) leg-membership checks.
func resultKeySet(results []Result) map[string]bool {
	keys := make(map[string]bool, len(results))
	for _, r := range results {
		path, _ := r.Payload["path"].(string)
		chunk := fmt.Sprintf("%v", r.Payload["chunk"])
		keys[path+":"+chunk] = true
	}
	return keys
}
