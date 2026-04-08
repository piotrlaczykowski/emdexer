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

func ollamaNonStreamResp(content string) []byte {
	cr := ollamaChatResponse{
		Choices: []ollamaChoice{
			{Message: ollamaMessage{Role: "assistant", Content: content}},
		},
	}
	b, _ := json.Marshal(cr)
	return b
}

func ollamaSSEChunk(content string) string {
	cr := ollamaChatResponse{
		Choices: []ollamaChoice{
			{Delta: ollamaMessage{Role: "assistant", Content: content}},
		},
	}
	b, _ := json.Marshal(cr)
	return fmt.Sprintf("data: %s\n\n", string(b))
}

func TestCallOllama_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(ollamaNonStreamResp("Hello from Ollama"))
	}))
	defer srv.Close()

	got, err := CallOllama(context.Background(), "ping", srv.URL, "gemma4:26b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Hello from Ollama" {
		t.Fatalf("got %q, want %q", got, "Hello from Ollama")
	}
}

func TestCallOllama_httpError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := CallOllama(context.Background(), "ping", srv.URL, "gemma4:26b")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected 500 in error, got: %v", err)
	}
}

func TestCallOllamaStream_success(t *testing.T) {
	want := []string{"Hello", ", world", "!"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, chunk := range want {
			fmt.Fprint(w, ollamaSSEChunk(chunk))
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	var got []string
	err := CallOllamaStream(context.Background(), "ping", srv.URL, "gemma4:26b", func(text string) error {
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

func TestCallOllamaStream_done(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, ollamaSSEChunk("token1"))
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		// Additional data after [DONE] must be ignored.
		fmt.Fprint(w, ollamaSSEChunk("token2"))
		flusher.Flush()
	}))
	defer srv.Close()

	var got []string
	err := CallOllamaStream(context.Background(), "ping", srv.URL, "gemma4:26b", func(text string) error {
		got = append(got, text)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "token1" {
		t.Fatalf("expected only [token1], got %v", got)
	}
}

func TestCallOllamaStream_contextCancel(t *testing.T) {
	ready := make(chan struct{})
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(ready)
		<-done
	}))
	defer func() {
		close(done)
		srv.CloseClientConnections()
		srv.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-ready
		cancel()
	}()

	err := CallOllamaStream(ctx, "ping", srv.URL, "gemma4:26b", func(_ string) error { return nil })
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}
