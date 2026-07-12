# PRD: Tandem (working title)

## A shared seat inside the engineer's coding agent session, built for stakeholder and engineer collaboration

Version 0.1 · July 2026 · Status: Draft for review

## 1. Summary

Tandem lets an engineer share a live coding agent session (Claude Code, Codex CLI, Gemini CLI, Aider, or any terminal agent) with a nontechnical stakeholder in real time. The stakeholder sees the engineer's actual terminal, can type or dictate directly into the prompt that will be sent to the agent, and can watch the agent plan and build. Only the engineer can execute: run the prompt, approve tool calls, run code, or touch the shell. Around the terminal, Tandem maintains a live domain model panel in the spirit of Domain Driven Design and EventStorming, so the shared vocabulary the two people build during the session becomes a durable artifact that feeds the agent as context.

Think of it as Tuple, but the object being paired on is not a code editor. It is the conversation with the agent itself.

## 2. Problem

Today the workflow between a stakeholder and an engineer looks like this: they meet, the stakeholder describes what they want, the engineer takes notes, the engineer goes away and prompts a coding agent using their own translation of the request, ships something, and comes back days later. Every hop in that chain loses information. It is a game of telephone with three players: stakeholder, engineer, agent.

The specific failure modes:

1. **Translation loss.** The engineer paraphrases the stakeholder's intent into a prompt. Nuance about the business domain (what a "booking" really means, what happens when a "claim is denied") gets flattened or guessed at.
2. **Latency of correction.** The stakeholder only discovers the misunderstanding after a build cycle, which can be hours or days. The cost of a wrong assumption compounds through everything the agent generated on top of it.
3. **No shared vocabulary artifact.** Meetings produce notes at best. There is no living glossary or model of the domain that both humans agree on and that the agent actually consumes. DDD calls this the ubiquitous language, and its absence is the root cause of most of the rework.
4. **Existing tools solve the wrong layer.** Tuple and screen sharing tools give the stakeholder eyes but no hands, or full hands with full risk. Terminal sharing tools like tmate, upterm, and sshx give a guest either read only access or a full shell. Neither offers the middle state this workflow needs: *write into the prompt, never execute*.

## 3. Insight

Coding agents changed what "pairing" means. The bottleneck is no longer typing code, it is expressing intent in natural language. Natural language is the one medium where the stakeholder is not the junior partner. If the stakeholder can write directly into the agent's prompt, the telephone game collapses from three players to a shared conversation with the agent, and the engineer's role shifts from translator to editor, safety gate, and technical enricher.

EventStorming proved that when business experts and developers model a domain together in a shared space, weeks of iteration compress into hours and the two sides converge on one vocabulary. Tandem applies that same principle, but the "wall of sticky notes" is the agent session, and the model that emerges is written down by the agent itself.

## 4. Goals and non goals

### Goals

1. Let a guest (stakeholder) see the host's live agent terminal with sub second latency.
2. Let the guest compose into the pending prompt via keyboard or voice dictation.
3. Guarantee, at the protocol level, that only the host can submit a prompt, answer agent permission requests, or reach the shell.
4. Capture the domain language of the session (terms, events, rules, actors) into a structured artifact the agent reads as context on every turn.
5. Make session setup under 30 seconds for the host and zero install for the guest.

### Non goals (v1)

1. Guest execution of anything, even with host approval toggles. Deliberately out.
2. Multi guest sessions beyond three participants.
3. Replacing the video call. v1 assumes the pair is already on Zoom, Meet, or Tuple for voice; Tandem embeds lightweight WebRTC audio as a convenience, not a differentiator.
4. IDE or GUI screen sharing. Tandem shares the agent terminal only.
5. Async collaboration (comments, review queues). v1 is synchronous sessions.

## 5. Personas

**Priya, staff engineer (host).** Runs Claude Code daily. Tired of being the human clipboard between her PM and her agent. Wants stakeholders closer to the work without giving anyone a shell on her laptop. Success for her: fewer rebuild cycles, and a defensible security story she can explain to her infra team in one sentence ("the guest never has stdin").

**Marcus, head of operations (guest).** Nontechnical. Knows the insurance claims domain cold. Comfortable in Google Docs, allergic to terminals. Success for him: he can say or type "no, a claim can be reopened within 90 days of denial, that is not a new claim" and watch that rule enter the prompt verbatim, in his words.

## 6. User journey (the golden path)

1. Priya opens her terminal and runs `tandem claude` (or `tandem codex`, or wraps any command). Tandem spawns the agent inside a managed PTY and prints a session link.
2. Marcus opens the link in his browser. No install, no account required for guests, name entry only. He sees a pixel faithful render of Priya's terminal plus two side panels: the **Prompt Composer** and the **Domain Board**.
3. They talk (on their existing call or Tandem's built in audio). Marcus clicks into the Composer and types, or holds the mic button and dictates: "When an adjuster denies a claim, the customer gets an email with the denial reason and a reopen window of 90 days."
4. Priya sees his text appear live in her terminal's input line, attributed to him with a colored cursor. She appends the technical framing: target service, constraints, "model this as a domain event ClaimDenied." Both are editing the same buffer, Google Docs style.
5. Priya hits Enter. Only her Enter works. The agent runs. Both watch the plan stream.
6. The agent asks permission to edit files. Only Priya sees and answers the approval. Marcus's view shows the approval moment but no control.
7. As the session proceeds, Tandem's domain extractor updates the Domain Board: new term "Reopen Window (90 days)", new event "ClaimDenied", new rule "Reopen creates no new claim ID." Marcus can edit a card's wording; Priya confirms the edit; the card writes into `DOMAIN.md` in the repo, which is injected into the agent's context on the next turn.
8. Session ends. Both receive a recap: transcript, prompts by author, the updated domain model diff, and a replayable session recording.

## 7. Product requirements

### 7.1 Session and sharing

* **FR1.** Host CLI wraps any terminal command in a PTY and streams terminal state to guests. Works with Claude Code, Codex CLI, Gemini CLI, Aider, and plain shells, with no agent specific integration required for baseline sharing.
* **FR2.** Guest joins via browser link. Terminal renders with full color, unicode, and TUI redraws (agent TUIs repaint aggressively; the renderer must handle alternate screen buffers).
* **FR3.** Latency target: p50 under 100 ms, p95 under 250 ms terminal echo for guests on the same continent.
* **FR4.** Host can pause sharing (privacy shutter) instantly with a hotkey; guests see a paused card, never a frozen frame of sensitive content.
* **FR5.** Sessions are end to end encrypted between host and guests; the relay server sees ciphertext only (the sshx model).

### 7.2 Gated input (the core mechanic)

* **FR6.** Guest keystrokes and dictation route to a shared Prompt Composer buffer, never to the PTY's stdin. The Composer mirrors into the agent's visible input line on the host terminal so both parties see one source of truth.
* **FR7.** The Composer is a collaboratively edited buffer (CRDT) with per author attribution and colored cursors, so the recap can show who contributed which words.
* **FR8.** Submission, interrupts (Ctrl C), permission approvals, mode switches, and any control sequence are host only. Enforced server side and host side, not by hiding buttons in the guest UI.
* **FR9.** Guest dictation: push to talk voice capture in the browser, transcribed to text (Whisper class model), inserted at the guest's cursor. Dictation is a first class input, since many stakeholders think out loud better than they type.
* **FR10.** Guests can react and point: click a region of the terminal to place a temporary highlight both parties see (borrowed from Tuple's on screen drawing), and drop emoji reactions.
* **FR11.** A guest "raise hand on text" affordance: guest can select any text in the agent's output and attach a note ("this is wrong, a policyholder is not the same as a claimant"), which lands in the Composer as a quoted correction ready to send.

### 7.3 Domain Board (the DDD layer)

* **FR12.** A sidecar process (an LLM watcher over the session transcript) continuously extracts candidate domain elements into four card types matching EventStorming grammar: **Domain Events** (ClaimDenied), **Commands** (DenyClaim), **Actors/Roles** (Adjuster), and **Terms/Rules** (Reopen Window, "reopen keeps the claim ID").
* **FR13.** Cards are proposed, not authoritative. Either party can edit wording; the stakeholder's wording wins by default (ubiquitous language comes from the domain expert). Host confirms a card to commit it.
* **FR14.** Confirmed cards serialize into a `DOMAIN.md` (plus machine readable `domain.yaml`) in the repository, versioned in git alongside the code.
* **FR15.** For supported agents, Tandem injects the domain file into context automatically: for Claude Code via CLAUDE.md include and hooks, for others via a preamble prepended at submit time. The agent is thereby instructed to name code constructs after the board's terms, keeping code and business language aligned (the core DDD promise).
* **FR16.** Board renders as a simple timeline/canvas the stakeholder can rearrange; drag ordering of events expresses process flow, which serializes as an ordered event list.
* **FR17.** Conflict surfacing: when the agent's output uses a term differently than a confirmed card (detected by the watcher), Tandem flags it to both parties. Vocabulary drift is the earliest signal of a misbuild.

### 7.4 Recap and memory

* **FR18.** Post session recap: full transcript, per author prompt contributions, domain model diff (cards added/changed), and links to commits or PRs the agent produced during the session.
* **FR19.** Session recording replayable as a terminal cast (asciinema style), scrubbing synced with the Composer history and Board changes.
* **FR20.** The next session with the same repo opens with the Board preloaded from `domain.yaml`, so the model accretes across meetings instead of restarting.

### 7.5 Security and trust

* **FR21.** Threat model assumes a curious or compromised guest browser. Because guest input structurally cannot reach stdin, the worst a hostile guest can do is write text the host reviews before submission. This must remain true even if the relay is compromised (host validates that all injected input carries the host's local signature before writing to the PTY).
* **FR22.** Host allowlist option: restrict guests by email or SSO for team plans.
* **FR23.** Redaction assist: Tandem detects likely secrets in the terminal stream (API keys, tokens, .env output) and masks them for guests by default, with a host override.
* **FR24.** Clear recording consent: both parties see and acknowledge recording state.

### 7.6 Non functional

* **NFR1.** Host CLI is a single static binary (macOS and Linux at launch; Windows via WSL). Guest side is browser only.
* **NFR2.** Terminal fidelity over prettiness: if the renderer cannot represent something, fall back to raw fidelity, never to a lossy transformation.
* **NFR3.** Session survives host network blips (PTY buffers locally, replays on reconnect) and guest refreshes (rejoin restores full scrollback).

## 8. Technical architecture

### 8.1 Components

1. **Host daemon (`tandem` CLI).** Rust or Go binary. Spawns the target agent inside a PTY, captures the output stream, and maintains the authoritative session state. Owns the only handle to stdin. Also runs the input signature check (FR21) and the privacy shutter.
2. **Relay service.** Lightweight server (the sshx/upterm pattern) that forwards encrypted frames between host and guests over WebSocket, handles session links, presence, and TURN style traversal. Stateless with respect to content; it cannot read frames.
3. **Guest web client.** xterm.js based renderer for the terminal stream, plus the Composer (CRDT client, e.g. Yjs) and the Domain Board (canvas UI). Voice capture via the browser, streamed to the transcription service.
4. **Prompt Composer service.** CRDT document per session. The host daemon subscribes; when the host submits, the daemon serializes the buffer, signs it, and writes it to the PTY stdin followed by the submit sequence. Guest clients hold no code path that writes to the PTY.
5. **Domain extractor.** Sidecar LLM pipeline consuming the rolling transcript (agent output plus Composer history plus optional call transcription). Emits candidate cards with confidence and provenance (which utterance produced the card). Runs host side or as a hosted service depending on plan, with a local model option for privacy sensitive teams.
6. **Context injector.** Per agent adapters. Claude Code adapter uses hooks and a managed CLAUDE.md include; generic adapter prepends a compact domain digest to each submitted prompt; file adapter simply maintains `DOMAIN.md` and `domain.yaml` in the repo for the agent to discover.

### 8.2 Key design decisions

**Why intercept at the PTY, not screen pixels.** Tuple ships video of a screen. Tandem ships terminal state, which is orders of magnitude cheaper, stays crisp at any guest window size, allows text selection and the "raise hand on text" feature, and makes the gated input model possible at all. Pixels cannot distinguish "typing into the prompt" from "typing into the shell." A PTY stream can.

**Why a Composer buffer instead of gating raw guest keys.** An alternative design forwards guest keystrokes to stdin but filters Enter and control codes. That design is fragile: agent TUIs interpret many keys as commands (slash menus, arrow driven pickers), and a filter list will miss something eventually. Routing guests into a separate CRDT buffer that only the host can flush is a structural guarantee rather than a filter, and it gives attribution and undo for free.

**Why EventStorming grammar for the Board.** It is the one modeling notation with a track record of working for mixed technical and nontechnical groups, precisely because it avoids specialist notation. Four card types cover the useful surface; anything richer becomes a tool for the engineer only, which defeats the purpose.

**Build vs. reuse.** Terminal transport borrows proven patterns (sshx for encrypted web relay, upterm for the host side PTY wrapping model). The novel engineering is the gated Composer, the per agent context injectors, and the domain extractor. Those three are the moat; everything else should lean on prior art.

### 8.3 Agent compatibility matrix (launch)

1. **Claude Code:** full support. Composer mirroring into the input line, hooks based context injection, permission prompt detection for the approval UX.
2. **Codex CLI, Gemini CLI, Aider:** shared terminal, gated Composer with prompt prepend injection, no native hook integration at launch.
3. **Anything else in a terminal:** view plus Composer in "clipboard mode" (host pastes manually), Domain Board fully functional.

## 9. Success metrics

1. **Rework rate:** percentage of sessions followed within 7 days by a prompt tagged as correcting a misunderstanding from that session. Target: 40 percent reduction against a baseline cohort.
2. **Stakeholder authorship:** median share of submitted prompt characters authored by guests. Target: above 25 percent by week 4 of a team's usage. If guests only watch, the product has failed at its premise.
3. **Domain model adoption:** percentage of sessions that end with at least one confirmed Board card, and percentage of repos where `domain.yaml` persists past three sessions.
4. **Time to first shared session** for a new host: under 5 minutes from install.
5. Standard health: weekly active pairs, session length, guest return rate.

## 10. Risks and open questions

1. **TUI mirroring fragility.** Mirroring the Composer into each agent's input line requires understanding each TUI's redraw behavior. Mitigation: the Composer panel is always the source of truth; in the worst case the host terminal shows the buffer only at submit time.
2. **Stakeholder intimidation.** A raw terminal may still read as hostile to guests. Mitigation: the guest client can render agent output in a "reader mode" (formatted markdown lane beside the raw terminal), toggleable.
3. **Extractor noise.** A chatty domain watcher that proposes junk cards will get ignored. Mitigation: high precision threshold, cards capped per interval, provenance shown on every card.
4. **Does audio belong in v1?** Embedding WebRTC audio competes with the meeting tool the pair already has open. Leaning toward shipping it behind a flag and watching usage.
5. **Naming rights on the ubiquitous language.** FR13 defaults to stakeholder wording, but engineers will push back when a business term collides with a codebase term. Need a lightweight aliasing mechanism (Board card holds both a business name and a code name, mapped).
6. **Pricing surface.** Likely per host seat (guests free, like Tuple's guest model) with the hosted extractor as the paid tier boundary. Needs validation.

## 11. Milestones

1. **M0 (weeks 1 to 4):** Host CLI with PTY wrap, encrypted relay, guest read only terminal in browser. Dogfood internally.
2. **M1 (weeks 5 to 9):** Gated Composer with CRDT editing, attribution, dictation, host only submit. This is the demo that sells the product.
3. **M2 (weeks 10 to 14):** Domain Board with manual cards, `DOMAIN.md` serialization, Claude Code context injection.
4. **M3 (weeks 15 to 18):** Automatic domain extractor, vocabulary drift flags, session recap and replay.
5. **Private beta** at M2; design partner target: three teams that ship weekly with a nontechnical decision maker in the loop.
