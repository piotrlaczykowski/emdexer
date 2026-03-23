package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"github.com/piotrlaczykowski/emdexer/safenet"
)

const defaultLLMModel = "gemini-3-flash-preview"

func llmModel() string {
	if m := os.Getenv("EMDEX_LLM_MODEL"); m != "" {
		return m
	}
	return defaultLLMModel
}

var llmDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "emdexer_gateway_llm_duration_ms",
	Help:    "Latency of LLM generation API calls in milliseconds",
	Buckets: []float64{100, 500, 1000, 2000, 5000, 10000, 20000, 30000},
}, []string{"model"})

var llmErrors = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_llm_errors_total",
	Help: "Total number of LLM generation API errors",
}, []string{"model"})

type GeminiPart struct {
	Text string `json:"text"`
}
type GeminiContent struct {
	Role  string       `json:"role"`
	Parts []GeminiPart `json:"parts"`
}
type GeminiGenerationConfig struct {
	ResponseMIMEType string `json:"responseMimeType,omitempty"`
}

type GeminiRequest struct {
	Contents         []GeminiContent         `json:"contents"`
	GenerationConfig *GeminiGenerationConfig `json:"generationConfig,omitempty"`
}
type GeminiCandidate struct {
	Content GeminiContent `json:"content"`
}
type GeminiResponse struct {
	Candidates []GeminiCandidate `json:"candidates"`
}

func CallGemini(ctx context.Context, prompt, apiKey string) (string, error) {
	model := llmModel()

	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.llm.generate")
	span.SetAttributes(attribute.String("llm.model", model))
	defer span.End()

	start := time.Now()
	result, err := callGemini(prompt, apiKey, model)
	llmDuration.WithLabelValues(model).Observe(float64(time.Since(start).Milliseconds()))
	if err != nil {
		llmErrors.WithLabelValues(model).Inc()
	}
	return result, err
}

// CallGeminiStructured calls Gemini with JSON mode enabled (responseMimeType: application/json).
// The response is guaranteed to be valid JSON. The caller is responsible for unmarshalling.
func CallGeminiStructured(ctx context.Context, prompt, apiKey string) (string, error) {
	model := llmModel()

	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.llm.structured")
	span.SetAttributes(attribute.String("llm.model", model))
	defer span.End()

	start := time.Now()
	result, err := callGeminiWithConfig(prompt, apiKey, model, &GeminiGenerationConfig{
		ResponseMIMEType: "application/json",
	})
	llmDuration.WithLabelValues(model).Observe(float64(time.Since(start).Milliseconds()))
	if err != nil {
		llmErrors.WithLabelValues(model).Inc()
	}
	return result, err
}

func callGemini(prompt, apiKey, model string) (string, error) {
	return callGeminiWithConfig(prompt, apiKey, model, nil)
}

func callGeminiWithConfig(prompt, apiKey, model string, genCfg *GeminiGenerationConfig) (string, error) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)

	reqBody := GeminiRequest{
		Contents: []GeminiContent{
			{Role: "user", Parts: []GeminiPart{{Text: prompt}}},
		},
		GenerationConfig: genCfg,
	}
	body, _ := json.Marshal(reqBody)

	client := safenet.NewSafeClient(30 * time.Second)
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("gemini API %d: %s", resp.StatusCode, string(b))
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
