package eval

import (
	"context"
	"testing"

	"github.com/piotrlaczykowski/emdexer/search"
)

func TestEvalMetricsRecorded(t *testing.T) {
	results := []search.Result{
		{Payload: map[string]any{"text": "Paris is the capital of France."}},
	}
	recallResp := `{"contains_answer": true, "confidence": 0.8}`
	faithResp := `{"faithful": true, "score": 0.9}`

	callCount := 0
	llm := func(ctx context.Context, prompt string) (string, error) {
		callCount++
		if callCount == 1 {
			return recallResp, nil
		}
		// second call is answer generation, third is faithfulness
		if callCount == 3 {
			return faithResp, nil
		}
		return "Generated answer.", nil
	}

	result := Run(context.Background(), Request{
		Question:       "What is the capital of France?",
		ExpectedAnswer: "Paris",
		Namespace:      "test",
		TopK:           1,
	}, mockSearch(results), llm)

	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Verdict == "" {
		t.Fatal("expected non-empty verdict")
	}
}
