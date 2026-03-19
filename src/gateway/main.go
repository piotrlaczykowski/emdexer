package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/piotrlaczykowski/emdexer/audit"
	"github.com/piotrlaczykowski/emdexer/auth"
	"github.com/piotrlaczykowski/emdexer/config"
	"github.com/piotrlaczykowski/emdexer/embed"
	"github.com/piotrlaczykowski/emdexer/llm"
	"github.com/piotrlaczykowski/emdexer/middleware"
	"github.com/piotrlaczykowski/emdexer/openai"
	"github.com/piotrlaczykowski/emdexer/rag"
	"github.com/piotrlaczykowski/emdexer/registry"
	"github.com/piotrlaczykowski/emdexer/search"
	"github.com/piotrlaczykowski/emdexer/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

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

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
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

	vector, err := s.embedder.Embed(query)
	if err != nil {
		http.Error(w, fmt.Sprintf("embedding error: %v", err), http.StatusInternalServerError)
		return
	}

	namespaces := search.ResolveNamespaces(requestedNamespace, allowedNamespaces, s.knownNamespaces())

	var results []search.Result
	if len(namespaces) <= 1 {
		// Single namespace — existing fast path.
		ns := ""
		if len(namespaces) == 1 {
			ns = namespaces[0]
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		results, err = search.SearchQdrant(ctx, s.pointsClient, s.collection, vector, 10, ns)
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
		results, err = search.FanOutSearch(r.Context(), s.pointsClient, s.collection, vector, namespaces, 10, s.globalSearchTimeout)
		if err != nil {
			http.Error(w, fmt.Sprintf("search error: %v", err), http.StatusInternalServerError)
			return
		}
	}

	resp := map[string]interface{}{
		"query":   query,
		"results": results,
	}
	if isGlobal {
		resp["namespaces_searched"] = namespaces
	}
	s.writeJSON(w, http.StatusOK, resp)

	audit.Log(audit.Entry{
		Action:    "search",
		Query:     query,
		Namespace: requestedNamespace,
		Results:   len(results),
		LatencyMS: time.Since(start).Milliseconds(),
		Status:    http.StatusOK,
	})
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	allowedNamespaces, ok := auth.GetAllowedNamespaces(r)
	if !ok {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
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

	vector, err := s.embedder.Embed(question)
	if err != nil {
		http.Error(w, fmt.Sprintf("embedding error: %v", err), http.StatusBadGateway)
		return
	}

	namespaces := search.ResolveNamespaces(requestedNamespace, allowedNamespaces, s.knownNamespaces())

	var results []search.Result
	if len(namespaces) <= 1 {
		ns := ""
		if len(namespaces) == 1 {
			ns = namespaces[0]
		}
		results, err = search.SearchQdrant(r.Context(), s.pointsClient, s.collection, vector, 5, ns)
		if err != nil {
			http.Error(w, fmt.Sprintf("search error: %v", err), http.StatusBadGateway)
			return
		}
		for i := range results {
			results[i].Payload["source_namespace"] = ns
		}
	} else {
		results, err = search.FanOutSearch(r.Context(), s.pointsClient, s.collection, vector, namespaces, 5, s.globalSearchTimeout)
		if err != nil {
			http.Error(w, fmt.Sprintf("search error: %v", err), http.StatusBadGateway)
			return
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
	eval, err := llm.CallGemini(finalPrompt, s.apiKey)
	if err != nil {
		http.Error(w, fmt.Sprintf("LLM error: %v", err), http.StatusBadGateway)
		return
	}

	if req.Stream {
		rag.StreamResponse(w, req.Model, eval)
	} else {
		s.writeJSON(w, http.StatusOK, openai.ChatResponse{
			ID: "chatcmpl-rag",
			Choices: []openai.ChatChoice{{Message: openai.ChatMessage{Role: "assistant", Content: eval}}},
		})
	}
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

	conn, err := grpc.NewClient(qdrantHost, grpc.WithTransportCredentials(insecure.NewCredentials()))
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
	}

	// Initial topology refresh + background ticker.
	srv.refreshTopology()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		for range ticker.C {
			srv.refreshTopology()
		}
	}()

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", srv.handleHealth)
	mux.HandleFunc("/healthz/liveness", srv.handleLiveness)
	mux.HandleFunc("/healthz/readiness", srv.handleReadiness)
	mux.HandleFunc("/healthz/startup", srv.handleStartup)
	mux.HandleFunc("/nodes/register", middleware.Instrument("/nodes/register", srv.authCfg.Middleware(srv.handleRegisterNode)))
	mux.HandleFunc("/nodes/deregister/", middleware.Instrument("/nodes/deregister", srv.authCfg.Middleware(srv.handleDeregisterNode)))
	mux.HandleFunc("/nodes", middleware.Instrument("/nodes", srv.authCfg.Middleware(srv.handleListNodes)))
	mux.HandleFunc("/v1/search", middleware.Instrument("/v1/search", srv.authCfg.Middleware(srv.handleSearch)))
	mux.HandleFunc("/v1/chat/completions", middleware.Instrument("/v1/chat/completions", srv.authCfg.Middleware(srv.handleChatCompletions)))
	mux.HandleFunc("/v1/whoami", middleware.Instrument("/v1/whoami", srv.authCfg.Middleware(srv.handleWhoami)))

	addr := ":" + port
	log.Printf("Gateway starting on %s", addr)
	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("gateway server error: %v", err)
	}
}
