package main

import "time"

// IndexingEvent is emitted when a node reports indexing completion.
type IndexingEvent struct {
	Namespace    string    `json:"namespace"`
	NodeID       string    `json:"node_id"`
	Status       string    `json:"status"` // "complete" | "error"
	FilesIndexed int       `json:"files_indexed"`
	FilesSkipped int       `json:"files_skipped"`
	Timestamp    time.Time `json:"timestamp"`
}
