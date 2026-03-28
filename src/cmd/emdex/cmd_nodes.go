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

// cmdNodes queries the gateway registry and displays a colorized node table.
func cmdNodes() {
	gatewayURL := os.Getenv("EMDEX_GATEWAY_URL")
	if gatewayURL == "" {
		gatewayURL = "http://localhost:7700"
	}
	authKey := os.Getenv("EMDEX_AUTH_KEY")
	if authKey == "" {
		fmt.Fprintf(os.Stderr, "  %s %s\n", "❌", ui.Red("EMDEX_AUTH_KEY required to query nodes"))
		os.Exit(1)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", gatewayURL+"/nodes", nil)
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

	var nodes []struct {
		ID            string   `json:"id"`
		URL           string   `json:"url"`
		Namespaces    []string `json:"namespaces"`
		Protocol      string   `json:"protocol"`
		HealthStatus  string   `json:"health_status"`
		LastHeartbeat string   `json:"last_heartbeat"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
		fmt.Fprintf(os.Stderr, "  %s %s\n", "❌", ui.Red("Invalid response from gateway"))
		os.Exit(1)
	}

	fmt.Printf("\n  %s  %s\n", ui.Bold("📡 Registered Nodes"), ui.Dim(fmt.Sprintf("(%d total)", len(nodes))))
	fmt.Printf("  %s\n\n", ui.Dim("─────────────────────────────────────────────────────────────────────────"))

	if len(nodes) == 0 {
		fmt.Printf("  %s\n\n", ui.Dim("No nodes registered. Nodes self-register when EMDEX_GATEWAY_URL is set."))
		return
	}

	// Header
	fmt.Printf("  %-24s  %-10s  %-10s  %-20s  %s\n",
		ui.Bold("NODE ID"), ui.Bold("STATUS"), ui.Bold("PROTOCOL"), ui.Bold("NAMESPACES"), ui.Bold("LAST HEARTBEAT"))
	fmt.Printf("  %s\n", ui.Dim("─────────────────────────────────────────────────────────────────────────"))

	for _, n := range nodes {
		statusFn := ui.Green
		if n.HealthStatus != "healthy" {
			statusFn = ui.Red
		}
		ago := ui.FormatTimeAgo(n.LastHeartbeat)
		nsStr := strings.Join(n.Namespaces, ", ")
		if nsStr == "" {
			nsStr = ui.Dim("(none)")
		}

		fmt.Printf("  %-24s  %-10s  %-10s  %-20s  %s\n",
			ui.Cyan(n.ID),
			statusFn(n.HealthStatus),
			n.Protocol,
			nsStr,
			ui.Dim(ago))
	}
	fmt.Println()
}
