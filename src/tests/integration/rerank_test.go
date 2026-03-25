package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/piotrlaczykowski/emdexer/rerank"
	"github.com/piotrlaczykowski/emdexer/search"
)

// ── NoOpReranker ──────────────────────────────────────────────────────────────

func TestNoOpReranker_ReturnsZeroScores(t *testing.T) {
	r := rerank.NoOpReranker{}
	texts := []string{"hello world", "foo bar", "baz qux"}
	scores, err := r.Rerank(context.Background(), "test query", texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scores) != len(texts) {
		t.Fatalf("expected %d scores, got %d", len(texts), len(scores))
	}
	for i, s := range scores {
		if s != 0.0 {
			t.Errorf("score[%d] = %v, want 0.0", i, s)
		}
	}
}

func TestNoOpReranker_EmptyTexts(t *testing.T) {
	r := rerank.NoOpReranker{}
	scores, err := r.Rerank(context.Background(), "query", []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scores) != 0 {
		t.Errorf("expected empty scores, got %d", len(scores))
	}
}

// ── rerank.Rank helper ────────────────────────────────────────────────────────

func TestRank_SortsByScoreDescending(t *testing.T) {
	// Mock reranker that returns fixed scores [0.1, 0.9, 0.5].
	texts := []string{"low", "high", "mid"}
	r := &fixedReranker{scores: []float32{0.1, 0.9, 0.5}}

	ranked, err := rerank.Rank(context.Background(), r, "q", texts, 0, "ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []int{1, 2, 0} // high → mid → low
	for i, sc := range ranked {
		if sc.Index != want[i] {
			t.Errorf("ranked[%d].Index = %d, want %d", i, sc.Index, want[i])
		}
	}
}

func TestRank_TopKTruncates(t *testing.T) {
	r := &fixedReranker{scores: []float32{0.3, 0.8, 0.6, 0.1, 0.9}}
	ranked, err := rerank.Rank(context.Background(), r, "q", make([]string, 5), 3, "ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ranked) != 3 {
		t.Errorf("expected 3 results, got %d", len(ranked))
	}
}

func TestRank_EmptyTexts(t *testing.T) {
	r := rerank.NoOpReranker{}
	ranked, err := rerank.Rank(context.Background(), r, "q", []string{}, 10, "ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ranked != nil {
		t.Errorf("expected nil for empty input, got %v", ranked)
	}
}

// ── SidecarReranker HTTP contract ─────────────────────────────────────────────

func TestSidecarReranker_CallsEndpointAndParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rerank" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Query string   `json:"query"`
			Texts []string `json:"texts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Return scores in reverse order to simulate a different ranking.
		type item struct {
			Index int     `json:"index"`
			Score float32 `json:"score"`
		}
		results := make([]item, len(req.Texts))
		for i := range req.Texts {
			results[i] = item{Index: i, Score: float32(len(req.Texts)-i) * 0.1}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": results})
	}))
	defer srv.Close()

	reranker := rerank.NewSidecarReranker(srv.URL, "")
	texts := []string{"a", "b", "c"}
	scores, err := reranker.Rerank(context.Background(), "query", texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scores) != 3 {
		t.Fatalf("expected 3 scores, got %d", len(scores))
	}
	// scores[0] should be highest (0.3), scores[2] lowest (0.1)
	if scores[0] <= scores[2] {
		t.Errorf("expected scores[0] > scores[2], got %v > %v", scores[0], scores[2])
	}
}

func TestSidecarReranker_SidecarDown_ReturnsError(t *testing.T) {
	// Point at a closed server to simulate sidecar being down.
	reranker := rerank.NewSidecarReranker("http://127.0.0.1:19999", "")
	_, err := reranker.Rerank(context.Background(), "q", []string{"text"})
	if err == nil {
		t.Error("expected error when sidecar is unreachable, got nil")
	}
}

func TestSidecarReranker_Non200_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not loaded", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	reranker := rerank.NewSidecarReranker(srv.URL, "")
	_, err := reranker.Rerank(context.Background(), "q", []string{"text"})
	if err == nil {
		t.Error("expected error for non-200 response, got nil")
	}
}

// ── search.Result RerankScore field ───────────────────────────────────────────

func TestResult_RerankScoreOmittedWhenNil(t *testing.T) {
	r := search.Result{ID: 1, Score: 0.8, Payload: map[string]interface{}{"text": "hello"}}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	if _, ok := m["rerank_score"]; ok {
		t.Error("rerank_score should be omitted when nil")
	}
}

func TestResult_RerankScorePresentWhenSet(t *testing.T) {
	score := float32(0.95)
	r := search.Result{ID: 1, Score: 0.8, RerankScore: &score, Payload: map[string]interface{}{}}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	v, ok := m["rerank_score"]
	if !ok {
		t.Fatal("rerank_score missing from JSON")
	}
	if fmt.Sprintf("%.2f", v.(float64)) != "0.95" {
		t.Errorf("rerank_score = %v, want 0.95", v)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

type fixedReranker struct{ scores []float32 }

func (f *fixedReranker) Rerank(_ context.Context, _ string, _ []string) ([]float32, error) {
	return f.scores, nil
}
