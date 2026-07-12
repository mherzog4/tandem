// Command tandem is the host CLI. It wraps a terminal command (a coding
// agent like Claude Code, or any program) in a managed PTY and shares the
// session with browser guests. See prd.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mherzog4/tandem/internal/hostlink"
	"github.com/mherzog4/tandem/internal/ptywrap"
)

// version is overridden at release time via -ldflags "-X main.version=...".
var version = "0.0.1-dev"

func main() {
	relayURL := flag.String("relay", "", "relay base URL (ws:// or wss://); empty runs unshared")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()
	if *showVersion {
		fmt.Println("tandem", version)
		return
	}
	argv := flag.Args()
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "usage: tandem [--relay ws://host:port] <command> [args...]")
		os.Exit(2)
	}

	var tap io.Writer
	if *relayURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		link, err := hostlink.Connect(ctx, *relayURL)
		cancel()
		if err != nil {
			fmt.Fprintln(os.Stderr, "tandem:", err)
			os.Exit(1)
		}
		defer link.Close()
		fmt.Fprintf(os.Stderr, "tandem: session live — share %s\n", link.JoinURL)
		tap = link
	}

	code, err := ptywrap.Run(argv, tap)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tandem:", err)
		os.Exit(1)
	}
	os.Exit(code)
}
