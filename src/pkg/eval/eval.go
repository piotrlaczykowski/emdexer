package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/piotrlaczykowski/emdexer/search"
)

var evalContextRecall = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "emdexer_gateway_eval_context_recall",
	Help:    "Context recall score from eval runs (0.0–1.0)",
	Buckets: []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
}, []string{"namespace"})

var evalFaithfulness = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "emdexer_gateway_eval_faithfulness",
	Help:    "Faithfulness score from eval runs (0.0–1.0)",
	Buckets: []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
}, []string{"namespace", "verdict"})

var evalRunsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_eval_runs_total",
	Help: "Total number of eval runs",
}, []string{"namespace", "verdict"})

// Request is the input for an eval run.
type Request struct {
	Question       string `json:"question"`
	ExpectedAnswer string `json:"expected_answer"`
	Namespace      string `json:"namespace"`
	TopK           int    `json:"top_k"` // default 5
}

// Result is the output of an eval run.
type Result struct {
	ContextRecall   float64 `json:"context_recall"`   // 0.0–1.0
	Faithfulness    float64 `json:"faithfulness"`     // 0.0–1.0
	RetrievedChunks int     `json:"retrieved_chunks"`
	LatencyMs       int64   `json:"latency_ms"`
	Verdict         string  `json:"verdict"` // "PASS" | "PARTIAL" | "FAIL"
	Error           string  `json:"error,omitempty"`
}

// SearchFn abstracts the search call for testability.
type SearchFn func(ctx context.Context, query string, topK int, namespace string) ([]search.Result, error)

// LLMFn abstracts the LLM call for testability.
// The prompt may request a JSON response; the function returns the raw response string.
type LLMFn func(ctx context.Context, prompt string) (string, error)

// Run executes an eval: searches for context, then runs two LLM-as-judge checks.
func Run(ctx context.Context, req Request, searchFn SearchFn, llmFn LLMFn) Result {
	start := time.Now()

	if req.TopK <= 0 {
		req.TopK = 5
	}

	// Step 1: retrieve context chunks.
	results, err := searchFn(ctx, req.Question, req.TopK, req.Namespace)
	if err != nil {
		return Result{Error: fmt.Sprintf("search failed: %v", err), LatencyMs: ms(start)}
	}

	// Build context string from results.
	var contextParts []string
	for _, r := range results {
		if t, ok := r.Payload["text"].(string); ok && t != "" {
			contextParts = append(contextParts, t)
		}
	}
	contextStr := strings.Join(contextParts, "\n---\n")

	// Step 2: context recall — does the context contain the answer?
	recallScore, recallErr := checkContextRecall(ctx, req.Question, req.ExpectedAnswer, contextStr, llmFn)
	if recallErr != nil {
		recallScore = 0.0
	}

	// Step 3: faithfulness — generate an answer and check it's grounded.
	faithScore, faithErr := checkFaithfulness(ctx, req.Question, contextStr, llmFn)
	if faithErr != nil {
		faithScore = 0.0
	}

	verdict := computeVerdict(recallScore, faithScore)

	result := Result{
		ContextRecall:   recallScore,
		Faithfulness:    faithScore,
		RetrievedChunks: len(results),
		LatencyMs:       ms(start),
		Verdict:         verdict,
	}

	evalContextRecall.WithLabelValues(req.Namespace).Observe(result.ContextRecall)
	evalFaithfulness.WithLabelValues(req.Namespace, result.Verdict).Observe(result.Faithfulness)
	evalRunsTotal.WithLabelValues(req.Namespace, result.Verdict).Inc()

	return result
}

func ms(start time.Time) int64 { return time.Since(start).Milliseconds() }

func computeVerdict(recall, faith float64) string {
	avg := (recall + faith) / 2
	switch {
	case avg >= 0.7:
		return "PASS"
	case avg >= 0.4:
		return "PARTIAL"
	default:
		return "FAIL"
	}
}

// checkContextRecall asks the LLM whether the retrieved context contains
// enough information to answer the question.
func checkContextRecall(ctx context.Context, question, expectedAnswer, contextStr string, llmFn LLMFn) (float64, error) {
	prompt := fmt.Sprintf(`You are evaluating a retrieval-augmented generation system.

Question: %s
Expected answer: %s

Retrieved context:
%s

Does the retrieved context contain information sufficient to answer the question?
Respond with valid JSON only, no explanation:
{"contains_answer": true/false, "confidence": 0.0-1.0}`, question, expectedAnswer, truncate(contextStr, 3000))

	raw, err := llmFn(ctx, prompt)
	if err != nil {
		return 0, err
	}
	var resp struct {
		ContainsAnswer bool    `json:"contains_answer"`
		Confidence     float64 `json:"confidence"`
	}
	if err := parseJSON(raw, &resp); err != nil {
		return 0, err
	}
	if !resp.ContainsAnswer {
		return resp.Confidence * 0.3, nil // partial credit for low-confidence miss
	}
	return resp.Confidence, nil
}

// checkFaithfulness generates an answer from context and checks it's grounded.
func checkFaithfulness(ctx context.Context, question, contextStr string, llmFn LLMFn) (float64, error) {
	// First generate an answer.
	genPrompt := fmt.Sprintf(`Answer this question using ONLY the provided context.
Be concise. If the context doesn't contain the answer, say "I don't know."

Context:
%s

Question: %s

Answer:`, truncate(contextStr, 3000), question)

	answer, err := llmFn(ctx, genPrompt)
	if err != nil {
		return 0, err
	}

	// Then judge faithfulness.
	judgePrompt := fmt.Sprintf(`You are evaluating whether an answer is faithful to its source context.

Context:
%s

Answer: %s

Is every claim in the answer supported by the context (no hallucination)?
Respond with valid JSON only, no explanation:
{"faithful": true/false, "score": 0.0-1.0}`, truncate(contextStr, 2000), truncate(answer, 500))

	raw, err := llmFn(ctx, judgePrompt)
	if err != nil {
		return 0, err
	}
	var resp struct {
		Faithful bool    `json:"faithful"`
		Score    float64 `json:"score"`
	}
	if err := parseJSON(raw, &resp); err != nil {
		return 0, err
	}
	return resp.Score, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func parseJSON(s string, v any) error {
	// LLMs sometimes wrap JSON in markdown code blocks — strip them.
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	return json.Unmarshal([]byte(s), v)
}
