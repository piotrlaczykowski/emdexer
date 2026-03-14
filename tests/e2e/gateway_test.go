package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
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
	gatewayURL = getEnv("EMDEX_GATEWAY_URL", "http://192.168.0.156:7700")
	authKey    = getEnv("EMDEX_AUTH_KEY", "44886d4f5d0e5a30ea1dd2d390928df76aec4bcbf96d81750991e9767229362e")
)

func TestHealthzReadiness(t *testing.T) {
	resp, err := http.Get(gatewayURL + "/healthz/readiness")
	if err != nil {
		t.Fatalf("Failed to call readiness: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

func TestNodesList(t *testing.T) {
	req, _ := http.NewRequest("GET", gatewayURL+"/nodes", nil)
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
	query := "test"
	url := fmt.Sprintf("%s/v1/search?q=%s&namespace=default", gatewayURL, query)
	req, _ := http.NewRequest("GET", url, nil)
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
	url := gatewayURL + "/v1/chat/completions"
	bodyStr := `{"model": "emdexer", "messages": [{"role": "user", "content": "What is in test.txt?"}], "stream": false}`
	req, _ := http.NewRequest("POST", url, strings.NewReader(bodyStr))
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
