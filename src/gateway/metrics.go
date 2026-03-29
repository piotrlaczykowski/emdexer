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
