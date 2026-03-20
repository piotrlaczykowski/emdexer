package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/piotrlaczykowski/emdexer/extractor"
)

// audioExts are file extensions routed to the Whisper sidecar for transcription.
var audioExts = map[string]bool{
	".mp3": true, ".wav": true, ".mp4": true, ".mkv": true,
	".m4a": true, ".ogg": true, ".flac": true, ".webm": true,
}

// IsAudioExt returns true if the file extension is handled by the Whisper sidecar.
func IsAudioExt(path string) bool {
	return audioExts[strings.ToLower(filepath.Ext(path))]
}

// WhisperClient calls an OpenAI-compatible /v1/audio/transcriptions endpoint
// hosted by a whisper.cpp sidecar. Zero tokens burned — all processing is local.
type WhisperClient struct {
	URL   string // e.g. "http://whisper:8080"
	Model string // e.g. "base", "small", "medium"
	HTTP  *http.Client
	CB    *extractor.CircuitBreaker
}

// whisperResponse is the JSON response from the whisper.cpp server.
type whisperResponse struct {
	Text string `json:"text"`
}

// Transcribe sends audio/video content from r to the Whisper sidecar and returns the transcript.
// The endpoint is OpenAI-compatible: POST /v1/audio/transcriptions with multipart form data.
// r is streamed via io.Pipe so memory usage stays constant regardless of file size.
func (w *WhisperClient) Transcribe(ctx context.Context, filename string, r io.Reader) (string, error) {
	if w.URL == "" {
		return "", fmt.Errorf("whisper sidecar not configured (EMDEX_WHISPER_URL is empty)")
	}

	if !w.CB.Allow() {
		return "", fmt.Errorf("whisper circuit breaker open")
	}

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	go func() {
		var werr error
		defer func() { pw.CloseWithError(werr) }()

		part, err := writer.CreateFormFile("file", filepath.Base(filename))
		if err != nil {
			werr = fmt.Errorf("whisper: create form file: %w", err)
			return
		}
		if _, err = io.Copy(part, r); err != nil {
			werr = fmt.Errorf("whisper: stream content: %w", err)
			return
		}

		// Model field (required by OpenAI-compatible API).
		model := w.Model
		if model == "" {
			model = "base"
		}
		_ = writer.WriteField("model", model)

		// Response format: json for structured output.
		_ = writer.WriteField("response_format", "json")

		werr = writer.Close()
	}()

	req, err := http.NewRequestWithContext(ctx, "POST", w.URL+"/v1/audio/transcriptions", pr)
	if err != nil {
		_ = pr.CloseWithError(err) // unblock the writer goroutine
		return "", fmt.Errorf("whisper: create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := w.HTTP.Do(req)
	if err != nil {
		w.CB.RecordFailure()
		return "", fmt.Errorf("whisper: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		w.CB.RecordFailure()
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("whisper: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	w.CB.RecordSuccess()

	var result whisperResponse
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("whisper: decode response: %w", err)
	}

	return strings.TrimSpace(result.Text), nil
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
