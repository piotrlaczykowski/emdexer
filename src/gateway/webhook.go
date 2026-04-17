package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"
)

// IndexedEvent is the JSON payload POSTed to EMDEX_WEBHOOK_URL when a namespace
// finishes indexing. Distinct from the internal IndexingEvent (which is broadcast
// on the SSE bus) — this is the external contract for webhook consumers.
type IndexedEvent struct {
	Event        string    `json:"event"` // always "namespace.indexed"
	Namespace    string    `json:"namespace"`
	NodeID       string    `json:"node_id"`
	FilesIndexed int       `json:"files_indexed"`
	Timestamp    time.Time `json:"timestamp"`
}

// dispatchWebhook sends evt to webhookURL in a goroutine.
//
// Fire-and-forget: the caller returns immediately; errors are logged but never
// propagated. SSRF protection is handled by the provided client — in production
// this is safenet.NewSafeClient which rejects private-range IPs at dial time,
// defeating DNS-rebinding attacks.
//
// Retry policy: a single retry is attempted on a 5xx response. 4xx, network
// errors, and timeouts are logged once and not retried.
func dispatchWebhook(client *http.Client, webhookURL string, evt IndexedEvent) {
	go func() {
		body, err := json.Marshal(evt)
		if err != nil {
			log.Printf("[webhook] marshal error: %v", err)
			return
		}
		postOnce(client, webhookURL, body, "initial")
	}()
}

// postOnce performs one POST. On 5xx with label=="initial" it recurses once
// with label=="retry" so the second call never retries further regardless of
// response.
func postOnce(client *http.Client, webhookURL string, body []byte, label string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		log.Printf("[webhook] request build error (%s): %v", label, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "emdexer-gateway/1.0")

	resp, err := client.Do(req)
	if err != nil {
		// Covers SSRF-guard dial errors, DNS failures, connection refused, 5s timeout.
		log.Printf("[webhook] POST %s error (%s): %v", webhookURL, label, err)
		return
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode >= 500 && label == "initial" {
		log.Printf("[webhook] POST %s returned %d — retrying once", webhookURL, resp.StatusCode)
		postOnce(client, webhookURL, body, "retry")
		return
	}
	log.Printf("[webhook] POST %s → %d (%s)", webhookURL, resp.StatusCode, label)
}
