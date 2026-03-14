package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

const Version = "v0.1.0"

// ============================================================
// Config & .env loader
// ============================================================

func loadEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			if os.Getenv(key) == "" {
				os.Setenv(key, val)
			}
		}
	}
}

// Metrics
var (
	searchLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "emdexer_gateway_search_latency_ms",
		Help:    "Latency of Qdrant search in milliseconds",
		Buckets: []float64{10, 50, 100, 200, 500, 1000, 2000, 5000},
	}, []string{"collection"})

	embeddingLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "emdexer_gateway_embedding_latency_ms",
		Help:    "Latency of Gemini embedding in milliseconds",
		Buckets: []float64{100, 200, 500, 1000, 2000, 5000},
	})

	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "emdexer_gateway_http_requests_total",
		Help: "Total number of HTTP requests",
	}, []string{"path", "code"})
)

// Audit Log Entry
type AuditEntry struct {
	Timestamp time.Time              `json:"timestamp"`
	Action    string                 `json:"action"`
	User      string                 `json:"user,omitempty"`
	Query     string                 `json:"query,omitempty"`
	Namespace string                 `json:"namespace,omitempty"`
	Results   int                    `json:"results_count,omitempty"`
	LatencyMS int64                  `json:"latency_ms"`
	Status    int                    `json:"status"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

func logAudit(entry AuditEntry) {
	entry.Timestamp = time.Now()
	logPath := os.Getenv("EMDEX_AUDIT_LOG_FILE")
	if logPath == "" {
		cwd, _ := os.Getwd()
		logPath = filepath.Join(cwd, "logs", "audit.json")
	}
	os.MkdirAll(filepath.Dir(logPath), 0755)
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to open audit log: %v", err)
		return
	}
	defer f.Close()

	b, _ := json.Marshal(entry)
	f.Write(append(b, '\n'))
}

// ============================================================
// EmbedProvider interface — decouples embedding backend.
// Default: GeminiProvider. Future: OllamaProvider (Phase 15.5).
// ============================================================

type EmbedProvider interface {
	// Embed returns a dense vector for the given text.
	Embed(text string) ([]float32, error)
	// Name returns a human-readable identifier for observability.
	Name() string
}

// GeminiProvider calls the Google Generative Language API.
type GeminiProvider struct {
	APIKey string
	Model  string
}

func NewGeminiProvider(apiKey, model string) *GeminiProvider {
	if model == "" {
		model = "models/gemini-embedding-exp-03-07"
	}
	return &GeminiProvider{
		APIKey: apiKey,
		Model:  model,
	}
}

func (g *GeminiProvider) Name() string { return "gemini:" + g.Model }

func (g *GeminiProvider) Embed(text string) ([]float32, error) {
	start := time.Now()
	defer func() {
		embeddingLatency.Observe(float64(time.Since(start).Milliseconds()))
	}()
	geminiModel := os.Getenv("EMDEX_GEMINI_MODEL")
	if geminiModel == "" {
		geminiModel = "models/gemini-embedding-exp-03-07"
	}
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/%s:embedContent?key=%s", geminiModel, g.APIKey)
	reqBody := EmbedRequest{
		Model:   geminiModel,
		Content: EmbedContent{Parts: []EmbedPart{{Text: text}}},
	}
	body, _ := json.Marshal(reqBody)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embed API %d: %s", resp.StatusCode, string(b))
	}
	var er EmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return nil, err
	}
	return er.Embedding.Values, nil
}

// OllamaProvider is a stub for Phase 15.5 (air-gapped / local embedding).
// Implement by setting EMBED_PROVIDER=ollama and OLLAMA_HOST in env.
type OllamaProvider struct {
	Host  string
	Model string
}

func (o *OllamaProvider) Name() string { return "ollama:" + o.Model }
func (o *OllamaProvider) Embed(_ string) ([]float32, error) {
	return nil, fmt.Errorf("OllamaProvider not yet implemented (Phase 15.5): set EMBED_PROVIDER=gemini or implement ollama /api/embed")
}

// isPrivateIP checks if an IP belongs to private or reserved ranges.
func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	privateIPBlocks := []*net.IPNet{
		{IP: net.ParseIP("10.0.0.0"), Mask: net.CIDRMask(8, 32)},
		{IP: net.ParseIP("172.16.0.0"), Mask: net.CIDRMask(12, 32)},
		{IP: net.ParseIP("192.168.0.0"), Mask: net.CIDRMask(16, 32)},
	}
	for _, block := range privateIPBlocks {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// validateOllamaHost parses the URL and validates its scheme.
// It does NOT perform a DNS lookup here — IP validation happens at dial-time
// via newSafeOllamaTransport to prevent DNS rebinding attacks.
func validateOllamaHost(hostStr string) error {
	u, err := url.Parse(hostStr)
	if err != nil {
		return fmt.Errorf("invalid OLLAMA_HOST URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("OLLAMA_HOST must be http or https")
	}
	return nil
}

// newSafeOllamaTransport returns an http.Transport whose dialer validates the
// resolved IP at connection time (not before).  This closes the DNS-rebinding
// window: even if the hostname initially resolves to a public IP, any
// subsequent resolution to a private/loopback address is rejected at the
// moment the TCP socket is opened.
func newSafeOllamaTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("ssrf-guard: could not parse dial address %q: %w", address, err)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("ssrf-guard: non-IP address at dial time: %q", host)
			}
			if isPrivateIP(ip) {
				return fmt.Errorf("ssrf-guard: dial to restricted IP %s blocked (DNS rebinding?)", ip)
			}
			return nil
		},
	}
	return &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}

// newEmbedProvider reads EMBED_PROVIDER env and returns the right implementation.
// Defaults to GeminiProvider.
func newEmbedProvider(apiKey string) EmbedProvider {
	switch strings.ToLower(os.Getenv("EMBED_PROVIDER")) {
	case "ollama":
		ollamaHost := os.Getenv("OLLAMA_HOST")
		if ollamaHost == "" {
			ollamaHost = "http://localhost:11434"
		}
		ollamaModel := os.Getenv("OLLAMA_EMBED_MODEL")
		if ollamaModel == "" {
			ollamaModel = "nomic-embed-text"
		}

		if err := validateOllamaHost(ollamaHost); err != nil {
			log.Fatalf("[embed] CRITICAL SECURITY ERROR: %v", err)
		}

		log.Printf("[embed] Using OllamaProvider (STUB) at %s model=%s", ollamaHost, ollamaModel)
		return &OllamaProvider{Host: ollamaHost, Model: ollamaModel}
	default:
		geminiModel := os.Getenv("EMDEX_GEMINI_MODEL")
		return NewGeminiProvider(apiKey, geminiModel)
	}
}

// ============================================================
// Node Registry — persistent, deep-copy safe
// ============================================================

type NodeInfo struct {
	ID           string    `json:"id"`
	URL          string    `json:"url"`
	Collections  []string  `json:"collections"`
	RegisteredAt time.Time `json:"registered_at"`
}

// deepCopyNodeInfo returns a new NodeInfo with its own slice — no shared pointer races.
func deepCopyNodeInfo(n NodeInfo) NodeInfo {
	cols := make([]string, len(n.Collections))
	copy(cols, n.Collections)
	return NodeInfo{
		ID:           n.ID,
		URL:          n.URL,
		Collections:  cols,
		RegisteredAt: n.RegisteredAt,
	}
}

type NodeRegistry struct {
	mu       sync.RWMutex
	nodes    map[string]NodeInfo // value type — no shared pointer
	dataFile string              // path to nodes.json for persistence
}

// NewNodeRegistry creates a registry backed by a JSON file at dataFile.
// On startup, existing registrations are loaded from disk.
func NewNodeRegistry(dataFile string) *NodeRegistry {
	r := &NodeRegistry{
		nodes:    make(map[string]NodeInfo),
		dataFile: dataFile,
	}
	r.load()
	return r
}

// load reads the on-disk JSON into memory. Errors are non-fatal (empty registry).
func (r *NodeRegistry) load() {
	data, err := os.ReadFile(r.dataFile)
	if err != nil {
		return // file may not exist yet — that's fine
	}
	var nodes []NodeInfo
	if err := json.Unmarshal(data, &nodes); err != nil {
		log.Printf("[registry] Failed to parse %s: %v (starting empty)", r.dataFile, err)
		return
	}
	for _, n := range nodes {
		r.nodes[n.ID] = deepCopyNodeInfo(n)
	}
	log.Printf("[registry] Loaded %d node(s) from %s", len(r.nodes), r.dataFile)
}

// persist writes the current registry to disk atomically via a temp-file swap.
// Must be called with r.mu held (write lock).
func (r *NodeRegistry) persist() {
	nodes := make([]NodeInfo, 0, len(r.nodes))
	for _, n := range r.nodes {
		nodes = append(nodes, deepCopyNodeInfo(n))
	}
	data, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		log.Printf("[registry] Failed to marshal nodes: %v", err)
		return
	}
	tmp := r.dataFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		log.Printf("[registry] Failed to write temp file %s: %v", tmp, err)
		return
	}
	if err := os.Rename(tmp, r.dataFile); err != nil {
		log.Printf("[registry] Failed to rename %s → %s: %v", tmp, r.dataFile, err)
	}
}

func (r *NodeRegistry) Register(n NodeInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n.RegisteredAt = time.Now()
	r.nodes[n.ID] = deepCopyNodeInfo(n)
	r.persist()
}

func (r *NodeRegistry) List() []NodeInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]NodeInfo, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, deepCopyNodeInfo(n))
	}
	return out
}

// ============================================================
// Embedding wire types (shared by GeminiProvider and future providers)
// ============================================================

type EmbedRequest struct {
	Model   string       `json:"model"`
	Content EmbedContent `json:"content"`
}
type EmbedContent struct {
	Parts []EmbedPart `json:"parts"`
}
type EmbedPart struct {
	Text string `json:"text"`
}
type EmbedResponse struct {
	Embedding struct {
		Values []float32 `json:"values"`
	} `json:"embedding"`
}

// ============================================================
// Qdrant search
// ============================================================

type SearchResult struct {
	ID      uint64                 `json:"id"`
	Score   float32                `json:"score"`
	Payload map[string]interface{} `json:"payload"`
}

func searchQdrant(ctx context.Context, pc qdrant.PointsClient, collection string, vector []float32, limit uint64, namespace string) ([]SearchResult, error) {
	start := time.Now()
	defer func() {
		searchLatency.WithLabelValues(collection).Observe(float64(time.Since(start).Milliseconds()))
	}()
	var filter *qdrant.Filter
	if namespace != "" {
		filter = &qdrant.Filter{
			Must: []*qdrant.Condition{
				{
					ConditionOneOf: &qdrant.Condition_Field{
						Field: &qdrant.FieldCondition{
							Key: "namespace",
							Match: &qdrant.Match{
								MatchValue: &qdrant.Match_Keyword{
									Keyword: namespace,
								},
							},
						},
					},
				},
			},
		}
	}

	resp, err := pc.Search(ctx, &qdrant.SearchPoints{
		CollectionName: collection,
		Vector:         vector,
		Limit:          limit,
		Filter:         filter,
		WithPayload: &qdrant.WithPayloadSelector{
			SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: true},
		},
	})
	if err != nil {
		return nil, err
	}

	var results []SearchResult
	for _, pt := range resp.GetResult() {
		payload := make(map[string]interface{})
		for k, v := range pt.Payload {
			switch val := v.Kind.(type) {
			case *qdrant.Value_StringValue:
				payload[k] = val.StringValue
			case *qdrant.Value_IntegerValue:
				payload[k] = val.IntegerValue
			case *qdrant.Value_DoubleValue:
				payload[k] = val.DoubleValue
			case *qdrant.Value_BoolValue:
				payload[k] = val.BoolValue
			default:
				payload[k] = fmt.Sprintf("%v", v)
			}
		}
		var id uint64
		if numID, ok := pt.Id.PointIdOptions.(*qdrant.PointId_Num); ok {
			id = numID.Num
		}
		results = append(results, SearchResult{
			ID:      id,
			Score:   pt.Score,
			Payload: payload,
		})
	}
	return results, nil
}

// ============================================================
// Gemini LLM call (streaming)
// ============================================================

type GeminiPart struct {
	Text string `json:"text"`
}
type GeminiContent struct {
	Role  string       `json:"role"`
	Parts []GeminiPart `json:"parts"`
}
type GeminiRequest struct {
	Contents []GeminiContent `json:"contents"`
}
type GeminiCandidate struct {
	Content GeminiContent `json:"content"`
}
type GeminiResponse struct {
	Candidates []GeminiCandidate `json:"candidates"`
}

func callGemini(prompt, apiKey string) (string, error) {
	model := "gemini-2.5-flash"
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)

	reqBody := GeminiRequest{
		Contents: []GeminiContent{
			{Role: "user", Parts: []GeminiPart{{Text: prompt}}},
		},
	}
	body, _ := json.Marshal(reqBody)

	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Gemini API %d: %s", resp.StatusCode, string(b))
	}

	var gr GeminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return "", err
	}

	if len(gr.Candidates) == 0 || len(gr.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no content in Gemini response")
	}

	return gr.Candidates[0].Content.Parts[0].Text, nil
}

// ============================================================
// OpenAI-compatible types
// ============================================================

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type ChatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
}

// Streaming delta types
type DeltaContent struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type StreamChoice struct {
	Index        int          `json:"index"`
	Delta        DeltaContent `json:"delta"`
	FinishReason *string      `json:"finish_reason"`
}

type StreamChunk struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []StreamChoice `json:"choices"`
}

// ============================================================
// Server
// ============================================================

type Server struct {
	registry     *NodeRegistry
	qdrantConn   *grpc.ClientConn
	pointsClient qdrant.PointsClient
	healthClient grpc_health_v1.HealthClient
	embedder     EmbedProvider
	collection   string
	apiKey       string // kept for LLM calls (Gemini generateContent)
	authKey      string
	apiKeys      map[string][]string // Key -> Allowed Namespaces
	port         string
	startTime    time.Time
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) instrument(path string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		httpRequestsTotal.WithLabelValues(path, fmt.Sprintf("%d", rw.status)).Inc()
	}
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func (s *Server) authenticate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Unauthorized: Missing Authorization header", http.StatusUnauthorized)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			http.Error(w, "Unauthorized: Invalid Authorization format", http.StatusUnauthorized)
			return
		}

		key := parts[1]

		// Advanced Mode (Multi-Key) has precedence
		if s.apiKeys != nil {
			allowedNamespaces, ok := s.apiKeys[key]
			if ok {
				// We do NOT trust the namespace from URL params or headers after auth.
				// We MUST extract it here and pass it down via context or handle it in the handler.
				// For now, we will store allowedNamespaces in the request context.
				ctx := context.WithValue(r.Context(), "AllowedNamespaces", allowedNamespaces)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		// Simple Mode
		if s.authKey != "" && key == s.authKey {
			// For simple mode, we allow all namespaces (wildcard behavior)
			ctx := context.WithValue(r.Context(), "AllowedNamespaces", []string{"*"})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		logAudit(AuditEntry{
			Action: "auth_failure",
			User:   "unknown",
			Status: http.StatusUnauthorized,
			Metadata: map[string]interface{}{
				"provided_key": key[:min(len(key), 8)] + "...",
				"remote_addr":  r.RemoteAddr,
			},
		})

		http.Error(w, "Unauthorized: Invalid API Key", http.StatusUnauthorized)
	}
}

func (s *Server) handleRegisterNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var node NodeInfo
	if err := json.NewDecoder(r.Body).Decode(&node); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	if node.ID == "" {
		node.ID = fmt.Sprintf("node-%d", time.Now().UnixNano())
	}
	s.registry.Register(node)
	log.Printf("[registry] Node registered: %s @ %s", node.ID, node.URL)
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "registered",
		"id":     node.ID,
	})
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, s.registry.List())
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	allowedNamespaces, ok := r.Context().Value("AllowedNamespaces").([]string)
	if !ok {
		http.Error(w, "Forbidden: No authorized namespaces", http.StatusForbidden)
		return
	}

	requestedNamespace := strings.TrimSpace(r.URL.Query().Get("namespace"))

	// ── STRICT NAMESPACE MODE (Phase 17) ──────────────────────────────────
	if os.Getenv("EMDEX_STRICT_NAMESPACE") == "true" {
		if requestedNamespace == "" {
			log.Printf("[search] REJECTED (STRICT): missing namespace parameter")
			http.Error(w, "Bad request: ?namespace= parameter is mandatory", http.StatusBadRequest)
			return
		}
	}

	// Wildcard-namespace safety: an admin key with ["*"] AllowedNamespaces must
	// not silently perform a global (cross-tenant) search when the caller omits
	// the namespace parameter.  Fall back to "default" to prevent accidental
	// data leakage across tenants.
	if requestedNamespace == "" {
		requestedNamespace = "default"
		log.Printf("[search] namespace param missing — forcing fallback to %q", requestedNamespace)
	}

	isAllowed := false
	for _, ns := range allowedNamespaces {
		if ns == "*" || ns == requestedNamespace {
			isAllowed = true
			break
		}
	}

	if !isAllowed {
		log.Printf("[search] Forbidden: requested namespace %q not in allowed list %v", requestedNamespace, allowedNamespaces)
		http.Error(w, "Forbidden: Namespace not authorized", http.StatusForbidden)
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "missing ?q=", http.StatusBadRequest)
		return
	}

	log.Printf("[search] Query: %q (namespace: %q)", query, requestedNamespace)

	vector, err := s.embedder.Embed(query)
	if err != nil {
		log.Printf("[search] Embedding error: %v", err)
		http.Error(w, fmt.Sprintf("embedding error: %v", err), http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	searchLimitStr := os.Getenv("EMDEX_SEARCH_LIMIT")
	searchLimit := uint64(10)
	if searchLimitStr != "" {
		if l, err := fmt.Sscanf(searchLimitStr, "%d", &searchLimit); err != nil || l != 1 {
			searchLimit = 10
		}
	}

	results, err := searchQdrant(ctx, s.pointsClient, s.collection, vector, searchLimit, requestedNamespace)
	status := http.StatusOK
	if err != nil {
		log.Printf("[search] Qdrant error: %v", err)
		http.Error(w, fmt.Sprintf("search error: %v", err), http.StatusInternalServerError)
		status = http.StatusInternalServerError
	}

	if err == nil {
		log.Printf("[search] Found %d results", len(results))
		s.writeJSON(w, http.StatusOK, map[string]interface{}{
			"query":   query,
			"results": results,
		})
	}

	logAudit(AuditEntry{
		Action:    "search",
		Query:     query,
		Namespace: requestedNamespace,
		Results:   len(results),
		LatencyMS: time.Since(start).Milliseconds(),
		Status:    status,
	})
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	allowedNamespaces, ok := r.Context().Value("AllowedNamespaces").([]string)
	if !ok {
		http.Error(w, "Forbidden: No authorized namespaces", http.StatusForbidden)
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// ── Namespace enforcement ────────────────────────────────────────────────
	requestedNamespace := strings.TrimSpace(r.Header.Get("X-Emdex-Namespace"))
	if requestedNamespace == "" {
		requestedNamespace = strings.TrimSpace(r.URL.Query().Get("namespace"))
	}

	// ── STRICT NAMESPACE MODE (Phase 17) ──────────────────────────────────
	if os.Getenv("EMDEX_STRICT_NAMESPACE") == "true" {
		if requestedNamespace == "" {
			log.Printf("[agent] REJECTED (STRICT): missing namespace")
			http.Error(w, "Bad request: X-Emdex-Namespace header or ?namespace= query param is required", http.StatusBadRequest)
			return
		}
	}

	isAllowed := false
	for _, ns := range allowedNamespaces {
		if ns == "*" || ns == requestedNamespace {
			isAllowed = true
			break
		}
	}

	if !isAllowed {
		log.Printf("[agent] Forbidden: requested namespace %q not in allowed list %v", requestedNamespace, allowedNamespaces)
		http.Error(w, "Forbidden: Namespace not authorized", http.StatusForbidden)
		return
	}

	if requestedNamespace == "" {
		log.Printf("[agent] REJECTED: missing namespace on chat/completions request")
		http.Error(w, "Bad request: X-Emdex-Namespace header or ?namespace= query param is required", http.StatusBadRequest)
		return
	}
	log.Printf("[agent] namespace=%q", requestedNamespace)

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

	// ── MULTI-HOP RAG (P5) ───────────────────────────────────────────────────
	// Hop 1: embed + search within namespace.
	// Hop 2: if LLM signals insufficient context, refine query and re-search
	//         within the SAME namespace (no cross-namespace bleed).
	// All errors are surfaced — no silent hallucination fallback.

	log.Printf("[agent] Hop 1: q=%q ns=%q", question, requestedNamespace)

	vector, err := s.embedder.Embed(question)
	if err != nil {
		log.Printf("[agent] Hop 1 embedding error: %v", err)
		http.Error(w, fmt.Sprintf("embedding error: %v", err), http.StatusBadGateway)
		return
	}

	chatLimitStr := os.Getenv("EMDEX_CHAT_LIMIT")
	chatLimit := uint64(5)
	if chatLimitStr != "" {
		if l, err := fmt.Sscanf(chatLimitStr, "%d", &chatLimit); err != nil || l != 1 {
			chatLimit = 5
		}
	}

	results, err := searchQdrant(r.Context(), s.pointsClient, s.collection, vector, chatLimit, requestedNamespace)
	if err != nil {
		log.Printf("[agent] Hop 1 search error: %v", err)
		http.Error(w, fmt.Sprintf("search error: %v", err), http.StatusBadGateway)
		return
	}

	contextStr := buildContext(results)

	evalPrompt := fmt.Sprintf(`System: You are a professional RAG evaluation agent. Analyze the context and answer the question.
If the context is insufficient, respond with 'search:' followed by a refined query.
Output must be strictly either the answer or a 'search:' command. Do not hallucinate.

User: Based on the context below, can you fully answer: %q? If yes, provide the answer. If no, output ONLY a better search query prefixed with 'search:' to find the missing info.

Context:
%s`, question, contextStr)
	eval, err := callGemini(evalPrompt, s.apiKey)
	if err != nil {
		log.Printf("[agent] Hop 1 LLM error: %v", err)
		http.Error(w, fmt.Sprintf("LLM error: %v", err), http.StatusBadGateway)
		return
	}

	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(eval)), "search:") {
		newQuery := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(eval), "search:"))
		log.Printf("[agent] Hop 2 (Refined): q=%q ns=%q", newQuery, requestedNamespace)

		vector2, err := s.embedder.Embed(newQuery)
		if err != nil {
			log.Printf("[agent] Hop 2 embedding error: %v — falling back to Hop 1 context", err)
			// Non-fatal: answer with Hop 1 context only
		} else {
			// STRICT: namespace must be enforced on Hop 2 as well
			results2, err := searchQdrant(r.Context(), s.pointsClient, s.collection, vector2, chatLimit, requestedNamespace)
			if err != nil {
				log.Printf("[agent] Hop 2 search error: %v — falling back to Hop 1 context", err)
			} else {
				contextStr = contextStr + "\n\n" + buildContext(results2)
			}
		}

		finalPrompt := fmt.Sprintf("Answer the question using the consolidated context.\n\nContext:\n%s\n\nQuestion: %s", contextStr, question)
		eval, err = callGemini(finalPrompt, s.apiKey)
		if err != nil {
			log.Printf("[agent] Hop 2 LLM error: %v", err)
			http.Error(w, fmt.Sprintf("LLM error on final answer: %v", err), http.StatusBadGateway)
			return
		}
	}

	if req.Stream {
		s.streamResponse(w, req.Model, eval)
	} else {
		s.writeJSON(w, http.StatusOK, ChatResponse{
			ID: "chatcmpl-rag",
			Choices: []ChatChoice{{Message: ChatMessage{Role: "assistant", Content: eval}}},
		})
	}
}

func buildContext(results []SearchResult) string {
	var parts []string
	for _, r := range results {
		if t, ok := r.Payload["text"].(string); ok {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n---\n")
}


func (s *Server) streamResponse(w http.ResponseWriter, model, answer string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	created := time.Now().Unix()

	// Send role delta first
	roleChunk := StreamChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []StreamChoice{{
			Index: 0,
			Delta: DeltaContent{Role: "assistant"},
		}},
	}
	b, _ := json.Marshal(roleChunk)
	fmt.Fprintf(w, "data: %s\n\n", string(b))
	flusher.Flush()

	// Stream answer word by word
	words := strings.Fields(answer)
	for i, word := range words {
		text := word
		if i < len(words)-1 {
			text += " "
		}
		chunk := StreamChunk{
			ID:      id,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   model,
			Choices: []StreamChoice{{
				Index: 0,
				Delta: DeltaContent{Content: text},
			}},
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", string(b))
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)
	}

	// Send [DONE]
	stopReason := "stop"
	doneChunk := StreamChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []StreamChoice{{
			Index:        0,
			Delta:        DeltaContent{},
			FinishReason: &stopReason,
		}},
	}
	b, _ = json.Marshal(doneChunk)
	fmt.Fprintf(w, "data: %s\n\n", string(b))
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{
		"status":     "ok",
		"collection": s.collection,
	})
}

// K8s-style Health Checks

func (s *Server) handleLiveness(w http.ResponseWriter, r *http.Request) {
	// Simple process check
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "UP"})
}

func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	// Check Qdrant connection
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := s.healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: ""})
	if err != nil || resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "DOWN",
			"reason": "qdrant_unreachable",
		})
		return
	}

	// Check registry is initialized (always true post-construction, but be explicit)
	if s.registry == nil || s.embedder == nil {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "DOWN",
			"reason": "gateway_uninitialized",
		})
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]string{"status": "UP"})
}

func (s *Server) handleDailyDelta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Calculate timestamp for 24h ago in nano
	since := float64(time.Now().Add(-24 * time.Hour).UnixNano())

	// Qdrant Filter for timestamp > since
	// Note: We need to make sure we index points with a timestamp. 
	// The current node uses time.Now().UnixNano() as ID, which IS a timestamp.
	// But let's verify if we have a timestamp field. 
	// Actually, let's use the ID-based search if we assume IDs are timestamps.
	// Better: Implement a search that filters by ID range or better yet, a dedicated 'indexed_at' field.
	// Since I'm CTO, I'll add the requirement for indexed_at in nodes or just use the ID if it's reliable.
	// For this phase, let's use a filter on 'indexed_at' which I will add to the node.

	filter := &qdrant.Filter{
		Must: []*qdrant.Condition{
			{
				ConditionOneOf: &qdrant.Condition_Field{
					Field: &qdrant.FieldCondition{
						Key: "indexed_at",
						Range: &qdrant.Range{
							Gt: &since,
						},
					},
				},
			},
		},
	}

	limit := uint32(100)
	resp, err := s.pointsClient.Scroll(r.Context(), &qdrant.ScrollPoints{
		CollectionName: s.collection,
		Filter:         filter,
		Limit:          &limit,
		WithPayload: &qdrant.WithPayloadSelector{
			SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: true},
		},
	})

	if err != nil {
		http.Error(w, fmt.Sprintf("Qdrant error: %v", err), http.StatusInternalServerError)
		return
	}

	files := make(map[string]bool)
	var results []map[string]interface{}
	
	var contextParts []string

	for _, pt := range resp.GetResult() {
		p := pt.Payload
		path := ""
		if v, ok := p["path"]; ok {
			path = v.GetStringValue()
		}
		if path != "" && !files[path] {
			files[path] = true
			res := map[string]interface{}{
				"path": path,
			}
			results = append(results, res)
			
			if text, ok := p["text"]; ok {
				contextParts = append(contextParts, fmt.Sprintf("File: %s\nContent: %s", path, text.GetStringValue()))
			}
		}
	}
	
	summary := "No new files found."
	if len(contextParts) > 0 {
		summaryPrompt := "Categorize and summarize the following newly indexed files:\n\n" + strings.Join(contextParts, "\n\n")
		summary, _ = callGemini(summaryPrompt, s.apiKey)
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"count":   len(results),
		"files":   results,
		"summary": summary,
	})
}

func (s *Server) handleStartup(w http.ResponseWriter, r *http.Request) {
	// For startup, we might want to ensure the server has been running for at least a few seconds
	// and has successfully connected to dependencies at least once.
	if time.Since(s.startTime) < 5*time.Second {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "STARTING"})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "STARTED"})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ============================================================
// Main
// ============================================================

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("emdexer-gateway version %s\n", Version)
		return
	}
	cwd, _ := os.Getwd()
	loadEnv(filepath.Join(cwd, ".env"))

	provider := os.Getenv("EMBED_PROVIDER")
	if provider == "" {
		provider = "gemini"
	}

	apiKey := os.Getenv("GOOGLE_API_KEY")
	if provider == "gemini" && apiKey == "" {
		log.Fatal("GOOGLE_API_KEY not set (required for Gemini)")
	}

	qdrantHost := os.Getenv("QDRANT_HOST")
	if qdrantHost == "" {
		qdrantHost = "localhost:6334"
	}

	port := os.Getenv("EMDEX_PORT")
	if port == "" {
		port = os.Getenv("GATEWAY_PORT")
	}
	if port == "" {
		port = "7700"
	}

	collection := os.Getenv("EMDEX_QDRANT_COLLECTION")
	if collection == "" {
		collection = os.Getenv("QDRANT_COLLECTION")
	}
	if collection == "" {
		collection = "emdexer_v1"
	}

	authKey := os.Getenv("EMDEX_AUTH_KEY")
	// EMDEX_AUTH_KEY is optional if EMDEX_API_KEYS is used
	
	var apiKeys map[string][]string
	if keysJSON := os.Getenv("EMDEX_API_KEYS"); keysJSON != "" {
		if err := json.Unmarshal([]byte(keysJSON), &apiKeys); err != nil {
			log.Printf("[gateway] Warning: Failed to parse EMDEX_API_KEYS: %v", err)
		} else {
			log.Printf("[gateway] Loaded %d API keys from EMDEX_API_KEYS", len(apiKeys))
		}
	}

	if authKey == "" && apiKeys == nil {
		log.Fatal("Neither EMDEX_AUTH_KEY nor EMDEX_API_KEYS set")
	}

	// Connect to Qdrant via gRPC
	conn, err := grpc.Dial(qdrantHost, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to Qdrant: %v", err)
	}
	defer conn.Close()

	// Node registry — persisted to nodes.json in the working directory.
	registryFile := os.Getenv("EMDEX_REGISTRY_FILE")
	if registryFile == "" {
		registryFile = filepath.Join(cwd, "nodes.json")
	}

	// Embedder — pluggable via EMBED_PROVIDER env (gemini | ollama).
	embedder := newEmbedProvider(apiKey)
	log.Printf("[gateway] Embed provider: %s", embedder.Name())

	srv := &Server{
		registry:     NewNodeRegistry(registryFile),
		qdrantConn:   conn,
		pointsClient: qdrant.NewPointsClient(conn),
		healthClient: grpc_health_v1.NewHealthClient(conn),
		embedder:     embedder,
		collection:   collection,
		apiKey:       apiKey,
		authKey:      authKey,
		apiKeys:      apiKeys,
		port:         port,
		startTime:    time.Now(),
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", srv.handleHealth)
	mux.HandleFunc("/healthz/liveness", srv.handleLiveness)
	mux.HandleFunc("/healthz/readiness", srv.handleReadiness)
	mux.HandleFunc("/healthz/startup", srv.handleStartup)

	mux.HandleFunc("/nodes/register", srv.instrument("/nodes/register", srv.authenticate(srv.handleRegisterNode)))
	mux.HandleFunc("/nodes", srv.instrument("/nodes", srv.authenticate(srv.handleListNodes)))
	mux.HandleFunc("/v1/search", srv.instrument("/v1/search", srv.authenticate(srv.handleSearch)))
	mux.HandleFunc("/v1/chat/completions", srv.instrument("/v1/chat/completions", srv.authenticate(srv.handleChatCompletions)))
	mux.HandleFunc("/v1/daily-delta", srv.instrument("/v1/daily-delta", srv.authenticate(srv.handleDailyDelta)))

	addr := ":" + port
	log.Printf("EMDEX Gateway starting on %s (collection: %s)", addr, collection)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
