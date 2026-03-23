package search

import (
	"fmt"
	"sort"
)

// MergeRRFHybrid merges vector and BM25 results from a single namespace using RRF.
// Results that appear in both legs get their per-leg scores accumulated, naturally
// surfacing high-confidence hits that match both semantically and lexically.
// Uses the package-level defaultRRFConfig (configurable via env vars).
func MergeRRFHybrid(vectorResults, bm25Results []Result, limit int) []Result {
	return mergeRRFHybrid(vectorResults, bm25Results, limit, defaultRRFConfig)
}

// mergeRRFHybrid is the internal implementation that accepts an explicit RRFConfig.
// Score formula per result per leg: weight * (1.0 / (K + rank + 1)).
// If a leg's weight is 0.0 its results are excluded from the pool entirely.
func mergeRRFHybrid(vectorResults, bm25Results []Result, limit int, cfg RRFConfig) []Result {
	type scored struct {
		result Result
		score  float64
	}
	scoreMap := make(map[string]*scored)

	addResults := func(results []Result, weight float64) {
		if weight == 0 {
			return
		}
		for rank, r := range results {
			rrfScore := weight * (1.0 / (cfg.K + float64(rank) + 1))
			path, _ := r.Payload["path"].(string)
			chunk := fmt.Sprintf("%v", r.Payload["chunk"])
			key := path + ":" + chunk

			if existing, ok := scoreMap[key]; ok {
				existing.score += rrfScore
			} else {
				scoreMap[key] = &scored{result: r, score: rrfScore}
			}
		}
	}

	addResults(vectorResults, cfg.VectorWeight)
	addResults(bm25Results, cfg.BM25Weight)

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

// MergeRRFWeighted merges a primary result set with a secondary (graph-neighbour) set using
// RRF.  The secondary leg is multiplied by secondaryWeight (typically 0.7) so that direct
// matches always rank above structurally adjacent results.
// Uses the same K constant as the package-level defaultRRFConfig.
func MergeRRFWeighted(primary, secondary []Result, secondaryWeight float64, limit int) []Result {
	cfg := RRFConfig{
		K:            defaultRRFConfig.K,
		VectorWeight: 1.0,
		BM25Weight:   secondaryWeight,
	}
	return mergeRRFHybrid(primary, secondary, limit, cfg)
}

// MergeRRF merges results from multiple namespace searches using Reciprocal Rank Fusion.
// Uses the package-level defaultRRFConfig (configurable via env vars).
func MergeRRF(perNS map[string][]Result, limit int) []Result {
	return mergeRRF(perNS, limit, defaultRRFConfig)
}

// mergeRRF is the internal implementation that accepts an explicit RRFConfig.
func mergeRRF(perNS map[string][]Result, limit int, cfg RRFConfig) []Result {
	type scored struct {
		result Result
		score  float64
	}
	scoreMap := make(map[string]*scored)

	for ns, nsResults := range perNS {
		for rank, r := range nsResults {
			rrfScore := 1.0 / (cfg.K + float64(rank) + 1)
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
