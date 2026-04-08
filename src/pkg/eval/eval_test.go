package eval

import (
	"context"
	"fmt"
	"testing"

	"github.com/piotrlaczykowski/emdexer/search"
)

func mockSearch(results []search.Result) SearchFn {
	return func(ctx context.Context, query string, topK int, ns string) ([]search.Result, error) {
		return results, nil
	}
}

func mockLLM(response string) LLMFn {
	return func(ctx context.Context, prompt string) (string, error) {
		return response, nil
	}
}

func TestRun_HighRecall(t *testing.T) {
	results := []search.Result{
		{Payload: map[string]any{"text": "The capital of France is Paris."}},
	}
	recallResp := `{"contains_answer": true, "confidence": 0.95}`
	faithResp := `{"faithful": true, "score": 0.9}`

	callCount := 0
	llmFn := func(ctx context.Context, prompt string) (string, error) {
		callCount++
		if callCount == 1 {
			return recallResp, nil // recall judge
		}
		if callCount == 2 {
			return "Paris", nil // generation
		}
		return faithResp, nil // faithfulness judge
	}

	result := Run(context.Background(), Request{
		Question:       "What is the capital of France?",
		ExpectedAnswer: "Paris",
		Namespace:      "test",
	}, mockSearch(results), llmFn)

	if result.Verdict != "PASS" {
		t.Errorf("expected PASS, got %s (recall=%.2f faith=%.2f)", result.Verdict, result.ContextRecall, result.Faithfulness)
	}
	if result.RetrievedChunks != 1 {
		t.Errorf("expected 1 chunk, got %d", result.RetrievedChunks)
	}
}

func TestRun_LowRecall(t *testing.T) {
	results := []search.Result{
		{Payload: map[string]any{"text": "The weather in London is rainy."}},
	}
	recallResp := `{"contains_answer": false, "confidence": 0.1}`
	faithResp := `{"faithful": true, "score": 0.5}`

	callCount := 0
	llmFn := func(ctx context.Context, prompt string) (string, error) {
		callCount++
		if callCount == 1 {
			return recallResp, nil
		}
		if callCount == 2 {
			return "I don't know.", nil
		}
		return faithResp, nil
	}

	result := Run(context.Background(), Request{
		Question:       "What is the capital of France?",
		ExpectedAnswer: "Paris",
		Namespace:      "test",
	}, mockSearch(results), llmFn)

	if result.Verdict == "PASS" {
		t.Errorf("expected FAIL or PARTIAL, got PASS")
	}
}

func TestRun_NoAPIKey_SearchStillRuns(t *testing.T) {
	// Even with LLM errors, Run should return a result (not panic).
	results := []search.Result{}
	llmFn := func(ctx context.Context, prompt string) (string, error) {
		return "", fmt.Errorf("no API key")
	}

	result := Run(context.Background(), Request{
		Question:  "What is X?",
		Namespace: "test",
	}, mockSearch(results), llmFn)

	if result.Error != "" {
		t.Errorf("unexpected error field: %s", result.Error)
	}
	if result.Verdict != "FAIL" {
		t.Errorf("expected FAIL when LLM unavailable, got %s", result.Verdict)
	}
}

func TestRun_SearchError(t *testing.T) {
	searchFn := func(ctx context.Context, query string, topK int, ns string) ([]search.Result, error) {
		return nil, fmt.Errorf("qdrant unavailable")
	}

	result := Run(context.Background(), Request{
		Question:  "What is X?",
		Namespace: "test",
	}, searchFn, mockLLM(""))

	if result.Error == "" {
		t.Error("expected error field to be set on search failure")
	}
}

func TestComputeVerdict(t *testing.T) {
	cases := []struct {
		recall, faith float64
		want          string
	}{
		{0.9, 0.9, "PASS"},
		{0.7, 0.7, "PASS"},
		{0.5, 0.5, "PARTIAL"},
		{0.4, 0.4, "PARTIAL"},
		{0.2, 0.2, "FAIL"},
		{0.0, 0.0, "FAIL"},
	}
	for _, c := range cases {
		got := computeVerdict(c.recall, c.faith)
		if got != c.want {
			t.Errorf("computeVerdict(%.1f, %.1f) = %s, want %s", c.recall, c.faith, got, c.want)
		}
	}
}

func TestParseJSON_StripsMarkdown(t *testing.T) {
	var out struct{ X int }
	if err := parseJSON("```json\n{\"x\": 42}\n```", &out); err != nil {
		t.Fatal(err)
	}
	if out.X != 42 {
		t.Errorf("expected 42, got %d", out.X)
	}
}

func TestTruncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("short string should be unchanged")
	}
	got := truncate("hello world", 5)
	if got != "hello..." {
		t.Errorf("unexpected truncation: %q", got)
	}
}
