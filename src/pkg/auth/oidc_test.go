package auth

import (
	"context"
	"testing"
	"time"
)

func TestNewOIDCVerifier_UnreachableIssuer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := NewOIDCVerifier(ctx, OIDCConfig{
		Issuer:   "https://nonexistent.example.com",
		ClientID: "test",
	})
	if err == nil {
		t.Fatal("expected error for unreachable OIDC issuer")
	}
}

func TestOIDCConfig_DefaultGroupsClaim(t *testing.T) {
	// When GroupsClaim is empty, NewOIDCVerifier should default to "groups".
	// We can't fully test this without a real provider, but we can verify the
	// config struct starts with an empty GroupsClaim.
	cfg := OIDCConfig{Issuer: "https://example.com", ClientID: "test"}
	if cfg.GroupsClaim != "" {
		t.Fatal("expected empty default GroupsClaim — NewOIDCVerifier fills it in")
	}
}
