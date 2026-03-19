package health

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/piotrlaczykowski/emdexer/watcher"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// ServerConfig holds injected dependencies for the health server.
type ServerConfig struct {
	QdrantConn      *grpc.ClientConn
	WorkerHeartbeat *watcher.Heartbeat
}

// StartServer starts the health/metrics HTTP server. This blocks.
func StartServer(cfg ServerConfig) {
	healthClient := grpc_health_v1.NewHealthClient(cfg.QdrantConn)
	startTime := time.Now()

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz/liveness", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "UP"})
	})

	mux.HandleFunc("/healthz/readiness", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: ""})
		if err != nil || resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "DOWN"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "UP"})
	})

	mux.HandleFunc("/healthz/worker", func(w http.ResponseWriter, r *http.Request) {
		if cfg.WorkerHeartbeat == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "NO_WORKER"})
			return
		}
		alive := cfg.WorkerHeartbeat.Alive(5 * time.Minute)
		lastActive := cfg.WorkerHeartbeat.LastActive().Format(time.RFC3339)
		if !alive {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":      "STALE",
				"last_active": lastActive,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "ALIVE",
			"last_active": lastActive,
		})
	})

	mux.HandleFunc("/healthz/startup", func(w http.ResponseWriter, r *http.Request) {
		if time.Since(startTime) < 5*time.Second {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "STARTING"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "STARTED"})
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
