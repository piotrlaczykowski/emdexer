package auth

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
)

// OIDCConfig holds OIDC provider settings.
type OIDCConfig struct {
	Issuer      string // OIDC_ISSUER (e.g. https://accounts.google.com)
	ClientID    string // OIDC_CLIENT_ID
	GroupsClaim string // OIDC_GROUPS_CLAIM (default: "groups")
}

// OIDCVerifier validates OIDC JWT tokens and extracts user claims.
type OIDCVerifier struct {
	verifier    *oidc.IDTokenVerifier
	groupsClaim string
}

// NewOIDCVerifier creates an OIDCVerifier by discovering the provider's JWKS endpoint.
// Returns an error if the provider is unreachable (fail-secure).
func NewOIDCVerifier(ctx context.Context, cfg OIDCConfig) (*OIDCVerifier, error) {
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc provider discovery failed for %s: %w", cfg.Issuer, err)
	}

	groupsClaim := cfg.GroupsClaim
	if groupsClaim == "" {
		groupsClaim = "groups"
	}

	verifier := provider.Verifier(&oidc.Config{
		ClientID: cfg.ClientID,
	})

	return &OIDCVerifier{
		verifier:    verifier,
		groupsClaim: groupsClaim,
	}, nil
}

// VerifyJWT validates a JWT token and extracts user claims.
func (v *OIDCVerifier) VerifyJWT(ctx context.Context, token string) (*UserClaims, error) {
	idToken, err := v.verifier.Verify(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("jwt verification failed: %w", err)
	}

	// Extract standard claims
	var standard struct {
		Subject string `json:"sub"`
		Email   string `json:"email"`
	}
	if err := idToken.Claims(&standard); err != nil {
		return nil, fmt.Errorf("failed to parse standard claims: %w", err)
	}

	// Extract groups from the configured claim
	var allClaims map[string]interface{}
	if err := idToken.Claims(&allClaims); err != nil {
		return nil, fmt.Errorf("failed to parse all claims: %w", err)
	}

	var groups []string
	if raw, ok := allClaims[v.groupsClaim]; ok {
		switch g := raw.(type) {
		case []interface{}:
			for _, v := range g {
				if s, ok := v.(string); ok {
					groups = append(groups, s)
				}
			}
		case []string:
			groups = g
		}
	}

	return &UserClaims{
		Subject:  standard.Subject,
		Email:    standard.Email,
		Groups:   groups,
		AuthType: "oidc",
	}, nil
}
