package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/piotrlaczykowski/emdexer/ui"
	"github.com/piotrlaczykowski/emdexer/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1][0] != '-' {
		switch os.Args[1] {
		case "init":
			cmdInit()
		case "start":
			cmdStart()
		case "status":
			cmdStatus()
		case "nodes":
			cmdNodes()
		case "search":
			cmdSearch()
		case "whoami":
			cmdWhoami()
		case "chat":
			cmdChat()
		default:
			fmt.Fprintf(os.Stderr, "\n  %s %s: %s\n", "❌", ui.Red("Unknown command"), os.Args[1])
			printUsage()
			os.Exit(1)
		}
		return
	}

	showVersion := flag.Bool("version", false, "show version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("emdex version %s\n", version.Version)
		os.Exit(0)
	}

	printUsage()
}

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

	var result struct {
		Query              string                   `json:"query"`
		NamespacesSearched []string                 `json:"namespaces_searched"`
		PartialFailures    []string                 `json:"partial_failures"`
		Results            []map[string]interface{} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Fprintf(os.Stderr, "  %s %s\n", "❌", ui.Red("Invalid response from gateway"))
		os.Exit(1)
	}

	fmt.Printf("\n  %s  %s\n", "🔍", ui.Bold(fmt.Sprintf("Search: %q", result.Query)))
	if len(result.NamespacesSearched) > 0 {
		fmt.Printf("  %s %s\n", ui.Dim("Namespaces:"), ui.Cyan(strings.Join(result.NamespacesSearched, ", ")))
	}
	if len(result.PartialFailures) > 0 {
		fmt.Printf("  %s %s\n", "⚠️", ui.Yellow(fmt.Sprintf("partial failures: %s", strings.Join(result.PartialFailures, ", "))))
	}
	fmt.Printf("  %s\n\n", ui.Dim(fmt.Sprintf("────────────────────────────────────  (%d results)", len(result.Results))))

	if len(result.Results) == 0 {
		fmt.Printf("  %s\n\n", ui.Dim("No results found."))
		return
	}

	for i, r := range result.Results {
		payload, _ := r["payload"].(map[string]interface{})
		if payload == nil {
			payload = r
		}
		text, _ := payload["text"].(string)
		path, _ := payload["path"].(string)
		ns, _ := payload["namespace"].(string)
		if ns == "" {
			ns, _ = payload["source_namespace"].(string)
		}
		score, _ := r["score"].(float64)

		fmt.Printf("  %s  %s\n", ui.Cyan(fmt.Sprintf("[%d]", i+1)), ui.Bold(path))
		if ns != "" {
			fmt.Printf("      %s %s  %s %.4f\n", ui.Dim("namespace:"), ui.Cyan(ns), ui.Dim("score:"), score)
		}
		if len(text) > 200 {
			text = text[:200] + "…"
		}
		if text != "" {
			fmt.Printf("      %s\n", ui.Dim(text))
		}
		fmt.Println()
	}
}

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
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}
			var chunk struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}
			if len(chunk.Choices) > 0 {
				fmt.Print(chunk.Choices[0].Delta.Content)
			}
		}
		fmt.Println()
	} else {
		var result struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			fmt.Fprintf(os.Stderr, "  %s %s\n", "❌", ui.Red("Invalid response from gateway"))
			os.Exit(1)
		}
		if len(result.Choices) > 0 {
			fmt.Printf("  %s\n\n", result.Choices[0].Message.Content)
		}
	}
}
