package main

import (
	"fmt"

	"github.com/piotrlaczykowski/emdexer/ui"
	"github.com/piotrlaczykowski/emdexer/version"
)

func printUsage() {
	fmt.Println()
	fmt.Printf("  %s  %s\n", ui.Bold("📦 Emdexer CLI"), ui.Dim("v"+version.Version))
	fmt.Println()
	fmt.Printf("  %s emdex <command>\n", ui.Dim("Usage:"))
	fmt.Println()
	fmt.Printf("  %s\n", ui.Bold("Commands:"))
	fmt.Printf("    %s      Initialize a new emdexer project (.env)\n", ui.Cyan("init"))
	fmt.Printf("    %s     Start emdexer services via Docker Compose\n", ui.Cyan("start"))
	fmt.Printf("    %s    Show status of emdexer services\n", ui.Cyan("status"))
	fmt.Printf("    %s     List registered nodes and their status\n", ui.Cyan("nodes"))
	fmt.Printf("    %s    Search indexed documents  %s\n", ui.Cyan("search"), ui.Dim("(--namespace, --global, --limit)"))
	fmt.Printf("    %s      Chat with the LLM  %s\n", ui.Cyan("chat"), ui.Dim("(--stream, --namespace, --model)"))
	fmt.Printf("    %s    Show current caller identity and authorized namespaces\n", ui.Cyan("whoami"))
	fmt.Println()
	fmt.Printf("  %s\n", ui.Bold("Flags:"))
	fmt.Printf("    %s  Show version information\n", ui.Cyan("--version"))
	fmt.Println()
}
