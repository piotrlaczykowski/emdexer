package main

import (
	"encoding/json"
	"log"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
)

type evalMetricsRequest struct {
	ContextRecall *float64 `json:"context_recall"`
	Faithfulness  *float64 `json:"faithfulness"`
}

func handleEvalMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.eval.metrics")
	defer span.End()
	r = r.WithContext(ctx)

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req evalMetricsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[eval/metrics] decode error: %v", err)
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.ContextRecall != nil {
		evalContextRecall.Set(*req.ContextRecall)
		span.SetAttributes(attribute.Float64("eval.context_recall", *req.ContextRecall))
	}
	if req.Faithfulness != nil {
		evalFaithfulness.Set(*req.Faithfulness)
		span.SetAttributes(attribute.Float64("eval.faithfulness", *req.Faithfulness))
	}
	log.Printf("[eval/metrics] gauges updated")
	w.WriteHeader(http.StatusNoContent)
}
