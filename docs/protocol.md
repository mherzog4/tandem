# Tandem wire protocol and the gated-input guarantee

The product's core invariant (FR6/FR8/FR21): **guests can write into the
prompt, never execute.** This is enforced structurally at three layers,
not by hiding UI.

## Layer 1 — topology (relay)

The relay is a star. Guest WebSocket frames are forwarded to the host
only; guests cannot reach each other or loop frames back. Binary frames
are opaque ciphertext to the relay (see `internal/e2e`): a compromised
relay can drop or replay ciphertext but cannot forge a frame that
decrypts.

Session resume requires the host's resume token, which only the host
ever receives; a guest's join URL (or a leaked session ID) grants no
host powers.

## Layer 2 — cryptography (e2e)

Every frame is sealed with AES-256-GCM under the per-session key carried
in the join link's URL fragment. The host drops any inbound frame that
fails authenticated decryption. What the relay sees is never
interpretable; what the host interprets is always authenticated.

## Layer 3 — the host message allowlist (broker)

All decrypted guest traffic lands in `internal/broker.handle`, which is
the complete set of things a guest can say. Sealed plaintext carries a
1-byte envelope:

| Kind | Byte | Direction | Meaning |
|------|------|-----------|---------|
| `FramePTY` | 0x00 | host → guest | raw terminal output |
| `FrameCtrl` | 0x01 | both | JSON control message |
| `FrameReplay` | 0x02 | host → guest | scrollback snapshot (reset + render) |

Guest → host messages must be `FrameCtrl` JSON with one of these types —
anything else is dropped and counted (`Broker.Dropped`):

| Type | Fields | Effect |
|------|--------|--------|
| `op` | `op: {author, baseRev, pos, del, ins}` | edit the Composer document (positions clamped, inserts capped at 16 KiB, document capped at 256 KiB) |
| `undo` | `author` | revert that author's latest surviving insert |
| `cursor` | `author, pos` | rebroadcast caret position to all clients |
| `highlight` | `author, x, y` | rebroadcast pointer ring (coordinates clamped to [0,1]) |
| `react` | `author, emoji` | rebroadcast emoji reaction (≤8 runes, rendered as text) |
| `ping` | `t` | echoed as `pong` by the hostlink read loop (latency probe) |

There is no guest message that reaches the PTY. The PTY's stdin has
exactly one writer: the host daemon's own terminal passthrough
(`internal/ptywrap`), plus — once issue #12 lands — the host-signed
submit path. Guest-originated `FramePTY` frames are explicitly dropped
by the broker.

Host → guest control messages: `resize`, `shutter`, `pong`,
`composer-op`, `composer-snapshot`, `cursor`, `submitted` (per-author
stats after a flush), plus relay-originated plaintext presence events
(`{type:"presence", event:"join"|"leave", name}`) on text frames.

## The submit path (FR8/FR21)

`Ctrl-]` on the host terminal flushes the Composer: the daemon signs
the buffer with a per-session in-memory Ed25519 key
(`internal/signer`), and `ptywrap.Injector` verifies signature and a
strictly increasing sequence number before writing to the PTY (as a
bracketed paste + carriage return). Forged, tampered, or replayed
submissions are logged and dropped. Guests have no message type that
reaches this path; the signing key never leaves the host process.

## Adversarial coverage

`internal/broker/broker_test.go` exercises: fake `submit`/`exec` types,
malformed JSON, anonymous ops, guest-sent PTY frames, oversized
inserts. `internal/hostlink/hostlink_test.go` covers forged (unsealed)
frames; `internal/relay/relay_test.go` covers guest→guest isolation,
wrong-token resume hijack attempts, and the participant cap.
