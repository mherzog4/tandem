# Changelog

All notable changes are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
aims for [Semantic Versioning](https://semver.org/).

## [Unreleased]

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

[Unreleased]: https://github.com/mherzog4/tandem/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/mherzog4/tandem/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/mherzog4/tandem/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/mherzog4/tandem/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/mherzog4/tandem/releases/tag/v0.1.0
