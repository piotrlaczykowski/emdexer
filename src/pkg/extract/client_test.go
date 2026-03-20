package extract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/piotrlaczykowski/emdexer/extractor"
)

func newTestClient(handler http.Handler) (*Client, *httptest.Server) {
	ts := httptest.NewServer(handler)
	return &Client{
		CB:        extractor.NewCircuitBreaker(5, time.Minute),
		HTTP:      ts.Client(),
		EnableOCR: true,
	}, ts
}

func TestExtractFromBytes_TextFile(t *testing.T) {
	client := &Client{
		CB:   extractor.NewCircuitBreaker(5, time.Minute),
		HTTP: http.DefaultClient,
	}

	text, err := client.ExtractFromBytes("readme.txt", []byte("hello world"), "http://unused")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "hello world" {
		t.Errorf("expected 'hello world', got %q", text)
	}
}

func TestExtractFromBytes_ImageOCR(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify OCR flag is set
		if r.URL.Query().Get("ocr") != "true" {
			t.Error("expected ocr=true query parameter for image")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Result{Text: "OCR extracted text"})
	})

	client, ts := newTestClient(handler)
	defer ts.Close()

	text, err := client.ExtractFromBytes("scan.png", []byte("fake image"), ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "OCR extracted text" {
		t.Errorf("expected 'OCR extracted text', got %q", text)
	}
}

func TestExtractFromBytes_ImageOCRDisabled(t *testing.T) {
	client := &Client{
		CB:        extractor.NewCircuitBreaker(5, time.Minute),
		HTTP:      http.DefaultClient,
		EnableOCR: false,
	}

	_, err := client.ExtractFromBytes("scan.png", []byte("fake image"), "http://unused")
	if err == nil {
		t.Error("expected error when OCR is disabled for image files")
	}
}

func TestExtractFromBytes_AudioWhisper(t *testing.T) {
	whisperServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": "Transcribed audio"})
	}))
	defer whisperServer.Close()

	client := &Client{
		CB:   extractor.NewCircuitBreaker(5, time.Minute),
		HTTP: whisperServer.Client(),
		Whisper: &WhisperClient{
			URL:   whisperServer.URL,
			Model: "base",
			HTTP:  whisperServer.Client(),
			CB:    extractor.NewCircuitBreaker(5, time.Minute),
		},
	}

	text, err := client.ExtractFromBytes("podcast.mp3", []byte("fake audio"), "http://unused")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Transcribed audio" {
		t.Errorf("expected 'Transcribed audio', got %q", text)
	}
}

func TestExtractFromBytes_AudioNoWhisper(t *testing.T) {
	client := &Client{
		CB:      extractor.NewCircuitBreaker(5, time.Minute),
		HTTP:    http.DefaultClient,
		Whisper: nil,
	}

	_, err := client.ExtractFromBytes("podcast.mp3", []byte("fake audio"), "http://unused")
	if err == nil {
		t.Error("expected error when Whisper is not configured")
	}
}

func TestExtractFromBytes_PDFOCRFallback(t *testing.T) {
	callCount := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("ocr") == "true" {
			// OCR retry returns good text
			_ = json.NewEncoder(w).Encode(Result{Text: "OCR extracted from scanned PDF with lots of content"})
		} else {
			// First attempt returns near-zero text (scanned PDF)
			_ = json.NewEncoder(w).Encode(Result{Text: ""})
		}
	})

	client, ts := newTestClient(handler)
	defer ts.Close()

	text, err := client.ExtractFromBytes("scanned.pdf", []byte("fake pdf"), ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(text, "OCR extracted from scanned PDF") {
		t.Errorf("expected OCR fallback text, got %q", text)
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls (initial + OCR retry), got %d", callCount)
	}
}

func TestExtractFromBytes_PDFNoFallbackNeeded(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ocr") == "true" {
			t.Error("OCR retry should not be triggered for text-rich PDF")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Result{Text: "This is a text-rich PDF with plenty of content that should not trigger OCR fallback"})
	})

	client, ts := newTestClient(handler)
	defer ts.Close()

	text, err := client.ExtractFromBytes("normal.pdf", []byte("fake pdf"), ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(text, "text-rich PDF") {
		t.Errorf("unexpected text: %q", text)
	}
}

func TestExtractFromBytes_Extractous(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ocr") == "true" {
			t.Error("OCR flag should not be set for DOCX")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Result{Text: "Document content"})
	})

	client, ts := newTestClient(handler)
	defer ts.Close()

	text, err := client.ExtractFromBytes("report.docx", []byte("fake docx"), ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Document content" {
		t.Errorf("expected 'Document content', got %q", text)
	}
}

func TestImageExtsMap(t *testing.T) {
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".tiff", ".tif", ".bmp"} {
		if !imageExts[ext] {
			t.Errorf("expected imageExts[%q] = true", ext)
		}
	}
	for _, ext := range []string{".pdf", ".mp3", ".docx", ".go"} {
		if imageExts[ext] {
			t.Errorf("expected imageExts[%q] = false", ext)
		}
	}
}
