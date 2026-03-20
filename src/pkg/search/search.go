package search

import (
	"context"
	"fmt"
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
