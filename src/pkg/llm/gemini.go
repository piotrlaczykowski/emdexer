package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/piotrlaczykowski/emdexer/safenet"
)

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
type GeminiRequest struct {
	Contents []GeminiContent `json:"contents"`
}
type GeminiCandidate struct {
	Content GeminiContent `json:"content"`
}
type GeminiResponse struct {
	Candidates []GeminiCandidate `json:"candidates"`
}

func CallGemini(prompt, apiKey string) (string, error) {
	model := "gemini-2.0-flash"

	start := time.Now()
	result, err := callGemini(prompt, apiKey, model)
	llmDuration.WithLabelValues(model).Observe(float64(time.Since(start).Milliseconds()))
	if err != nil {
		llmErrors.WithLabelValues(model).Inc()
	}
	return result, err
}

func callGemini(prompt, apiKey, model string) (string, error) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)

	reqBody := GeminiRequest{
		Contents: []GeminiContent{
			{Role: "user", Parts: []GeminiPart{{Text: prompt}}},
		},
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
