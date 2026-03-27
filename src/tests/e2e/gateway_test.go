package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

var (
	gatewayURL = getEnv("EMDEX_GATEWAY_URL", "")
	authKey    = getEnv("EMDEX_AUTH_KEY", "emdex-test-key-primary")
)

func setupMockGateway() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz/readiness", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"UP"}`))
	})
	mux.HandleFunc("/nodes", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+authKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/v1/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+authKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		ns := r.URL.Query().Get("namespace")
		if ns == "*" || ns == "__global__" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"query":"test","results":[{"id":1,"score":0.032,"payload":{"path":"doc.txt","chunk":"0","namespace":"ns1","source_namespace":"ns1","text":"hello world"}},{"id":2,"score":0.016,"payload":{"path":"other.txt","chunk":"0","namespace":"ns2","source_namespace":"ns2","text":"hello again"}}]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"query":"test","results":[{"id":1,"score":0.032,"payload":{"path":"doc.txt","chunk":"0","namespace":"default","text":"hello world"}},{"id":2,"score":0.016,"payload":{"path":"other.txt","chunk":"1","namespace":"default","text":"world peace"}}]}`))
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+authKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"chatcmpl-mock","choices":[{"message":{"role":"assistant","content":"Mock response"}}]}`))
	})
	return httptest.NewServer(mux)
}

func TestHealthzReadiness(t *testing.T) {
	url := gatewayURL
	if url == "" {
		server := setupMockGateway()
		defer server.Close()
		url = server.URL
	}

	resp, err := http.Get(url + "/healthz/readiness")
	if err != nil {
		t.Fatalf("Failed to call readiness: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

func TestNodesList(t *testing.T) {
	url := gatewayURL
	if url == "" {
		server := setupMockGateway()
		defer server.Close()
		url = server.URL
	}

	req, _ := http.NewRequest("GET", url+"/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+authKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to list nodes: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	var nodes []interface{}
	if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
		t.Fatalf("Failed to decode nodes: %v", err)
	}
}

func TestSearchFlow(t *testing.T) {
	url := gatewayURL
	if url == "" {
		server := setupMockGateway()
		defer server.Close()
		url = server.URL
	}

	query := "test"
	searchURL := fmt.Sprintf("%s/v1/search?q=%s&namespace=default", url, query)
	req, _ := http.NewRequest("GET", searchURL, nil)
	req.Header.Set("Authorization", "Bearer "+authKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed search: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

func TestChatCompletionsFlow(t *testing.T) {
	url := gatewayURL
	if url == "" {
		server := setupMockGateway()
		defer server.Close()
		url = server.URL
	}

	chatURL := url + "/v1/chat/completions"
	bodyStr := `{"model": "emdexer", "messages": [{"role": "user", "content": "What is in test.txt?"}], "stream": false}`
	req, _ := http.NewRequest("POST", chatURL, strings.NewReader(bodyStr))
	req.Header.Set("Authorization", "Bearer "+authKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Emdex-Namespace", "default")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed chat: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

// searchResponse mirrors the gateway's search response envelope.
type searchResponse struct {
	Query   string           `json:"query"`
	Results []map[string]any `json:"results"`
}

// TestHybridSearchResponseStructure verifies that /v1/search returns a valid
// JSON envelope with a "query" string and a "results" array containing scored
// hits with path/chunk/namespace payload fields.
func TestHybridSearchResponseStructure(t *testing.T) {
	url := gatewayURL
	if url == "" {
		server := setupMockGateway()
		defer server.Close()
		url = server.URL
	}

	req, _ := http.NewRequest("GET", url+"/v1/search?q=hello+world&namespace=default", nil)
	req.Header.Set("Authorization", "Bearer "+authKey)

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if body.Query == "" {
		t.Error("expected non-empty query field in response")
	}
	if body.Results == nil {
		t.Error("expected results array in response, got nil")
	}
	for i, r := range body.Results {
		if _, ok := r["score"]; !ok {
			t.Errorf("result[%d] missing score field", i)
		}
		payload, ok := r["payload"].(map[string]any)
		if !ok {
			t.Errorf("result[%d] missing or invalid payload field", i)
			continue
		}
		for _, field := range []string{"path", "chunk", "namespace"} {
			if _, ok := payload[field]; !ok {
				t.Errorf("result[%d].payload missing field %q", i, field)
			}
		}
	}
}

// TestHybridSearchUnauthorized verifies that /v1/search rejects requests
// without a valid Authorization header.
func TestHybridSearchUnauthorized(t *testing.T) {
	url := gatewayURL
	if url == "" {
		server := setupMockGateway()
		defer server.Close()
		url = server.URL
	}

	req, _ := http.NewRequest("GET", url+"/v1/search?q=test&namespace=default", nil)
	// intentionally omit Authorization header

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

// TestGlobalSearchRRFMerge verifies that namespace=* returns results from
// multiple namespaces with source_namespace set in each payload (indicating
// cross-namespace RRF merging occurred).
func TestGlobalSearchRRFMerge(t *testing.T) {
	url := gatewayURL
	if url == "" {
		server := setupMockGateway()
		defer server.Close()
		url = server.URL
	}

	req, _ := http.NewRequest("GET", url+"/v1/search?q=hello&namespace=*", nil)
	req.Header.Set("Authorization", "Bearer "+authKey)

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(body.Results) == 0 {
		t.Skip("no results returned — requires a live gateway with indexed data")
	}

	for i, r := range body.Results {
		payload, ok := r["payload"].(map[string]any)
		if !ok {
			t.Errorf("result[%d] missing payload", i)
			continue
		}
		if _, ok := payload["source_namespace"]; !ok {
			t.Errorf("result[%d].payload missing source_namespace — cross-namespace RRF merge may not be working", i)
		}
	}
}

// TestHybridSearchScoresDescending verifies that results returned by the
// search endpoint are ordered by descending score (highest-confidence first),
// as required by RRF post-merge sorting.
func TestHybridSearchScoresDescending(t *testing.T) {
	url := gatewayURL
	if url == "" {
		server := setupMockGateway()
		defer server.Close()
		url = server.URL
	}

	req, _ := http.NewRequest("GET", url+"/v1/search?q=hello&namespace=default", nil)
	req.Header.Set("Authorization", "Bearer "+authKey)

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(body.Results) < 2 {
		t.Skip("need at least 2 results to check ordering")
	}

	prev := float64(1<<31 - 1)
	for i, r := range body.Results {
		scoreRaw, ok := r["score"]
		if !ok {
			t.Fatalf("result[%d] missing score", i)
		}
		score, ok := scoreRaw.(float64)
		if !ok {
			t.Fatalf("result[%d] score is not a number: %T", i, scoreRaw)
		}
		if score > prev {
			t.Errorf("results not sorted descending: result[%d] score %v > result[%d] score %v", i, score, i-1, prev)
		}
		prev = score
	}
}

// TestChatCompletions_trueStreaming verifies that the gateway streams
// chat completions as SSE when "stream":true is set, and that each
// data chunk is a valid StreamChunk JSON object.
func TestChatCompletions_trueStreaming(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	if gatewayURL == "" {
		t.Skip("EMDEX_GATEWAY_URL not set — skipping live streaming test")
	}

	body := `{"model":"gemini-3-flash-preview","stream":true,"messages":[{"role":"user","content":"say hi"}]}`
	req, err := http.NewRequest(http.MethodPost, gatewayURL+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+authKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(b))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	scanner := bufio.NewScanner(resp.Body)
	var chunks []string
	for scanner.Scan() {
		line := scanner.Text()
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		if data == "[DONE]" {
			break
		}
		// Validate it's a parseable StreamChunk.
		var chunk struct {
			Choices []struct {
				Delta struct{ Content string } `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Errorf("malformed chunk: %s", data)
			continue
		}
		if len(chunk.Choices) > 0 {
			chunks = append(chunks, chunk.Choices[0].Delta.Content)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if len(chunks) == 0 {
		t.Error("no chunks received")
	}
	t.Logf("received %d chunks; assembled=%q", len(chunks), strings.Join(chunks, ""))
}
