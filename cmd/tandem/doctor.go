package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

// doctor runs preflight checks so a first-time host can tell, before a real
// session, whether the relay is reachable and the terminal will cooperate.
// Returns a non-zero code if a required check fails.
func doctor(relay string) int {
	fmt.Println("tandem doctor — preflight checks")
	fmt.Println()
	ok := true

	// 1. Relay reachability via /healthz (ws -> http for the probe).
	httpURL := strings.Replace(strings.Replace(relay, "wss://", "https://", 1), "ws://", "http://", 1)
	healthURL := strings.TrimRight(httpURL, "/") + "/healthz"
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	start := time.Now()
	if resp, err := http.DefaultClient.Do(req); err != nil {
		ok = false
		fmt.Printf("  ✗ relay unreachable at %s\n      %v\n", relay, err)
	} else {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			fmt.Printf("  ✓ relay reachable — %s (%dms)\n", relay, time.Since(start).Milliseconds())
		} else {
			ok = false
			fmt.Printf("  ✗ relay returned HTTP %d at %s\n", resp.StatusCode, healthURL)
		}
	}

	// 2. Controlling TTY: the key intercepts and launch wait need one.
	if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Println("  ✓ terminal detected — Ctrl-] run, Ctrl-\\ shutter, Ctrl-^ copy-link are available")
	} else {
		ok = false
		fmt.Println("  ✗ no terminal — key intercepts and the share-then-launch wait are disabled")
	}

	// 3. Clipboard: OSC 52 support can't be probed, so name the terminal
	//    and point at the setting to check.
	tp := os.Getenv("TERM_PROGRAM")
	if tp == "" {
		tp = os.Getenv("TERM")
	}
	if tp == "" {
		tp = "unknown"
	}
	fmt.Printf("  • the join link is copied via OSC 52 (terminal: %s). If Ctrl-^ does\n", tp)
	fmt.Println("      not copy, enable clipboard access in your terminal's settings.")

	// 4. Bracketed-paste note for --no-mirror.
	fmt.Println("  • --no-mirror submits with bracketed paste; coding agents accept it,")
	fmt.Println("      but a plain shell may show marker cruft (use the default mirror).")

	fmt.Println()
	if ok {
		fmt.Println("ready — start a session with:  tandem <agent>   (e.g. tandem claude)")
		return 0
	}
	fmt.Println("some checks failed; see above.")
	return 1
}

// quickstart is the friendly no-args output: what tandem is and how to
// start, instead of a bare usage line.
func quickstart() {
	fmt.Println("tandem — a shared seat inside your coding-agent session.")
	fmt.Println()
	fmt.Println("Start a session and share the link it copies to your clipboard:")
	fmt.Println("  tandem claude            # or codex, aider, gemini, or any command")
	fmt.Println()
	fmt.Println("Your stakeholder joins in a browser and types into your prompt live;")
	fmt.Println("you review and press Ctrl-] to run it. They can't execute anything.")
	fmt.Println()
	fmt.Println("Useful flags and commands:")
	fmt.Println("  tandem doctor            check the relay and terminal before you start")
	fmt.Println("  --approve                admit each guest with Ctrl-G (waiting room)")
	fmt.Println("  --no-mirror              guests compose in a side panel instead of live")
	fmt.Println("  --no-wait                launch immediately instead of pausing to share")
	fmt.Println("  --relay wss://…          use a different relay (or TANDEM_RELAY)")
	fmt.Println("  --no-share               run locally with no session")
}
