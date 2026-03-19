package extractor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
)

// VideoSampler extracts semantic content from audio and video files by sending
// them to a whisper.cpp sidecar for transcription. Zero tokens burned — all
// processing is done locally by the C++ sidecar.
//
// Supported formats: .mp3, .wav, .mp4, .mkv, .m4a, .ogg, .flac, .webm
type VideoSampler struct {
	WhisperURL   string       // e.g. "http://whisper:8080"
	WhisperModel string       // e.g. "base", "small", "medium"
	HTTP         *http.Client // reuse from caller; nil = http.DefaultClient
}

// whisperTranscription matches the OpenAI-compatible whisper.cpp JSON response.
type whisperTranscription struct {
	Text string `json:"text"`
}

// ProcessWhisper transcribes audio content using the whisper.cpp sidecar.
// It posts to the OpenAI-compatible /v1/audio/transcriptions endpoint.
func (v *VideoSampler) ProcessWhisper(ctx context.Context, filename string, audioContent []byte) (string, error) {
	if v.WhisperURL == "" {
		return "", fmt.Errorf("whisper: sidecar URL not configured (EMDEX_WHISPER_URL)")
	}

	client := v.HTTP
	if client == nil {
		client = http.DefaultClient
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", filepath.Base(filename))
	if err != nil {
		return "", fmt.Errorf("whisper: create form file: %w", err)
	}
	if _, err = part.Write(audioContent); err != nil {
		return "", fmt.Errorf("whisper: write content: %w", err)
	}

	model := v.WhisperModel
	if model == "" {
		model = "base"
	}
	_ = writer.WriteField("model", model)
	_ = writer.WriteField("response_format", "json")

	if err = writer.Close(); err != nil {
		return "", fmt.Errorf("whisper: close writer: %w", err)
	}

	endpoint := v.WhisperURL + "/v1/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, body)
	if err != nil {
		return "", fmt.Errorf("whisper: create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("whisper: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("whisper: HTTP %d", resp.StatusCode)
	}

	var result whisperTranscription
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("whisper: decode response: %w", err)
	}

	return strings.TrimSpace(result.Text), nil
}

// SampleFrames is reserved for future visual frame extraction (ffmpeg + vision model).
// Currently returns an empty string — audio transcription via ProcessWhisper is the
// primary extraction path for video files.
func (v *VideoSampler) SampleFrames(ctx context.Context, videoPath string) (string, error) {
	return "", fmt.Errorf("video frame extraction not yet implemented: audio transcription via Whisper is the primary path")
}
