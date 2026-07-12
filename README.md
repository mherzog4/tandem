# Tandem

[![CI](https://github.com/mherzog4/tandem/actions/workflows/ci.yml/badge.svg)](https://github.com/mherzog4/tandem/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/mherzog4/tandem?sort=semver)](https://github.com/mherzog4/tandem/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/mherzog4/tandem)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**A shared seat inside the engineer's coding-agent session.** Share a live
terminal session (Claude Code, Codex, Gemini, Aider, Factory, or any command)
with a nontechnical stakeholder: they see everything and can compose directly
into the prompt sent to the agent — and **structurally cannot execute
anything.** Only the engineer submits, approves tool calls, or touches the
shell.

Think of it as Tuple, but the object you're pairing on isn't a code editor —
it's the conversation with the agent itself.

## Why

The workflow today is a game of telephone: stakeholder describes what they
want → engineer translates it into a prompt → agent builds → everyone
discovers the misunderstanding days later. Tandem collapses that to a shared
conversation with the agent. Natural language is the one medium where the
stakeholder isn't the junior partner, so they write into the prompt in their
own words; the engineer stays the editor, safety gate, and technical
enricher. A live **Domain Board** (EventStorming-style) captures the shared
vocabulary into `DOMAIN.md` in the repo, which feeds the agent as context.

Full product rationale: [prd.md](prd.md).

## How it works

- **Host CLI** (`tandem`) wraps the agent in a PTY and owns the only handle to
  its stdin.
- **Relay** forwards end-to-end-encrypted frames between host and guests; it
  only ever sees ciphertext (the session key lives in the join link's `#`
  fragment, which browsers never send to the server).
- **Guest** joins in a browser — no install. What they type appears live in
  the engineer's agent prompt; the engineer reviews it and presses `Ctrl-]` to
  run it. Guests can never execute — their terminal is read-only and their
  keystrokes have no path to the PTY. Only the host runs anything, and every
  injected keystroke passes an Ed25519 signing chokepoint. That's a structural
  guarantee, not a filtered UI. See [docs/protocol.md](docs/protocol.md).

## Install (host)

```sh
curl -fsSL https://raw.githubusercontent.com/mherzog4/tandem/main/scripts/install.sh | sh
```

Installs to `~/.local/bin` — **no sudo**. If that dir isn't on your `PATH`,
the installer prints the one line to add it. Override the location with
`TANDEM_BIN_DIR` (e.g. `TANDEM_BIN_DIR=/usr/local/bin`, which needs sudo).

macOS (arm64/x86_64) and Linux (x86_64/arm64). Windows via WSL. Guests
need nothing but a browser.

## Use

```sh
tandem claude          # or codex, aider, gemini, or any command
```

`tandem` connects to the hosted relay by default, copies a join link to your
clipboard, and pauses so you can share it before the agent takes the screen.
Send the link to your stakeholder, then press Enter to launch.

**The loop:** they type in the browser → it appears live in your agent's
prompt → you review or edit it → you press `Ctrl-]` to run it. They can watch
and write the prompt but can never execute — only your keyboard runs anything.

- `Ctrl-]` — run the composed prompt
- `Ctrl-\` — privacy shutter (guest sees a paused card)
- `--no-mirror` — don't mirror into the prompt; guests compose in a side panel
  and `Ctrl-]` sends it
- `--no-wait` — launch immediately instead of pausing to share the link
- `--relay wss://…` / `TANDEM_RELAY` — use a different relay
- `--no-share` — run locally with no session

The link's `#` fragment holds the encryption key and never reaches the relay —
it forwards ciphertext only.

## Deploy your own relay (Railway)

Guests join through the relay, so it must be publicly reachable over
`wss://`. The relay is a stateless single binary; deploy it to Railway
(builds the Dockerfile remotely — no local Docker needed):

```sh
railway init                                              # create the project
railway up                                                # remote build + deploy
railway domain                                            # get https://<name>.up.railway.app + TLS

# point join links at the public URL, then redeploy so it takes effect:
railway variables --set TANDEM_BASE_URL=https://<name>.up.railway.app
railway up
```

Then hosts connect to it:

```sh
tandem --relay wss://<name>.up.railway.app claude
```

Sessions live in the relay's memory, so keep it at **one** replica for now
(scale-out needs session affinity). Railway provides TLS automatically and
health-checks `/healthz`. The relay never sees plaintext or the encryption
key regardless of where it runs.

Public-endpoint limits (all optional env vars, sane defaults):

| Var | Default | Meaning |
|-----|---------|---------|
| `TANDEM_MAX_SESSIONS` | 200 | concurrent sessions cap |
| `TANDEM_CONN_PER_MIN` | 30 | new connections/minute per IP |
| `TANDEM_CONN_BURST` | 10 | per-IP burst allowance |

The relay pings each connection every 30s and reaps dead peers, so a host
whose network drops doesn't hold a session (a live-but-quiet session is kept).
It reads `X-Forwarded-For` for the real client IP behind the proxy.

## Develop

```sh
go test ./...        # tests
go build ./...       # build all
```

Releases: either push a `v*` tag (CI builds and publishes the binaries),
or cut one locally with no CI —

```sh
scripts/release.sh v0.1.0
```

which cross-compiles all four targets on your machine and publishes the
GitHub Release directly (uses zero Actions minutes).

## Documentation

- [docs/protocol.md](docs/protocol.md) — wire protocol and the gated-input guarantee
- [docs/compat.md](docs/compat.md) — agent compatibility matrix + `--mirror`, dictation
- [docs/latency.md](docs/latency.md) — latency targets and how they're measured
- [prd.md](prd.md) — product requirements

## Contributing

Contributions welcome — see [CONTRIBUTING.md](CONTRIBUTING.md). Please keep the
[security invariant](docs/protocol.md) intact and run `go test -race ./...`.
Be kind ([Code of Conduct](CODE_OF_CONDUCT.md)). Found a vulnerability?
[SECURITY.md](SECURITY.md) — report it privately, not as an issue.

## License

[MIT](LICENSE) © Matt Herzog
