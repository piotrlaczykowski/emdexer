package main

import (
	"fmt"
	"strings"

	"github.com/piotrlaczykowski/emdexer/ui"
)

// searchResult is the decoded response from /v1/search.
type searchResult struct {
	Query              string                   `json:"query"`
	NamespacesSearched []string                 `json:"namespaces_searched"`
	PartialFailures    []string                 `json:"partial_failures"`
	Results            []map[string]interface{} `json:"results"`
}

// printSearchResults prints the formatted search output to stdout.
func printSearchResults(result searchResult) {
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
