package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/piotrlaczykowski/emdexer/auth"
)

// postWithNamespace builds a POST request with JSON body and namespace auth in context.
func postWithNamespace(target string, body string, namespaces []string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, target, bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	ctx := r.Context()
	if namespaces != nil {
		ctx = context.WithValue(r.Context(), auth.AllowedNamespacesKey, namespaces)
	}
	return r.WithContext(ctx)
}

func TestGraphSearch_POSTBodyParsed(t *testing.T) {
	s := newTestServer()
	s.graphCfg.Enabled = true

	body := `{"query":"test","depth":1,"limit":5,"namespace":"default"}`
	r := postWithNamespace("/v1/search/graph", body, []string{"*"})
	w := httptest.NewRecorder()

	s.handleGraphSearch(w, r)

	if w.Code == http.StatusBadRequest {
		t.Errorf("expected non-400 when JSON body is provided, got 400: %s", w.Body.String())
	}
}

func TestGraphSearch_GETQueryParam(t *testing.T) {
	s := newTestServer()
	s.graphCfg.Enabled = true

	r := requestWithNamespace("/v1/search/graph?q=test&depth=1", []string{"*"})
	w := httptest.NewRecorder()

	s.handleGraphSearch(w, r)

	if w.Code == http.StatusBadRequest {
		t.Errorf("expected non-400 for GET with ?q=, got 400: %s", w.Body.String())
	}
}

func TestGraphSearch_MissingQuery(t *testing.T) {
	s := newTestServer()
	s.graphCfg.Enabled = true

	body := `{}`
	r := postWithNamespace("/v1/search/graph", body, []string{"*"})
	w := httptest.NewRecorder()

	s.handleGraphSearch(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty query, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "missing query") {
		t.Errorf("expected 'missing query' in error body, got %q", w.Body.String())
	}
}
