package mock

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
)

type OllamaMockServer struct {
	Server *httptest.Server
	Handler func(w http.ResponseWriter, r *http.Request)
}

func NewOllamaMockServer() *OllamaMockServer {
	m := &OllamaMockServer{}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.Handler != nil {
			m.Handler(w, r)
			return
		}

		if r.URL.Path != "/api/embed" {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}

		var req struct {
			Model string `json:"model"`
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}

		resp := struct {
			Embeddings [][]float32 `json:"embeddings"`
		}{
			Embeddings: [][]float32{{0.1, 0.2, 0.3}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	return m
}

func (m *OllamaMockServer) Close() {
	m.Server.Close()
}
