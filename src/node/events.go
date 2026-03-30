package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/piotrlaczykowski/emdexer/indexer"
)

// reportIndexingProgress notifies the gateway of ongoing indexing progress.
// Called periodically during the startup walk (every progressInterval files).
// Fire-and-forget: errors are silently discarded to avoid log noise during long walks.
func reportIndexingProgress(gatewayURL, nodeID, namespace, authKey string, filesIndexed, filesSkipped int) {
	if gatewayURL == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"namespace":     namespace,
		"files_indexed": filesIndexed,
		"files_skipped": filesSkipped,
		"status":        "in_progress",
	})
	url := gatewayURL + "/v1/nodes/" + nodeID + "/indexed"
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if authKey != "" {
		req.Header.Set("Authorization", "Bearer "+authKey)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return // fire-and-forget, don't log noise
	}
	_ = resp.Body.Close()
}

// reportIndexingComplete notifies the gateway that this node finished its startup walk.
// Fire-and-forget: errors are logged but do not affect indexing.
func reportIndexingComplete(gatewayURL, nodeID, namespace, authKey string, stats indexer.WalkStats) {
	if gatewayURL == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"namespace":     namespace,
		"files_indexed": stats.FilesIndexed,
		"files_skipped": stats.FilesSkipped,
		"status":        "complete",
	})
	url := gatewayURL + "/v1/nodes/" + nodeID + "/indexed"
	req, err := http.NewRequestWithContext(
		context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("[node] indexing-event: failed to create request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if authKey != "" {
		req.Header.Set("Authorization", "Bearer "+authKey)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[node] indexing-event: failed to notify gateway: %v", err)
		return
	}
	_ = resp.Body.Close()
	log.Printf("[node] indexing-event: notified gateway (status %d)", resp.StatusCode)
}
