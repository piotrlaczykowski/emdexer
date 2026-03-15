package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/piotrlaczykowski/emdexer/version"
)

func main() {
	showVersion := flag.Bool("version", false, "show version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("emdex version %s\n", version.Version)
		os.Exit(0)
	}

	fmt.Println("Emdexer CLI")
}
