package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/piotrlaczykowski/emdexer/ui"
)

// cmdChat sends a chat completion request to the gateway and prints the response.
// Usage: emdex chat <prompt> [--stream] [--namespace=<ns>] [--model=<model>]
func cmdChat() {
	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Printf("\n  %s\n\n", ui.Bold("emdex chat <prompt> [flags]"))
		fmt.Printf("  %s\n", ui.Bold("Flags:"))
		fmt.Printf("    %s           Stream tokens as they arrive (uses SSE)\n", ui.Cyan("--stream"))
		fmt.Printf("    %s       Namespace context for RAG retrieval\n", ui.Cyan("--namespace=<ns>"))
		fmt.Printf("    %s          Gemini model to use\n", ui.Cyan("--model=<model>"))
		fmt.Println()
		return
	}

	gatewayURL := os.Getenv("EMDEX_GATEWAY_URL")
	if gatewayURL == "" {
		gatewayURL = "http://localhost:7700"
	}
	authKey := os.Getenv("EMDEX_AUTH_KEY")
	if authKey == "" {
		fmt.Fprintf(os.Stderr, "  %s %s\n", "❌", ui.Red("EMDEX_AUTH_KEY required to chat"))
		os.Exit(1)
	}

	stream := false
	namespace := ""
	model := "gemini-2.0-flash"
	var promptParts []string

	for _, arg := range args {
		switch {
		case arg == "--stream":
			stream = true
		case strings.HasPrefix(arg, "--namespace="):
			namespace = strings.TrimPrefix(arg, "--namespace=")
		case strings.HasPrefix(arg, "--model="):
			model = strings.TrimPrefix(arg, "--model=")
		default:
			promptParts = append(promptParts, arg)
		}
	}

	prompt := strings.Join(promptParts, " ")
	if prompt == "" {
		fmt.Fprintf(os.Stderr, "  %s %s\n", "❌", ui.Red("Chat prompt required"))
		os.Exit(1)
	}

	if namespace == "" {
		namespace = os.Getenv("EMDEX_NAMESPACE")
	}
	if namespace == "" {
		namespace = "default"
	}

	body := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"stream":    stream,
		"namespace": namespace,
	}
	bodyBytes, _ := json.Marshal(body)

	// No timeout: streaming responses can be arbitrarily long.
	client := &http.Client{}
	req, _ := http.NewRequest("POST", gatewayURL+"/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer "+authKey)
	req.Header.Set("Content-Type", "application/json")

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

	fmt.Printf("\n  %s  %s\n\n", "💬", ui.Bold("Response"))

	if stream {
		printStreamedChatResponse(resp.Body)
	} else {
		printFullChatResponse(resp.Body)
	}
}
