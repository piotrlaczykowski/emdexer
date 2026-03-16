package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/piotrlaczykowski/emdexer/embed"
	"github.com/piotrlaczykowski/emdexer/version"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/qdrant/go-client/qdrant"
	_ "github.com/lib/pq"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

const Version = "v1.0.5"

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
		Help:    "Latency of embedding in milliseconds",
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
// Node Registry — interface + implementations
// ============================================================

type NodeInfo struct {
	ID           string    `json:"id"`
	URL          string    `json:"url"`
	Collections  []string  `json:"collections"`
	RegisteredAt time.Time `json:"registered_at"`
}

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

// NodeRegistry is the interface that all registry backends must implement.
type NodeRegistry interface {
	// Register adds or updates a node in the registry.
	Register(n NodeInfo)
	// Deregister removes a node from the registry by ID.
	Deregister(id string)
	// List returns all currently registered nodes.
	List() []NodeInfo
}

// ------------------------------------------------------------
// FileNodeRegistry — local nodes.json backend (default)
// ------------------------------------------------------------

type FileNodeRegistry struct {
	mu       sync.RWMutex
	nodes    map[string]NodeInfo
	dataFile string
}

func NewFileNodeRegistry(dataFile string) *FileNodeRegistry {
	r := &FileNodeRegistry{
		nodes:    make(map[string]NodeInfo),
		dataFile: dataFile,
	}
	r.load()
	return r
}

func (r *FileNodeRegistry) load() {
	data, err := os.ReadFile(r.dataFile)
	if err != nil {
		return
	}
	var nodes []NodeInfo
	if err := json.Unmarshal(data, &nodes); err != nil {
		log.Printf("[registry] Failed to parse %s: %v", r.dataFile, err)
		return
	}
	for _, n := range nodes {
		r.nodes[n.ID] = deepCopyNodeInfo(n)
	}
}

func (r *FileNodeRegistry) persist() {
	nodes := make([]NodeInfo, 0, len(r.nodes))
	for _, n := range r.nodes {
		nodes = append(nodes, deepCopyNodeInfo(n))
	}
	data, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return
	}
	tmp := r.dataFile + ".tmp"
	os.WriteFile(tmp, data, 0600)
	os.Rename(tmp, r.dataFile)
}

func (r *FileNodeRegistry) Register(n NodeInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n.RegisteredAt = time.Now()
	r.nodes[n.ID] = deepCopyNodeInfo(n)
	r.persist()
}

func (r *FileNodeRegistry) Deregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.nodes, id)
	r.persist()
}

func (r *FileNodeRegistry) List() []NodeInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]NodeInfo, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, deepCopyNodeInfo(n))
	}
	return out
}

// ------------------------------------------------------------
// DBNodeRegistry — PostgreSQL backend (HA mode)
// ------------------------------------------------------------

type DBNodeRegistry struct {
	db *sql.DB
}

// NewDBNodeRegistry opens a PostgreSQL connection, runs auto-migration,
// and returns a ready-to-use DBNodeRegistry.
func NewDBNodeRegistry(dsn string) (*DBNodeRegistry, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("[registry] failed to open postgres: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("[registry] failed to ping postgres: %w", err)
	}

	r := &DBNodeRegistry{db: db}
	if err := r.migrate(); err != nil {
		return nil, fmt.Errorf("[registry] migration failed: %w", err)
	}

	log.Println("[registry] PostgreSQL backend ready")
	return r, nil
}

// migrate creates the registered_nodes table if it does not already exist.
func (r *DBNodeRegistry) migrate() error {
	const ddl = `
CREATE TABLE IF NOT EXISTS registered_nodes (
    id           TEXT        PRIMARY KEY,
    url          TEXT        NOT NULL,
    collections  JSONB       NOT NULL DEFAULT '[]',
    registered_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`
	_, err := r.db.Exec(ddl)
	return err
}

func (r *DBNodeRegistry) Register(n NodeInfo) {
	n.RegisteredAt = time.Now()
	colsJSON, _ := json.Marshal(n.Collections)
	_, err := r.db.Exec(`
		INSERT INTO registered_nodes (id, url, collections, registered_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE
		  SET url          = EXCLUDED.url,
		      collections  = EXCLUDED.collections,
		      registered_at = EXCLUDED.registered_at
	`, n.ID, n.URL, string(colsJSON), n.RegisteredAt)
	if err != nil {
		log.Printf("[registry] Register error: %v", err)
	}
}

func (r *DBNodeRegistry) Deregister(id string) {
	if _, err := r.db.Exec(`DELETE FROM registered_nodes WHERE id = $1`, id); err != nil {
		log.Printf("[registry] Deregister error: %v", err)
	}
}

func (r *DBNodeRegistry) List() []NodeInfo {
	rows, err := r.db.Query(`SELECT id, url, collections, registered_at FROM registered_nodes ORDER BY registered_at`)
	if err != nil {
		log.Printf("[registry] List error: %v", err)
		return nil
	}
	defer rows.Close()

	var out []NodeInfo
	for rows.Next() {
		var n NodeInfo
		var colsJSON string
		if err := rows.Scan(&n.ID, &n.URL, &colsJSON, &n.RegisteredAt); err != nil {
			log.Printf("[registry] scan error: %v", err)
			continue
		}
		if err := json.Unmarshal([]byte(colsJSON), &n.Collections); err != nil {
			n.Collections = []string{}
		}
		out = append(out, n)
	}
	return out
}

// ------------------------------------------------------------
// Registry factory — picks backend based on env vars
// ------------------------------------------------------------

// newRegistry returns a DBNodeRegistry if POSTGRES_URL is set,
// otherwise falls back to FileNodeRegistry.
func newRegistry(dataFile string) NodeRegistry {
	if dsn := os.Getenv("POSTGRES_URL"); dsn != "" {
		log.Printf("[registry] POSTGRES_URL detected — using PostgreSQL backend")
		reg, err := NewDBNodeRegistry(dsn)
		if err != nil {
			log.Printf("[registry] WARNING: PostgreSQL init failed (%v) — falling back to FileNodeRegistry", err)
		} else {
			return reg
		}
	}
	log.Printf("[registry] Using FileNodeRegistry at %s", dataFile)
	return NewFileNodeRegistry(dataFile)
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
	model := "gemini-2.0-flash"
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

type Server struct {
	registry     NodeRegistry
	qdrantConn   *grpc.ClientConn
	pointsClient qdrant.PointsClient
	healthClient grpc_health_v1.HealthClient
	embedder     embed.EmbedProvider
	collection   string
	apiKey       string
	authKey      string
	apiKeys      map[string][]string
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
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		key := parts[1]
		if s.apiKeys != nil {
			allowedNamespaces, ok := s.apiKeys[key]
			if ok {
				ctx := context.WithValue(r.Context(), "AllowedNamespaces", allowedNamespaces)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		if s.authKey != "" && key == s.authKey {
			ctx := context.WithValue(r.Context(), "AllowedNamespaces", []string{"*"})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		http.Error(w, "Unauthorized", http.StatusUnauthorized)
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
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "registered", "id": node.ID})
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, s.registry.List())
}

func (s *Server) handleDeregisterNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/nodes/")
	id = strings.TrimSuffix(id, "/deregister")
	if id == "" {
		http.Error(w, "Bad request: missing node id", http.StatusBadRequest)
		return
	}
	s.registry.Deregister(id)
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "deregistered", "id": id})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	allowedNamespaces, ok := r.Context().Value("AllowedNamespaces").([]string)
	if !ok {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	requestedNamespace := strings.TrimSpace(r.URL.Query().Get("namespace"))
	if requestedNamespace == "" {
		requestedNamespace = "default"
	}

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

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	results, err := searchQdrant(ctx, s.pointsClient, s.collection, vector, 10, requestedNamespace)
	if err != nil {
		http.Error(w, fmt.Sprintf("search error: %v", err), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"query":   query,
		"results": results,
	})

	logAudit(AuditEntry{
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

	allowedNamespaces, ok := r.Context().Value("AllowedNamespaces").([]string)
	if !ok {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var req ChatRequest
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

	results, err := searchQdrant(r.Context(), s.pointsClient, s.collection, vector, 5, requestedNamespace)
	if err != nil {
		http.Error(w, fmt.Sprintf("search error: %v", err), http.StatusBadGateway)
		return
	}

	contextStr := buildContext(results)
	finalPrompt := fmt.Sprintf("Answer the question using the consolidated context.\n\nContext:\n%s\n\nQuestion: %s", contextStr, question)
	eval, err := callGemini(finalPrompt, s.apiKey)
	if err != nil {
		http.Error(w, fmt.Sprintf("LLM error: %v", err), http.StatusBadGateway)
		return
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

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	created := time.Now().Unix()

	words := strings.Fields(answer)
	for _, word := range words {
		chunk := StreamChunk{
			ID:      id,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   model,
			Choices: []StreamChoice{{Index: 0, Delta: DeltaContent{Content: word + " "}}},
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", string(b))
		flusher.Flush()
	}
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
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

func main() {
	cwd, _ := os.Getwd()
	loadEnv(filepath.Join(cwd, ".env"))

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
		json.Unmarshal([]byte(keysJSON), &apiKeys)
	}

	conn, err := grpc.Dial(qdrantHost, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to Qdrant: %v", err)
	}
	defer conn.Close()

	registryFile := os.Getenv("EMDEX_REGISTRY_FILE")
	if registryFile == "" {
		registryFile = filepath.Join(cwd, "nodes.json")
	}

	registry := newRegistry(registryFile)

	embedder := embed.New(
		apiKey,
		os.Getenv("EMBED_PROVIDER"),
		os.Getenv("OLLAMA_HOST"),
		os.Getenv("OLLAMA_EMBED_MODEL"),
		os.Getenv("EMDEX_GEMINI_MODEL"),
	)

	srv := &Server{
		registry:     registry,
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
	mux.HandleFunc("/nodes/deregister/", srv.instrument("/nodes/deregister", srv.authenticate(srv.handleDeregisterNode)))
	mux.HandleFunc("/nodes", srv.instrument("/nodes", srv.authenticate(srv.handleListNodes)))
	mux.HandleFunc("/v1/search", srv.instrument("/v1/search", srv.authenticate(srv.handleSearch)))
	mux.HandleFunc("/v1/chat/completions", srv.instrument("/v1/chat/completions", srv.authenticate(srv.handleChatCompletions)))

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
