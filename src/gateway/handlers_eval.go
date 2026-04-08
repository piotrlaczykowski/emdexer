package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"

	"github.com/piotrlaczykowski/emdexer/eval"
	"github.com/piotrlaczykowski/emdexer/llm"
	"github.com/piotrlaczykowski/emdexer/search"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
)

func (s *Server) handleEval(w http.ResponseWriter, r *http.Request) {
	ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.eval")
	defer span.End()
	r = r.WithContext(ctx)

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Guard: eval requires LLM access.
	if s.apiKey == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "eval requires LLM access; set GOOGLE_API_KEY or EMDEX_OLLAMA_URL",
		})
		return
	}

	var req eval.Request
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Question == "" {
		http.Error(w, "question is required", http.StatusBadRequest)
		return
	}

	span.SetAttributes(
		attribute.String("eval.namespace", req.Namespace),
		attribute.Int("eval.top_k", req.TopK),
	)

	// Wire up searchFn — reuse existing embed + search infrastructure.
	apiKey := s.apiKey
	searchFn := func(ctx context.Context, query string, topK int, namespace string) ([]search.Result, error) {
		vector, err := s.embedder.Embed(ctx, query)
		if err != nil {
			return nil, err
		}
		if s.bm25Enabled {
			return search.HybridSearch(ctx, s.pointsClient, s.collection, query, vector, uint64(topK), namespace)
		}
		return search.SearchQdrant(ctx, s.pointsClient, s.collection, vector, uint64(topK), namespace)
	}

	// Wire up llmFn — use JSON-mode Gemini for all eval calls.
	llmFn := func(ctx context.Context, prompt string) (string, error) {
		return llm.CallGeminiStructured(ctx, prompt, apiKey)
	}

	result := eval.Run(r.Context(), req, searchFn, llmFn)

	if result.Error != "" {
		log.Printf("[eval] namespace=%q verdict=%s error=%s", req.Namespace, result.Verdict, result.Error)
	} else {
		log.Printf("[eval] namespace=%q verdict=%s recall=%.2f faith=%.2f chunks=%d latency=%dms",
			req.Namespace, result.Verdict, result.ContextRecall, result.Faithfulness, result.RetrievedChunks, result.LatencyMs)
	}

	span.SetAttributes(
		attribute.String("eval.verdict", result.Verdict),
		attribute.Float64("eval.context_recall", result.ContextRecall),
		attribute.Float64("eval.faithfulness", result.Faithfulness),
	)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
