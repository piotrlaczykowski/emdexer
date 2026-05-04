package main

import (
	"encoding/json"
	"net/http"
)

type evalMetricsRequest struct {
	ContextRecall *float64 `json:"context_recall"`
	Faithfulness  *float64 `json:"faithfulness"`
}

func handleEvalMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req evalMetricsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.ContextRecall != nil {
		evalContextRecall.Set(*req.ContextRecall)
	}
	if req.Faithfulness != nil {
		evalFaithfulness.Set(*req.Faithfulness)
	}
	w.WriteHeader(http.StatusNoContent)
}
