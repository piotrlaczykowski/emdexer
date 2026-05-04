package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestEvalMetricsHandler_SetsGauges(t *testing.T) {
	// Reset gauges to known state
	evalContextRecall.Set(0)
	evalFaithfulness.Set(0)

	body := `{"context_recall": 0.82, "faithfulness": 0.75}`
	req := httptest.NewRequest(http.MethodPost, "/v1/eval/metrics", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleEvalMetrics(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if v := testutil.ToFloat64(evalContextRecall); v != 0.82 {
		t.Errorf("recall gauge = %v, want 0.82", v)
	}
	if v := testutil.ToFloat64(evalFaithfulness); v != 0.75 {
		t.Errorf("faithfulness gauge = %v, want 0.75", v)
	}
}

func TestEvalMetricsHandler_RejectsBadJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/eval/metrics", strings.NewReader("{bad"))
	rec := httptest.NewRecorder()
	handleEvalMetrics(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestEvalMetricsHandler_RejectsWrongMethod(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/eval/metrics", nil)
	rec := httptest.NewRecorder()
	handleEvalMetrics(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}
