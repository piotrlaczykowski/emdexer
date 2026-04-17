package main

import (
	"context"
	"encoding/json"
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
	"github.com/piotrlaczykowski/emdexer/cache"
	"github.com/piotrlaczykowski/emdexer/config"
	"github.com/piotrlaczykowski/emdexer/embed"
	"github.com/piotrlaczykowski/emdexer/graph"
	"github.com/piotrlaczykowski/emdexer/llm"
	"github.com/piotrlaczykowski/emdexer/middleware"
	"github.com/piotrlaczykowski/emdexer/qdrantcreds"
	"github.com/piotrlaczykowski/emdexer/rag"
	"github.com/piotrlaczykowski/emdexer/registry"
	"github.com/piotrlaczykowski/emdexer/rerank"
	"github.com/piotrlaczykowski/emdexer/telemetry"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
)

type Server struct {
	reg          registry.NodeRegistry
	qdrantConn   *grpc.ClientConn
	pointsClient qdrant.PointsClient
	healthClient grpc_health_v1.HealthClient
	embedder     embed.EmbedProvider
	cache        cache.Cache // nil when EMDEX_CACHE_ENABLED != "true"
	collection   string
	apiKey       string
	authCfg      *auth.Config
	port         string
	startTime    time.Time

	// Namespace topology — refreshed every 30s from registry.
	topoMu     sync.RWMutex
	nsTopology map[string][]string // namespace -> []nodeID

	globalSearchTimeout time.Duration
	embedTimeout        time.Duration
	bm25Enabled         bool
	agenticCfg          rag.AgenticConfig

	// Graph-RAG (Phase 24)
	graphCfg       GraphConfig
	knowledgeGraph *graph.Graph

	// Reranking (Phase 30)
	reranker        rerank.Reranker
	rerankTopK      int
	rerankThreshold float64

	// Topology shutdown (Fix R1)
	stopTopology chan struct{}
	// topologyRefreshCh debounces burst registrations — signals a refresh without
	// spawning unbounded goroutines when many nodes register simultaneously.
	topologyRefreshCh chan struct{}

	// Indexing events (Phase 33)
	events *eventBus

	// Prometheus file_sd service discovery writer.
	sdWriter *SDWriter

	// True LLM token streaming (Phase 37)
	streamEnabled bool

	// otelShutdown is the teardown function returned by telemetry.InitTracer.
	otelShutdown func()

	// llmCallFn is the non-streaming LLM completion function. Defaults to llm.CallGemini;
	// overridden in tests to avoid real network calls.
	llmCallFn func(ctx context.Context, prompt, apiKey string) (string, error)

	// streamCallFn is the streaming LLM call function. Defaults to llm.CallGeminiStream;
	// overridden in tests to avoid real network calls.
	streamCallFn func(ctx context.Context, prompt, apiKey string, onChunk func(string) error) error
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON encode error: %v", err)
	}
}

func newServer() *Server {
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
	conn, err := grpc.NewClient(qdrantHost, qdrantDialOpt,
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(32*1024*1024),
		),
	)
	if err != nil {
		log.Fatalf("Failed to connect to Qdrant: %v", err)
	}

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
		os.Getenv("OPENAI_API_KEY"),
		os.Getenv("OPENAI_EMBED_MODEL"),
	)

	// Query vector LRU cache — avoids redundant embed calls for repeated queries.
	embedCacheSize := 1000
	if v := os.Getenv("EMDEX_EMBED_CACHE_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			embedCacheSize = n
		}
	}
	embedCacheTTL := 5 * time.Minute
	embedder = newCachedEmbedProvider(embedder, embedCacheSize, embedCacheTTL)
	if embedCacheSize > 0 {
		log.Printf("[gateway] query embed cache: enabled (maxEntries=%d ttl=%v)", embedCacheSize, embedCacheTTL)
	} else {
		log.Printf("[gateway] query embed cache: disabled (EMDEX_EMBED_CACHE_SIZE=0)")
	}

	var responseCache cache.Cache
	if c, err := cache.NewFromEnv(); err != nil {
		log.Printf("[gateway] cache init error: %v — continuing without cache", err)
	} else if c != nil {
		log.Printf("[gateway] cache enabled backend=%s", os.Getenv("EMDEX_CACHE_BACKEND"))
		responseCache = c
	}

	globalSearchTimeout := 500 * time.Millisecond
	if t := os.Getenv("EMDEX_GLOBAL_SEARCH_TIMEOUT"); t != "" {
		if ms, err := strconv.Atoi(t); err == nil && ms > 0 {
			globalSearchTimeout = time.Duration(ms) * time.Millisecond
		}
	}

	embedTimeout := 30 * time.Second
	if t := os.Getenv("EMDEX_EMBED_TIMEOUT"); t != "" {
		if ms, err := strconv.Atoi(t); err == nil && ms > 0 {
			embedTimeout = time.Duration(ms) * time.Millisecond
		}
	}
	log.Printf("[gateway] embed timeout: %v, search fan-out timeout: %v", embedTimeout, globalSearchTimeout)

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
		cache:               responseCache,
		collection:          collection,
		apiKey:              apiKey,
		authCfg:             &auth.Config{AuthKey: authKey, APIKeys: apiKeys, OIDC: oidcVerifier, GroupACL: groupACL},
		port:                port,
		startTime:           time.Now(),
		nsTopology:          make(map[string][]string),
		globalSearchTimeout: globalSearchTimeout,
		embedTimeout:        embedTimeout,
		bm25Enabled:         bm25Enabled,
		agenticCfg:          agenticCfg,
		graphCfg:            graphCfg,
		knowledgeGraph:      graph.New(5 * time.Minute),
		reranker:            reranker,
		rerankTopK:          rerankTopK,
		rerankThreshold:     rerankThreshold,
		streamEnabled:       streamEnabled,
		otelShutdown:        otelShutdown,
	}

	ollamaURL := os.Getenv("EMDEX_OLLAMA_URL")
	ollamaModel := os.Getenv("EMDEX_OLLAMA_MODEL")

	srv.llmCallFn = llm.CallGemini
	srv.streamCallFn = llm.CallGeminiStream

	if ollamaURL != "" {
		capturedURL := ollamaURL
		capturedModel := ollamaModel
		srv.llmCallFn = func(ctx context.Context, prompt, _ string) (string, error) {
			return llm.CallOllama(ctx, prompt, capturedURL, capturedModel)
		}
		srv.streamCallFn = func(ctx context.Context, prompt, _ string, onChunk func(string) error) error {
			return llm.CallOllamaStream(ctx, prompt, capturedURL, capturedModel, onChunk)
		}
		log.Printf("[gateway] LLM provider: ollama url=%s model=%s", logSafe(ollamaURL), logSafe(ollamaModel))
	} else {
		log.Printf("[gateway] LLM provider: gemini")
	}

	srv.stopTopology = make(chan struct{})
	srv.topologyRefreshCh = make(chan struct{}, 1)
	srv.events = newEventBus()

	sdPath := os.Getenv("EMDEX_SD_FILE")
	sdHostOverride := os.Getenv("EMDEX_SD_HOST_OVERRIDE")
	srv.sdWriter = NewSDWriter(sdPath, sdHostOverride)
	if sdPath != "" {
		log.Printf("[gateway] Prometheus SD file: %s", sdPath)
		if sdHostOverride != "" {
			log.Printf("[gateway] Prometheus SD host override: %s", sdHostOverride)
		}
	} else {
		log.Printf("[gateway] Prometheus SD file: disabled (set EMDEX_SD_FILE to enable)")
	}

	return srv
}

func (s *Server) Run() {
	// Initial topology refresh + background ticker.
	s.refreshTopology()
	s.startTopologyLoop()
	go s.prewarmGraphs()

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
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/healthz/liveness", s.handleLiveness)
	mux.HandleFunc("/healthz/readiness", s.handleReadiness)
	mux.HandleFunc("/healthz/startup", s.handleStartup)
	mux.HandleFunc("/healthz/qdrant", s.handleQdrantHealth)
	mux.HandleFunc("/nodes/register", middleware.Instrument("/nodes/register", s.authCfg.Middleware(s.handleRegisterNode)))
	mux.HandleFunc("/nodes/deregister/", middleware.Instrument("/nodes/deregister", s.authCfg.Middleware(s.handleDeregisterNode)))
	mux.HandleFunc("/nodes", middleware.Instrument("/nodes", s.authCfg.Middleware(s.handleListNodes)))
	mux.HandleFunc("/v1/search", middleware.Instrument("/v1/search", s.authCfg.Middleware(s.handleSearch)))
	mux.HandleFunc("/v1/search/graph", middleware.Instrument("/v1/search/graph", s.authCfg.Middleware(s.handleGraphSearch)))
	mux.HandleFunc("/v1/chat/completions", middleware.Instrument("/v1/chat/completions", s.authCfg.Middleware(s.handleChatCompletions)))
	mux.HandleFunc("/v1/whoami", middleware.Instrument("/v1/whoami", s.authCfg.Middleware(s.handleWhoami)))
	mux.HandleFunc("/v1/events/indexing", middleware.Instrument("/v1/events/indexing", s.authCfg.Middleware(s.handleIndexingEvents)))
	mux.HandleFunc("/v1/eval", middleware.Instrument("/v1/eval", s.authCfg.Middleware(s.handleEval)))
	mux.HandleFunc("/v1/nodes/", middleware.Instrument("/v1/nodes/", s.authCfg.Middleware(s.handleNodeIndexed)))

	addr := ":" + s.port
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
	close(s.stopTopology)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("[gateway] HTTP shutdown error: %v", err)
	}
	if err := metricsServer.Shutdown(ctx); err != nil {
		log.Printf("[gateway] metrics server shutdown error: %v", err)
	}

	if s.cache != nil {
		if err := s.cache.Close(); err != nil {
			log.Printf("[gateway] cache close error: %v", err)
		}
	}

	audit.Shutdown()
	s.otelShutdown()
	log.Printf("[gateway] Shutdown complete")
}

// logSafe strips ASCII control characters (including newlines) from s to
// prevent log injection when user-supplied values appear in log entries.
func logSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}
