package extract

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/piotrlaczykowski/emdexer/safenet"
)

// geminiVisionBaseURL is the base URL for Gemini API calls. Override in tests.
var geminiVisionBaseURL = "https://generativelanguage.googleapis.com"

// newVisionHTTPClient creates the HTTP client for vision API calls.
// Tests may override this to bypass SSRF guards and point at httptest servers.
var newVisionHTTPClient = func() *http.Client {
	return safenet.NewSafeClient(30 * time.Second)
}

const visionPrompt = "Describe the content of this image in detail. Focus on any text, charts, diagrams, objects, and scenes visible."

// Prometheus metrics for the Gemini Vision path.
var (
	visionCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "emdexer_node_vision_calls_total",
		Help: "Total number of Gemini Vision caption calls by status (ok/error/skipped_size)",
	}, []string{"status"})
	visionDurationMs = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "emdexer_node_vision_duration_ms",
		Help:    "Latency of Gemini Vision API calls in milliseconds",
		Buckets: []float64{200, 500, 1000, 2000, 5000, 10000, 20000},
	})
)

// visionInlineData is the Gemini API inline image data part.
type visionInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64-encoded
}

// visionPart is a single part in a Gemini content message.
type visionPart struct {
	Text       string            `json:"text,omitempty"`
	InlineData *visionInlineData `json:"inlineData,omitempty"`
}

// visionContent is a Gemini content object.
type visionContent struct {
	Parts []visionPart `json:"parts"`
}

// visionRequest is the Gemini generateContent request body for multimodal input.
type visionRequest struct {
	Contents []visionContent `json:"contents"`
}

// visionCandidate is a single candidate in the Gemini response.
type visionCandidate struct {
	Content visionContent `json:"content"`
}

// visionResponse is the Gemini generateContent response.
type visionResponse struct {
	Candidates []visionCandidate `json:"candidates"`
}

// visionModel returns the LLM model name from EMDEX_LLM_MODEL.
func visionModel() string {
	if m := os.Getenv("EMDEX_LLM_MODEL"); m != "" {
		return m
	}
	return "gemini-3-flash-preview"
}

// imageMimeType returns the MIME type for a given image file extension.
func imageMimeType(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".tiff", ".tif":
		return "image/tiff"
	case ".bmp":
		return "image/bmp"
	default:
		return "image/jpeg"
	}
}

// imageMimeTypeFromPath returns the MIME type for an image file path.
func imageMimeTypeFromPath(path string) string {
	return imageMimeType(filepath.Ext(path))
}

// CaptionImage sends image bytes to Gemini Vision and returns a text description.
// Uses EMDEX_LLM_MODEL (default: gemini-3-flash-preview).
// On error: logs a warning and returns an empty string (never blocks indexing).
func CaptionImage(ctx context.Context, apiKey string, data []byte, mimeType string) (string, error) {
	model := visionModel()
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s",
		geminiVisionBaseURL, model, apiKey)

	reqBody := visionRequest{
		Contents: []visionContent{
			{
				Parts: []visionPart{
					{
						InlineData: &visionInlineData{
							MimeType: mimeType,
							Data:     base64.StdEncoding.EncodeToString(data),
						},
					},
					{Text: visionPrompt},
				},
			},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("vision: marshal request: %w", err)
	}

	client := newVisionHTTPClient()
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("vision: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[extract] WARN: Gemini Vision request failed: %v", err)
		return "", fmt.Errorf("vision: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("vision: Gemini API %d: %s", resp.StatusCode, string(b))
	}

	var vr visionResponse
	if err := json.NewDecoder(resp.Body).Decode(&vr); err != nil {
		return "", fmt.Errorf("vision: decode response: %w", err)
	}

	if len(vr.Candidates) == 0 || len(vr.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("vision: no content in Gemini response")
	}

	return strings.TrimSpace(vr.Candidates[0].Content.Parts[0].Text), nil
}

// captionWithMetrics wraps CaptionImage with Prometheus instrumentation.
func captionWithMetrics(ctx context.Context, apiKey string, data []byte, mimeType string) (string, error) {
	start := time.Now()
	caption, err := CaptionImage(ctx, apiKey, data, mimeType)
	visionDurationMs.Observe(float64(time.Since(start).Milliseconds()))
	if err != nil {
		visionCallsTotal.WithLabelValues("error").Inc()
	} else {
		visionCallsTotal.WithLabelValues("ok").Inc()
	}
	return caption, err
}
