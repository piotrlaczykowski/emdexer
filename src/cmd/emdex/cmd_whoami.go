package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/piotrlaczykowski/emdexer/ui"
)

// cmdWhoami queries the gateway's /v1/whoami endpoint and displays the caller's identity.
func cmdWhoami() {
	gatewayURL := os.Getenv("EMDEX_GATEWAY_URL")
	if gatewayURL == "" {
		gatewayURL = "http://localhost:7700"
	}
	authKey := os.Getenv("EMDEX_AUTH_KEY")
	if authKey == "" {
		fmt.Fprintf(os.Stderr, "  %s %s\n", "❌", ui.Red("EMDEX_AUTH_KEY required"))
		os.Exit(1)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", gatewayURL+"/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+authKey)

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  %s %s: %v\n", "❌", ui.Red("Cannot reach gateway"), err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "  %s HTTP %d from gateway\n", "❌", resp.StatusCode)
		os.Exit(1)
	}

	var identity struct {
		AuthType   string   `json:"auth_type"`
		Subject    string   `json:"subject"`
		Email      string   `json:"email"`
		Groups     []string `json:"groups"`
		Namespaces []string `json:"namespaces"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&identity); err != nil {
		fmt.Fprintf(os.Stderr, "  %s %s\n", "❌", ui.Red("Invalid response"))
		os.Exit(1)
	}

	fmt.Printf("\n  %s  %s\n", "🔑", ui.Bold("Identity"))
	fmt.Printf("  %s\n\n", ui.Dim("────────────────────────────────────"))

	// Color auth type: green for OIDC, yellow for API-Key
	authColor := ui.Yellow
	if identity.AuthType == "oidc" {
		authColor = ui.Green
	}
	fmt.Printf("  %-14s %s\n", ui.Bold("Auth Type"), authColor(identity.AuthType))

	if identity.Subject != "" {
		fmt.Printf("  %-14s %s\n", ui.Bold("Subject"), identity.Subject)
	}
	if identity.Email != "" {
		fmt.Printf("  %-14s %s\n", ui.Bold("Email"), identity.Email)
	}
	if len(identity.Groups) > 0 {
		fmt.Printf("  %-14s %s\n", ui.Bold("Groups"), strings.Join(identity.Groups, ", "))
	}
	if len(identity.Namespaces) > 0 {
		fmt.Printf("  %-14s %s\n", ui.Bold("Namespaces"), ui.Cyan(strings.Join(identity.Namespaces, ", ")))
	} else {
		fmt.Printf("  %-14s %s\n", ui.Bold("Namespaces"), ui.Dim("(none)"))
	}

	fmt.Printf("\n  %s\n\n", ui.Dim("────────────────────────────────────"))
}
