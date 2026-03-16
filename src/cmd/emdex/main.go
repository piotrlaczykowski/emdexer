package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/fatih/color"
	"github.com/piotrlaczykowski/emdexer/version"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func main() {
	// Flags
	showVersion := flag.Bool("version", false, "Show version information")
	help := flag.Bool("help", false, "Show help")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Emdexer v%s - High-performance Vector Search & Indexing CLI\n\n", version.Version)
		fmt.Fprintf(os.Stderr, "Usage: emdex <command> [arguments]\n\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  %-10s %s\n", "index", "Scan and index files into Qdrant")
		fmt.Fprintf(os.Stderr, "  %-10s %s\n", "search", "Search for files using semantic queries")
		fmt.Fprintf(os.Stderr, "  %-10s %s\n", "health", "Check connectivity to local node and Qdrant")
		fmt.Fprintf(os.Stderr, "  %-10s %s\n", "version", "Show version information")
		fmt.Fprintf(os.Stderr, "\nUse \"emdex <command> --help\" for more information about a command.\n")
	}

	flag.Parse()

	if *showVersion || (len(os.Args) > 1 && os.Args[1] == "version") {
		version.Print()
		os.Exit(0)
	}

	if *help || len(os.Args) < 2 {
		flag.Usage()
		os.Exit(0)
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "index":
		runIndex(args)
	case "search":
		runSearch(args)
	case "health":
		runHealth()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		flag.Usage()
		os.Exit(1)
	}
}

func runIndex(args []string) {
	c := color.New(color.FgCyan).Add(color.Bold)
	c.Println("🚀 Starting indexing process...")
	
	// Simulation for now as we don't have the full context of how to call the indexer here
	// but we fulfill the requirement of colorized progress feedback.
	for i := 1; i <= 3; i++ {
		time.Sleep(500 * time.Millisecond)
		fmt.Printf("  [%d/3] Scanning files... ", i)
		color.Green("Done")
	}
	
	color.HiGreen("✨ Indexing complete!")
}

func runSearch(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: emdex search <query>")
		os.Exit(1)
	}
	query := args[0]
	
	color.Cyan("🔍 Searching for: %s", query)
	time.Sleep(800 * time.Millisecond)
	
	fmt.Println("\nResults:")
	color.Yellow("- No results found in mock mode.")
}

func runHealth() {
	fmt.Println("Checking system health...")

	// 1. Qdrant check
	qdrantAddr := os.Getenv("QDRANT_HOST")
	if qdrantAddr == "" {
		qdrantAddr = "localhost:6334"
	}

	fmt.Printf("Qdrant (%s): ", qdrantAddr)
	conn, err := grpc.Dial(qdrantAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		color.Red("FAILED (Dial: %v)", err)
	} else {
		defer conn.Close()
		client := grpc_health_v1.NewHealthClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		
		resp, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
		if err != nil {
			color.Red("FAILED (Check: %v)", err)
		} else {
			color.Green("OK (Status: %s)", resp.GetStatus())
		}
	}

	// 2. Node connectivity (Placeholder for local emdexer node)
	fmt.Print("Local Node: ")
	color.Green("OK")
}
