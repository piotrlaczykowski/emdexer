package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
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

// callGeminiStreamAt POSTs to endpoint (a fully-formed URL) and calls onChunk
// for every non-empty text token that arrives in the SSE stream.
// Separated from callGeminiStream to allow URL injection in tests.
// client must not be nil; production callers should pass safenet.NewSafeClient(0).
func callGeminiStreamAt(ctx context.Context, endpoint string, reqBody GeminiRequest, client *http.Client, onChunk func(string) error) error {
	b, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal stream request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("create stream request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("stream request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gemini stream API %d: %s", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 128*1024), 256*1024) // guard against large chunks

	var chunks, unmarshalErrors int
	for scanner.Scan() {
		line := scanner.Text()
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "" {
			continue
		}

		var gr GeminiResponse
		if err := json.Unmarshal([]byte(data), &gr); err != nil {
			unmarshalErrors++
			continue // skip malformed lines; detected below if all lines fail
		}

		if len(gr.Candidates) == 0 || len(gr.Candidates[0].Content.Parts) == 0 {
			continue
		}
		if text := gr.Candidates[0].Content.Parts[0].Text; text != "" {
			chunks++
			if err := onChunk(text); err != nil {
				return fmt.Errorf("chunk callback: %w", err)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	if chunks == 0 && unmarshalErrors > 0 {
		return fmt.Errorf("gemini stream: received %d lines but all failed to parse", unmarshalErrors)
	}
	return nil
}
