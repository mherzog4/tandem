// Command tandem is the host CLI. It wraps a terminal command (a coding
// agent like Claude Code, or any program) in a managed PTY and shares the
// session with browser guests. See prd.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"sync/atomic"

	"github.com/mherzog4/tandem/internal/adapter"
	"github.com/mherzog4/tandem/internal/board"
	"github.com/mherzog4/tandem/internal/broker"
	"github.com/mherzog4/tandem/internal/domainfile"
	"github.com/mherzog4/tandem/internal/hostlink"
	"github.com/mherzog4/tandem/internal/mirror"
	"github.com/mherzog4/tandem/internal/ptywrap"
	"github.com/mherzog4/tandem/internal/redact"
	"github.com/mherzog4/tandem/internal/signer"
)

// version is overridden at release time via -ldflags "-X main.version=...".
var version = "0.0.1-dev"

func main() {
	relayURL := flag.String("relay", "", "relay base URL (ws:// or wss://); empty runs unshared")
	mirrorLive := flag.Bool("mirror", false, "live-mirror the Composer into the agent's input line (Claude Code; opt-in, see docs)")
	noRedact := flag.Bool("no-redact", false, "disable secret masking in the guest stream (FR23; host always sees originals)")
	allow := flag.String("allow", "", "comma-separated guest emails allowed to join (FR22; claimed, not verified)")
	recordIntent := flag.Bool("record", false, "declare the session recorded; guests must acknowledge before viewing (FR24)")
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
		fmt.Fprintf(os.Stderr, "tandem: your host link (confirm powers, keep private) %s&h=%s\n", link.JoinURL, b.HostToken)
		fmt.Fprintln(os.Stderr, "tandem: Ctrl-\\ shutter · Ctrl-] submit composer")
		if *allow != "" {
			link.SetAllowlist(strings.Split(*allow, ","))
			fmt.Fprintln(os.Stderr, "tandem: guest allowlist active (emails are claimed, not verified)")
		}
		if *recordIntent {
			link.SetRecording(true)
			fmt.Fprintln(os.Stderr, "tandem: session declared as recorded; guests must acknowledge")
		}

		// Serialize confirmed cards into the wrapped repo (FR14). The
		// working directory is where the agent runs, so DOMAIN.md lands
		// beside the code it describes. domainDirty flags that the model
		// changed since the last submit, so the agent can be told to
		// re-read it (CLAUDE.md imports load at conversation start only).
		var domainDirty atomic.Bool
		if cwd, err := os.Getwd(); err == nil {
			// Preload the Board from a previous session's domain.yaml
			// (FR20): the model accretes across meetings.
			if cards, err := domainfile.Load(cwd); err != nil {
				fmt.Fprintf(os.Stderr, "tandem: reading %s: %v (starting with an empty board)\n", domainfile.YAMLName, err)
			} else if len(cards) > 0 {
				b.Board.Load(cards)
				fmt.Fprintf(os.Stderr, "tandem: domain board preloaded — %d confirmed card(s) from %s\n", len(cards), domainfile.YAMLName)
			}
			b.OnBoardChange = func(cards []board.Card) {
				wrote, err := domainfile.WriteFiles(cwd, cards)
				if err != nil {
					fmt.Fprintf(os.Stderr, "tandem: writing domain files: %v\r\n", err)
				}
				if wrote {
					domainDirty.Store(true)
				}
			}
			// Claude Code adapter (FR15): managed CLAUDE.md include so
			// DOMAIN.md is in context each conversation.
			if adapter.IsClaude(argv) {
				if err := adapter.EnsureClaudeInclude(cwd); err != nil {
					fmt.Fprintf(os.Stderr, "tandem: CLAUDE.md include: %v\n", err)
				}
			}
		}
		opts.Tap = link
		// Secret masking (FR23) sits between the PTY tap and the link:
		// strictly pre-encryption, guests only. The host terminal shows
		// originals; a bell rings when masking fires, and a count prints
		// at session end.
		var red *redact.Redactor
		if !*noRedact {
			red = redact.New(link)
			red.OnRedact = func() { fmt.Fprint(os.Stdout, "\a") }
			opts.Tap = red
			defer func() {
				if n := red.Count.Load(); n > 0 {
					fmt.Fprintf(os.Stderr, "tandem: masked %d likely secret(s) from guests this session\n", n)
				}
			}()
		}
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
				if domainDirty.Swap(false) {
					text = "(The domain model changed — re-read DOMAIN.md before acting.)\n\n" + text
				}
				injector.Submit(sign.Sign(text))
			},
		}

		// Live mirroring (issue #13): opt-in, pauses while the host
		// types, degrades to nothing worse than submit-time injection.
		if *mirrorLive {
			var lastKey atomic.Int64
			opts.OnHostInput = func() { lastKey.Store(time.Now().UnixNano()) }
			mir := mirror.New(
				func(raw string) { injector.Submit(sign.SignRaw(raw)) },
				func() bool { return time.Since(time.Unix(0, lastKey.Load())) < time.Second },
			)
			b.OnChange = mir.Update
			// After a submit the agent clears its input line; forget
			// the mirrored state so the next compose starts clean.
			prevSubmit := opts.Intercepts[0x1D]
			opts.Intercepts[0x1D] = func() { mir.Reset(); prevSubmit() }
		}
	}

	code, err := ptywrap.Run(argv, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tandem:", err)
		os.Exit(1)
	}
	os.Exit(code)
}
