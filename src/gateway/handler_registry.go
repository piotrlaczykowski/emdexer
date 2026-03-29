package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/piotrlaczykowski/emdexer/registry"
)

func (s *Server) handleRegisterNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var node registry.NodeInfo
	if err := json.NewDecoder(r.Body).Decode(&node); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	if node.ID == "" {
		node.ID = fmt.Sprintf("node-%d", time.Now().UnixNano())
	}
	if err := s.reg.Register(r.Context(), node); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	// Refresh topology immediately so new namespaces are discoverable.
	go s.refreshTopology()
	// Update Prometheus SD file asynchronously — non-blocking.
	go func() {
		nodes, _ := s.reg.List(r.Context())
		s.sdWriter.Write(nodes)
	}()
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "registered", "id": node.ID})
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.reg.List(r.Context())
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, http.StatusOK, nodes)
}

func (s *Server) handleDeregisterNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/nodes/deregister/")
	id = strings.TrimSuffix(id, "/")

	if id == "" {
		http.Error(w, "Bad request: missing node id", http.StatusBadRequest)
		return
	}
	if err := s.reg.Deregister(r.Context(), id); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "deregistered", "id": id})
}
