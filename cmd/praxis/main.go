// Command praxis is the CLI entrypoint for the Praxis agent runtime.
//
// Subcommands (filled in as milestones land):
//
//	praxis run "<goal>"   run the DevOps agent headlessly on a goal
//	praxis serve          launch the web workbench
//	praxis version        print the version
//	praxis help           show usage
package main

import (
	"fmt"
	"os"
)

const version = "0.1.0"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage(os.Stdout)
		return
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Printf("praxis %s\n", version)
	case "help", "--help", "-h":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "praxis: unknown command %q\n\n", args[0])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprintf(w, `praxis %s - a from-scratch AI-agent orchestration runtime

Usage:
  praxis <command> [arguments]

Commands:
  run "<goal>"   run the DevOps agent on a goal (headless)
  serve          launch the web workbench (default :7777)
  version        print version
  help           show this help

Praxis has zero third-party dependencies: the graph executor, tool protocol,
memory/vector index, HTTP server, and web UI are all built on the Go standard
library. The reasoning model is a swappable external service.
`, version)
}
