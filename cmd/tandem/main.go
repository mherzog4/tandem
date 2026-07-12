// Command tandem is the host CLI. It wraps a terminal command (a coding
// agent like Claude Code, or any program) in a managed PTY and shares the
// session with browser guests. See prd.md.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"sync/atomic"

	"github.com/mherzog4/tandem/internal/adapter"
	"github.com/mherzog4/tandem/internal/board"
	"github.com/mherzog4/tandem/internal/broker"
	"github.com/mherzog4/tandem/internal/domainfile"
	"github.com/mherzog4/tandem/internal/extract"
	"github.com/mherzog4/tandem/internal/hostlink"
	"github.com/mherzog4/tandem/internal/mirror"
	"github.com/mherzog4/tandem/internal/ptywrap"
	"github.com/mherzog4/tandem/internal/recap"
	"github.com/mherzog4/tandem/internal/record"
	"github.com/mherzog4/tandem/internal/redact"
	"github.com/mherzog4/tandem/internal/signer"

	"golang.org/x/term"
)

// version is overridden at release time via -ldflags "-X main.version=...".
var version = "0.0.1-dev"

// defaultRelay is the hosted relay tandem shares through when neither
// --relay nor TANDEM_RELAY is set. Overridable at build time via
// -ldflags "-X main.defaultRelay=wss://your-relay".
var defaultRelay = "wss://tandem-relay-production.up.railway.app"

// main is a thin wrapper so run's defers (recap write, link close,
// extractor stop, redaction summary) actually execute — os.Exit skips
// deferred calls, so the exit code must return up to here.
func main() { os.Exit(run()) }

func run() int {
	relayURL := flag.String("relay", "", "relay base URL (ws:// or wss://); overrides TANDEM_RELAY and the built-in default")
	noShare := flag.Bool("no-share", false, "run the agent locally with no relay/session (unshared)")
	noWait := flag.Bool("no-wait", false, "launch the agent immediately instead of pausing to share the link first")
	noMirror := flag.Bool("no-mirror", false, "don't mirror the Composer into the agent's input line (guests then compose in the panel; Ctrl-] pastes it)")
	noRedact := flag.Bool("no-redact", false, "disable secret masking in the guest stream (FR23; host always sees originals)")
	allow := flag.String("allow", "", "comma-separated guest emails allowed to join (FR22; claimed, not verified)")
	approve := flag.Bool("approve", false, "hold each guest in a waiting room until you admit them with Ctrl-G (link-leak defense)")
	recordIntent := flag.Bool("record", false, "declare the session recorded; guests must acknowledge before viewing (FR24)")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()
	if *showVersion {
		fmt.Println("tandem", version)
		return 0
	}
	argv := flag.Args()
	if len(argv) == 0 {
		quickstart()
		return 2
	}

	// Relay resolution: --relay flag → TANDEM_RELAY env → built-in
	// default. --no-share opts out of sharing entirely.
	relay := *relayURL
	if relay == "" {
		relay = os.Getenv("TANDEM_RELAY")
	}
	if relay == "" {
		relay = defaultRelay
	}

	// Preflight subcommand: check the configured relay and terminal, then
	// exit. Runs before --no-share blanks the relay so it always probes one.
	if argv[0] == "doctor" {
		return doctor(relay)
	}

	if *noShare {
		relay = ""
	}

	agentKind := adapter.Detect(argv)
	opts := ptywrap.Options{}
	if relay != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		link, err := hostlink.Connect(ctx, relay)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "tandem: could not reach the relay at %s\n", relay)
			fmt.Fprintf(os.Stderr, "        (%v)\n", err)
			fmt.Fprintln(os.Stderr, "        Set a different one with --relay wss://… or TANDEM_RELAY,")
			fmt.Fprintln(os.Stderr, "        or run unshared with --no-share.")
			return 1
		}
		defer func() { _ = link.Close() }()
		b := broker.New(link)
		go b.Run()
		fmt.Fprintf(os.Stderr, "tandem: session live — share %s\n", link.JoinURL)
		fmt.Fprintf(os.Stderr, "tandem: your host link (confirm powers, keep private) %s&h=%s\n", link.JoinURL, b.HostToken)
		fmt.Fprintln(os.Stderr, "tandem: Ctrl-\\ shutter · Ctrl-] submit composer · Ctrl-^ re-copy join link")
		if *allow != "" {
			link.SetAllowlist(strings.Split(*allow, ","))
			fmt.Fprintln(os.Stderr, "tandem: guest allowlist active (emails are claimed, not verified)")
		}

		// Waiting room (--approve): guests queue until the host admits the
		// next one with Ctrl-G. The queue is FIFO; a hostile link-holder
		// never reaches the terminal without an explicit admit.
		var pendMu sync.Mutex
		var pendCIDs []string
		pendName := map[string]string{}
		if *approve {
			link.SetApproval(true)
			fmt.Fprintln(os.Stderr, "tandem: approval mode on — guests wait until you admit them (Ctrl-G admits the next)")
			link.OnJoinRequest = func(name, cid string) {
				pendMu.Lock()
				pendCIDs = append(pendCIDs, cid)
				pendName[cid] = name
				n := len(pendCIDs)
				pendMu.Unlock()
				fmt.Fprintf(os.Stderr, "\r\ntandem: 🔔 %q wants to join (%d waiting) — Ctrl-G to admit\r\n", name, n)
				fmt.Fprint(os.Stdout, "\a")
			}
		}
		var rc *record.Recorder
		if *recordIntent {
			link.SetRecording(true)
			// Cast file sits beside the recap. Records the REDACTED
			// stream, so masked secrets stay masked in replay (FR19).
			castName := "tandem-cast-" + time.Now().Format("2006-01-02-1504") + ".cast"
			if f, err := os.Create(castName); err == nil {
				rc, _ = record.New(f, 120, 40)
				defer f.Close()
				fmt.Fprintf(os.Stderr, "tandem: recording to %s (guests must acknowledge)\n", castName)
			} else {
				fmt.Fprintf(os.Stderr, "tandem: could not open cast file: %v\n", err)
			}
		}

		// Serialize confirmed cards into the wrapped repo (FR14). The
		// working directory is where the agent runs, so DOMAIN.md lands
		// beside the code it describes. domainDirty flags that the model
		// changed since the last submit, so the agent can be told to
		// re-read it (CLAUDE.md imports load at conversation start only).
		var domainDirty atomic.Bool
		var recorder *recap.Recorder
		if cwd, err := os.Getwd(); err == nil {
			// Preload the Board from a previous session's domain.yaml
			// (FR20): the model accretes across meetings.
			if cards, err := domainfile.Load(cwd); err != nil {
				fmt.Fprintf(os.Stderr, "tandem: reading %s: %v (starting with an empty board)\n", domainfile.YAMLName, err)
			} else if len(cards) > 0 {
				b.Board.Load(cards)
				fmt.Fprintf(os.Stderr, "tandem: domain board preloaded — %d confirmed card(s) from %s\n", len(cards), domainfile.YAMLName)
			}
			// Post-session recap (FR18): snapshot start, record submits,
			// write markdown + broadcast to guests on exit.
			rec := recap.New(b.Board.Cards())
			defer func() {
				path, md, err := rec.WriteFile(b.Board.Cards(), cwd)
				if err != nil {
					fmt.Fprintf(os.Stderr, "tandem: writing recap: %v\n", err)
					return
				}
				fmt.Fprintf(os.Stderr, "tandem: session recap written to %s\n", path)
				_ = link.WriteControl(map[string]any{"type": "recap", "markdown": md})
			}()
			recorder = rec
			b.OnBoardChange = func(cards []board.Card) {
				wrote, err := domainfile.WriteFiles(cwd, cards)
				if err != nil {
					fmt.Fprintf(os.Stderr, "tandem: writing domain files: %v\r\n", err)
				}
				if wrote {
					domainDirty.Store(true)
				}
			}
			// Context injection adapter (FR15, PRD §8.3). Claude Code
			// gets the managed CLAUDE.md include; other agents get a
			// prompt-prepended digest or clipboard mode.
			switch agentKind {
			case adapter.KindClaude:
				if err := adapter.EnsureClaudeInclude(cwd); err != nil {
					fmt.Fprintf(os.Stderr, "tandem: CLAUDE.md include: %v\n", err)
				}
			case adapter.KindAgentsMD:
				if err := adapter.EnsureAgentsInclude(cwd); err != nil {
					fmt.Fprintf(os.Stderr, "tandem: AGENTS.md include: %v\n", err)
				} else {
					fmt.Fprintln(os.Stderr, "tandem: managed AGENTS.md block points the agent at DOMAIN.md")
				}
			case adapter.KindPrepend:
				fmt.Fprintln(os.Stderr, "tandem: domain digest prepended to each submitted prompt")
			case adapter.KindClipboard:
				fmt.Fprintln(os.Stderr, "tandem: clipboard mode — Ctrl-] copies the composed prompt for you to paste")
			}
		}
		// Domain extractor (FR12): watches the REDACTED transcript (the
		// same bytes guests see, so masked secrets never reach the
		// model) and proposes cards into the normal lifecycle. Off
		// unless ANTHROPIC_API_KEY is set.
		ext := extract.New(b.Board.Cards, b.ProposeCards)
		guestStream := io.Writer(link)
		// The recorder tees the redacted stream (added below, after
		// redaction) so the cast matches what guests saw. Board and
		// composer events are recorded via the broker hooks.
		if rc != nil {
			prevBoard := b.OnBoardChange
			b.OnBoardChange = func(cards []board.Card) {
				if prevBoard != nil {
					prevBoard(cards)
				}
				if j, err := json.Marshal(cards); err == nil {
					rc.Board(string(j))
				}
			}
			prevCompose := b.OnChange
			b.OnChange = func(text string) {
				if prevCompose != nil {
					prevCompose(text)
				}
				rc.Composer(text)
			}
		}
		if ext != nil {
			// Vocabulary drift flags (FR17) ride the same LLM pass.
			ext.OnDrift = func(conflicts []extract.Conflict) {
				_ = link.WriteControl(map[string]any{"type": "drift", "conflicts": conflicts})
			}
			go ext.Run()
			defer ext.Close()
			guestStream = io.MultiWriter(link, ext)
			fmt.Fprintln(os.Stderr, "tandem: domain extractor active (proposals + drift flags)")
		}
		// Recorder tees the (about-to-be-redacted) guest output so the
		// cast contains exactly what guests saw — masked secrets stay
		// masked in replay.
		if rc != nil {
			guestStream = io.MultiWriter(guestStream, rc)
		}

		opts.Tap = guestStream
		// Secret masking (FR23) sits between the PTY tap and the link:
		// strictly pre-encryption, guests only. The host terminal shows
		// originals; a bell rings when masking fires, and a count prints
		// at session end.
		var red *redact.Redactor
		if !*noRedact {
			red = redact.New(guestStream)
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

		// Mirror is created after the launch wait (below) so nothing
		// composed pre-launch double-injects; the submit intercept refers
		// to it, so declare it here.
		var mir *mirror.Mirror
		var lastKey atomic.Int64
		mirrorOn := !*noMirror

		// Terminal title presence: composed from the sharing state and the
		// live guest count, refreshed whenever either changes. Written as an
		// OSC title sequence so it never corrupts the wrapped TUI's screen.
		var titleMu sync.Mutex
		shuttered := false
		guestN := 0
		setTitle := func() {
			titleMu.Lock()
			defer titleMu.Unlock()
			state := "● sharing live"
			if shuttered {
				state = "⏸ SHARING PAUSED"
			}
			guests := ""
			switch {
			case guestN == 1:
				guests = " · 1 guest"
			case guestN > 1:
				guests = fmt.Sprintf(" · %d guests", guestN)
			}
			fmt.Fprintf(os.Stdout, "\033]0;tandem %s%s\007", state, guests)
		}
		link.OnGuestChange = func(n int) { guestN = n; setTitle() }

		// Privacy shutter (FR4) on Ctrl-\. Intercepted bytes are
		// swallowed before the child, so the wrapped TUI never sees
		// them (it loses Ctrl-\/SIGQUIT and Ctrl-] — documented).
		opts.Intercepts = map[byte]func(){
			0x1C: func() { // Ctrl-\ : shutter
				shuttered = !shuttered
				link.SetShuttered(shuttered)
				fmt.Fprint(os.Stdout, "\a") // bell: state changed
				setTitle()
			},
			0x1E: func() { // Ctrl-^ : re-copy the join link to the clipboard
				copyToClipboard(link.JoinURL)
				titleMu.Lock()
				fmt.Fprint(os.Stdout, "\a\033]0;tandem ● link copied\007")
				titleMu.Unlock()
			},
			0x1D: func() { // Ctrl-] : RUN the composed prompt
				// Erase the live mirror preview first, so the authoritative
				// paste replaces it instead of doubling. Done before Flush
				// so Flush's clear-broadcast → mir.Update("") is a no-op.
				var clearSeq string
				if mir != nil {
					clearSeq = mir.ClearAndReset()
				}
				text, stats := b.Flush()
				if text == "" {
					return
				}
				if recorder != nil {
					recorder.RecordSubmit(text, stats)
				}
				if ext != nil {
					ext.NoteComposer(text)
				}
				// Prepend the confirmed-card digest for agents without a
				// native include (Codex/Gemini/Aider); Claude Code reads
				// DOMAIN.md via the CLAUDE.md include instead.
				if agentKind == adapter.KindPrepend {
					text = adapter.PrependPrompt(agentKind,
						adapter.Digest(b.Board.Cards(), 1024), text)
				}
				if domainDirty.Swap(false) {
					text = "(The domain model changed — re-read DOMAIN.md before acting.)\n\n" + text
				}
				// Clipboard fallback applies only when we're NOT mirroring
				// (mirroring is itself injection — if it's on, run the line).
				if mir == nil && agentKind == adapter.KindClipboard {
					fmt.Fprintf(os.Stdout, "\x1b]52;c;%s\a", base64.StdEncoding.EncodeToString([]byte(text)))
					fmt.Fprint(os.Stderr, "\r\ntandem: composed prompt copied to clipboard — paste to send\r\n")
					return
				}
				if mir != nil {
					// Mirroring: erase the live preview, then retype the
					// authoritative text raw on one line and press Enter.
					// Raw (not bracketed paste) so it runs cleanly on every
					// agent — shells don't strip paste markers. The text was
					// already signed as it mirrored; this is the same
					// chokepoint. Newlines flatten to spaces (single line).
					injector.Submit(sign.SignRaw(clearSeq + flattenLine(text) + "\r"))
				} else {
					// No mirror: bracketed paste + Enter (preserves
					// multi-line prompts; clean on agents that strip markers).
					injector.Submit(sign.Sign(text))
				}
				fmt.Fprint(os.Stdout, "\a") // host cue: it ran
			},
		}

		// Ctrl-G admits the next waiting guest (only bound in --approve
		// mode so it stays a normal keystroke otherwise).
		if *approve {
			opts.Intercepts[0x07] = func() {
				pendMu.Lock()
				if len(pendCIDs) == 0 {
					pendMu.Unlock()
					return
				}
				cid := pendCIDs[0]
				pendCIDs = pendCIDs[1:]
				name := pendName[cid]
				delete(pendName, cid)
				remaining := len(pendCIDs)
				pendMu.Unlock()
				link.Admit(cid)
				fmt.Fprintf(os.Stderr, "\r\ntandem: admitted %q (%d still waiting)\r\n", name, remaining)
			}
		}

		// Auto-copy the guest link to the host's clipboard (OSC 52) so
		// there's nothing to select by hand.
		copyToClipboard(link.JoinURL)
		fmt.Fprintln(os.Stderr, "tandem: guest link copied to your clipboard")
		if mirrorOn {
			fmt.Fprintln(os.Stderr, "tandem: guest typing appears in your prompt — review, then Ctrl-] to run it")
		}

		// Hold the agent until the host has shared the link — the agent's
		// full-screen TUI would otherwise hide it immediately. Skipped
		// with --no-wait or when stdin isn't a terminal.
		if !*noWait && term.IsTerminal(int(os.Stdin.Fd())) {
			// Let an early guest know nothing's wrong yet.
			_, _ = link.Write([]byte("\r\n⏳ waiting for the host to start the session…\r\n"))
			fmt.Fprintf(os.Stderr, "\ntandem: share the link, then press Enter to launch %s… ", argv[0])
			waitForEnter()
		}

		// Start live mirroring now that the agent is about to launch:
		// guest Composer edits inject into the agent's input line, pausing
		// whenever the host is typing. Wiring here (post-wait) avoids
		// double-injecting anything composed during the wait.
		if mirrorOn {
			opts.OnHostInput = func() { lastKey.Store(time.Now().UnixNano()) }
			mir = mirror.New(
				func(raw string) { injector.Submit(sign.SignRaw(raw)) },
				func() bool { return time.Since(time.Unix(0, lastKey.Load())) < time.Second },
			)
			b.OnChange = mir.Update
			mir.Update(b.Doc.Text()) // reflect anything composed during the wait
		}
	}

	code, err := ptywrap.Run(argv, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tandem:", err)
		return 1
	}
	return code
}

// flattenLine collapses a prompt to a single line for raw injection:
// newlines/tabs become spaces, other control runes drop. The mirror
// preview is already single-line (sanitize); this keeps the run in sync.
func flattenLine(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t' || r == '\r':
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7f:
			// drop other control characters
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// copyToClipboard puts s on the host's terminal clipboard via OSC 52.
// A no-op on terminals that don't support it (harmless escape).
func copyToClipboard(s string) {
	fmt.Fprintf(os.Stdout, "\x1b]52;c;%s\a", base64.StdEncoding.EncodeToString([]byte(s)))
}

// waitForEnter blocks until the host presses Enter. It reads stdin one
// byte at a time (not buffered) so it consumes exactly the newline and
// leaves any later input for the agent's PTY.
func waitForEnter() {
	var b [1]byte
	for {
		n, err := os.Stdin.Read(b[:])
		if err != nil || (n > 0 && b[0] == '\n') {
			return
		}
	}
}
