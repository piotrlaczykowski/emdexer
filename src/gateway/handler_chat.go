package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/piotrlaczykowski/emdexer/audit"
	"github.com/piotrlaczykowski/emdexer/auth"
	"github.com/piotrlaczykowski/emdexer/llm"
	"github.com/piotrlaczykowski/emdexer/openai"
	"github.com/piotrlaczykowski/emdexer/rag"
	"github.com/piotrlaczykowski/emdexer/search"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	// Extract W3C Trace Context from incoming headers and create root span.
	ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.chat")
	defer span.End()
	// Hard deadline to prevent goroutine leaks on slow LLM/search calls (Fix R2).
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	r = r.WithContext(ctx)

	start := time.Now()

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	allowedNamespaces, ok := auth.GetAllowedNamespaces(r)
	if !ok {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var user string
	if claims, ok := auth.GetUserClaims(r); ok {
		user = logSafe(claims.Subject)
	}

	var req openai.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	requestedNamespace := logSafe(strings.TrimSpace(r.Header.Get("X-Emdex-Namespace")))
	if requestedNamespace == "" {
		requestedNamespace = logSafe(strings.TrimSpace(r.URL.Query().Get("namespace")))
	}
	if requestedNamespace == "" {
		requestedNamespace = "default"
	}

	isGlobal := requestedNamespace == "*" || requestedNamespace == "__global__"
	if !isGlobal {
		isAllowed := false
		for _, ns := range allowedNamespaces {
			if ns == "*" || ns == requestedNamespace {
				isAllowed = true
				break
			}
		}
		if !isAllowed {
			http.Error(w, "Forbidden: Namespace not authorized", http.StatusForbidden)
			return
		}
	}

	var question string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			question = logSafe(req.Messages[i].Content)
			break
		}
	}
	if question == "" {
		http.Error(w, "Bad request: no user message found", http.StatusBadRequest)
		return
	}

	embedCtx, embedCancel := context.WithTimeout(r.Context(), s.embedTimeout)
	defer embedCancel()
	vector, err := s.embedder.Embed(embedCtx, question)
	if err != nil {
		http.Error(w, fmt.Sprintf("embedding error: %v", err), http.StatusBadGateway)
		audit.Log(audit.Entry{
			Action:    "chat",
			User:      user,
			Query:     question,
			Namespace: requestedNamespace,
			LatencyMS: time.Since(start).Milliseconds(),
			Status:    http.StatusBadGateway,
		})
		return
	}

	namespaces := search.ResolveNamespaces(requestedNamespace, allowedNamespaces, s.knownNamespaces())

	var results []search.Result
	if len(namespaces) <= 1 {
		ns := ""
		if len(namespaces) == 1 {
			ns = namespaces[0]
		}
		if s.bm25Enabled {
			results, err = search.HybridSearch(r.Context(), s.pointsClient, s.collection, question, vector, 5, ns)
		} else {
			results, err = search.SearchQdrant(r.Context(), s.pointsClient, s.collection, vector, 5, ns)
		}
		if err != nil {
			http.Error(w, fmt.Sprintf("search error: %v", err), http.StatusBadGateway)
			audit.Log(audit.Entry{
				Action:    "chat",
				User:      user,
				Query:     question,
				Namespace: requestedNamespace,
				LatencyMS: time.Since(start).Milliseconds(),
				Status:    http.StatusBadGateway,
			})
			return
		}
		for i := range results {
			results[i].Payload["source_namespace"] = ns
		}
	} else {
		var failedNS []string
		if s.bm25Enabled {
			results, failedNS, err = search.FanOutHybridSearch(r.Context(), s.pointsClient, s.collection, question, vector, namespaces, 5, s.globalSearchTimeout)
		} else {
			results, failedNS, err = search.FanOutSearch(r.Context(), s.pointsClient, s.collection, vector, namespaces, 5, s.globalSearchTimeout)
		}
		if err != nil {
			http.Error(w, fmt.Sprintf("search error: %v", err), http.StatusBadGateway)
			audit.Log(audit.Entry{
				Action:    "chat",
				User:      user,
				Query:     question,
				Namespace: requestedNamespace,
				LatencyMS: time.Since(start).Milliseconds(),
				Status:    http.StatusBadGateway,
			})
			return
		}
		if len(failedNS) > 0 {
			log.Printf("[chat] fan-out partial failure: %d/%d namespaces errored",
				len(failedNS), len(namespaces))
		}
	}

	// Graph-RAG expansion — single namespace only; silently skipped on errors.
	if s.graphCfg.Enabled && len(namespaces) <= 1 {
		ns := ""
		if len(namespaces) == 1 {
			ns = namespaces[0]
		}
		results = s.graphExpandResults(r.Context(), results, question, vector, ns, 5)
	}

	// Agentic multi-hop RAG — only for single-namespace requests (not global fan-out).
	// The searchFn is wrapped to apply graph expansion on each follow-up search so that
	// the agentic loop also benefits from structurally adjacent context.
	agenticHops := 0
	if s.agenticCfg.Enabled && len(namespaces) <= 1 {
		ns := ""
		if len(namespaces) == 1 {
			ns = namespaces[0]
		}
		searchFn := func(ctx context.Context, query string, vec []float32, limit uint64, searchNS string) ([]search.Result, error) {
			var res []search.Result
			var err error
			if s.bm25Enabled {
				res, err = search.HybridSearch(ctx, s.pointsClient, s.collection, query, vec, limit, searchNS)
			} else {
				res, err = search.SearchQdrant(ctx, s.pointsClient, s.collection, vec, limit, searchNS)
			}
			if err != nil {
				return nil, err
			}
			// Apply graph expansion to follow-up hop results as well.
			return s.graphExpandResults(ctx, res, query, vec, searchNS, int(limit)), nil
		}
		agResults, totalHops, agErr := rag.RunAgenticLoop(
			r.Context(), s.agenticCfg, searchFn, s.embedder.Embed, audit.Log, llm.CallGeminiStructured,
			question, ns, results, s.apiKey,
		)
		if agErr == nil {
			results = agResults
			agenticHops = totalHops
		}
	}

	contextStr := rag.BuildContext(results)

	var finalPrompt string
	if isGlobal {
		finalPrompt = fmt.Sprintf("Answer the question using the consolidated context below. "+
			"Each context block is tagged with [Source: namespace/path]. "+
			"When referencing information, cite the source namespace and file path. "+
			"If information from multiple namespaces is relevant, synthesize across sources "+
			"and note which namespace each fact comes from.\n\nContext:\n%s\n\nQuestion: %s", contextStr, question)
	} else {
		finalPrompt = fmt.Sprintf("Answer the question using the consolidated context.\n\nContext:\n%s\n\nQuestion: %s", contextStr, question)
	}
	var eval string

	if req.Stream && s.streamEnabled {
		// Phase 37: true token streaming — Gemini tokens piped directly to the client.
		streamErr := rag.StreamLLMResponse(w, req.Model, func(onChunk func(string) error) error {
			return llm.CallGeminiStream(r.Context(), finalPrompt, s.apiKey, onChunk)
		})
		if streamErr != nil {
			log.Printf("[chat] stream error: %v", streamErr)
			audit.Log(audit.Entry{
				Action:    "chat",
				User:      user,
				Query:     question,
				Namespace: requestedNamespace,
				Results:   len(results),
				LatencyMS: time.Since(start).Milliseconds(),
				Status:    http.StatusBadGateway,
				Metadata:  map[string]interface{}{"stream_error": streamErr.Error()},
			})
			return
		}
	} else {
		var llmErr error
		eval, llmErr = llm.CallGemini(r.Context(), finalPrompt, s.apiKey)
		if llmErr != nil {
			http.Error(w, fmt.Sprintf("LLM error: %v", llmErr), http.StatusBadGateway)
			audit.Log(audit.Entry{
				Action:    "chat",
				User:      user,
				Query:     question,
				Namespace: requestedNamespace,
				Results:   len(results),
				LatencyMS: time.Since(start).Milliseconds(),
				Status:    http.StatusBadGateway,
			})
			return
		}

		if req.Stream {
			// Deprecated: fake streaming (EMDEX_STREAM_ENABLED=false).
			rag.StreamResponse(w, req.Model, eval) //nolint:staticcheck
		} else {
			s.writeJSON(w, http.StatusOK, openai.ChatResponse{
				ID:      "chatcmpl-rag",
				Choices: []openai.ChatChoice{{Message: openai.ChatMessage{Role: "assistant", Content: eval}}},
			})
		}
	}

	chatEntry := audit.Entry{
		Action:    "chat",
		User:      user,
		Query:     question,
		Namespace: requestedNamespace,
		Results:   len(results),
		LatencyMS: time.Since(start).Milliseconds(),
		Status:    http.StatusOK,
	}
	chatMeta := map[string]interface{}{}
	if isGlobal {
		chatMeta["namespaces_searched"] = namespaces
	}
	if agenticHops > 0 {
		chatMeta["agentic_hops"] = agenticHops
	}
	if len(chatMeta) > 0 {
		chatEntry.Metadata = chatMeta
	}
	audit.Log(chatEntry)
}
