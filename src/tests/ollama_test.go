package tests

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/laczyk/emdexer/node/pkg/embed"
	"github.com/laczyk/emdexer/tests/mock"
)

// Note: In a real scenario, we'd need to handle go module paths correctly.
// For now, I'm assuming the test will run within the context where these are resolvable.

func TestOllamaProvider_SSRF(t *testing.T) {
	t.Run("SSRF Protection Blocks 127.0.0.1", func(t *testing.T) {
		p := &embed.OllamaProvider{
			Host:  "http://127.0.0.1:11434",
			Model: "nomic-embed-text",
		}
		_, err := p.Embed("test")
		if err == nil {
			t.Fatal("expected error for 127.0.0.1, got nil")
		}
		if !strings.Contains(err.Error(), "ssrf-guard") {
			t.Errorf("expected ssrf-guard error, got: %v", err)
		}
	})

	t.Run("SSRF Protection Blocks localhost", func(t *testing.T) {
		p := &embed.OllamaProvider{
			Host:  "http://localhost:11434",
			Model: "nomic-embed-text",
		}
		_, err := p.Embed("test")
		if err == nil {
			t.Fatal("expected error for localhost, got nil")
		}
		if !strings.Contains(err.Error(), "ssrf-guard") {
			t.Errorf("expected ssrf-guard error, got: %v", err)
		}
	})
}

func TestOllamaProvider_Logic(t *testing.T) {
	m := mock.NewOllamaMockServer()
	defer m.Close()

	t.Run("Success Path", func(t *testing.T) {
		// Using the internal provider to bypass SSRF for logic testing
		p := &embed.InternalOllamaProvider{
			Host:  m.Server.URL,
			Model: "nomic-embed-text",
		}
		vec, err := p.Embed("hello")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(vec) != 3 {
			t.Errorf("expected 3 dimensions, got %d", len(vec))
		}
	})

	t.Run("Error 500", func(t *testing.T) {
		m.Handler = func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		p := &embed.InternalOllamaProvider{
			Host:  m.Server.URL,
			Model: "nomic-embed-text",
		}
		_, err := p.Embed("fail")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Errorf("expected 500 error, got: %v", err)
		}
	})

	t.Run("Timeout", func(t *testing.T) {
		m.Handler = func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond)
		}
		p := &embed.InternalOllamaProvider{
			Host:  m.Server.URL,
			Model: "nomic-embed-text",
			Client: &http.Client{Timeout: 50 * time.Millisecond},
		}
		_, err := p.Embed("slow")
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}
		if !strings.Contains(err.Error(), "Client.Timeout") && !strings.Contains(err.Error(), "deadline exceeded") {
			t.Errorf("expected timeout error, got: %v", err)
		}
	})
}
