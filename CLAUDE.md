# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project state

Greenfield. The only artifact is `prd.md` — the PRD for **Tandem**, a tool that lets an engineer share a live coding-agent terminal session with a nontechnical stakeholder. No code, build system, or tests exist yet. Read `prd.md` before designing or implementing anything; it is the source of truth for requirements (FR1–FR24, NFR1–3) and milestones (M0–M3).

## Core invariant (never violate)

**Guests can write into the prompt, never execute.** Guest keystrokes/dictation go to a shared CRDT Composer buffer — never to the PTY's stdin. Only the host can submit, send interrupts, answer agent permission prompts, or reach the shell. This is a structural guarantee, not UI hiding or key filtering: guest clients must hold no code path that writes to the PTY, and the host daemon validates a host-local signature on all input before writing to stdin (FR21). Any design that gates guest keys by filtering control codes instead of routing them to a separate buffer is explicitly rejected in the PRD (§8.2).

## Architecture (planned, per PRD §8)

Six components:

1. **Host daemon** (`tandem` CLI) — Rust or Go, single static binary (macOS/Linux; Windows via WSL). Spawns the target agent (Claude Code, Codex CLI, Gemini CLI, Aider, or any command) in a PTY; owns the *only* stdin handle; runs input signature checks and the privacy shutter.
2. **Relay service** — stateless WebSocket forwarder of end-to-end-encrypted frames (sshx model: relay sees ciphertext only). Handles session links, presence, TURN-style traversal.
3. **Guest web client** — browser only, zero install. xterm.js terminal renderer (must handle alternate screen buffers and aggressive TUI repaints), CRDT Composer client (e.g. Yjs), Domain Board canvas, push-to-talk voice capture.
4. **Prompt Composer service** — one CRDT doc per session with per-author attribution. On host submit, the daemon serializes, signs, and writes the buffer to PTY stdin.
5. **Domain extractor** — sidecar LLM watching the transcript; proposes EventStorming-grammar cards (Domain Events, Commands, Actors/Roles, Terms/Rules) with confidence and provenance. Cards are proposals; host confirms; stakeholder wording wins by default.
6. **Context injector** — per-agent adapters. Confirmed cards serialize to `DOMAIN.md` + `domain.yaml` in the target repo; Claude Code adapter injects via hooks/CLAUDE.md include, generic adapter prepends a domain digest at submit time.

Build vs. reuse: transport/relay follows sshx and upterm prior art. The novel work (the moat) is the gated Composer, the context injectors, and the domain extractor — spend effort there.

## Key constraints to preserve while implementing

- Latency: p50 < 100 ms, p95 < 250 ms terminal echo (same continent).
- Terminal fidelity over prettiness — fall back to raw fidelity, never a lossy transformation.
- Sessions survive host network blips (local PTY buffering + replay) and guest refreshes (rejoin restores scrollback).
- Secret redaction for guests on by default (API keys, tokens, .env output), host override.
- v1 non-goals (do not build): guest execution in any form, >3 participants, IDE/GUI sharing, async collaboration, audio as a differentiator.

## Milestone order (PRD §11)

M0: PTY wrap + encrypted relay + read-only guest terminal → M1: gated CRDT Composer with attribution/dictation/host-only submit → M2: Domain Board, `DOMAIN.md` serialization, Claude Code injection → M3: automatic extractor, drift flags, recap/replay. Build in this order; M1 is the demo that sells the product.
