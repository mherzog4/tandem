## Summary

<!-- What does this change and why? Link the issue: Closes #NN -->

## How verified

<!-- Tests added/run, manual end-to-end steps, screenshots for UI. -->

- [ ] `go test -race ./...` green
- [ ] `go vet ./...` and `gofmt -l .` clean
- [ ] Preserves the security invariant (guests cannot execute; see docs/protocol.md)
- [ ] Updated docs if the protocol or user-facing behavior changed
