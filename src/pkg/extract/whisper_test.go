package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/piotrlaczykowski/emdexer/extractor"
)

// whisperErrAfterReader returns an error once exactly n bytes have been delivered.
// Setting remaining=0 causes failure on the very first Read call, simulating a
// source that errors before any payload bytes can be streamed.
type whisperErrAfterReader struct {
	remaining int64
	err       error
}

func (r *whisperErrAfterReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, r.err
	}
	n := int64(len(p))
	if n > r.remaining {
		n = r.remaining
	}
	for i := int64(0); i < n; i++ {
		p[i] = 0
	}
	r.remaining -= n
	return int(n), nil
}

func TestWhisperTranscribe(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		ct := r.Header.Get("Content-Type")
		if ct == "" {
			t.Error("missing Content-Type header")
		}

		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}

		model := r.FormValue("model")
		if model != "base" {
			t.Errorf("expected model=base, got %q", model)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(whisperResponse{Text: "Hello world from audio"})
	}))
	defer ts.Close()

	client := &WhisperClient{
		URL:   ts.URL,
		Model: "base",
		HTTP:  ts.Client(),
		CB:    extractor.NewCircuitBreaker(5, time.Minute),
	}

	text, err := client.Transcribe(context.Background(), "test.mp3", bytes.NewReader([]byte("fake audio data")))
	if err != nil {
		t.Fatalf("Transcribe failed: %v", err)
	}
	if text != "Hello world from audio" {
		t.Errorf("expected 'Hello world from audio', got %q", text)
	}
}

func TestWhisperTranscribeError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer ts.Close()

	client := &WhisperClient{
		URL:  ts.URL,
		HTTP: ts.Client(),
		CB:   extractor.NewCircuitBreaker(5, time.Minute),
	}

	_, err := client.Transcribe(context.Background(), "test.mp3", bytes.NewReader([]byte("fake audio data")))
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestWhisperNotConfigured(t *testing.T) {
	client := &WhisperClient{
		URL:  "",
		HTTP: http.DefaultClient,
		CB:   extractor.NewCircuitBreaker(5, time.Minute),
	}

	_, err := client.Transcribe(context.Background(), "test.mp3", bytes.NewReader([]byte("data")))
	if err == nil {
		t.Error("expected error when URL is empty")
	}
}

func TestWhisperHealth(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := &WhisperClient{
		URL:  ts.URL,
		HTTP: ts.Client(),
		CB:   extractor.NewCircuitBreaker(5, time.Minute),
	}

	if err := client.Health(); err != nil {
		t.Errorf("Health check failed: %v", err)
	}
}

// TestWhisperTranscribe_MidStreamFailure verifies that when the source io.Reader
// returns an error, that error propagates back through the io.Pipe, aborts the
// in-flight HTTP request, and surfaces from Transcribe.
//
// Flow:
//  1. Goroutine writes the multipart file-part header to pw (HTTP transport reads it).
//  2. io.Copy calls r.Read — whisperErrAfterReader(remaining=0) returns sentinelErr.
//  3. Goroutine calls pw.CloseWithError(wrapped sentinelErr).
//  4. HTTP transport's next read from pr returns that error → client.Do fails.
//  5. Transcribe returns an error whose chain contains sentinelErr.
func TestWhisperTranscribe_MidStreamFailure(t *testing.T) {
	sentinelErr := errors.New("simulated source read failure")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain whatever bytes arrived before the connection is reset.
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	client := &WhisperClient{
		URL:   ts.URL,
		Model: "base",
		HTTP:  ts.Client(),
		CB:    extractor.NewCircuitBreaker(5, time.Minute),
	}

	r := &whisperErrAfterReader{remaining: 0, err: sentinelErr}
	_, err := client.Transcribe(context.Background(), "failing.mp3", r)
	if err == nil {
		t.Fatal("expected error when source reader fails; got nil")
	}
	if !errors.Is(err, sentinelErr) {
		t.Errorf("expected error chain to contain sentinel error\ngot: %v", err)
	}
}

func TestIsAudioExt(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"song.mp3", true},
		{"video.mp4", true},
		{"movie.mkv", true},
		{"audio.wav", true},
		{"music.flac", true},
		{"voice.ogg", true},
		{"clip.webm", true},
		{"voice.m4a", true},
		{"doc.pdf", false},
		{"image.png", false},
		{"code.go", false},
	}
	for _, tt := range tests {
		if got := IsAudioExt(tt.path); got != tt.want {
			t.Errorf("IsAudioExt(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
