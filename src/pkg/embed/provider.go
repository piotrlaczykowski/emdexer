package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"github.com/piotrlaczykowski/emdexer/safenet"
)

var embedDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "emdexer_gateway_embed_duration_ms",
	Help:    "Latency of embedding API calls in milliseconds",
	Buckets: []float64{10, 50, 100, 200, 500, 1000, 2000, 5000, 10000},
}, []string{"provider", "model"})

var embedErrors = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_embed_errors_total",
	Help: "Total number of embedding API errors",
}, []string{"provider", "model"})

// validateOllamaHost parses the URL and validates its scheme.
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

// EmbedProvider is the single abstraction over any dense-embedding backend.
type EmbedProvider interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	Name() string
}

// GeminiProvider — Google Generative Language API
const defaultGeminiModel = "models/text-embedding-004"

type GeminiProvider struct {
	APIKey string
	Model  string
}

func NewGeminiProvider(apiKey, model string) *GeminiProvider {
	if model == "" {
		model = defaultGeminiModel
	}
	return &GeminiProvider{APIKey: apiKey, Model: model}
}

func (g *GeminiProvider) Name() string { return "gemini:" + g.Model }

type embedRequest struct {
	Model   string       `json:"model"`
	Content embedContent `json:"content"`
}
type embedContent struct {
	Parts []embedPart `json:"parts"`
}
type embedPart struct {
	Text string `json:"text"`
}
type embedResponse struct {
	Embedding struct {
		Values []float32 `json:"values"`
	} `json:"embedding"`
}

func (g *GeminiProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	geminiModel := g.Model
	if envModel := os.Getenv("EMDEX_GEMINI_MODEL"); envModel != "" {
		geminiModel = envModel
	}

	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.embed")
	span.SetAttributes(attribute.String("embed.provider", "gemini"), attribute.String("embed.model", geminiModel))
	defer span.End()

	start := time.Now()
	result, err := g.embed(text, geminiModel)
	embedDuration.WithLabelValues("gemini", geminiModel).Observe(float64(time.Since(start).Milliseconds()))
	if err != nil {
		embedErrors.WithLabelValues("gemini", geminiModel).Inc()
	}
	return result, err
}

func (g *GeminiProvider) embed(text, geminiModel string) ([]float32, error) {
	url := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/%s:embedContent?key=%s",
		geminiModel, g.APIKey,
	)

	body, _ := json.Marshal(embedRequest{
		Model:   geminiModel,
		Content: embedContent{Parts: []embedPart{{Text: text}}},
	})

	client := safenet.NewSafeClient(30 * time.Second)
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemini embed HTTP: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini embed %d: %s", resp.StatusCode, string(b))
	}

	var er embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return nil, fmt.Errorf("gemini embed decode: %w", err)
	}
	return er.Embedding.Values, nil
}

// OllamaProvider
const defaultOllamaModel = "nomic-embed-text:v2"

type OllamaProvider struct {
	Host        string
	Model       string
	TruncateDim int // 0 means no truncation (use model default)
}

func (o *OllamaProvider) Name() string { return "ollama:" + o.Model }

func (o *OllamaProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.embed")
	span.SetAttributes(attribute.String("embed.provider", "ollama"), attribute.String("embed.model", o.Model))
	defer span.End()

	start := time.Now()
	result, err := o.embed(text)
	embedDuration.WithLabelValues("ollama", o.Model).Observe(float64(time.Since(start).Milliseconds()))
	if err != nil {
		embedErrors.WithLabelValues("ollama", o.Model).Inc()
	}
	return result, err
}

func (o *OllamaProvider) embed(text string) ([]float32, error) {
	endpoint := fmt.Sprintf("%s/api/embed", o.Host)

	type options struct {
		TruncateDim int `json:"truncate_dim"`
	}
	type req struct {
		Model   string   `json:"model"`
		Input   string   `json:"input"`
		Options *options `json:"options,omitempty"`
	}
	type resp struct {
		Embeddings [][]float32 `json:"embeddings"`
	}

	r := req{Model: o.Model, Input: text}
	if o.TruncateDim > 0 {
		r.Options = &options{TruncateDim: o.TruncateDim}
	}

	body, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("ollama marshal: %w", err)
	}

	// OLLAMA_HOST is operator-configured (env var), not user-supplied input.
	// SSRF guard is not applicable — use a plain HTTP client.
	client := &http.Client{Timeout: 60 * time.Second}
	hresp, err := client.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama HTTP: %w", err)
	}
	defer hresp.Body.Close()

	if hresp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(hresp.Body)
		return nil, fmt.Errorf("ollama %d: %s", hresp.StatusCode, string(b))
	}

	var or resp
	if err := json.NewDecoder(hresp.Body).Decode(&or); err != nil {
		return nil, fmt.Errorf("ollama decode: %w", err)
	}

	if len(or.Embeddings) == 0 {
		return nil, fmt.Errorf("ollama returned no embeddings")
	}

	return or.Embeddings[0], nil
}

// OpenAIProvider — OpenAI Embeddings API
const defaultOpenAIModel = "text-embedding-3-small"

type OpenAIProvider struct {
	APIKey  string
	Model   string
	BaseURL string // defaults to https://api.openai.com/v1; override for Azure/proxy
}

func NewOpenAIProvider(apiKey, model, baseURL string) *OpenAIProvider {
	if model == "" {
		model = defaultOpenAIModel
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAIProvider{APIKey: apiKey, Model: model, BaseURL: baseURL}
}

func (o *OpenAIProvider) Name() string { return "openai:" + o.Model }

func (o *OpenAIProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.embed")
	span.SetAttributes(attribute.String("embed.provider", "openai"), attribute.String("embed.model", o.Model))
	defer span.End()

	start := time.Now()
	result, err := o.embed(ctx, text)
	embedDuration.WithLabelValues("openai", o.Model).Observe(float64(time.Since(start).Milliseconds()))
	if err != nil {
		embedErrors.WithLabelValues("openai", o.Model).Inc()
	}
	return result, err
}

func (o *OpenAIProvider) embed(ctx context.Context, text string) ([]float32, error) {
	type req struct {
		Input string `json:"input"`
		Model string `json:"model"`
	}
	type embeddingItem struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	}
	type resp struct {
		Data []embeddingItem `json:"data"`
	}

	body, err := json.Marshal(req{Input: text, Model: o.Model})
	if err != nil {
		return nil, fmt.Errorf("openai embed marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai embed new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.APIKey)

	client := safenet.NewSafeClient(30 * time.Second)
	hresp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai embed HTTP: %w", err)
	}
	defer hresp.Body.Close()

	if hresp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(hresp.Body)
		return nil, fmt.Errorf("openai embed %d: %s", hresp.StatusCode, string(b))
	}

	var er resp
	if err := json.NewDecoder(hresp.Body).Decode(&er); err != nil {
		return nil, fmt.Errorf("openai embed decode: %w", err)
	}
	if len(er.Data) == 0 {
		return nil, fmt.Errorf("openai embed returned no data")
	}
	return er.Data[0].Embedding, nil
}

// New returns the EmbedProvider selected by the EMBED_PROVIDER environment variable.
func New(apiKey, providerEnv, ollamaHost, ollamaModel, geminiModel, openaiAPIKey, openaiModel string) EmbedProvider {
	switch strings.ToLower(providerEnv) {
	case "ollama":
		if ollamaHost == "" {
			ollamaHost = "http://localhost:11434"
		}
		if ollamaModel == "" {
			ollamaModel = defaultOllamaModel
		}

		if err := validateOllamaHost(ollamaHost); err != nil {
			log.Fatalf("[embed] CRITICAL SECURITY ERROR: %v", err)
		}

		truncateDim := 0
		if v := os.Getenv("OLLAMA_EMBED_DIMS"); v != "" {
			d, err := strconv.Atoi(v)
			if err != nil || d < 32 || d > 4096 {
				log.Printf("[embed] WARN: OLLAMA_EMBED_DIMS=%q invalid (must be int 32–4096), ignoring", v)
			} else {
				truncateDim = d
				log.Printf("[embed] ollama truncate_dim=%d", truncateDim)
			}
		}

		return &OllamaProvider{Host: ollamaHost, Model: ollamaModel, TruncateDim: truncateDim}
	case "openai":
		if openaiAPIKey == "" {
			log.Fatalf("[embed] FATAL: EMBED_PROVIDER=openai requires OPENAI_API_KEY")
		}
		return NewOpenAIProvider(openaiAPIKey, openaiModel, os.Getenv("OPENAI_BASE_URL"))
	default:
		return NewGeminiProvider(apiKey, geminiModel)
	}
}
