package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/piotrlaczykowski/emdexer/ui"
)

// cmdStart runs docker compose up -d.
func cmdStart() {
	composeFile := os.Getenv("EMDEX_COMPOSE_FILE")
	if composeFile == "" {
		composeFile = "docker-compose.yml"
	}

	args := []string{"compose", "-f", composeFile, "up", "-d"}
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("\n  %s %s %s\n\n", "🚀", ui.Bold("Starting emdexer services"), ui.Dim("("+composeFile+")"))
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "\n  %s %s: %v\n", "❌", ui.Red("Failed to start services"), err)
		os.Exit(1)
	}
	fmt.Printf("\n  %s %s\n\n", "✅", ui.Green("All services started. Run 'emdex status' to verify."))
}
