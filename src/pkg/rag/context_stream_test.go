package rag

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// spyFlusher wraps httptest.ResponseRecorder and tracks flush calls.
type spyFlusher struct {
	*httptest.ResponseRecorder
	flushCount int
}

func (s *spyFlusher) Flush() {
	s.flushCount++
	// ResponseRecorder doesn't buffer-flush, but we track the calls.
}

func TestStreamLLMResponse_sendsOpenAIChunksAndDone(t *testing.T) {
	w := &spyFlusher{ResponseRecorder: httptest.NewRecorder()}

	tokens := []string{"Hello", " world", "!"}
	err := StreamLLMResponse(w, "gemini-3-flash-preview", func(onChunk func(string) error) error {
		for _, tok := range tokens {
			if err := onChunk(tok); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	body := w.Body.String()

	// Every token should appear in the SSE body.
	for _, tok := range tokens {
		if !strings.Contains(body, tok) {
			t.Errorf("token %q not found in body", tok)
		}
	}

	// Terminal marker must be present.
	if !strings.Contains(body, "data: [DONE]") {
		t.Error("missing data: [DONE] terminator")
	}

	// Content-Type header.
	ct := w.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	// At least one flush per token plus final DONE flush.
	if w.flushCount < len(tokens)+1 {
		t.Errorf("flushCount = %d, want >= %d", w.flushCount, len(tokens)+1)
	}
}

func TestStreamLLMResponse_propagatesStreamFnError(t *testing.T) {
	w := &spyFlusher{ResponseRecorder: httptest.NewRecorder()}
	boom := errors.New("upstream gone")

	err := StreamLLMResponse(w, "gemini-3-flash-preview", func(onChunk func(string) error) error {
		_ = onChunk("partial")
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("expected boom error, got %v", err)
	}
}

// noFlushWriter implements only http.ResponseWriter — deliberately omits http.Flusher.
// Cannot embed *httptest.ResponseRecorder because that type exposes Flush().
type noFlushWriter struct {
	header http.Header
	buf    bytes.Buffer
}

func (n *noFlushWriter) Header() http.Header         { return n.header }
func (n *noFlushWriter) WriteHeader(_ int)            {}
func (n *noFlushWriter) Write(b []byte) (int, error) { return n.buf.Write(b) }

func TestStreamLLMResponse_noFlusherReturnsError(t *testing.T) {
	w := &noFlushWriter{header: make(http.Header)}

	err := StreamLLMResponse(w, "model", func(onChunk func(string) error) error { return nil })
	if err == nil {
		t.Fatal("expected error when ResponseWriter does not implement http.Flusher")
	}
}
