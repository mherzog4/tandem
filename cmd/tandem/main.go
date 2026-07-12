// Command tandem is the host CLI. It wraps a terminal command (a coding
// agent like Claude Code, or any program) in a managed PTY and shares the
// session with browser guests. See prd.md.
package main

import (
	"context"
	"flag"
	"fmt"
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

	opts := ptywrap.Options{}
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
		fmt.Fprintln(os.Stderr, "tandem: Ctrl-\\ toggles the privacy shutter")
		opts.Tap = link
		opts.OnResize = func(cols, rows uint16) {
			_ = link.WriteControl(map[string]any{"type": "resize", "cols": cols, "rows": rows})
		}
		// Privacy shutter (FR4) on Ctrl-\. The byte is swallowed before
		// the child, so the wrapped TUI never sees it (and loses its
		// SIGQUIT binding — documented trade-off).
		shuttered := false
		opts.InterceptKey = 0x1C
		opts.OnIntercept = func() {
			shuttered = !shuttered
			link.SetShuttered(shuttered)
			if shuttered {
				// Bell + title: visible without corrupting the TUI.
				fmt.Fprint(os.Stdout, "\a\033]0;tandem ⏸ SHARING PAUSED\007")
			} else {
				fmt.Fprint(os.Stdout, "\a\033]0;tandem ● sharing live\007")
			}
		}
	}

	code, err := ptywrap.Run(argv, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tandem:", err)
		os.Exit(1)
	}
	os.Exit(code)
}
