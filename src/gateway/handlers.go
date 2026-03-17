package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/piotrlaczykowski/emdexer/util"
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

	nodes := s.registry.List()

	var allResults []SearchResult
	var resultsMu sync.Mutex
	var wg sync.WaitGroup

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	for _, node := range nodes {
		wg.Add(1)
		go func(n NodeInfo) {
			defer wg.Done()
			nodeCtx, nodeCancel := context.WithTimeout(ctx, 3*time.Second)
			defer nodeCancel()

			params := url.Values{}
			params.Add("q", query)
			params.Add("namespace", requestedNamespace)

			searchURL := fmt.Sprintf("%s/v1/search?%s", strings.TrimSuffix(n.URL, "/"), params.Encode())
			req, err := http.NewRequestWithContext(nodeCtx, "GET", searchURL, nil)
			if err != nil {
				log.Printf("Node %s request creation error: %v", n.ID, err)
				return
			}

			req.Header.Set("Authorization", r.Header.Get("Authorization"))

			client := util.NewSafeHTTPClient(3 * time.Second)
			resp, err := client.Do(req)
			if err != nil {
				log.Printf("Node %s search error: %v", n.ID, err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				log.Printf("Node %s returned status %d", n.ID, resp.StatusCode)
				return
			}

			var nodeResponse struct {
				Results []SearchResult `json:"results"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&nodeResponse); err != nil {
				log.Printf("Node %s decoding error: %v", n.ID, err)
				return
			}

			resultsMu.Lock()
			allResults = append(allResults, nodeResponse.Results...)
			resultsMu.Unlock()
		}(node)
	}

	wg.Wait()

	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].Score > allResults[j].Score
	})

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"query":   query,
		"results": allResults,
	})

	logAudit(AuditEntry{
		Action:    "search",
		Query:     query,
		Namespace: requestedNamespace,
		Results:   len(allResults),
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
