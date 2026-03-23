package search

import (
	"math"
	"testing"
)

func TestMergeRRF_SingleNamespace(t *testing.T) {
	perNS := map[string][]Result{
		"docs": {
			{ID: 1, Score: 0.9, Payload: map[string]interface{}{"path": "a.md", "chunk": 0}},
			{ID: 2, Score: 0.8, Payload: map[string]interface{}{"path": "b.md", "chunk": 1}},
		},
	}

	results := MergeRRF(perNS, 10)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Rank 0 -> 1/(60+0+1) = 1/61; Rank 1 -> 1/(60+1+1) = 1/62
	// Results should be sorted by RRF score descending
	if results[0].Score <= results[1].Score {
		t.Errorf("expected results sorted by RRF score descending, got %f <= %f", results[0].Score, results[1].Score)
	}

	expectedFirst := float32(1.0 / 61.0)
	if math.Abs(float64(results[0].Score-expectedFirst)) > 1e-6 {
		t.Errorf("expected first score ~%f, got %f", expectedFirst, results[0].Score)
	}
}

func TestMergeRRF_TwoNamespaces(t *testing.T) {
	perNS := map[string][]Result{
		"ns1": {
			{ID: 1, Score: 0.9, Payload: map[string]interface{}{"path": "shared.md", "chunk": 0}},
			{ID: 2, Score: 0.8, Payload: map[string]interface{}{"path": "only1.md", "chunk": 0}},
		},
		"ns2": {
			{ID: 3, Score: 0.95, Payload: map[string]interface{}{"path": "only2.md", "chunk": 0}},
		},
	}

	results := MergeRRF(perNS, 10)

	if len(results) != 3 {
		t.Fatalf("expected 3 results (no duplicates across different namespaces with different keys), got %d", len(results))
	}

	// Verify results are sorted by score descending
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted: index %d score %f > index %d score %f", i, results[i].Score, i-1, results[i-1].Score)
		}
	}
}

func TestMergeRRF_TwoNamespaces_Dedup(t *testing.T) {
	// Same ns:path:chunk key should be deduplicated with accumulated scores
	perNS := map[string][]Result{
		"ns1": {
			{ID: 1, Score: 0.9, Payload: map[string]interface{}{"path": "a.md", "chunk": 0}},
		},
	}
	// Add a second entry with the same namespace, path, and chunk at a different rank
	perNS["ns1"] = append(perNS["ns1"],
		Result{ID: 2, Score: 0.8, Payload: map[string]interface{}{"path": "b.md", "chunk": 0}},
	)

	results := MergeRRF(perNS, 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestMergeRRF_Empty(t *testing.T) {
	results := MergeRRF(map[string][]Result{}, 10)
	if len(results) != 0 {
		t.Fatalf("expected 0 results for empty input, got %d", len(results))
	}

	results = MergeRRF(nil, 10)
	if len(results) != 0 {
		t.Fatalf("expected 0 results for nil input, got %d", len(results))
	}
}

func TestMergeRRF_InjectsSourceNamespace(t *testing.T) {
	perNS := map[string][]Result{
		"finance": {
			{ID: 1, Score: 0.9, Payload: map[string]interface{}{"path": "report.md", "chunk": 0}},
		},
	}

	results := MergeRRF(perNS, 10)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	ns, ok := results[0].Payload["source_namespace"].(string)
	if !ok {
		t.Fatal("source_namespace not set in payload")
	}
	if ns != "finance" {
		t.Errorf("expected source_namespace 'finance', got %q", ns)
	}
}

func TestMergeRRF_Limit(t *testing.T) {
	perNS := map[string][]Result{
		"ns": {
			{ID: 1, Payload: map[string]interface{}{"path": "a.md", "chunk": 0}},
			{ID: 2, Payload: map[string]interface{}{"path": "b.md", "chunk": 0}},
			{ID: 3, Payload: map[string]interface{}{"path": "c.md", "chunk": 0}},
		},
	}

	results := MergeRRF(perNS, 2)
	if len(results) != 2 {
		t.Fatalf("expected 2 results with limit=2, got %d", len(results))
	}
}

// ── MergeRRFHybrid tests ──────────────────────────────────────────────────────

func TestMergeRRFHybrid_BothLegsAccumulateScore(t *testing.T) {
	// A result appearing in both vector and BM25 legs should accumulate scores
	// and rank higher than a result that appears in only one leg.
	shared := Result{ID: 1, Payload: map[string]interface{}{"path": "shared.md", "chunk": 0}}
	onlyVector := Result{ID: 2, Payload: map[string]interface{}{"path": "vector-only.md", "chunk": 0}}
	onlyBM25 := Result{ID: 3, Payload: map[string]interface{}{"path": "bm25-only.md", "chunk": 0}}

	vector := []Result{shared, onlyVector}
	bm25 := []Result{shared, onlyBM25}

	results := MergeRRFHybrid(vector, bm25, 10)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	// shared appears rank-0 in both legs: 1/61 + 1/61 ≈ 0.0328
	// onlyVector is rank-1 in vector: 1/62 ≈ 0.0161
	if results[0].Payload["path"] != "shared.md" {
		t.Errorf("expected shared.md to rank first, got %v", results[0].Payload["path"])
	}
}

func TestMergeRRFHybrid_VectorOnlyWhenBM25Empty(t *testing.T) {
	vector := []Result{
		{ID: 1, Payload: map[string]interface{}{"path": "a.md", "chunk": 0}},
		{ID: 2, Payload: map[string]interface{}{"path": "b.md", "chunk": 0}},
	}

	results := MergeRRFHybrid(vector, nil, 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 results from vector-only, got %d", len(results))
	}
}

func TestMergeRRFHybrid_BM25OnlyWhenVectorEmpty(t *testing.T) {
	bm25 := []Result{
		{ID: 1, Payload: map[string]interface{}{"path": "a.md", "chunk": 0}},
	}

	results := MergeRRFHybrid(nil, bm25, 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 result from BM25-only, got %d", len(results))
	}
}

func TestMergeRRFHybrid_Dedup(t *testing.T) {
	// Same path:chunk in both legs must appear exactly once.
	r := Result{ID: 1, Payload: map[string]interface{}{"path": "doc.md", "chunk": 2}}

	results := MergeRRFHybrid([]Result{r}, []Result{r}, 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 deduplicated result, got %d", len(results))
	}
	// Accumulated score should be twice the single-leg score.
	singleLegScore := float32(1.0 / 61.0)
	if results[0].Score < 2*singleLegScore-1e-6 {
		t.Errorf("expected accumulated score ≥ %f, got %f", 2*singleLegScore, results[0].Score)
	}
}

func TestMergeRRFHybrid_Limit(t *testing.T) {
	vector := []Result{
		{ID: 1, Payload: map[string]interface{}{"path": "a.md", "chunk": 0}},
		{ID: 2, Payload: map[string]interface{}{"path": "b.md", "chunk": 0}},
		{ID: 3, Payload: map[string]interface{}{"path": "c.md", "chunk": 0}},
	}

	results := MergeRRFHybrid(vector, nil, 2)
	if len(results) != 2 {
		t.Fatalf("expected 2 results with limit=2, got %d", len(results))
	}
}

func TestMergeRRFHybrid_Empty(t *testing.T) {
	results := MergeRRFHybrid(nil, nil, 10)
	if len(results) != 0 {
		t.Fatalf("expected 0 results for nil inputs, got %d", len(results))
	}

	results = MergeRRFHybrid([]Result{}, []Result{}, 10)
	if len(results) != 0 {
		t.Fatalf("expected 0 results for empty inputs, got %d", len(results))
	}
}

func TestMergeRRFHybrid_SortedDescending(t *testing.T) {
	// Rank-0 vector result scores 1/61; rank-0 BM25 also 1/61.
	// A result at rank 0 in vector only scores 1/61; a result at rank 0 in
	// both scores 2/61 — verify output is sorted highest-score-first.
	shared := Result{ID: 1, Payload: map[string]interface{}{"path": "shared.md", "chunk": 0}}
	solo := Result{ID: 2, Payload: map[string]interface{}{"path": "solo.md", "chunk": 0}}

	results := MergeRRFHybrid([]Result{shared, solo}, []Result{shared}, 10)

	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted: index %d score %f > index %d score %f",
				i, results[i].Score, i-1, results[i-1].Score)
		}
	}
}
