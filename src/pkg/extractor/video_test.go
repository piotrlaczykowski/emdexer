package extractor

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVideoSampler_ProcessWhisper(t *testing.T) {
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

		// We don't need to parse the whole multipart form here, just verify it's a valid request.
		// If we wanted to be more thorough, we could use mime/multipart.NewReader.

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(whisperTranscription{Text: "Transcribed text from video"})
	}))
	defer ts.Close()

	v := &VideoSampler{
		WhisperURL:   ts.URL,
		WhisperModel: "base",
		HTTP:         ts.Client(),
	}

	text, err := v.ProcessWhisper(context.Background(), "test.mp4", bytes.NewReader([]byte("fake video data")))
	if err != nil {
		t.Fatalf("ProcessWhisper failed: %v", err)
	}

	if text != "Transcribed text from video" {
		t.Errorf("expected 'Transcribed text from video', got %q", text)
	}
}

func TestVideoSampler_SampleFrames(t *testing.T) {
	v := &VideoSampler{}
	_, err := v.SampleFrames(context.Background(), "test.mp4")
	if err == nil {
		t.Error("expected error for SampleFrames (not implemented)")
	}
}
