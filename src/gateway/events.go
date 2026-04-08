package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// IndexingEvent is emitted when a node reports indexing completion.
type IndexingEvent struct {
	Namespace    string    `json:"namespace"`
	NodeID       string    `json:"node_id"`
	Status       string    `json:"status"` // "complete" | "error"
	FilesIndexed int       `json:"files_indexed"`
	FilesSkipped int       `json:"files_skipped"`
	Timestamp    time.Time `json:"timestamp"`
}

// handleIndexingEvents streams IndexingEvents as SSE to the client.
// Requires auth. Sends keepalive comment every 30s.
// GET /v1/events/indexing
func (s *Server) handleIndexingEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Clear the server-level write deadline so long-lived SSE connections
	// are not killed by the gateway's 60s WriteTimeout (Go 1.20+).
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := s.events.subscribe()
	defer s.events.unsubscribe(ch)

	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(evt)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", string(b))
			flusher.Flush()
		case <-keepalive.C:
			_, _ = fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// handleNodeIndexed receives a POST from nodes when they complete a walk.
// POST /v1/nodes/{nodeId}/indexed
// Body: {"namespace":"...", "files_indexed":N, "files_skipped":N, "status":"complete"}
func (s *Server) handleNodeIndexed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract nodeId: path is /v1/nodes/{nodeId}/indexed
	path := strings.TrimPrefix(r.URL.Path, "/v1/nodes/")
	path = strings.TrimSuffix(path, "/indexed")
	nodeID := path

	var body struct {
		Namespace    string `json:"namespace"`
		FilesIndexed int    `json:"files_indexed"`
		FilesSkipped int    `json:"files_skipped"`
		Status       string `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if body.Status == "" {
		body.Status = "complete"
	}

	nodeFilesIndexedTotal.WithLabelValues(body.Namespace, nodeID).Add(float64(body.FilesIndexed))
	nodeFilesSkippedTotal.WithLabelValues(body.Namespace, nodeID).Add(float64(body.FilesSkipped))
	nodeIndexingCompleteTotal.WithLabelValues(body.Namespace, nodeID, body.Status).Inc()
	nodeIndexingLastFilesIndexed.WithLabelValues(body.Namespace, nodeID).Set(float64(body.FilesIndexed))

	s.events.publish(IndexingEvent{
		Namespace:    body.Namespace,
		NodeID:       nodeID,
		Status:       body.Status,
		FilesIndexed: body.FilesIndexed,
		FilesSkipped: body.FilesSkipped,
		Timestamp:    time.Now().UTC(),
	})
	w.WriteHeader(http.StatusNoContent)
}
