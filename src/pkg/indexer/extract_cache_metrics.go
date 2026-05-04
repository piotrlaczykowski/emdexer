package indexer

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	extractCacheHits = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "emdexer_extract_cache_hits_total",
		Help: "Cross-node extraction cache hits, by extractor tag.",
	}, []string{"extractor"})

	extractCacheMisses = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "emdexer_extract_cache_misses_total",
		Help: "Cross-node extraction cache misses, by extractor tag.",
	}, []string{"extractor"})
)
