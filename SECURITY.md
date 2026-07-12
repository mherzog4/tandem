# Security Policy

Tandem's whole premise is a security boundary — a stakeholder can compose
into a coding agent's prompt but structurally cannot execute anything.
Vulnerability reports are taken seriously.

## Reporting a vulnerability

**Do not open a public issue for security problems.**

Use GitHub's private reporting: **Security → Advisories → Report a
vulnerability** on this repository
(https://github.com/mherzog4/tandem/security/advisories/new), or email the
maintainer at matthewherzog4@gmail.com.

Please include: what you found, how to reproduce it, and the impact. A
proof-of-concept helps. You'll get an acknowledgement within a few days.

## What's in scope

The properties that matter most:

- **Guest input cannot reach the wrapped command's stdin.** Any path by
  which a guest (or a compromised relay) injects into the PTY, bypasses the
  host-only submit, or forges a signed submission.
- **The relay cannot read session content.** Frames are AES-256-GCM sealed
  with a key that lives only in the join link's URL fragment. A way for the
  relay to recover plaintext or the key is in scope.
- **Secret redaction** bypasses that leak masked content to guests.
- **The email allowlist / recording consent** being circumvented.
- Relay denial-of-service beyond the built-in caps and rate limits.

## What's out of scope

- A malicious *host* — the host runs the agent and owns the machine; Tandem
  does not defend the host against itself.
- Social engineering of the host into submitting bad input (the host reviews
  everything before it executes — that's the design).
- The stakeholder seeing terminal content the host chose to share.

## Supported versions

The latest release on `main` is supported. This is a young project; please
run the current version before reporting.
