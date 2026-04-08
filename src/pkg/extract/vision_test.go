package extract

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/piotrlaczykowski/emdexer/extractor"
)

// mockGeminiVision creates a test server that returns a canned Gemini Vision response.
func mockGeminiVision(t *testing.T, caption string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := visionResponse{
			Candidates: []visionCandidate{
				{Content: visionContent{Parts: []visionPart{{Text: caption}}}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// overrideVision patches geminiVisionBaseURL and newVisionHTTPClient for the given server.
// Returns a restore function to be deferred.
func overrideVision(ts *httptest.Server) func() {
	oldURL := geminiVisionBaseURL
	oldClient := newVisionHTTPClient
	geminiVisionBaseURL = ts.URL
	newVisionHTTPClient = func() *http.Client { return ts.Client() }
	return func() {
		geminiVisionBaseURL = oldURL
		newVisionHTTPClient = oldClient
	}
}

// TestVisionCaption_Success verifies that CaptionImage parses the Gemini response correctly.
func TestVisionCaption_Success(t *testing.T) {
	ts := mockGeminiVision(t, "A detailed image description with charts and text.")
	defer ts.Close()
	defer overrideVision(ts)()

	caption, err := CaptionImage(context.Background(), "fake-key", []byte("fake png"), "image/png")
	if err != nil {
		t.Fatalf("CaptionImage failed: %v", err)
	}
	if caption != "A detailed image description with charts and text." {
		t.Errorf("unexpected caption: %q", caption)
	}
}

// TestVisionCaption_TooLarge verifies that images exceeding VisionMaxSizeMB are skipped gracefully.
func TestVisionCaption_TooLarge(t *testing.T) {
	client := &Client{
		CB:              extractor.NewCircuitBreaker(5, time.Minute),
		HTTP:            http.DefaultClient,
		VisionEnabled:   true,
		VisionMaxSizeMB: 1,
		VisionAPIKey:    "key",
	}
	// 2 MB of data — over the 1 MB limit
	bigData := make([]byte, 2*1024*1024+1)

	text, _, err := client.ExtractFromBytes("photo.png", bigData, "http://unused")
	if err != nil {
		t.Fatalf("expected graceful skip (no error), got: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty text for oversized image, got %q", text)
	}
}

// TestVisionPriorityOverOCR verifies that when both VisionEnabled and EnableOCR are true,
// Gemini Vision is used and extraction_method is set in the extra metadata.
func TestVisionPriorityOverOCR(t *testing.T) {
	ts := mockGeminiVision(t, "Vision caption result.")
	defer ts.Close()
	defer overrideVision(ts)()

	// Extractous should NOT be called; use a server that fails loudly if hit.
	extractousCalled := false
	extractousServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		extractousCalled = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer extractousServer.Close()

	client := &Client{
		CB:              extractor.NewCircuitBreaker(5, time.Minute),
		HTTP:            http.DefaultClient,
		EnableOCR:       true,
		VisionEnabled:   true,
		VisionMaxSizeMB: 10,
		VisionAPIKey:    "key",
	}

	text, payload, err := client.ExtractFromBytes("photo.png", []byte("fake image"), extractousServer.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Vision caption result." {
		t.Errorf("expected vision caption, got %q", text)
	}
	if method, ok := payload["extraction_method"]; !ok || method != "gemini-vision" {
		t.Errorf("expected extraction_method=gemini-vision in payload, got %v", payload)
	}
	if extractousCalled {
		t.Error("Extractous should not be called when Vision is enabled")
	}
}
