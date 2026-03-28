package main

import (
	"os"

	"github.com/piotrlaczykowski/emdexer/version"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		version.Print()
		return
	}
	app := newApp()
	app.Run()
}
