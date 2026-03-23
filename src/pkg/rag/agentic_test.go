package rag

import (
	"context"
	"errors"
	"testing"

	"github.com/piotrlaczykowski/emdexer/audit"
	"github.com/piotrlaczykowski/emdexer/search"
)

// helpers

func makeResults(ids ...uint64) []search.Result {
	results := make([]search.Result, len(ids))
	for i, id := range ids {
		results[i] = search.Result{
			ID:      id,
			Score:   float32(100 - i),
			Payload: map[string]interface{}{"text": "content for " + string(rune('A'+i))},
		}
	}
	return results
}

func noopAudit(_ audit.Entry) {}

func fixedEmbed(_ context.Context, _ string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}

func fixedSearch(extraResults []search.Result) SearchFn {
	return func(_ context.Context, _ string, _ []float32, _ uint64, _ string) ([]search.Result, error) {
		return extraResults, nil
	}
}

// TestEarlyStop — LLM immediately reports answer_ready=true on the first assessment.
// Expect: only 1 hop total (no additional queries).
func TestEarlyStop(t *testing.T) {
	cfg := AgenticConfig{Enabled: true, MaxHops: 3, ConfidenceThreshold: 0.7}
	hop1 := makeResults(1, 2, 3)

	assessCallCount := 0
	assessFn := func(_ context.Context, _, _ string) (string, error) {
		assessCallCount++
		return `{"confidence":0.9,"answer_ready":true,"follow_up_queries":[],"reasoning":"sufficient"}`, nil
	}

	searchCallCount := 0
	searchFn := func(_ context.Context, _ string, _ []float32, _ uint64, _ string) ([]search.Result, error) {
		searchCallCount++
		return makeResults(10, 11), nil
	}

	results, hops, err := RunAgenticLoop(context.Background(), cfg, searchFn, fixedEmbed, noopAudit, assessFn,
		"test question", "ns1", hop1, "key")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hops != 1 {
		t.Errorf("expected 1 hop (early stop), got %d", hops)
	}
	if assessCallCount != 1 {
		t.Errorf("expected 1 assess call, got %d", assessCallCount)
	}
	if searchCallCount != 0 {
		t.Errorf("expected 0 follow-up searches, got %d", searchCallCount)
	}
	if len(results) != len(hop1) {
		t.Errorf("expected %d results (hop1 unchanged), got %d", len(hop1), len(results))
	}
}

// TestMaxHops — LLM never reaches confidence threshold.
// Expect: exactly MaxHops total (loop runs until exhausted).
func TestMaxHops(t *testing.T) {
	cfg := AgenticConfig{Enabled: true, MaxHops: 3, ConfidenceThreshold: 0.7}
	hop1 := makeResults(1, 2)

	assessFn := func(_ context.Context, _, _ string) (string, error) {
		return `{"confidence":0.3,"answer_ready":false,"follow_up_queries":["more info"],"reasoning":"not enough"}`, nil
	}

	_, hops, err := RunAgenticLoop(context.Background(), cfg, fixedSearch(makeResults(5, 6)), fixedEmbed, noopAudit, assessFn,
		"test question", "ns1", hop1, "key")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hops != cfg.MaxHops {
		t.Errorf("expected %d hops (max), got %d", cfg.MaxHops, hops)
	}
}

// TestNamespaceIsolation — follow-up searches MUST use the same namespace as the original.
func TestNamespaceIsolation(t *testing.T) {
	cfg := AgenticConfig{Enabled: true, MaxHops: 2, ConfidenceThreshold: 0.7}
	hop1 := makeResults(1)
	const wantNS = "isolated-ns"

	assessFn := func(_ context.Context, _, _ string) (string, error) {
		return `{"confidence":0.3,"answer_ready":false,"follow_up_queries":["follow-up query"],"reasoning":"need more"}`, nil
	}

	var gotNS string
	namespaceCaptureSearch := func(_ context.Context, _ string, _ []float32, _ uint64, ns string) ([]search.Result, error) {
		gotNS = ns
		return makeResults(99), nil
	}

	_, _, err := RunAgenticLoop(context.Background(), cfg, namespaceCaptureSearch, fixedEmbed, noopAudit, assessFn,
		"test question", wantNS, hop1, "key")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotNS != wantNS {
		t.Errorf("namespace isolation violated: follow-up search used namespace %q, want %q", gotNS, wantNS)
	}
}

// TestFallbackOnError — if the assess function returns an error, return hop1Results unchanged.
func TestFallbackOnError(t *testing.T) {
	cfg := AgenticConfig{Enabled: true, MaxHops: 3, ConfidenceThreshold: 0.7}
	hop1 := makeResults(1, 2, 3)

	assessFn := func(_ context.Context, _, _ string) (string, error) {
		return "", errors.New("LLM unavailable")
	}

	searchCallCount := 0
	searchFn := func(_ context.Context, _ string, _ []float32, _ uint64, _ string) ([]search.Result, error) {
		searchCallCount++
		return makeResults(10), nil
	}

	results, hops, err := RunAgenticLoop(context.Background(), cfg, searchFn, fixedEmbed, noopAudit, assessFn,
		"test question", "ns1", hop1, "key")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hops != 1 {
		t.Errorf("expected 1 hop on error fallback, got %d", hops)
	}
	if searchCallCount != 0 {
		t.Errorf("expected 0 follow-up searches on error, got %d", searchCallCount)
	}
	if len(results) != len(hop1) {
		t.Errorf("expected hop1 results returned on fallback, got %d results", len(results))
	}
	for i, r := range results {
		if r.ID != hop1[i].ID {
			t.Errorf("result[%d] ID mismatch: got %d, want %d", i, r.ID, hop1[i].ID)
		}
	}
}
