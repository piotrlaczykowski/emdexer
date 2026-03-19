package auth

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const AllowedNamespacesKey contextKey = "AllowedNamespaces"

// Config holds authentication credentials.
type Config struct {
	AuthKey string              // Simple mode: single shared bearer token
	APIKeys map[string][]string // Advanced mode: per-key namespace ACL
}

// Middleware returns an HTTP middleware that validates Bearer tokens.
func (c *Config) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		key := parts[1]
		if c.APIKeys != nil {
			allowedNamespaces, ok := c.APIKeys[key]
			if ok {
				ctx := context.WithValue(r.Context(), AllowedNamespacesKey, allowedNamespaces)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		if c.AuthKey != "" && key == c.AuthKey {
			ctx := context.WithValue(r.Context(), AllowedNamespacesKey, []string{"*"})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}
}

// GetAllowedNamespaces extracts the allowed namespaces from the request context.
func GetAllowedNamespaces(r *http.Request) ([]string, bool) {
	ns, ok := r.Context().Value(AllowedNamespacesKey).([]string)
	return ns, ok
}
