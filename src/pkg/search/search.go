package search

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/qdrant/go-client/qdrant"
)

var searchLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "emdexer_gateway_search_latency_ms",
	Help:    "Latency of Qdrant search in milliseconds",
	Buckets: []float64{10, 50, 100, 200, 500, 1000, 2000, 5000},
}, []string{"collection"})

var bm25Latency = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "emdexer_gateway_bm25_latency_ms",
	Help:    "Latency of BM25 keyword scroll search in milliseconds",
	Buckets: []float64{10, 50, 100, 200, 500, 1000, 2000, 5000},
}, []string{"collection"})

type Result struct {
	ID      uint64                 `json:"id"`
	Score   float32                `json:"score"`
	Payload map[string]interface{} `json:"payload"`
}

func SearchQdrant(ctx context.Context, pc qdrant.PointsClient, collection string, vector []float32, limit uint64, namespace string) ([]Result, error) {
	start := time.Now()
	defer func() {
		searchLatency.WithLabelValues(collection).Observe(float64(time.Since(start).Milliseconds()))
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
	start := time.Now()
	defer func() {
		bm25Latency.WithLabelValues(collection).Observe(float64(time.Since(start).Milliseconds()))
	}()

	must := []*qdrant.Condition{
		{
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
		},
	}
	if namespace != "" {
		must = append(must, &qdrant.Condition{
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
		})
	}

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
	type leg struct {
		results []Result
		err     error
	}

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
		return vRes.results, nil
	}

	return MergeRRFHybrid(vRes.results, bRes.results, int(limit)), nil
}
