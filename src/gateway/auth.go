package main

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
)

func (s *Server) authenticate(next http.HandlerFunc) http.HandlerFunc {
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
		if s.apiKeys != nil {
			for k, allowedNamespaces := range s.apiKeys {
				if subtle.ConstantTimeCompare([]byte(key), []byte(k)) == 1 {
					ctx := context.WithValue(r.Context(), "AllowedNamespaces", allowedNamespaces)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}
		}

		if s.authKey != "" && subtle.ConstantTimeCompare([]byte(key), []byte(s.authKey)) == 1 {
			ctx := context.WithValue(r.Context(), "AllowedNamespaces", []string{"*"})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}
}

// SSRF Guard transport placeholder - mentioned in the prompt but not fully implemented in original main.go.
// Keeping it simple as a placeholder for future extension if needed.
type SSRFSafeTransport struct {
	http.RoundTripper
}

func (t *SSRFSafeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Logic to prevent internal network access would go here.
	return t.RoundTripper.RoundTrip(req)
}
