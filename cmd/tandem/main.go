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

	"github.com/mherzog4/tandem/internal/broker"
	"github.com/mherzog4/tandem/internal/hostlink"
	"github.com/mherzog4/tandem/internal/ptywrap"
	"github.com/mherzog4/tandem/internal/signer"
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
		b := broker.New(link)
		go b.Run()
		fmt.Fprintf(os.Stderr, "tandem: session live — share %s\n", link.JoinURL)
		fmt.Fprintln(os.Stderr, "tandem: Ctrl-\\ shutter · Ctrl-] submit composer")
		opts.Tap = link
		opts.OnResize = func(cols, rows uint16) {
			_ = link.WriteControl(map[string]any{"type": "resize", "cols": cols, "rows": rows})
		}
		// Host-only submit path (FR8/FR21): Ctrl-] flushes the Composer
		// through the signing chokepoint into the PTY. Only the host's
		// terminal can trigger this — guests have no message for it.
		sign, err := signer.New()
		if err != nil {
			fmt.Fprintln(os.Stderr, "tandem:", err)
			os.Exit(1)
		}
		injector := ptywrap.NewInjector(signer.NewVerifier(sign.Public()))
		opts.Injector = injector

		// Privacy shutter (FR4) on Ctrl-\. Intercepted bytes are
		// swallowed before the child, so the wrapped TUI never sees
		// them (it loses Ctrl-\/SIGQUIT and Ctrl-] — documented).
		shuttered := false
		opts.Intercepts = map[byte]func(){
			0x1C: func() { // Ctrl-\ : shutter
				shuttered = !shuttered
				link.SetShuttered(shuttered)
				if shuttered {
					// Bell + title: visible without corrupting the TUI.
					fmt.Fprint(os.Stdout, "\a\033]0;tandem ⏸ SHARING PAUSED\007")
				} else {
					fmt.Fprint(os.Stdout, "\a\033]0;tandem ● sharing live\007")
				}
			},
			0x1D: func() { // Ctrl-] : submit the Composer buffer
				text, _ := b.Flush()
				if text == "" {
					return
				}
				injector.Submit(sign.Sign(text))
			},
		}
	}

	code, err := ptywrap.Run(argv, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tandem:", err)
		os.Exit(1)
	}
	os.Exit(code)
}
