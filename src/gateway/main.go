package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/piotrlaczykowski/emdexer/audit"
	"github.com/piotrlaczykowski/emdexer/auth"
	"github.com/piotrlaczykowski/emdexer/config"
	"github.com/piotrlaczykowski/emdexer/embed"
	"github.com/piotrlaczykowski/emdexer/graph"
	"github.com/piotrlaczykowski/emdexer/llm"
	"github.com/piotrlaczykowski/emdexer/middleware"
	"github.com/piotrlaczykowski/emdexer/openai"
	"github.com/piotrlaczykowski/emdexer/qdrantcreds"
	"github.com/piotrlaczykowski/emdexer/rag"
	"github.com/piotrlaczykowski/emdexer/registry"
	"github.com/piotrlaczykowski/emdexer/rerank"
	"github.com/piotrlaczykowski/emdexer/search"
	"github.com/piotrlaczykowski/emdexer/telemetry"
	"github.com/piotrlaczykowski/emdexer/version"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

var searchEmptyResults = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_search_empty_results_total",
	Help: "Number of search requests that returned zero results",
}, []string{"namespace", "mode"})

var topologyNamespacesKnown = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "emdexer_gateway_topology_namespaces_known",
	Help: "Number of namespaces currently known from the node registry",
})

var topologyNodesKnown = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "emdexer_gateway_topology_nodes_known",
	Help: "Number of nodes currently known from the node registry",
})

type Server struct {
	reg          registry.NodeRegistry
	qdrantConn   *grpc.ClientConn
	pointsClient qdrant.PointsClient
	healthClient grpc_health_v1.HealthClient
	embedder     embed.EmbedProvider
	collection   string
	apiKey       string
	authCfg      *auth.Config
	port         string
	startTime    time.Time

	// Namespace topology — refreshed every 30s from registry.
	topoMu     sync.RWMutex
	nsTopology map[string][]string // namespace -> []nodeID

	globalSearchTimeout time.Duration
	bm25Enabled         bool
	agenticCfg          rag.AgenticConfig

	// Graph-RAG (Phase 24)
	graphCfg       GraphConfig
	knowledgeGraph *graph.Graph

	// Reranking (Phase 30)
	reranker   rerank.Reranker
	rerankTopK int
	rerankThreshold float64

	// Topology shutdown (Fix R1)
	stopTopology chan struct{}

	// Indexing events (Phase 33)
	events *eventBus

	// True LLM token streaming (Phase 37)
	streamEnabled bool
}

// GraphConfig holds feature-flag settings for the knowledge-graph expansion.
type GraphConfig struct {
	Enabled bool
	Depth   int // BFS depth: 1–3
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON encode error: %v", err)
	}
}

// ============================================================
// Namespace Topology
// ============================================================

// refreshTopology rebuilds the in-memory namespace->nodeIDs map from the registry.
func (s *Server) refreshTopology() {
	nodes, err := s.reg.List(context.Background())
	if err != nil {
		log.Printf("[topology] refresh failed: %v", err)
		return
	}
	topo := make(map[string][]string)
	for _, n := range nodes {
		for _, ns := range n.Namespaces {
			topo[ns] = append(topo[ns], n.ID)
		}
	}
	s.topoMu.Lock()
	s.nsTopology = topo
	s.topoMu.Unlock()
	topologyNamespacesKnown.Set(float64(len(topo)))
	topologyNodesKnown.Set(float64(len(nodes)))
	log.Printf("[topology] Refreshed: %d namespaces across %d nodes", len(topo), len(nodes))
}

// knownNamespaces returns all namespace strings from the topology map.
func (s *Server) knownNamespaces() []string {
	s.topoMu.RLock()
	defer s.topoMu.RUnlock()
	out := make([]string, 0, len(s.nsTopology))
	for ns := range s.nsTopology {
		out = append(out, ns)
	}
	return out
}

func (s *Server) handleRegisterNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var node registry.NodeInfo
	if err := json.NewDecoder(r.Body).Decode(&node); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	if node.ID == "" {
		node.ID = fmt.Sprintf("node-%d", time.Now().UnixNano())
	}
	if err := s.reg.Register(r.Context(), node); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	// Refresh topology immediately so new namespaces are discoverable.
	go s.refreshTopology()
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "registered", "id": node.ID})
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.reg.List(r.Context())
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, http.StatusOK, nodes)
}

func (s *Server) handleDeregisterNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/nodes/deregister/")
	id = strings.TrimSuffix(id, "/")

	if id == "" {
		http.Error(w, "Bad request: missing node id", http.StatusBadRequest)
		return
	}
	if err := s.reg.Deregister(r.Context(), id); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "deregistered", "id": id})
}

// ============================================================
// Graph-RAG helpers (Phase 24)
// ============================================================

// uniquePaths returns deduplicated file paths from the payload of a result set.
func uniquePaths(results []search.Result) []string {
	seen := make(map[string]bool, len(results))
	var paths []string
	for _, r := range results {
		if p, ok := r.Payload["path"].(string); ok && p != "" && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	return paths
}

// graphExpandResults augments results by finding structurally adjacent files in the
// knowledge graph and issuing a follow-up search restricted to those files.
// Neighbour results are merged using RRF with a 0.7 weight so direct matches
// always rank higher than graph-expanded ones.
// On any error the original results are returned unchanged.
func (s *Server) graphExpandResults(ctx context.Context, results []search.Result, query string, vector []float32, namespace string, limit int) []search.Result {
	if !s.graphCfg.Enabled || len(results) == 0 {
		return results
	}

	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.graph.expand")
	defer span.End()

	sourceFiles := uniquePaths(results)
	neighborSet := make(map[string]bool)
	for _, file := range sourceFiles {
		for _, nb := range s.knowledgeGraph.Neighbors(ctx, s.pointsClient, s.collection, namespace, file, s.graphCfg.Depth) {
			neighborSet[nb] = true
		}
	}
	// Remove files already in initial results to avoid re-fetching them.
	for _, f := range sourceFiles {
		delete(neighborSet, f)
	}
	if len(neighborSet) == 0 {
		return results
	}

	neighbors := make([]string, 0, len(neighborSet))
	for f := range neighborSet {
		neighbors = append(neighbors, f)
	}

	neighborResults, err := search.HybridSearchByPaths(ctx, s.pointsClient, s.collection, query, vector, uint64(limit), namespace, neighbors)
	if err != nil {
		log.Printf("[graph] neighbor search failed namespace=%q: %v", namespace, err)
		return results
	}
	if len(neighborResults) == 0 {
		return results
	}

	// Merge: primary (weight=1.0) and neighbour (weight=0.7).
	return search.MergeRRFWeighted(results, neighborResults, 0.7, limit)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	// Extract W3C Trace Context from incoming headers and create root span.
	ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.search")
	defer span.End()
	r = r.WithContext(ctx)

	start := time.Now()
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	allowedNamespaces, ok := auth.GetAllowedNamespaces(r)
	if !ok {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	requestedNamespace := strings.TrimSpace(r.URL.Query().Get("namespace"))
	if requestedNamespace == "" {
		requestedNamespace = "default"
	}

	// For global search (namespace=* or __global__), resolve to authorized namespaces.
	// For single namespace, validate against allowedNamespaces as before.
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

	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "missing ?q=", http.StatusBadRequest)
		return
	}

	vector, err := s.embedder.Embed(r.Context(), query)
	if err != nil {
		http.Error(w, fmt.Sprintf("embedding error: %v", err), http.StatusInternalServerError)
		return
	}

	namespaces := search.ResolveNamespaces(requestedNamespace, allowedNamespaces, s.knownNamespaces())

	var results []search.Result
	var fanoutFailedNS []string
	if len(namespaces) <= 1 {
		// Single namespace — fast path (hybrid or vector-only).
		ns := ""
		if len(namespaces) == 1 {
			ns = namespaces[0]
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if s.bm25Enabled {
			results, err = search.HybridSearch(ctx, s.pointsClient, s.collection, query, vector, 10, ns)
		} else {
			results, err = search.SearchQdrant(ctx, s.pointsClient, s.collection, vector, 10, ns)
		}
		if err != nil {
			http.Error(w, fmt.Sprintf("search error: %v", err), http.StatusInternalServerError)
			return
		}
		// Inject source_namespace for consistent buildContext behavior.
		for i := range results {
			results[i].Payload["source_namespace"] = ns
		}
	} else {
		// Multi-namespace fan-out with RRF merge.
		// Partial failures are surfaced in the response so clients can detect degraded results;
		// a complete failure returns 200 with empty results rather than a 504.
		if s.bm25Enabled {
			results, fanoutFailedNS, err = search.FanOutHybridSearch(r.Context(), s.pointsClient, s.collection, query, vector, namespaces, 10, s.globalSearchTimeout)
		} else {
			results, fanoutFailedNS, err = search.FanOutSearch(r.Context(), s.pointsClient, s.collection, vector, namespaces, 10, s.globalSearchTimeout)
		}
		if err != nil {
			http.Error(w, fmt.Sprintf("search error: %v", err), http.StatusInternalServerError)
			return
		}
		if len(fanoutFailedNS) > 0 {
			log.Printf("[search] fan-out partial failure: %d/%d namespaces errored: %v",
				len(fanoutFailedNS), len(namespaces), fanoutFailedNS)
		}
	}

	if len(results) == 0 {
		mode := "vector"
		if s.bm25Enabled {
			mode = "hybrid"
		}
		searchEmptyResults.WithLabelValues(requestedNamespace, mode).Inc()
	}

	// ── Phase 30: Late-interaction reranking ──────────────────────────────────
	// Apply only when results are available and a real Reranker is wired in.
	if _, isNoop := s.reranker.(rerank.NoOpReranker); !isNoop && len(results) > 0 {
		texts := make([]string, len(results))
		for i, r := range results {
			if t, ok := r.Payload["text"].(string); ok {
				texts[i] = t
			}
		}
		ranked, rerr := rerank.Rank(r.Context(), s.reranker, query, texts, s.rerankTopK, requestedNamespace)
		if rerr != nil {
			log.Printf("[rerank] error for namespace %q — skipping rerank: %v", requestedNamespace, rerr)
		} else {
			// Log rank delta for research audit.
			changed := 0
			for newPos, sc := range ranked {
				if sc.Index != newPos {
					changed++
				}
				score := sc.Score
				results[sc.Index].RerankScore = &score
			}
			log.Printf("[rerank] namespace=%q candidates=%d changed_rank=%d", requestedNamespace, len(ranked), changed)

			// Rebuild results in reranked order, applying threshold filter.
			reranked := make([]search.Result, 0, len(ranked))
			for _, sc := range ranked {
				if float64(sc.Score) >= s.rerankThreshold {
					reranked = append(reranked, results[sc.Index])
				}
			}
			if len(reranked) > 0 {
				results = reranked
			}
		}
	}
	// ─────────────────────────────────────────────────────────────────────────

	resp := map[string]interface{}{
		"query":   query,
		"results": results,
	}
	if isGlobal {
		resp["namespaces_searched"] = namespaces
		if len(fanoutFailedNS) > 0 {
			resp["partial_failures"] = fanoutFailedNS
		}
	}
	s.writeJSON(w, http.StatusOK, resp)

	auditEntry := audit.Entry{
		Action:    "search",
		Query:     query,
		Namespace: requestedNamespace,
		Results:   len(results),
		LatencyMS: time.Since(start).Milliseconds(),
		Status:    http.StatusOK,
	}
	if claims, ok := auth.GetUserClaims(r); ok {
		auditEntry.User = claims.Subject
	}
	if isGlobal {
		auditEntry.Metadata = map[string]interface{}{"namespaces_searched": namespaces}
	}
	audit.Log(auditEntry)
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
		user = claims.Subject
	}

	var req openai.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	requestedNamespace := strings.TrimSpace(r.Header.Get("X-Emdex-Namespace"))
	if requestedNamespace == "" {
		requestedNamespace = strings.TrimSpace(r.URL.Query().Get("namespace"))
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
			question = req.Messages[i].Content
			break
		}
	}
	if question == "" {
		http.Error(w, "Bad request: no user message found", http.StatusBadRequest)
		return
	}

	vector, err := s.embedder.Embed(r.Context(), question)
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
			log.Printf("[chat] fan-out partial failure: %d/%d namespaces errored: %v",
				len(failedNS), len(namespaces), failedNS)
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

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{
		"status":      "ok",
		"version":     version.Version,
		"collection":  s.collection,
	})
}

func (s *Server) handleLiveness(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "UP"})
}

func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := s.healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: ""})
	if err != nil || resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "DOWN", "reason": "qdrant_unreachable"})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "UP"})
}

func (s *Server) handleStartup(w http.ResponseWriter, r *http.Request) {
	if time.Since(s.startTime) < 5*time.Second {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "STARTING"})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "STARTED"})
}

// handleQdrantHealth checks the Qdrant cluster health via the /cluster REST endpoint.
// In single-node deployments (cluster disabled) the Raft check is skipped and the
// gRPC health probe result is returned instead.
func (s *Server) handleQdrantHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	qdrantHTTPAddr := os.Getenv("QDRANT_HTTP_HOST")
	if qdrantHTTPAddr == "" {
		// Derive HTTP host from gRPC host by replacing port 6334 → 6333.
		grpcHost := os.Getenv("QDRANT_HOST")
		if grpcHost == "" {
			grpcHost = "localhost:6334"
		}
		qdrantHTTPAddr = strings.Replace(grpcHost, ":6334", ":6333", 1)
	}

	cs, err := registry.CheckRaftCluster(ctx, qdrantHTTPAddr)
	if err != nil {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"status": "DOWN",
			"reason": err.Error(),
		})
		return
	}
	if !cs.RaftReady {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"status":      "DOWN",
			"reason":      "raft_not_ready",
			"cluster":     cs.Status,
			"node_count":  cs.NodeCount,
			"leader_id":   cs.LeaderID,
		})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":       "UP",
		"cluster":      cs.Status,
		"node_count":   cs.NodeCount,
		"leader_id":    cs.LeaderID,
		"commit_index": cs.CommitIndex,
	})
}

func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.GetUserClaims(r)
	if !ok {
		http.Error(w, "No identity", http.StatusForbidden)
		return
	}
	ns, _ := auth.GetAllowedNamespaces(r)
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"auth_type":  claims.AuthType,
		"subject":    claims.Subject,
		"email":      claims.Email,
		"groups":     claims.Groups,
		"namespaces": ns,
	})
}

func main() {
	cwd, _ := os.Getwd()
	config.LoadEnv(filepath.Join(cwd, ".env"))

	// OpenTelemetry — no-op when EMDEX_OTEL_ENDPOINT is unset.
	otelServiceName := os.Getenv("EMDEX_OTEL_SERVICE_NAME")
	if otelServiceName == "" {
		otelServiceName = "emdex-gateway"
	}
	otelShutdown, err := telemetry.InitTracer(otelServiceName, os.Getenv("EMDEX_OTEL_ENDPOINT"))
	if err != nil {
		log.Printf("[gateway] WARN: OpenTelemetry init failed: %v — tracing disabled", err)
		otelShutdown = func() {}
	} else if ep := os.Getenv("EMDEX_OTEL_ENDPOINT"); ep != "" {
		log.Printf("[gateway] OpenTelemetry tracing enabled: endpoint=%s service=%s", ep, otelServiceName)
	}

	apiKey := os.Getenv("GOOGLE_API_KEY")
	qdrantHost := os.Getenv("QDRANT_HOST")
	if qdrantHost == "" {
		qdrantHost = "localhost:6334"
	}

	port := os.Getenv("EMDEX_PORT")
	if port == "" {
		port = "7700"
	}

	collection := os.Getenv("EMDEX_QDRANT_COLLECTION")
	if collection == "" {
		collection = "emdexer_v1"
	}

	authKey := os.Getenv("EMDEX_AUTH_KEY")
	var apiKeys map[string][]string
	if keysJSON := os.Getenv("EMDEX_API_KEYS"); keysJSON != "" {
		if err := json.Unmarshal([]byte(keysJSON), &apiKeys); err != nil {
			log.Printf("Failed to parse EMDEX_API_KEYS: %v", err)
		}
	}

	// OIDC configuration (optional — enabled when OIDC_ISSUER is set)
	var oidcVerifier *auth.OIDCVerifier
	if issuer := os.Getenv("OIDC_ISSUER"); issuer != "" {
		clientID := os.Getenv("OIDC_CLIENT_ID")
		if clientID == "" {
			log.Fatalf("[auth] FATAL: OIDC_ISSUER is set but OIDC_CLIENT_ID is missing")
		}
		groupsClaim := os.Getenv("OIDC_GROUPS_CLAIM")
		if groupsClaim == "" {
			groupsClaim = "groups"
		}
		var oidcErr error
		oidcVerifier, oidcErr = auth.NewOIDCVerifier(context.Background(), auth.OIDCConfig{
			Issuer:      issuer,
			ClientID:    clientID,
			GroupsClaim: groupsClaim,
		})
		if oidcErr != nil {
			log.Fatalf("[auth] FATAL: OIDC configured but provider unreachable: %v", oidcErr)
		}
		log.Printf("[auth] OIDC enabled: issuer=%s client_id=%s groups_claim=%s", issuer, clientID, groupsClaim)
	}

	var groupACL *auth.GroupACL
	if aclJSON := os.Getenv("EMDEX_GROUP_ACL"); aclJSON != "" {
		var aclErr error
		groupACL, aclErr = auth.NewGroupACL(aclJSON)
		if aclErr != nil {
			log.Fatalf("[auth] FATAL: invalid EMDEX_GROUP_ACL: %v", aclErr)
		}
		log.Printf("[auth] Group ACL loaded: %d group mappings", len(groupACL.Mapping))
	}

	qdrantDialOpt, err := qdrantcreds.FromEnv()
	if err != nil {
		log.Fatalf("qdrant TLS config: %v", err)
	}
	conn, err := grpc.NewClient(qdrantHost, qdrantDialOpt)
	if err != nil {
		log.Fatalf("Failed to connect to Qdrant: %v", err)
	}
	defer func() { _ = conn.Close() }()

	registryFile := os.Getenv("EMDEX_REGISTRY_FILE")
	if registryFile == "" {
		registryFile = filepath.Join(cwd, "nodes.json")
	}

	reg := registry.NewRegistry(registryFile)

	embedder := embed.New(
		apiKey,
		os.Getenv("EMBED_PROVIDER"),
		os.Getenv("OLLAMA_HOST"),
		os.Getenv("OLLAMA_EMBED_MODEL"),
		os.Getenv("EMDEX_GEMINI_MODEL"),
	)

	globalSearchTimeout := 500 * time.Millisecond
	if t := os.Getenv("EMDEX_GLOBAL_SEARCH_TIMEOUT"); t != "" {
		if ms, err := strconv.Atoi(t); err == nil && ms > 0 {
			globalSearchTimeout = time.Duration(ms) * time.Millisecond
		}
	}

	// BM25 hybrid search is enabled by default; set EMDEX_BM25_ENABLED=false to use vector-only.
	bm25Enabled := os.Getenv("EMDEX_BM25_ENABLED") != "false"
	log.Printf("[gateway] hybrid search (BM25+vector): enabled=%v", bm25Enabled)

	// Agentic multi-hop RAG — enabled by default; set EMDEX_AGENTIC_ENABLED=false to disable.
	agenticEnabled := os.Getenv("EMDEX_AGENTIC_ENABLED") != "false"
	agenticMaxHops := 3
	if v := os.Getenv("EMDEX_MAX_HOPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			if n > 5 {
				n = 5
			}
			agenticMaxHops = n
		}
	}
	agenticThreshold := 0.7
	if v := os.Getenv("EMDEX_HOP_CONFIDENCE_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 1 {
			agenticThreshold = f
		}
	}
	agenticCfg := rag.AgenticConfig{
		Enabled:             agenticEnabled,
		MaxHops:             agenticMaxHops,
		ConfidenceThreshold: agenticThreshold,
	}
	log.Printf("[gateway] agentic RAG: enabled=%v max_hops=%d confidence_threshold=%.2f",
		agenticCfg.Enabled, agenticCfg.MaxHops, agenticCfg.ConfidenceThreshold)

	// Graph-RAG — enabled by default; set EMDEX_GRAPH_ENABLED=false to disable.
	graphEnabled := os.Getenv("EMDEX_GRAPH_ENABLED") != "false"
	graphDepth := 1
	if v := os.Getenv("EMDEX_GRAPH_DEPTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			if n > 3 {
				n = 3
			}
			graphDepth = n
		}
	}
	graphCfg := GraphConfig{Enabled: graphEnabled, Depth: graphDepth}
	log.Printf("[gateway] graph-RAG: enabled=%v depth=%d", graphCfg.Enabled, graphCfg.Depth)

	// True streaming — enabled by default; set EMDEX_STREAM_ENABLED=false to use deprecated fake streaming.
	streamEnabled := os.Getenv("EMDEX_STREAM_ENABLED") != "false"
	log.Printf("[gateway] true LLM streaming: enabled=%v", streamEnabled)

	// Reranking — disabled by default; set EMDEX_RERANK_ENABLED=true to enable.
	rerankEnabled := os.Getenv("EMDEX_RERANK_ENABLED") == "true"
	rerankURL := os.Getenv("EMDEX_RERANK_URL")
	rerankToken := os.Getenv("EMDEX_RERANK_TOKEN")
	if rerankURL == "" {
		rerankURL = "http://reranker:8005"
	}
	rerankTopK := 20
	if v := os.Getenv("EMDEX_RERANK_TOP_K"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			rerankTopK = n
		}
	}
	rerankThreshold := 0.0
	if v := os.Getenv("EMDEX_RERANK_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			rerankThreshold = f
		}
	}
	var reranker rerank.Reranker = rerank.NoOpReranker{}
	if rerankEnabled {
		reranker = rerank.NewSidecarReranker(rerankURL, rerankToken)
		log.Printf("[gateway] reranking: enabled=true url=%s top_k=%d threshold=%.3f", rerankURL, rerankTopK, rerankThreshold)
	} else {
		log.Printf("[gateway] reranking: enabled=false")
	}

	srv := &Server{
		reg:                 reg,
		qdrantConn:          conn,
		pointsClient:        qdrant.NewPointsClient(conn),
		healthClient:        grpc_health_v1.NewHealthClient(conn),
		embedder:            embedder,
		collection:          collection,
		apiKey:              apiKey,
		authCfg:             &auth.Config{AuthKey: authKey, APIKeys: apiKeys, OIDC: oidcVerifier, GroupACL: groupACL},
		port:                port,
		startTime:           time.Now(),
		nsTopology:          make(map[string][]string),
		globalSearchTimeout: globalSearchTimeout,
		bm25Enabled:         bm25Enabled,
		agenticCfg:          agenticCfg,
		graphCfg:            graphCfg,
		knowledgeGraph:      graph.New(5 * time.Minute),
		reranker:            reranker,
		rerankTopK:          rerankTopK,
		rerankThreshold:     rerankThreshold,
		streamEnabled:       streamEnabled,
	}

	// Initial topology refresh + background ticker.
	srv.stopTopology = make(chan struct{})
	srv.events = newEventBus()
	srv.refreshTopology()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				srv.refreshTopology()
			case <-srv.stopTopology:
				return
			}
		}
	}()

	// Metrics server on internal port 9090 (Fix R5).
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsServer := &http.Server{
		Addr:        ":9090",
		Handler:     metricsMux,
		ReadTimeout: 5 * time.Second,
		IdleTimeout: 60 * time.Second,
	}
	go func() {
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[gateway] metrics server error: %v", err)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", srv.handleHealth)
	mux.HandleFunc("/healthz/liveness", srv.handleLiveness)
	mux.HandleFunc("/healthz/readiness", srv.handleReadiness)
	mux.HandleFunc("/healthz/startup", srv.handleStartup)
	mux.HandleFunc("/healthz/qdrant", srv.handleQdrantHealth)
	mux.HandleFunc("/nodes/register", middleware.Instrument("/nodes/register", srv.authCfg.Middleware(srv.handleRegisterNode)))
	mux.HandleFunc("/nodes/deregister/", middleware.Instrument("/nodes/deregister", srv.authCfg.Middleware(srv.handleDeregisterNode)))
	mux.HandleFunc("/nodes", middleware.Instrument("/nodes", srv.authCfg.Middleware(srv.handleListNodes)))
	mux.HandleFunc("/v1/search", middleware.Instrument("/v1/search", srv.authCfg.Middleware(srv.handleSearch)))
	mux.HandleFunc("/v1/chat/completions", middleware.Instrument("/v1/chat/completions", srv.authCfg.Middleware(srv.handleChatCompletions)))
	mux.HandleFunc("/v1/whoami", middleware.Instrument("/v1/whoami", srv.authCfg.Middleware(srv.handleWhoami)))
	mux.HandleFunc("/v1/events/indexing", middleware.Instrument("/v1/events/indexing", srv.authCfg.Middleware(srv.handleIndexingEvents)))
	mux.HandleFunc("/v1/eval", middleware.Instrument("/v1/eval", srv.authCfg.Middleware(srv.handleEval)))
	mux.HandleFunc("/v1/nodes/", middleware.Instrument("/v1/nodes/", srv.authCfg.Middleware(srv.handleNodeIndexed)))

	addr := ":" + port
	log.Printf("Gateway starting on %s", addr)
	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("gateway server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit
	log.Printf("[gateway] Shutting down...")

	// Stop topology background goroutine.
	close(srv.stopTopology)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("[gateway] HTTP shutdown error: %v", err)
	}
	if err := metricsServer.Shutdown(ctx); err != nil {
		log.Printf("[gateway] metrics server shutdown error: %v", err)
	}

	audit.Shutdown()
	otelShutdown()
	log.Printf("[gateway] Shutdown complete")
}
