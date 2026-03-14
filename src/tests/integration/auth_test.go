package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthAndNamespaceIntegration(t *testing.T) {
	// API Keys config
	apiKeys := map[string][]string{
		"key-alpha": {"alpha"},
		"key-beta":  {"beta"},
		"key-admin": {"*"},
	}

	// Simple Middleware mock
	authenticate := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			key := strings.TrimPrefix(authHeader, "Bearer ")
			namespaces, ok := apiKeys[key]
			if !ok {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), "AllowedNamespaces", namespaces)
			next.ServeHTTP(w, r.WithContext(ctx))
		}
	}

	// Logic mock
	handleSearch := func(w http.ResponseWriter, r *http.Request) {
		allowed, _ := r.Context().Value("AllowedNamespaces").([]string)
		requested := r.URL.Query().Get("namespace")
		if requested == "" {
			requested = "default"
		}

		isAllowed := false
		for _, ns := range allowed {
			if ns == "*" || ns == requested {
				isAllowed = true
				break
			}
		}

		if !isAllowed {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}

	handler := authenticate(handleSearch)

	tests := []struct {
		name       string
		key        string
		namespace  string
		wantStatus int
	}{
		{"Scoped key 'alpha' accessing namespace 'alpha' -> 200", "key-alpha", "alpha", http.StatusOK},
		{"Scoped key 'alpha' accessing namespace 'beta' -> 403", "key-alpha", "beta", http.StatusForbidden},
		{"Wildcard key with missing namespace -> 200 (forces default)", "key-admin", "", http.StatusOK},
		{"Missing auth token -> 401", "", "alpha", http.StatusUnauthorized},
		{"Invalid key -> 401", "key-bad", "alpha", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/search?namespace="+tt.namespace, nil)
			if tt.key != "" {
				req.Header.Set("Authorization", "Bearer "+tt.key)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
		})
	}
}
