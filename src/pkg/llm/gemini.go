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

var llmStreamTTFT = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "emdexer_gateway_llm_stream_ttft_ms",
	Help:    "Time-to-first-token for LLM streaming calls in milliseconds",
	Buckets: []float64{50, 100, 250, 500, 1000, 2000, 5000},
}, []string{"model"})

var llmStreamChunks = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_llm_stream_chunks_total",
	Help: "Total number of token chunks received from LLM streaming calls",
}, []string{"model"})

// geminiStreamEndpointFmt is the URL format for Gemini's streaming API.
// Two %s slots: model, apiKey.
// Overridden in tests to point at a local httptest.Server.
var geminiStreamEndpointFmt = "https://generativelanguage.googleapis.com/v1beta/models/%s:streamGenerateContent?alt=sse&key=%s"

// geminiStreamClientFn returns the HTTP client used for streaming calls.
// Overridden in tests to bypass the SSRF guard when hitting a local httptest.Server.
var geminiStreamClientFn = func() *http.Client { return safenet.NewSafeClient(0) }

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
// client must not be nil. Production callers pass safenet.NewSafeClient(0):
//   - http.Client.Timeout=0 disables the whole-request deadline, allowing
//     arbitrarily long streaming responses.
//   - The transport's ResponseHeaderTimeout (30s) applies only to the wait for
//     the first response header byte; it does NOT apply to body reads, so
//     long-running token streams are unaffected.
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

func callGeminiStream(ctx context.Context, prompt, apiKey, model string, onChunk func(string) error) error {
	endpoint := fmt.Sprintf(geminiStreamEndpointFmt, model, apiKey)
	reqBody := GeminiRequest{
		Contents: []GeminiContent{
			{Role: "user", Parts: []GeminiPart{{Text: prompt}}},
		},
	}
	// Timeout 0 means no client-level timeout — streaming responses must not be
	// killed by a client timer; rely on the caller's context for cancellation.
	return callGeminiStreamAt(ctx, endpoint, reqBody, geminiStreamClientFn(), onChunk)
}

// CallGeminiStream calls Gemini's streamGenerateContent SSE endpoint and invokes
// onChunk for each token as it arrives. Use instead of CallGemini when req.Stream==true.
func CallGeminiStream(ctx context.Context, prompt, apiKey string, onChunk func(string) error) error {
	model := llmModel()

	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.llm.stream")
	span.SetAttributes(attribute.String("llm.model", model))
	defer span.End()

	start := time.Now()
	firstToken := true
	chunks := 0

	wrapped := func(text string) error {
		if firstToken {
			llmStreamTTFT.WithLabelValues(model).Observe(float64(time.Since(start).Milliseconds()))
			firstToken = false
		}
		chunks++
		return onChunk(text)
	}

	err := callGeminiStream(ctx, prompt, apiKey, model, wrapped)
	llmDuration.WithLabelValues(model).Observe(float64(time.Since(start).Milliseconds()))
	llmStreamChunks.WithLabelValues(model).Add(float64(chunks))
	if err != nil {
		llmErrors.WithLabelValues(model).Inc()
	}
	return err
}
