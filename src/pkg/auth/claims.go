package auth

import (
	"context"
	"net/http"
)

// UserClaims holds the identity of an authenticated user.
type UserClaims struct {
	Subject  string   `json:"sub"`
	Email    string   `json:"email"`
	Groups   []string `json:"groups,omitempty"`
	AuthType string   `json:"auth_type"` // "oidc" or "api-key"
}

const userClaimsKey contextKey = "UserClaims"

// WithUserClaims stores UserClaims in the context.
func WithUserClaims(ctx context.Context, claims *UserClaims) context.Context {
	return context.WithValue(ctx, userClaimsKey, claims)
}

// GetUserClaims extracts UserClaims from the request context.
func GetUserClaims(r *http.Request) (*UserClaims, bool) {
	c, ok := r.Context().Value(userClaimsKey).(*UserClaims)
	return c, ok
}
