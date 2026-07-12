# Latency measurement (FR3)

Target: terminal echo p50 < 100 ms, p95 < 250 ms for same-continent guests.

## How it's measured

The guest client sends a sealed `{type:"ping",t:performance.now()}`
control frame every 5 s over the real frame path (guest → relay → host
daemon). The host daemon echoes a `pong` carrying the same timestamp.
The guest computes RTT and reports one-way latency as RTT/2 over a
rolling 60-sample window — this sidesteps host/guest clock skew.
p50/p95 render live at the right of the guest status bar.

The probe traverses encryption, the relay hop, and the daemon's read
loop — the same path as terminal frames. It excludes xterm.js render
time (sub-millisecond for typical frames).

## Reproducing

1. `go build ./cmd/relay && go build ./cmd/tandem`
2. `./relay --addr :8099 --base-url http://localhost:8099`
3. `tandem --relay ws://localhost:8099 <command>`
4. Open the join link, wait ≥3 ping cycles, read the status-bar stat.

## Baseline

| Date | Path | p50 | p95 |
|------|------|-----|-----|
| 2026-07-12 | localhost loopback (macOS arm64) | 0 ms | 4 ms |

Loopback establishes the software-stack floor: sealing, relay
forwarding, daemon echo, and unsealing cost ≈4 ms worst-case. The FR3
budget is therefore dominated by network RTT; a same-continent link
(~20–60 ms RTT) lands well inside p50 < 100 ms.
