package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/piotrlaczykowski/emdexer/extractor"
)

// audioExts are file extensions handled by the Whisper sidecar (audio and video).
var audioExts = map[string]bool{
	".mp3": true, ".wav": true, ".mp4": true, ".mkv": true,
	".m4a": true, ".ogg": true, ".flac": true, ".webm": true,
	".avi": true, ".mov": true,
}

// videoExts are a subset of audioExts for files with video tracks (eligible for frame extraction).
var videoExts = map[string]bool{
	".mp4": true, ".mkv": true, ".avi": true, ".mov": true, ".webm": true,
}

// IsAudioExt returns true if the file extension is handled by the Whisper sidecar.
func IsAudioExt(path string) bool {
	return audioExts[strings.ToLower(filepath.Ext(path))]
}

// IsVideoExt returns true for video formats eligible for frame extraction.
func IsVideoExt(path string) bool {
	return videoExts[strings.ToLower(filepath.Ext(path))]
}

// whisperBackoffs holds the sleep durations between retry attempts on HTTP 503.
// Tests may override this to avoid slow unit tests.
var whisperBackoffs = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// Prometheus metrics for the Whisper sidecar.
var (
	whisperRetriesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "emdexer_node_whisper_retries_total",
		Help: "Total number of Whisper HTTP 503 retries",
	})
	whisperSkippedShortTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "emdexer_node_whisper_skipped_short_total",
		Help: "Total number of Whisper transcripts skipped for being below the minimum character threshold",
	})
	whisperAudioSkippedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "emdexer_node_audio_skipped_total",
		Help: "Total number of audio files skipped because EMDEX_WHISPER_ENABLED is false",
	})
)

// WhisperSegment represents a timed segment from a verbose_json Whisper response.
type WhisperSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// WhisperClient calls an OpenAI-compatible /v1/audio/transcriptions endpoint
// hosted by a whisper.cpp sidecar. Zero tokens burned — all processing is local.
type WhisperClient struct {
	URL      string // e.g. "http://whisper:8080"
	Model    string // e.g. "base", "small", "medium"
	HTTP     *http.Client
	CB       *extractor.CircuitBreaker
	Enabled  bool   // must be true to transcribe; controlled by EMDEX_WHISPER_ENABLED
	MinChars int    // skip transcripts shorter than this; 0 = no filter (EMDEX_WHISPER_MIN_CHARS)
	Language string // optional language hint passed to the API (EMDEX_WHISPER_LANGUAGE)
}

// whisperResponse is the JSON response from the whisper.cpp server (verbose_json format).
type whisperResponse struct {
	Text     string           `json:"text"`
	Segments []WhisperSegment `json:"segments"`
}

// Transcribe sends audio/video content to the Whisper sidecar and returns the transcript
// and optional timed segments. On HTTP 503 the call is retried up to 3 times with
// exponential back-off (1 s, 2 s, 4 s). The MinChars quality filter is applied after
// a successful HTTP response.
func (w *WhisperClient) Transcribe(ctx context.Context, filename string, data []byte) (string, []WhisperSegment, error) {
	if w.URL == "" {
		return "", nil, fmt.Errorf("whisper sidecar not configured (EMDEX_WHISPER_URL is empty)")
	}
	if !w.CB.Allow() {
		return "", nil, fmt.Errorf("whisper circuit breaker open")
	}

	var lastErr error
	for attempt := 0; attempt <= len(whisperBackoffs); attempt++ {
		text, segs, statusCode, err := w.transcribeOnce(ctx, filename, data)
		if err == nil {
			w.CB.RecordSuccess()
			if w.MinChars > 0 && len(strings.TrimSpace(text)) < w.MinChars {
				whisperSkippedShortTotal.Inc()
				return "", nil, fmt.Errorf("whisper: transcript too short (%d chars, min %d)",
					len(strings.TrimSpace(text)), w.MinChars)
			}
			return text, segs, nil
		}
		if statusCode == http.StatusServiceUnavailable && attempt < len(whisperBackoffs) {
			whisperRetriesTotal.Inc()
			log.Printf("[extract] Whisper 503 for %s — retry %d/%d after %v",
				filename, attempt+1, len(whisperBackoffs), whisperBackoffs[attempt])
			time.Sleep(whisperBackoffs[attempt])
			lastErr = err
			continue
		}
		w.CB.RecordFailure()
		return "", nil, err
	}
	w.CB.RecordFailure()
	return "", nil, lastErr
}

// transcribeOnce performs a single POST /v1/audio/transcriptions call.
// It returns the parsed text, segments, the HTTP status code, and any error.
func (w *WhisperClient) transcribeOnce(ctx context.Context, filename string, data []byte) (
	text string, segs []WhisperSegment, statusCode int, err error,
) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, createErr := writer.CreateFormFile("file", filepath.Base(filename))
	if createErr != nil {
		err = fmt.Errorf("whisper: create form file: %w", createErr)
		return
	}
	if _, copyErr := io.Copy(part, bytes.NewReader(data)); copyErr != nil {
		err = fmt.Errorf("whisper: buffer content: %w", copyErr)
		return
	}

	model := w.Model
	if model == "" {
		model = "base"
	}
	_ = writer.WriteField("model", model)
	_ = writer.WriteField("response_format", "verbose_json")
	if w.Language != "" {
		_ = writer.WriteField("language", w.Language)
	}
	if closeErr := writer.Close(); closeErr != nil {
		err = fmt.Errorf("whisper: close writer: %w", closeErr)
		return
	}

	req, reqErr := http.NewRequestWithContext(ctx, "POST", w.URL+"/v1/audio/transcriptions", &buf)
	if reqErr != nil {
		err = fmt.Errorf("whisper: create request: %w", reqErr)
		return
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, doErr := w.HTTP.Do(req)
	if doErr != nil {
		err = fmt.Errorf("whisper: request failed: %w", doErr)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	statusCode = resp.StatusCode
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		err = fmt.Errorf("whisper: HTTP %d: %s", resp.StatusCode, string(body))
		return
	}

	var result whisperResponse
	if decodeErr := json.NewDecoder(resp.Body).Decode(&result); decodeErr != nil {
		err = fmt.Errorf("whisper: decode response: %w", decodeErr)
		return
	}

	return strings.TrimSpace(result.Text), result.Segments, resp.StatusCode, nil
}

// Health checks if the Whisper sidecar is reachable.
func (w *WhisperClient) Health() error {
	if w.URL == "" {
		return fmt.Errorf("not configured")
	}
	resp, err := w.HTTP.Get(w.URL + "/health")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
