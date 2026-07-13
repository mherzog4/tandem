# Changelog

All notable changes are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
aims for [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Changed
- **Native Enter submit in live mirror mode.** Guest text still mirrors into the
  engineer's prompt through signed keystrokes, but the engineer now runs the
  visible line with normal Enter instead of `Ctrl-]`. `Ctrl-]` remains the
  `--no-mirror` fallback submit path.

## [0.5.0] — 2026-07-12

### Added
- **AGENTS.md injection adapter.** Codex, Cursor, Amp, opencode, and Factory
  now auto-inject the domain model via a managed `AGENTS.md` block pointing at
  `DOMAIN.md`, instead of the per-prompt prepend digest.
- **Reprint-link hotkey (`Ctrl-^`)** re-copies the join link to the clipboard
  once the agent's TUI has hidden it.
- **Title-bar presence**: the terminal title shows the sharing state and live
  guest count.
- **`tandem doctor`**: preflight checks for relay reachability, a controlling
  terminal, and clipboard/paste notes.
- **Homebrew install** via a formula in `HomebrewFormula/tandem.rb`, refreshed
  on each release.

### Changed
- Running `tandem` with no command prints a short quickstart instead of a bare
  usage line.

## [0.4.0] — 2026-07-12

### Added
- **Join approval (waiting room), `--approve`.** Holds each guest until the
  engineer admits it with `Ctrl-G`, defending against leaked links. Guests
  see a "waiting to be admitted" overlay until let in.
- **Go fuzz targets** for the untrusted-input surfaces: composer OT apply,
  redaction, e2e frame open/seal, and the broker message allowlist.
- **CI security gates**: gofmt check, golangci-lint, govulncheck, and a
  Dependabot config.
- **Web-client unit tests** (Node's test runner) over the guest client's
  frame crypto, Jupiter rebase, and input diff, run in CI.

### Changed
- The guest client's pure helpers now live in `web/lib.js` (loaded before
  `app.js`) so they can be tested outside the browser. No behavior change.

## [0.3.0] — 2026-07-12

### Added
- **Guest reader mode.** A "📖 reader" toggle renders the agent's output as
  clean, larger markdown instead of raw ANSI, reading xterm's already-resolved
  screen buffer so TUI repaints are settled. Targets stakeholder intimidation.
- **Guest onboarding overlay.** First-join card explaining the compose-only
  model (you type the prompt, the engineer runs it, you can't break anything);
  dismissal is remembered.
- **Turn-state chip.** Shows whose turn it is — your turn / engineer reviewing /
  engineer ran it — derived from the composer doc with no new protocol frame.
- **Presence roster.** The bar shows who is connected (an always-present
  engineer chip plus a colored chip per guest). The relay sends a roster
  snapshot to each newcomer so late joiners see incumbents.
- **Mobile-responsive guest layout.** On narrow screens the terminal stacks
  over the composer, the bar wraps, and the board goes full-width.

## [0.2.1] — 2026-07-12

### Fixed
- Mirror preview and Ctrl-] run now use raw keystrokes instead of bracketed
  paste, so the guest's text and the run render cleanly on every agent
  (plain shells no longer show `…~` marker cruft). `--no-mirror` still uses
  bracketed-paste submit for multi-line prompts.

## [0.2.0] — 2026-07-12

### Changed
- **Live mirroring is now the default.** The guest's Composer text appears live
  in the engineer's agent prompt; the engineer reviews and presses `Ctrl-]` to
  run it. Guests still cannot execute. `--no-mirror` restores the
  compose-then-submit model. Cleanest on Claude Code; plain shells show
  bracketed-paste marker cruft (use `--no-mirror`).
- Guest Composer UI relabeled to explain the model and confirm when the
  engineer runs a prompt.

## [0.1.2] — 2026-07-12

### Added
- `tandem <agent>` now auto-copies the guest link to your clipboard and pauses
  ("press Enter to launch…") so you can share it before the agent's full-screen
  TUI hides it. `--no-wait` skips the pause.

## [0.1.1] — 2026-07-12

### Added
- Auto-detection for all major terminal coding agents (Factory `droid`,
  `cursor-agent`, `amp`, `opencode`, `goose`, `crush`, `qwen`, and more) with
  the domain-digest prepend tier; `TANDEM_PREPEND_AGENTS` registers new ones
  without a code change.

## [0.1.0] — 2026-07-12

First public release.

### Added
- **Shared terminal (M0):** host CLI wraps any command in a PTY; stateless
  encrypted relay; browser guest client; reconnect resilience; privacy
  shutter; latency instrumentation.
- **Gated Composer (M1):** host-authoritative CRDT/OT prompt buffer with
  per-author attribution; guest input structurally cannot reach stdin;
  Ed25519-signed host-only submit; live mirroring; dictation; pointing and
  reactions; secret redaction.
- **Domain Board (M2):** four EventStorming card types with drag ordering;
  host-confirmed cards serialized to `DOMAIN.md` / `domain.yaml`; Claude Code
  context injection; cross-session board preload; recording consent and email
  allowlist.
- **Extractor & replay (M3):** LLM extractor proposing cards from the
  transcript; vocabulary-drift flags; post-session recap; asciinema-format
  recording with a synced replay player.
- **Deployment:** Dockerfile + Railway config; public relay with TLS; zero-
  config host connect (`tandem claude`); public-endpoint hardening (session
  cap, per-IP rate limit, dead-peer reaping); local `scripts/release.sh`.

[Unreleased]: https://github.com/mherzog4/tandem/compare/v0.2.1...HEAD
[0.2.1]: https://github.com/mherzog4/tandem/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/mherzog4/tandem/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/mherzog4/tandem/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/mherzog4/tandem/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/mherzog4/tandem/releases/tag/v0.1.0
