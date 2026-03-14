// Package embed defines the EmbedProvider interface and its implementations.
//
// Design intent: Emdexer must never be hard-locked to a single embedding
// backend. Phase 15.5 targets air-gapped deployments with local Ollama/vLLM.
// All indexing paths use EmbedProvider; swapping the backend requires only an
// env var change, not a code change.
package embed

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

// isPrivateIP checks if an IP belongs to private or reserved ranges.
func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}

	// RFC 1918
	privateIPBlocks := []*net.IPNet{
		{IP: net.ParseIP("10.0.0.0"), Mask: net.CIDRMask(8, 32)},
		{IP: net.ParseIP("172.16.0.0"), Mask: net.CIDRMask(12, 32)},
		{IP: net.ParseIP("192.168.0.0"), Mask: net.CIDRMask(16, 32)},
	}

	for _, block := range privateIPBlocks {
		if block.Contains(ip) {
			return true
		}
	}

	return false
}

// validateOllamaHost parses the URL and validates its scheme.
// It does NOT perform a DNS lookup here — IP validation happens at dial-time
// via newSafeOllamaTransport to prevent DNS rebinding attacks.
func validateOllamaHost(hostStr string) error {
	u, err := url.Parse(hostStr)
	if err != nil {
		return fmt.Errorf("invalid OLLAMA_HOST URL: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("OLLAMA_HOST must be http or https")
	}

	return nil
}

// newSafeOllamaTransport returns an http.Transport whose dialer validates the
// resolved IP at connection time (not before).  This closes the DNS-rebinding
// window: even if the hostname initially resolves to a public IP, any
// subsequent resolution to a private/loopback address is rejected at the
// moment the TCP socket is opened.
func newSafeOllamaTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("ssrf-guard: could not parse dial address %q: %w", address, err)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("ssrf-guard: non-IP address at dial time: %q", host)
			}
			if isPrivateIP(ip) {
				return fmt.Errorf("ssrf-guard: dial to restricted IP %s blocked (DNS rebinding?)", ip)
			}
			return nil
		},
	}
	return &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}

// newSafeOllamaClient returns an *http.Client that blocks connections to
// private/loopback IPs at dial time, defeating DNS rebinding.
func newSafeOllamaClient() *http.Client {
	return &http.Client{
		Transport: newSafeOllamaTransport(),
		Timeout:   60 * time.Second,
	}
}



// EmbedProvider is the single abstraction over any dense-embedding backend.
type EmbedProvider interface {
	// Embed returns a float32 vector for text.
	Embed(text string) ([]float32, error)
	// Name returns a human-readable identifier for logging/metrics.
	Name() string
}

// ────────────────────────────────────────────────
// GeminiProvider — Google Generative Language API
// ────────────────────────────────────────────────

const defaultGeminiModel = "models/gemini-embedding-exp-03-07"

// GeminiProvider calls the Google embedding API.
// It is the default provider when EMBED_PROVIDER is unset or "gemini".
type GeminiProvider struct {
	APIKey string
	Model  string
}

// NewGeminiProvider constructs a GeminiProvider with the selected model.
func NewGeminiProvider(apiKey, model string) *GeminiProvider {
	if model == "" {
		model = defaultGeminiModel
	}
	return &GeminiProvider{APIKey: apiKey, Model: model}
}

func (g *GeminiProvider) Name() string { return "gemini:" + g.Model }

func (g *GeminiProvider) Embed(text string) ([]float32, error) {
	geminiModel := os.Getenv("EMDEX_GEMINI_MODEL")
	if geminiModel == "" {
		geminiModel = defaultGeminiModel
	}
	url := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/%s:embedContent?key=%s",
		geminiModel, g.APIKey,
	)
	type part struct {
		Text string `json:"text"`
	}
	type content struct {
		Parts []part `json:"parts"`
	}
	type req struct {
		Model   string  `json:"model"`
		Content content `json:"content"`
	}
	type embedResp struct {
		Embedding struct {
			Values []float32 `json:"values"`
		} `json:"embedding"`
	}

	body, _ := json.Marshal(req{
		Model:   g.Model,
		Content: content{Parts: []part{{Text: text}}},
	})

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemini embed HTTP: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini embed %d: %s", resp.StatusCode, string(b))
	}

	var er embedResp
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return nil, fmt.Errorf("gemini embed decode: %w", err)
	}
	return er.Embedding.Values, nil
}

// ────────────────────────────────────────────────
// OllamaProvider — Phase 15.5 stub (air-gapped)
// ────────────────────────────────────────────────

// OllamaProvider will call the local Ollama /api/embed endpoint.
type OllamaProvider struct {
	Host  string // e.g. http://localhost:11434
	Model string // e.g. nomic-embed-text
}

func (o *OllamaProvider) Name() string { return "ollama:" + o.Model }

func (o *OllamaProvider) Embed(text string) ([]float32, error) {
	endpoint := fmt.Sprintf("%s/api/embed", o.Host)
	type req struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}
	type resp struct {
		Embeddings [][]float32 `json:"embeddings"`
	}

	body, err := json.Marshal(req{
		Model: o.Model,
		Input: text,
	})
	if err != nil {
		return nil, fmt.Errorf("ollama marshal: %w", err)
	}

	// Use the SSRF-safe client: IP is validated at dial time on every request,
	// preventing DNS rebinding attacks.
	client := newSafeOllamaClient()
	hresp, err := client.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama HTTP: %w", err)
	}
	defer hresp.Body.Close()

	if hresp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(hresp.Body)
		return nil, fmt.Errorf("ollama %d: %s", hresp.StatusCode, string(b))
	}

	var or resp
	if err := json.NewDecoder(hresp.Body).Decode(&or); err != nil {
		return nil, fmt.Errorf("ollama decode: %w", err)
	}

	if len(or.Embeddings) == 0 {
		return nil, fmt.Errorf("ollama returned no embeddings")
	}

	return or.Embeddings[0], nil
}

// ────────────────────────────────────────────────
// Factory
// ────────────────────────────────────────────────

// New returns the EmbedProvider selected by the EMBED_PROVIDER environment
// variable.  Accepted values: "gemini" (default), "ollama".
func New(apiKey, providerEnv, ollamaHost, ollamaModel, geminiModel string) EmbedProvider {
	switch strings.ToLower(providerEnv) {
	case "ollama":
		if ollamaHost == "" {
			ollamaHost = "http://localhost:11434"
		}
		if ollamaModel == "" {
			ollamaModel = "nomic-embed-text"
		}

		if err := validateOllamaHost(ollamaHost); err != nil {
			log.Fatalf("[embed] CRITICAL SECURITY ERROR: %v", err)
		}

		return &OllamaProvider{Host: ollamaHost, Model: ollamaModel}
	default:
		return NewGeminiProvider(apiKey, geminiModel)
	}
}
