package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func startHealthServer(qdrantConn *grpc.ClientConn) {
	healthClient := grpc_health_v1.NewHealthClient(qdrantConn)
	startTime := time.Now()

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz/liveness", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "UP"})
	})

	mux.HandleFunc("/healthz/readiness", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: ""})
		if err != nil || resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"status": "DOWN"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "UP"})
	})

	mux.HandleFunc("/healthz/startup", func(w http.ResponseWriter, r *http.Request) {
		if time.Since(startTime) < 5*time.Second {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"status": "STARTING"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "STARTED"})
	})

	port := os.Getenv("NODE_HEALTH_PORT")
	if port == "" {
		port = "8081"
	}
	addr := ":" + port
	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("health server error: %v", err)
	}
}
