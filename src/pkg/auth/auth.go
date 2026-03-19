package auth

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const AllowedNamespacesKey contextKey = "AllowedNamespaces"

// Config holds authentication credentials for both OIDC and legacy static keys.
type Config struct {
	// Legacy static keys
	AuthKey string              // Simple mode: single shared bearer token
	APIKeys map[string][]string // Advanced mode: per-key namespace ACL

	// OIDC (optional — enabled when OIDC is non-nil)
	OIDC     *OIDCVerifier
	GroupACL *GroupACL
}

// Middleware returns an HTTP middleware that validates Bearer tokens.
// Auth strategy: try OIDC first (if configured), then fall back to legacy static keys.
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

		token := parts[1]

		// Step 1: Try OIDC JWT validation (if configured)
		if c.OIDC != nil {
			claims, err := c.OIDC.VerifyJWT(r.Context(), token)
			if err == nil {
				// OIDC auth succeeded — resolve namespaces from group ACL
				var namespaces []string
				if c.GroupACL != nil {
					namespaces = c.GroupACL.ResolveNamespaces(claims.Groups)
				}
				if len(namespaces) == 0 {
					// User authenticated but has no authorized namespaces
					http.Error(w, "Forbidden: no namespaces authorized for your groups", http.StatusForbidden)
					return
				}
				ctx := r.Context()
				ctx = context.WithValue(ctx, AllowedNamespacesKey, namespaces)
				ctx = WithUserClaims(ctx, claims)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			// OIDC verification failed — fall through to legacy auth
		}

		// Step 2: Legacy static key auth
		if c.APIKeys != nil {
			allowedNamespaces, ok := c.APIKeys[token]
			if ok {
				ctx := context.WithValue(r.Context(), AllowedNamespacesKey, allowedNamespaces)
				ctx = WithUserClaims(ctx, &UserClaims{AuthType: "api-key"})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		if c.AuthKey != "" && token == c.AuthKey {
			ctx := context.WithValue(r.Context(), AllowedNamespacesKey, []string{"*"})
			ctx = WithUserClaims(ctx, &UserClaims{AuthType: "api-key"})
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
