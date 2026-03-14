package benchmark

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func BenchmarkSearchQueryLatency(b *testing.B) {
	// Assumption: Gateway is running on localhost:8080
	url := "http://localhost:8080/v1/search"
	
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	query := map[string]interface{}{
		"query": "test search query",
		"limit": 10,
	}
	body, _ := json.Marshal(query)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			b.Skip("Gateway not available, skipping benchmark")
			return
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b.Logf("Search failed with status %d", resp.StatusCode)
		}
	}
}
