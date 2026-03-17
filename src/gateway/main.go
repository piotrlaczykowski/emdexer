package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/piotrlaczykowski/emdexer/embed"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/qdrant/go-client/qdrant"
	_ "github.com/lib/pq"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

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
// Qdrant search utils
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

func buildContext(results []SearchResult) string {
	var parts []string
	for _, r := range results {
		if t, ok := r.Payload["text"].(string); ok {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n---\n")
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

	if err := startServer(srv); err != nil && err != http.ErrServerClosed {
		log.Fatalf("gateway server error: %v", err)
	}
}
