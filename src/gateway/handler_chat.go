package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/piotrlaczykowski/emdexer/audit"
	"github.com/piotrlaczykowski/emdexer/auth"
	"github.com/piotrlaczykowski/emdexer/cache"
	"github.com/piotrlaczykowski/emdexer/llm"
	"github.com/piotrlaczykowski/emdexer/openai"
	"github.com/piotrlaczykowski/emdexer/rag"
	"github.com/piotrlaczykowski/emdexer/search"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
)

// isAuthError returns true when err looks like an LLM authentication failure
// (403, 401, PERMISSION_DENIED, API_KEY_INVALID). Used to distinguish
// misconfiguration (→ 503) from transient upstream failures (→ 502).
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "403") ||
		strings.Contains(s, "401") ||
		strings.Contains(s, "PERMISSION_DENIED") ||
		strings.Contains(s, "API_KEY_INVALID")
}

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

	var question, rawQuestion string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			rawQuestion = req.Messages[i].Content
			question = logSafe(rawQuestion)
			break
		}
	}
	if question == "" {
		http.Error(w, "Bad request: no user message found", http.StatusBadRequest)
		return
	}

	var (
		cacheKey      string
		cacheEligible = s.cache != nil
	)
	if cacheEligible {
		gen := s.cache.GetGeneration(r.Context(), requestedNamespace)
		cacheKey = cache.BuildKey(requestedNamespace, gen, req.Model, rawQuestion)
		if cached, ok := s.cache.Get(r.Context(), cacheKey); ok {
			cacheHits.Inc()
			w.Header().Set("X-Emdexer-Cache", "hit")
			span.SetAttributes(attribute.Bool("cache.hit", true))
			if req.Stream {
				rag.StreamResponse(w, req.Model, cached.Answer)
			} else {
				s.writeJSON(w, http.StatusOK, openai.ChatResponse{
					ID: "chatcmpl-cache",
					Choices: []openai.ChatChoice{{
						Message: openai.ChatMessage{Role: "assistant", Content: cached.Answer},
					}},
				})
			}
			audit.Log(audit.Entry{
				Action:    "chat",
				User:      user,
				Query:     question,
				Namespace: requestedNamespace,
				Results:   0,
				LatencyMS: time.Since(start).Milliseconds(),
				Status:    http.StatusOK,
				Metadata:  map[string]interface{}{"cache": "hit"},
			})
			return
		}
		cacheMisses.Inc()
		w.Header().Set("X-Emdexer-Cache", "miss")
		span.SetAttributes(attribute.Bool("cache.hit", false))
	} else {
		w.Header().Set("X-Emdexer-Cache", "disabled")
	}

	storeIfOK := func(answer string) {
		if !cacheEligible || answer == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.cache.Set(ctx, cacheKey, &cache.CachedResponse{
			Answer:    answer,
			Model:     req.Model,
			Namespace: requestedNamespace,
			CachedAt:  time.Now(),
		}, 0)
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
		} else if isAuthError(agErr) {
			log.Printf("[agentic] WARN: LLM unavailable for hop assessment — returning accumulated results")
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
		var ttftOnce sync.Once
		streamStart := time.Now()
		callStream := s.streamCallFn
		if callStream == nil {
			callStream = llm.CallGeminiStream
		}
		var streamBuf strings.Builder
		streamErr := rag.StreamLLMResponse(w, req.Model, func(onChunk func(string) error) error {
			return callStream(r.Context(), finalPrompt, s.apiKey, func(chunk string) error {
				streamBuf.WriteString(chunk)
				ttftOnce.Do(func() {
					chatStreamTTFT.Observe(float64(time.Since(streamStart).Milliseconds()))
				})
				chatStreamChunksTotal.Inc()
				return onChunk(chunk)
			})
		})
		if streamErr == nil {
			storeIfOK(streamBuf.String())
		}
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
		callLLM := s.llmCallFn
		if callLLM == nil {
			callLLM = llm.CallGemini
		}
		eval, llmErr = callLLM(r.Context(), finalPrompt, s.apiKey)
		if llmErr != nil {
			if isAuthError(llmErr) {
				// LLM is misconfigured — return top retrieved context as best-effort answer.
				if len(results) > 0 {
					path, _ := results[0].Payload["path"].(string)
					text, _ := results[0].Payload["text"].(string)
					eval = fmt.Sprintf("[LLM unavailable — showing top retrieved context]\n\n%s\n\n(source: %s)", text, path)
					// Fall through to write the response below.
				} else {
					http.Error(w, "LLM unavailable: invalid or missing API key", http.StatusServiceUnavailable)
					audit.Log(audit.Entry{
						Action:    "chat",
						User:      user,
						Query:     question,
						Namespace: requestedNamespace,
						LatencyMS: time.Since(start).Milliseconds(),
						Status:    http.StatusServiceUnavailable,
					})
					return
				}
			} else {
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
		}

		if req.Stream {
			// Deprecated: fake streaming (EMDEX_STREAM_ENABLED=false).
			rag.StreamResponse(w, req.Model, eval) //nolint:staticcheck
		} else {
			s.writeJSON(w, http.StatusOK, openai.ChatResponse{
				ID:      "chatcmpl-rag",
				Choices: []openai.ChatChoice{{Message: openai.ChatMessage{Role: "assistant", Content: eval}}},
			})
			storeIfOK(eval)
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
