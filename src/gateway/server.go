package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/piotrlaczykowski/emdexer/embed"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type Server struct {
	registry     NodeRegistry
	qdrantConn   *grpc.ClientConn
	pointsClient qdrant.PointsClient
	healthClient grpc_health_v1.HealthClient
	embedder     embed.EmbedProvider
	collection   string
	apiKey       string
	authKey      string
	apiKeys      map[string][]string
	port         string
	startTime    time.Time
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) instrument(path string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		httpRequestsTotal.WithLabelValues(path, fmt.Sprintf("%d", rw.status)).Inc()
	}
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func (s *Server) streamResponse(w http.ResponseWriter, model, answer string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	created := time.Now().Unix()

	words := strings.Fields(answer)
	for _, word := range words {
		chunk := StreamChunk{
			ID:      id,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   model,
			Choices: []StreamChoice{{Index: 0, Delta: DeltaContent{Content: word + " "}}},
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", string(b))
		flusher.Flush()
	}
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func startServer(s *Server) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/healthz/liveness", s.handleLiveness)
	mux.HandleFunc("/healthz/readiness", s.handleReadiness)
	mux.HandleFunc("/healthz/startup", s.handleStartup)
	mux.HandleFunc("/nodes/register", s.instrument("/nodes/register", s.authenticate(s.handleRegisterNode)))
	mux.HandleFunc("/nodes/deregister/", s.instrument("/nodes/deregister", s.authenticate(s.handleDeregisterNode)))
	mux.HandleFunc("/nodes", s.instrument("/nodes", s.authenticate(s.handleListNodes)))
	mux.HandleFunc("/v1/namespaces", s.instrument("/v1/namespaces", s.authenticate(s.handleListNamespaces)))
	mux.HandleFunc("/v1/search", s.instrument("/v1/search", s.authenticate(s.handleSearch)))
	mux.HandleFunc("/v1/chat/completions", s.instrument("/v1/chat/completions", s.authenticate(s.handleChatCompletions)))

	addr := ":" + s.port
	fmt.Printf("Gateway starting on %s\n", addr)
	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	return server.ListenAndServe()
}
