package main

// GraphConfig holds feature-flag settings for the knowledge-graph expansion.
type GraphConfig struct {
	Enabled bool
	Depth   int // BFS depth: 1–3
}
