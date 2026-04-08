package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var searchEmptyResults = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_search_empty_results_total",
	Help: "Number of search requests that returned zero results",
}, []string{"namespace", "mode"})

var graphSearchEmptyResults = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_graph_search_empty_results_total",
	Help: "Number of graph search requests that returned zero results",
}, []string{"namespace"})

var topologyNamespacesKnown = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "emdexer_gateway_topology_namespaces_known",
	Help: "Number of namespaces currently known from the node registry",
})

var topologyNodesKnown = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "emdexer_gateway_topology_nodes_known",
	Help: "Number of nodes currently known from the node registry",
})

var nodeFilesIndexedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_node_files_indexed_total",
	Help: "Total number of files indexed, as reported by nodes on walk completion",
}, []string{"namespace", "node_id"})

var nodeFilesSkippedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_node_files_skipped_total",
	Help: "Total number of files skipped during indexing walk",
}, []string{"namespace", "node_id"})

var nodeIndexingCompleteTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_node_indexing_complete_total",
	Help: "Number of completed indexing walks per node/namespace",
}, []string{"namespace", "node_id", "status"})

var nodeIndexingLastFilesIndexed = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "emdexer_gateway_node_last_files_indexed",
	Help: "Files indexed in the most recent walk (gauge, per node/namespace)",
}, []string{"namespace", "node_id"})

// bm25FallbackTotal counts hybrid searches that returned zero results to the client.
// The metric name uses _empty_results_ rather than _fallback_ because
// emdexer_gateway_bm25_fallback_total is already registered in
// src/pkg/search/search.go (counts Qdrant API errors, not empty result sets).
var bm25FallbackTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_bm25_empty_results_total",
	Help: "Number of hybrid searches that returned zero results to the client",
}, []string{"namespace"})

var chatStreamTTFT = promauto.NewHistogram(prometheus.HistogramOpts{
	Name:    "emdexer_gateway_chat_stream_ttft_ms",
	Help:    "Time-to-first-token for LLM streaming responses in milliseconds, measured at the chat handler",
	Buckets: []float64{100, 200, 500, 1000, 2000, 5000, 10000},
})

var chatStreamChunksTotal = promauto.NewCounter(prometheus.CounterOpts{
	Name: "emdexer_gateway_chat_stream_chunks_total",
	Help: "Total SSE token chunks delivered to clients via the chat handler",
})
