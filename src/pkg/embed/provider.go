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
type OllamaProvider struct {
	Host  string
	Model string
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
	type req struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}
	type resp struct {
		Embeddings [][]float32 `json:"embeddings"`
	}

	body, err := json.Marshal(req{
		Model: o.Model,
		Input: text,
	})
	if err != nil {
		return nil, fmt.Errorf("ollama marshal: %w", err)
	}

	client := safenet.NewSafeClient(60 * time.Second)
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

// New returns the EmbedProvider selected by the EMBED_PROVIDER environment variable.
func New(apiKey, providerEnv, ollamaHost, ollamaModel, geminiModel string) EmbedProvider {
	switch strings.ToLower(providerEnv) {
	case "ollama":
		if ollamaHost == "" {
			ollamaHost = "http://localhost:11434"
		}
		if ollamaModel == "" {
			ollamaModel = "nomic-embed-text"
		}

		if err := validateOllamaHost(ollamaHost); err != nil {
			log.Fatalf("[embed] CRITICAL SECURITY ERROR: %v", err)
		}

		return &OllamaProvider{Host: ollamaHost, Model: ollamaModel}
	default:
		return NewGeminiProvider(apiKey, geminiModel)
	}
}
