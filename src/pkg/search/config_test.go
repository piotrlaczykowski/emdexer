package search

import (
	"testing"
)

func TestLoadRRFConfig_Defaults(t *testing.T) {
	cfg := loadRRFConfig()
	if cfg.VectorWeight != 1.0 {
		t.Errorf("expected default VectorWeight=1.0, got %f", cfg.VectorWeight)
	}
	if cfg.BM25Weight != 1.0 {
		t.Errorf("expected default BM25Weight=1.0, got %f", cfg.BM25Weight)
	}
	if cfg.K != 60 {
		t.Errorf("expected default K=60, got %f", cfg.K)
	}
}

func TestLoadRRFConfig_EnvVars(t *testing.T) {
	t.Setenv("EMDEX_RRF_VECTOR_WEIGHT", "0.7")
	t.Setenv("EMDEX_RRF_BM25_WEIGHT", "0.3")
	t.Setenv("EMDEX_RRF_K", "30")

	cfg := loadRRFConfig()

	if cfg.VectorWeight != 0.7 {
		t.Errorf("expected VectorWeight=0.7, got %f", cfg.VectorWeight)
	}
	if cfg.BM25Weight != 0.3 {
		t.Errorf("expected BM25Weight=0.3, got %f", cfg.BM25Weight)
	}
	if cfg.K != 30 {
		t.Errorf("expected K=30, got %f", cfg.K)
	}
}

func TestLoadRRFConfig_InvalidValues(t *testing.T) {
	t.Setenv("EMDEX_RRF_VECTOR_WEIGHT", "invalid")
	t.Setenv("EMDEX_RRF_BM25_WEIGHT", "-1")
	t.Setenv("EMDEX_RRF_K", "-5")

	cfg := loadRRFConfig()

	if cfg.VectorWeight != 1.0 {
		t.Errorf("expected fallback to default 1.0, got %f", cfg.VectorWeight)
	}
	if cfg.BM25Weight != 1.0 {
		t.Errorf("expected fallback to default 1.0, got %f", cfg.BM25Weight)
	}
	if cfg.K != 60 {
		t.Errorf("expected fallback to default 60, got %f", cfg.K)
	}
}
