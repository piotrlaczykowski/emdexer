package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/piotrlaczykowski/emdexer/ui"
)

// cmdStatus checks health endpoints of gateway and node, including worker heartbeat.
func cmdStatus() {
	gatewayURL := os.Getenv("EMDEX_GATEWAY_URL")
	if gatewayURL == "" {
		gatewayURL = "http://localhost:7700"
	}
	nodeURL := os.Getenv("EMDEX_NODE_URL")
	if nodeURL == "" {
		nodeURL = "http://localhost:8081"
	}

	fmt.Printf("\n  %s  %s\n", "📦", ui.Bold("Emdexer Service Status"))
	fmt.Printf("  %s\n\n", ui.Dim("────────────────────────────────────"))

	whisperURL := os.Getenv("EMDEX_WHISPER_URL")
	if whisperURL == "" {
		whisperURL = "http://localhost:8080"
	}

	gwStatus, gwOK := checkHealth(gatewayURL + "/healthz/readiness")
	nodeStatus, nodeOK := checkHealth(nodeURL + "/healthz/readiness")
	workerStatus := checkWorker(nodeURL + "/healthz/worker")
	whisperStatus, whisperOK := checkHealth(whisperURL + "/health")

	regStatus := checkRegistry(gatewayURL)

	printStatusLine("Gateway", gatewayURL, gwStatus, gwOK)
	printStatusLine("Node", nodeURL, nodeStatus, nodeOK)
	printStatusLine("Whisper", whisperURL, whisperStatus, whisperOK)
	fmt.Printf("  %s  %-10s %s\n", workerStatus.emoji, ui.Bold("Worker"), workerStatus.detail)
	fmt.Printf("  %s  %-10s %s\n", regStatus.emoji, ui.Bold("Registry"), regStatus.detail)

	fmt.Printf("\n  %s\n\n", ui.Dim("────────────────────────────────────"))
}

func printStatusLine(name, url, status string, ok bool) {
	emoji := "✅"
	coloredStatus := ui.Green(status)
	if !ok {
		emoji = "❌"
		coloredStatus = ui.Red(status)
	}
	fmt.Printf("  %s  %-10s %s  %s\n", emoji, ui.Bold(name), coloredStatus, ui.Dim(url))
}

func checkHealth(url string) (string, bool) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "DOWN (unreachable)", false
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return "UP", true
	}
	return fmt.Sprintf("UNHEALTHY (%d)", resp.StatusCode), false
}

func checkWorker(url string) workerResult {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return workerResult{"❌", ui.Red("DOWN") + "  " + ui.Dim("(unreachable)")}
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return workerResult{"❓", ui.Yellow("UNKNOWN") + "  " + ui.Dim("(bad response)")}
	}

	status, _ := result["status"].(string)
	lastActive, _ := result["last_active"].(string)

	switch {
	case resp.StatusCode == http.StatusOK && status == "ALIVE":
		ago := ui.FormatTimeAgo(lastActive)
		return workerResult{"✅", ui.Green("ALIVE") + "  " + ui.Dim("last heartbeat "+ago)}
	case status == "STALE":
		ago := ui.FormatTimeAgo(lastActive)
		return workerResult{"⚠️", ui.Yellow("STALLED") + "  " + ui.Dim("no heartbeat for "+ago) + "\n" +
			"        " + ui.Dim("→ Worker may be stuck. Check node logs: journalctl -u emdex-node -f")}
	case status == "NO_WORKER":
		return workerResult{"⚠️", ui.Yellow("NO_WORKER") + "  " + ui.Dim("no background indexing worker registered")}
	default:
		return workerResult{"❓", ui.Yellow(status)}
	}
}

// checkRegistry verifies the gateway's node registry is reachable and parseable.
// It also counts distinct namespaces across all nodes for the topology summary.
func checkRegistry(gatewayURL string) workerResult {
	authKey := os.Getenv("EMDEX_AUTH_KEY")
	if authKey == "" {
		return workerResult{"⚠️", ui.Yellow("SKIPPED") + "  " + ui.Dim("set EMDEX_AUTH_KEY to check registry")}
	}

	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequest("GET", gatewayURL+"/nodes", nil)
	if err != nil {
		return workerResult{"❌", ui.Red("ERROR") + "  " + ui.Dim(err.Error())}
	}
	req.Header.Set("Authorization", "Bearer "+authKey)

	resp, err := client.Do(req)
	if err != nil {
		return workerResult{"❌", ui.Red("DOWN") + "  " + ui.Dim("(gateway unreachable)")}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return workerResult{"⚠️", ui.Yellow("AUTH FAILED") + "  " + ui.Dim("check EMDEX_AUTH_KEY")}
	}
	if resp.StatusCode != http.StatusOK {
		return workerResult{"❌", ui.Red(fmt.Sprintf("HTTP %d", resp.StatusCode))}
	}

	var nodes []struct {
		Namespaces []string `json:"namespaces"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
		return workerResult{"❌", ui.Red("CORRUPT") + "  " + ui.Dim("registry response not valid JSON")}
	}

	// Count distinct namespaces across all nodes for the topology summary.
	nsSet := make(map[string]struct{})
	for _, n := range nodes {
		for _, ns := range n.Namespaces {
			nsSet[ns] = struct{}{}
		}
	}
	detail := fmt.Sprintf("OK  %s  %s",
		ui.Green(fmt.Sprintf("%d nodes", len(nodes))),
		ui.Cyan(fmt.Sprintf("%d namespaces", len(nsSet))),
	)
	return workerResult{"✅", detail}
}
