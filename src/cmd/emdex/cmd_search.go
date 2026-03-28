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

// cmdSearch queries the gateway's /v1/search endpoint.
// Usage: emdex search <query> [--namespace=<ns>] [--global] [--limit=<n>]
func cmdSearch() {
	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Printf("\n  %s\n\n", ui.Bold("emdex search <query> [flags]"))
		fmt.Printf("  %s\n", ui.Bold("Flags:"))
		fmt.Printf("    %s       Namespace to search (default: 'default')\n", ui.Cyan("--namespace=<ns>"))
		fmt.Printf("    %s           Fan-out across all authorized namespaces\n", ui.Cyan("--global"))
		fmt.Printf("    %s       Max results to return\n", ui.Cyan("--limit=<n>"))
		fmt.Println()
		return
	}

	gatewayURL := os.Getenv("EMDEX_GATEWAY_URL")
	if gatewayURL == "" {
		gatewayURL = "http://localhost:7700"
	}
	authKey := os.Getenv("EMDEX_AUTH_KEY")
	if authKey == "" {
		fmt.Fprintf(os.Stderr, "  %s %s\n", "❌", ui.Red("EMDEX_AUTH_KEY required to search"))
		os.Exit(1)
	}

	// Parse flags and collect query words.
	namespace := ""
	global := false
	limit := ""
	var queryParts []string

	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--namespace="):
			namespace = strings.TrimPrefix(arg, "--namespace=")
		case arg == "--global":
			global = true
		case strings.HasPrefix(arg, "--limit="):
			limit = strings.TrimPrefix(arg, "--limit=")
		default:
			queryParts = append(queryParts, arg)
		}
	}

	query := strings.Join(queryParts, " ")
	if query == "" {
		fmt.Fprintf(os.Stderr, "  %s %s\n", "❌", ui.Red("Search query required"))
		os.Exit(1)
	}

	if global {
		namespace = "*"
	}
	if namespace == "" {
		namespace = os.Getenv("EMDEX_NAMESPACE")
	}
	if namespace == "" {
		namespace = "default"
	}

	searchURL := fmt.Sprintf("%s/v1/search?q=%s&namespace=%s", gatewayURL,
		strings.ReplaceAll(query, " ", "+"), namespace)
	if limit != "" {
		searchURL += "&limit=" + limit
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequest("GET", searchURL, nil)
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

	var result searchResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Fprintf(os.Stderr, "  %s %s\n", "❌", ui.Red("Invalid response from gateway"))
		os.Exit(1)
	}

	printSearchResults(result)
}
