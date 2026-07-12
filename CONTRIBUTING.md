# Contributing to Tandem

Thanks for your interest. Tandem is a Go host CLI + relay plus a browser
guest client; the security model (guests write into the prompt, never
execute) is the thing to protect above all else.

## Development setup

Requires Go 1.25+. No other toolchain for the core; the browser client is
plain vendored JS (no build step).

```sh
git clone https://github.com/mherzog4/tandem
cd tandem
go build ./...
go test ./...
```

Run a local session end-to-end:

```sh
go run ./cmd/relay --addr :8080 --base-url http://localhost:8080   # terminal A
go run ./cmd/tandem --relay ws://localhost:8080 bash               # terminal B
```

Open the printed join link in a browser.

## Before you open a PR

- `go test -race ./...` — all green, including any new tests for your change.
- `go vet ./...` and `gofmt -l .` — no output.
- Add a test for non-trivial logic. Every package here has tests; match the
  style (`internal/<pkg>/<pkg>_test.go`).
- Keep the diff focused. One logical change per PR.

## The security invariant (do not weaken)

Guest input must never reach the wrapped command's stdin. It routes to the
host-authoritative Composer buffer; only the host can flush it, and only
through the Ed25519 signing chokepoint. See `docs/protocol.md`. Any change
near `internal/broker`, `internal/hostlink`, `internal/relay`, or
`internal/ptywrap` should preserve this and keep the adversarial tests
passing. If you're changing the wire protocol, update `docs/protocol.md`.

## Code style

- Standard Go: `gofmt`, small focused packages, table-driven tests.
- Prefer the standard library; add a dependency only when it clearly earns
  its place.
- Comments explain *why* / constraints, not *what*.

## Commit and PR conventions

- Present-tense, imperative commit subjects (`Add …`, `Fix …`).
- Reference the issue you're closing (`Closes #NN`) in the PR body.
- The PR template asks how you verified the change — fill it in.

## Reporting bugs / requesting features

Use the issue templates. For anything security-sensitive, follow
[SECURITY.md](SECURITY.md) instead of opening a public issue.
