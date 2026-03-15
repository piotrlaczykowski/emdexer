package e2e

import (
	"encoding/json"
	"fmt"
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
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"query":"test","results":[]}`))
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
