package extract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/piotrlaczykowski/emdexer/extractor"
)

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

	text, err := client.Transcribe("test.mp3", []byte("fake audio data"))
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

	_, err := client.Transcribe("test.mp3", []byte("fake audio data"))
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

	_, err := client.Transcribe("test.mp3", []byte("data"))
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
