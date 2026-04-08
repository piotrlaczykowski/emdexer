package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/piotrlaczykowski/emdexer/auth"
	"github.com/piotrlaczykowski/emdexer/registry"
	"github.com/piotrlaczykowski/emdexer/version"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	llmStatus := "ok"
	if os.Getenv("GOOGLE_API_KEY") == "" && os.Getenv("OPENAI_API_KEY") == "" {
		llmStatus = "degraded (no API key configured)"
	}
	s.writeJSON(w, http.StatusOK, map[string]string{
		"status":     "ok",
		"version":    version.Version,
		"collection": s.collection,
		"llm":        llmStatus,
	})
}

func (s *Server) handleLiveness(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "UP"})
}

func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := s.healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: ""})
	if err != nil || resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "DOWN", "reason": "qdrant_unreachable"})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "UP"})
}

func (s *Server) handleStartup(w http.ResponseWriter, r *http.Request) {
	if time.Since(s.startTime) < 5*time.Second {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "STARTING"})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "STARTED"})
}

// handleQdrantHealth checks the Qdrant cluster health via the /cluster REST endpoint.
// In single-node deployments (cluster disabled) the Raft check is skipped and the
// gRPC health probe result is returned instead.
func (s *Server) handleQdrantHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	qdrantHTTPAddr := os.Getenv("QDRANT_HTTP_HOST")
	if qdrantHTTPAddr == "" {
		// Derive HTTP host from gRPC host by replacing port 6334 → 6333.
		grpcHost := os.Getenv("QDRANT_HOST")
		if grpcHost == "" {
			grpcHost = "localhost:6334"
		}
		qdrantHTTPAddr = strings.Replace(grpcHost, ":6334", ":6333", 1)
	}

	cs, err := registry.CheckRaftCluster(ctx, qdrantHTTPAddr)
	if err != nil {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"status": "DOWN",
			"reason": err.Error(),
		})
		return
	}
	if !cs.RaftReady {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"status":      "DOWN",
			"reason":      "raft_not_ready",
			"cluster":     cs.Status,
			"node_count":  cs.NodeCount,
			"leader_id":   cs.LeaderID,
		})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":       "UP",
		"cluster":      cs.Status,
		"node_count":   cs.NodeCount,
		"leader_id":    cs.LeaderID,
		"commit_index": cs.CommitIndex,
	})
}

func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.GetUserClaims(r)
	if !ok {
		http.Error(w, "No identity", http.StatusForbidden)
		return
	}
	ns, _ := auth.GetAllowedNamespaces(r)
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"auth_type":  claims.AuthType,
		"subject":    claims.Subject,
		"email":      claims.Email,
		"groups":     claims.Groups,
		"namespaces": ns,
	})
}
