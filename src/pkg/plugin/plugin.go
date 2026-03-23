package plugin

import "context"

// Relation represents a structural link that a plugin extracts from a file.
// It mirrors indexer.Relation to avoid a circular import between the two packages.
type Relation struct {
	Type   string `json:"type"`
	Target string `json:"target,omitempty"` // imports, links_to
	Name   string `json:"name,omitempty"`   // defines
}

// ExtractorPlugin is the interface that all extractor plugins must implement.
// Plugins are loaded from EMDEX_PLUGIN_DIR at node startup and invoked during
// the indexing pipeline for the file extensions they declare.
type ExtractorPlugin interface {
	// Extensions returns the file extensions this plugin handles (e.g. [".csv", ".xlsx"]).
	// Extensions must be lowercase and include the leading dot.
	Extensions() []string

	// Extract extracts text and optional structural relations from raw file bytes.
	// filename is the base filename (e.g. "report.csv"); data is the raw file content.
	// ctx carries a per-call deadline set to EMDEX_PLUGIN_TIMEOUT (default 10s).
	Extract(ctx context.Context, filename string, data []byte) (text string, relations []Relation, err error)

	// Name returns a human-readable plugin identifier used in logs and metrics.
	Name() string
}
