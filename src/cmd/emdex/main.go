package main

import (
	"flag"
	"fmt"
	"os"

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
		case "eval":
			cmdEval()
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
