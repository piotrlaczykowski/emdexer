package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"github.com/piotrlaczykowski/emdexer/audit"
	"github.com/piotrlaczykowski/emdexer/search"
)

var agenticHopsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_agentic_hops_total",
	Help: "Total number of additional agentic hops performed beyond the initial retrieval",
}, []string{"namespace"})

var agenticConfidenceScore = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "emdexer_gateway_agentic_confidence_score",
	Help:    "Confidence score assessed by the LLM at each hop",
	Buckets: []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
}, []string{"namespace"})

var agenticEarlyStopTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_agentic_early_stop_total",
	Help: "Number of agentic loops that stopped early due to sufficient confidence",
}, []string{"namespace"})

// AgenticConfig holds configuration for the multi-hop RAG loop.
type AgenticConfig struct {
	Enabled             bool
	MaxHops             int
	ConfidenceThreshold float64
}

// HopAssessment is the structured response from the LLM during a hop assessment.
type HopAssessment struct {
	Confidence      float64  `json:"confidence"`
	AnswerReady     bool     `json:"answer_ready"`
	FollowUpQueries []string `json:"follow_up_queries"`
	Reasoning       string   `json:"reasoning"`
}

// SearchFn is the search function signature used by the agentic loop.
type SearchFn func(ctx context.Context, query string, vector []float32, limit uint64, namespace string) ([]search.Result, error)

// EmbedFn is the embedding function signature used by the agentic loop.
type EmbedFn func(ctx context.Context, text string) ([]float32, error)

// AuditFn is the audit function signature used by the agentic loop.
type AuditFn func(entry audit.Entry)

// AssessFn is the LLM call used for hop assessment. Returns raw JSON matching HopAssessment.
type AssessFn func(ctx context.Context, prompt, apiKey string) (string, error)

// RunAgenticLoop performs multi-hop retrieval starting from hop1Results.
// It iteratively asks the LLM whether the accumulated context is sufficient,
// runs follow-up queries when it is not, and merges results via score-based dedup.
// On any error, it returns hop1Results unchanged (safe fallback).
func RunAgenticLoop(
	ctx context.Context,
	cfg AgenticConfig,
	searchFn SearchFn,
	embedFn EmbedFn,
	auditFn AuditFn,
	assessFn AssessFn,
	question, namespace string,
	hop1Results []search.Result,
	apiKey string,
) ([]search.Result, int, error) {
	const maxResultsPerHop = 5
	const maxAccumulated = 20

	ctx, loopSpan := otel.Tracer("emdexer").Start(ctx, "emdex.agentic.loop")
	loopSpan.SetAttributes(
		attribute.String("agentic.namespace", namespace),
		attribute.Int("agentic.max_hops", cfg.MaxHops),
	)
	defer loopSpan.End()

	accumulated := make([]search.Result, len(hop1Results))
	copy(accumulated, hop1Results)
	totalHops := 1

	for hop := 2; hop <= cfg.MaxHops; hop++ {
		hopCtx, hopSpan := otel.Tracer("emdexer").Start(ctx, "emdex.agentic.hop")
		hopSpan.SetAttributes(attribute.Int("agentic.hop", hop))

		contextStr := BuildContext(accumulated)
		prompt := buildAssessmentPrompt(question, contextStr)

		rawJSON, err := assessFn(hopCtx, prompt, apiKey)
		if err != nil {
			log.Printf("[agentic] hop %d assessment error: %v — falling back to accumulated results", hop, err)
			hopSpan.End()
			break
		}

		var assessment HopAssessment
		if err := json.Unmarshal([]byte(rawJSON), &assessment); err != nil {
			log.Printf("[agentic] hop %d JSON parse error: %v — falling back to accumulated results", hop, err)
			hopSpan.End()
			break
		}

		agenticConfidenceScore.WithLabelValues(namespace).Observe(assessment.Confidence)
		hopSpan.SetAttributes(
			attribute.Float64("agentic.confidence", assessment.Confidence),
			attribute.Bool("agentic.answer_ready", assessment.AnswerReady),
		)

		if assessment.AnswerReady || assessment.Confidence >= cfg.ConfidenceThreshold {
			agenticEarlyStopTotal.WithLabelValues(namespace).Inc()
			log.Printf("[agentic] early stop at hop %d, confidence=%.2f, answer_ready=%v",
				hop, assessment.Confidence, assessment.AnswerReady)
			hopSpan.End()
			break
		}

		var hopResults []search.Result
		for _, followUpQuery := range assessment.FollowUpQueries {
			if followUpQuery == "" {
				continue
			}
			vec, err := embedFn(hopCtx, followUpQuery)
			if err != nil {
				log.Printf("[agentic] hop %d embed error for follow-up %q: %v", hop, followUpQuery, err)
				continue
			}
			r, err := searchFn(hopCtx, followUpQuery, vec, uint64(maxResultsPerHop), namespace)
			if err != nil {
				log.Printf("[agentic] hop %d search error for follow-up %q: %v", hop, followUpQuery, err)
				continue
			}
			hopResults = append(hopResults, r...)
		}

		auditFn(audit.Entry{
			Action:    "agentic_hop",
			Query:     question,
			Namespace: namespace,
			Results:   len(hopResults),
			Metadata: map[string]interface{}{
				"hop":             hop,
				"confidence":      assessment.Confidence,
				"follow_up_count": len(assessment.FollowUpQueries),
				"reasoning":       assessment.Reasoning,
			},
		})

		accumulated = mergeAgenticResults(accumulated, hopResults, maxAccumulated)
		totalHops = hop
		agenticHopsTotal.WithLabelValues(namespace).Inc()
		hopSpan.End()
	}

	return accumulated, totalHops, nil
}

// buildAssessmentPrompt constructs the prompt sent to the LLM for hop assessment.
func buildAssessmentPrompt(question, contextStr string) string {
	return fmt.Sprintf(`You are a search quality evaluator. Assess whether the retrieved context is sufficient to answer the question comprehensively.

Question: %s

Retrieved context:
%s

Respond ONLY with valid JSON in this exact format, with no additional text:
{
  "confidence": <0.0 to 1.0>,
  "answer_ready": <true or false>,
  "follow_up_queries": ["<query1>", "<query2>"],
  "reasoning": "<brief explanation>"
}

confidence: 0.0 means no relevant info found, 1.0 means the question can be answered fully.
answer_ready: set to true when confidence is high enough to answer without more retrieval.
follow_up_queries: 1-3 specific search queries to fill knowledge gaps (empty array if answer_ready is true).
reasoning: one sentence explaining the assessment.`, question, contextStr)
}

// mergeAgenticResults deduplicates results by ID, keeps the higher score for duplicates,
// sorts by score descending, and trims to limit.
func mergeAgenticResults(existing, incoming []search.Result, limit int) []search.Result {
	seen := make(map[uint64]int, len(existing)+len(incoming))
	merged := make([]search.Result, len(existing))
	copy(merged, existing)

	for i, r := range merged {
		seen[r.ID] = i
	}

	for _, r := range incoming {
		if idx, ok := seen[r.ID]; ok {
			if r.Score > merged[idx].Score {
				merged[idx] = r
			}
		} else {
			seen[r.ID] = len(merged)
			merged = append(merged, r)
		}
	}

	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})

	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}
