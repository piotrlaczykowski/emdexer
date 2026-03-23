package extract

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestFrameExtraction_MaxCap verifies that the client caps returned frames at MaxFrames
// even when the sidecar returns more.
func TestFrameExtraction_MaxCap(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return 15 frames — more than MaxFrames=10
		entries := make([]ffmpegFrameEntry, 15)
		for i := range entries {
			entries[i] = ffmpegFrameEntry{
				TimestampSec: i * 30,
				Data:         base64.StdEncoding.EncodeToString([]byte("fake-jpeg")),
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ffmpegFrameResponse{Frames: entries})
	}))
	defer ts.Close()

	client := &FFmpegClient{
		URL:         ts.URL,
		HTTP:        ts.Client(),
		IntervalSec: 30,
		MaxFrames:   10,
	}

	frames, err := client.ExtractFrames(context.Background(), []byte("fake video"), "video.mp4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(frames) != 10 {
		t.Errorf("expected 10 frames (MaxFrames cap), got %d", len(frames))
	}
}

// TestFrameExtraction_DisabledWhenNoURL verifies that an empty URL returns an error.
func TestFrameExtraction_DisabledWhenNoURL(t *testing.T) {
	client := &FFmpegClient{
		URL:  "",
		HTTP: http.DefaultClient,
	}

	frames, err := client.ExtractFrames(context.Background(), []byte("fake video"), "video.mp4")
	if err == nil {
		t.Error("expected error when FFmpegClient URL is empty")
	}
	if len(frames) != 0 {
		t.Errorf("expected no frames when URL is empty, got %d", len(frames))
	}
}

// TestFrameExtraction_TimestampPreserved verifies that frame timestamps are preserved correctly.
func TestFrameExtraction_TimestampPreserved(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entries := []ffmpegFrameEntry{
			{TimestampSec: 0, Data: base64.StdEncoding.EncodeToString([]byte("jpeg1"))},
			{TimestampSec: 30, Data: base64.StdEncoding.EncodeToString([]byte("jpeg2"))},
			{TimestampSec: 60, Data: base64.StdEncoding.EncodeToString([]byte("jpeg3"))},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ffmpegFrameResponse{Frames: entries})
	}))
	defer ts.Close()

	client := &FFmpegClient{
		URL:         ts.URL,
		HTTP:        ts.Client(),
		IntervalSec: 30,
		MaxFrames:   10,
	}

	frames, err := client.ExtractFrames(context.Background(), []byte("fake video"), "video.mp4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(frames) != 3 {
		t.Fatalf("expected 3 frames, got %d", len(frames))
	}
	if frames[0].TimestampSec != 0 || frames[1].TimestampSec != 30 || frames[2].TimestampSec != 60 {
		t.Errorf("unexpected timestamps: %v, %v, %v", frames[0].TimestampSec, frames[1].TimestampSec, frames[2].TimestampSec)
	}
}
