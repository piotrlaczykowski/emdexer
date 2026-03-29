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
