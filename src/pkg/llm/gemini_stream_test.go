package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sseChunk builds a minimal Gemini SSE data line for a given text token.
func sseChunk(text string) string {
	resp := GeminiResponse{
		Candidates: []GeminiCandidate{
			{Content: GeminiContent{Parts: []GeminiPart{{Text: text}}}},
		},
	}
	b, _ := json.Marshal(resp)
	return fmt.Sprintf("data: %s\n\n", string(b))
}

func TestCallGeminiStreamAt_collectsChunks(t *testing.T) {
	want := []string{"Hello", ", world", "!"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, chunk := range want {
			fmt.Fprint(w, sseChunk(chunk))
			flusher.Flush()
		}
	}))
	defer srv.Close()

	var got []string
	err := callGeminiStreamAt(context.Background(), srv.URL, GeminiRequest{
		Contents: []GeminiContent{{Role: "user", Parts: []GeminiPart{{Text: "ping"}}}},
	}, http.DefaultClient, func(text string) error {
		got = append(got, text)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(got, "") != strings.Join(want, "") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCallGeminiStreamAt_propagatesNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "quota exceeded", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	err := callGeminiStreamAt(context.Background(), srv.URL, GeminiRequest{}, http.DefaultClient, func(_ string) error { return nil })
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

func TestCallGeminiStreamAt_skipsEmptyAndMalformedLines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "\n")                 // blank line — skip
		fmt.Fprint(w, ": keepalive\n\n")    // SSE comment — skip
		fmt.Fprint(w, "data: not-json\n\n") // malformed — skip
		fmt.Fprint(w, sseChunk("token"))    // valid
		flusher.Flush()
	}))
	defer srv.Close()

	var got []string
	_ = callGeminiStreamAt(context.Background(), srv.URL, GeminiRequest{}, http.DefaultClient, func(t string) error {
		got = append(got, t)
		return nil
	})
	if len(got) != 1 || got[0] != "token" {
		t.Fatalf("expected exactly one chunk 'token', got %v", got)
	}
}

func TestCallGeminiStreamAt_contextCancellation(t *testing.T) {
	ready := make(chan struct{})
	done := make(chan struct{}) // closed by the test to unblock the handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(ready)
		<-done // hang until the test signals completion
	}))
	defer func() {
		close(done)            // unblock the handler goroutine
		srv.CloseClientConnections()
		srv.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-ready
		cancel()
	}()
	err := callGeminiStreamAt(ctx, srv.URL, GeminiRequest{}, http.DefaultClient, func(_ string) error { return nil })
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestCallGeminiStream_metricsAndChunks(t *testing.T) {
	want := []string{"chunk1", "chunk2"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, c := range want {
			fmt.Fprint(w, sseChunk(c))
			flusher.Flush()
		}
	}))
	defer srv.Close()

	// Override the endpoint format so CallGeminiStream hits the test server.
	// The format has two %s slots: model and apiKey (both ignored by the test server).
	origFmt := geminiStreamEndpointFmt
	geminiStreamEndpointFmt = srv.URL + "?model=%s&key=%s"
	defer func() { geminiStreamEndpointFmt = origFmt }()

	// Override the client factory to bypass the SSRF guard for the loopback address.
	origClient := geminiStreamClientFn
	geminiStreamClientFn = func() *http.Client { return http.DefaultClient }
	defer func() { geminiStreamClientFn = origClient }()

	var got []string
	err := CallGeminiStream(context.Background(), "hello", "test-key", func(text string) error {
		got = append(got, text)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(got, "") != strings.Join(want, "") {
		t.Fatalf("got %v want %v", got, want)
	}
}
