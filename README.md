# tandem

A shared seat inside the engineer's coding agent session. Share a live
terminal session (Claude Code, Codex CLI, Gemini CLI, Aider, or any
command) with a stakeholder: they see everything, can compose into the
prompt — and structurally cannot execute anything. See [prd.md](prd.md).

## Install (host)

```sh
curl -fsSL https://raw.githubusercontent.com/mherzog4/tandem/main/scripts/install.sh | sh
```

macOS (arm64/x86_64) and Linux (x86_64/arm64). Windows via WSL. Guests
need nothing but a browser.

## Use

```sh
tandem claude          # or codex, aider, gemini, or any command
```

That's it — `tandem` connects to the hosted relay by default and prints a
join link. Send it to your guest; the link's `#` fragment holds the
encryption key and never reaches the relay — the relay forwards ciphertext
only. `Ctrl-\` toggles the privacy shutter, `Ctrl-]` submits the Composer.

Point at a different relay with `--relay wss://your-relay.example` or the
`TANDEM_RELAY` env var; run locally with no session via `--no-share`.

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

Releases: push a `v*` tag; CI builds static binaries for all four
targets and attaches them to a GitHub release.
