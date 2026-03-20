package search

import (
	"fmt"
	"sort"
)

// MergeRRF merges results from multiple namespace searches using Reciprocal Rank Fusion.
// k=60 is the standard RRF constant.
func MergeRRF(perNS map[string][]Result, limit int) []Result {
	type scored struct {
		result Result
		score  float64
	}
	scoreMap := make(map[string]*scored)

	for ns, nsResults := range perNS {
		for rank, r := range nsResults {
			rrfScore := 1.0 / float64(60+rank+1)
			path, _ := r.Payload["path"].(string)
			chunk := fmt.Sprintf("%v", r.Payload["chunk"])
			key := ns + ":" + path + ":" + chunk

			if existing, ok := scoreMap[key]; ok {
				existing.score += rrfScore
			} else {
				r.Payload["source_namespace"] = ns
				scoreMap[key] = &scored{result: r, score: rrfScore}
			}
		}
	}

	sorted := make([]scored, 0, len(scoreMap))
	for _, s := range scoreMap {
		sorted = append(sorted, *s)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].score > sorted[j].score
	})

	out := make([]Result, 0, limit)
	for i := 0; i < len(sorted) && i < limit; i++ {
		r := sorted[i].result
		r.Score = float32(sorted[i].score)
		out = append(out, r)
	}
	return out
}
