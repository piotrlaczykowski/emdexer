package search

import (
	"log"
	"os"
	"strconv"
)

// RRFConfig holds tuning parameters for Reciprocal Rank Fusion.
// All fields have validated defaults so callers can treat zero values as
// "use defaults" only if they construct via loadRRFConfig.
type RRFConfig struct {
	// K is the rank-smoothing constant in 1/(K + rank + 1). Default: 60.
	K float64
	// VectorWeight scales the per-result score from the vector leg. Default: 1.0.
	VectorWeight float64
	// BM25Weight scales the per-result score from the BM25 leg. Default: 1.0.
	// Set to 0.0 to exclude the BM25 leg entirely.
	BM25Weight float64
}

// defaultRRFConfig is loaded once at package init from the environment.
var defaultRRFConfig = loadRRFConfig()

// loadRRFConfig reads RRF tuning parameters from the environment.
//
//	EMDEX_RRF_K              float64, default 60, minimum 1
//	EMDEX_RRF_VECTOR_WEIGHT  float64, default 1.0, range [0, 10]
//	EMDEX_RRF_BM25_WEIGHT    float64, default 1.0, range [0, 10]
func loadRRFConfig() RRFConfig {
	cfg := RRFConfig{
		K:            60,
		VectorWeight: 1.0,
		BM25Weight:   1.0,
	}

	if v := os.Getenv("EMDEX_RRF_K"); v != "" {
		k, err := strconv.ParseFloat(v, 64)
		if err != nil || k < 1 {
			log.Printf("[search] EMDEX_RRF_K=%q is invalid (must be a float ≥ 1); using default %.0f", v, cfg.K)
		} else {
			cfg.K = k
		}
	}

	if v := os.Getenv("EMDEX_RRF_VECTOR_WEIGHT"); v != "" {
		w, err := strconv.ParseFloat(v, 64)
		if err != nil || w < 0 || w > 10 {
			log.Printf("[search] EMDEX_RRF_VECTOR_WEIGHT=%q is invalid (must be a float in [0, 10]); using default %.1f", v, cfg.VectorWeight)
		} else {
			cfg.VectorWeight = w
		}
	}

	if v := os.Getenv("EMDEX_RRF_BM25_WEIGHT"); v != "" {
		w, err := strconv.ParseFloat(v, 64)
		if err != nil || w < 0 || w > 10 {
			log.Printf("[search] EMDEX_RRF_BM25_WEIGHT=%q is invalid (must be a float in [0, 10]); using default %.1f", v, cfg.BM25Weight)
		} else {
			cfg.BM25Weight = w
		}
	}

	log.Printf("[search] RRF config: K=%.0f VectorWeight=%.2f BM25Weight=%.2f", cfg.K, cfg.VectorWeight, cfg.BM25Weight)
	return cfg
}
