package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/piotrlaczykowski/emdexer/version"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func (s *Server) handleRegisterNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var node NodeInfo
	if err := json.NewDecoder(r.Body).Decode(&node); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	if node.ID == "" {
		node.ID = fmt.Sprintf("node-%d", time.Now().UnixNano())
	}
	s.registry.Register(node)
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "registered", "id": node.ID})
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, s.registry.List())
}

func (s *Server) handleDeregisterNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/nodes/deregister/")
	if id == "" {
		id = strings.TrimPrefix(r.URL.Path, "/nodes/")
		id = strings.TrimSuffix(id, "/deregister")
	}
	id = strings.TrimSuffix(id, "/")
	if id == "" {
		http.Error(w, "Bad request: missing node id", http.StatusBadRequest)
		return
	}
	s.registry.Deregister(id)
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "deregistered", "id": id})
}

func (s *Server) handleListNamespaces(w http.ResponseWriter, r *http.Request) {
	nodes := s.registry.List()

	nsSet := make(map[string]struct{})
	type nodeEntry struct {
		ID         string   `json:"id"`
		URL        string   `json:"url"`
		Namespaces []string `json:"namespaces"`
		LastSeen   string   `json:"last_seen"`
	}
	var nodeEntries []nodeEntry
	for _, n := range nodes {
		for _, c := range n.Collections {
			nsSet[c] = struct{}{}
		}
		nodeEntries = append(nodeEntries, nodeEntry{
			ID:         n.ID,
			URL:        n.URL,
			Namespaces: n.Collections,
			LastSeen:   n.LastSeen.Format(time.RFC3339),
		})
	}

	namespaces := make([]string, 0, len(nsSet))
	for ns := range nsSet {
		namespaces = append(namespaces, ns)
	}
	sort.Strings(namespaces)

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"namespaces": namespaces,
		"nodes":      nodeEntries,
	})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	allowedNamespaces, ok := r.Context().Value("AllowedNamespaces").([]string)
	if !ok {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	requestedNamespace := strings.TrimSpace(r.URL.Query().Get("namespace"))
	if requestedNamespace == "" {
		requestedNamespace = "default"
	}

	isAllowed := false
	for _, ns := range allowedNamespaces {
		if ns == "*" || ns == requestedNamespace {
			isAllowed = true
			break
		}
	}
	if !isAllowed {
		http.Error(w, "Forbidden: Namespace not authorized", http.StatusForbidden)
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "missing ?q=", http.StatusBadRequest)
		return
	}

	limit := uint64(10)
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	vector, err := s.embedder.Embed(query)
	if err != nil {
		http.Error(w, fmt.Sprintf("embedding error: %v", err), http.StatusBadGateway)
		return
	}

	results, err := searchQdrant(r.Context(), s.pointsClient, s.collection, vector, limit, requestedNamespace)
	if err != nil {
		http.Error(w, fmt.Sprintf("search error: %v", err), http.StatusBadGateway)
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"query":   query,
		"results": results,
	})

	logAudit(AuditEntry{
		Action:    "search",
		Query:     query,
		Namespace: requestedNamespace,
		Results:   len(results),
		LatencyMS: time.Since(start).Milliseconds(),
		Status:    http.StatusOK,
	})
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	allowedNamespaces, ok := r.Context().Value("AllowedNamespaces").([]string)
	if !ok {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	requestedNamespace := strings.TrimSpace(r.Header.Get("X-Emdex-Namespace"))
	if requestedNamespace == "" {
		requestedNamespace = strings.TrimSpace(r.URL.Query().Get("namespace"))
	}
	if requestedNamespace == "" {
		requestedNamespace = "default"
	}

	isAllowed := false
	for _, ns := range allowedNamespaces {
		if ns == "*" || ns == requestedNamespace {
			isAllowed = true
			break
		}
	}

	if !isAllowed {
		http.Error(w, "Forbidden: Namespace not authorized", http.StatusForbidden)
		return
	}

	var question string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			question = req.Messages[i].Content
			break
		}
	}
	if question == "" {
		http.Error(w, "Bad request: no user message found", http.StatusBadRequest)
		return
	}

	vector, err := s.embedder.Embed(question)
	if err != nil {
		http.Error(w, fmt.Sprintf("embedding error: %v", err), http.StatusBadGateway)
		return
	}

	results, err := searchQdrant(r.Context(), s.pointsClient, s.collection, vector, 5, requestedNamespace)
	if err != nil {
		http.Error(w, fmt.Sprintf("search error: %v", err), http.StatusBadGateway)
		return
	}

	contextStr := buildContext(results)
	finalPrompt := fmt.Sprintf("Answer the question using the consolidated context.\n\nContext:\n%s\n\nQuestion: %s", contextStr, question)
	eval, err := callGemini(finalPrompt, s.apiKey)
	if err != nil {
		http.Error(w, fmt.Sprintf("LLM error: %v", err), http.StatusBadGateway)
		return
	}

	if req.Stream {
		s.streamResponse(w, req.Model, eval)
	} else {
		s.writeJSON(w, http.StatusOK, ChatResponse{
			ID: "chatcmpl-rag",
			Choices: []ChatChoice{{Message: ChatMessage{Role: "assistant", Content: eval}}},
		})
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{
		"status":      "ok",
		"version":     version.Version,
		"collection":  s.collection,
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
