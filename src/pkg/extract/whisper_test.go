package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/piotrlaczykowski/emdexer/extractor"
)

func newWhisperClient(ts *httptest.Server) *WhisperClient {
	return &WhisperClient{
		URL:     ts.URL,
		Model:   "base",
		HTTP:    ts.Client(),
		CB:      extractor.NewCircuitBreaker(5, time.Minute),
		Enabled: true,
	}
}

func TestWhisperTranscribe(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		if model := r.FormValue("model"); model != "base" {
			t.Errorf("expected model=base, got %q", model)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(whisperResponse{Text: "Hello world from audio"})
	}))
	defer ts.Close()

	text, _, err := newWhisperClient(ts).Transcribe(context.Background(), "test.mp3", []byte("fake audio data"))
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

	_, _, err := newWhisperClient(ts).Transcribe(context.Background(), "test.mp3", []byte("fake audio data"))
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestWhisperNotConfigured(t *testing.T) {
	client := &WhisperClient{
		URL:     "",
		HTTP:    http.DefaultClient,
		CB:      extractor.NewCircuitBreaker(5, time.Minute),
		Enabled: true,
	}
	_, _, err := client.Transcribe(context.Background(), "test.mp3", []byte("data"))
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

	client := &WhisperClient{URL: ts.URL, HTTP: ts.Client(), CB: extractor.NewCircuitBreaker(5, time.Minute)}
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
		{"film.avi", true},
		{"clip.mov", true},
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

// TestWhisperRetry_503Backoff verifies that the client retries on HTTP 503
// and succeeds on the third attempt.
func TestWhisperRetry_503Backoff(t *testing.T) {
	// Use zero-duration backoffs so the test runs in milliseconds.
	old := whisperBackoffs
	whisperBackoffs = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	defer func() { whisperBackoffs = old }()

	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(whisperResponse{Text: "Recovered after retry. This is a sufficiently long transcript."})
	}))
	defer ts.Close()

	text, _, err := newWhisperClient(ts).Transcribe(context.Background(), "test.mp3", []byte("fake audio"))
	if err != nil {
		t.Fatalf("unexpected error after retry: %v", err)
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls (2×503 + success), got %d", callCount)
	}
	_ = text
}

// TestWhisperQualityFilter_TooShort verifies that transcripts below MinChars are rejected.
func TestWhisperQualityFilter_TooShort(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(whisperResponse{Text: "Hi."})
	}))
	defer ts.Close()

	client := newWhisperClient(ts)
	client.MinChars = 50

	_, _, err := client.Transcribe(context.Background(), "test.mp3", []byte("fake audio"))
	if err == nil {
		t.Error("expected error for transcript shorter than MinChars")
	}
}

// TestWhisperSegmentMetadata verifies that segments from verbose_json are returned.
func TestWhisperSegmentMetadata(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(whisperResponse{
			Text: "Hello world this is a test transcript long enough to pass the filter.",
			Segments: []WhisperSegment{
				{Start: 0.0, End: 2.5, Text: "Hello world"},
				{Start: 2.5, End: 5.0, Text: "this is a test transcript."},
			},
		})
	}))
	defer ts.Close()

	_, segs, err := newWhisperClient(ts).Transcribe(context.Background(), "test.mp3", []byte("fake audio"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segs))
	}
	if segs[0].Start != 0.0 || segs[0].End != 2.5 {
		t.Errorf("segment 0: got start=%.1f end=%.1f, want start=0.0 end=2.5", segs[0].Start, segs[0].End)
	}
	if segs[1].Start != 2.5 || segs[1].End != 5.0 {
		t.Errorf("segment 1: got start=%.1f end=%.1f, want start=2.5 end=5.0", segs[1].Start, segs[1].End)
	}
}

// TestWhisperDisabled_SkipsAudio verifies that audio files are skipped gracefully
// when EMDEX_WHISPER_ENABLED=false, without calling the sidecar.
func TestWhisperDisabled_SkipsAudio(t *testing.T) {
	serverCalled := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := &Client{
		CB:   extractor.NewCircuitBreaker(5, time.Minute),
		HTTP: ts.Client(),
		Whisper: &WhisperClient{
			URL:     ts.URL,
			Model:   "base",
			HTTP:    ts.Client(),
			CB:      extractor.NewCircuitBreaker(5, time.Minute),
			Enabled: false, // disabled
		},
	}

	text, _, err := client.ExtractFromBytes("audio.mp3", []byte("fake audio"), "http://unused")
	if err != nil {
		t.Fatalf("expected graceful skip (no error), got: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty text when whisper disabled, got %q", text)
	}
	if serverCalled {
		t.Error("whisper server must not be called when Enabled=false")
	}
}

// TestWhisperLanguageHint verifies the language field is sent to the sidecar.
func TestWhisperLanguageHint(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		if lang := r.FormValue("language"); lang != "en" {
			t.Errorf("expected language=en, got %q", lang)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(whisperResponse{Text: "Language hint test transcript, long enough to pass the filter check."})
	}))
	defer ts.Close()

	client := newWhisperClient(ts)
	client.Language = "en"

	_, _, err := client.Transcribe(context.Background(), "test.mp3", bytes.NewBufferString("fake audio").Bytes())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
