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
# on a machine running the relay (or use a hosted one):
tandem-relay --addr :8080 --base-url https://your-relay.example

# host a session:
tandem --relay wss://your-relay.example claude
```

Tandem prints a join link. Send it to your guest; the link's `#` fragment
holds the encryption key and never reaches the relay — the relay forwards
ciphertext only. `Ctrl-\` toggles the privacy shutter.

## Develop

```sh
go test ./...        # tests
go build ./...       # build all
```

Releases: push a `v*` tag; CI builds static binaries for all four
targets and attaches them to a GitHub release.
