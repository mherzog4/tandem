package adapter

import (
	"path/filepath"
)

// agentsBlock is the managed block written into AGENTS.md. Unlike Claude
// Code's CLAUDE.md, the AGENTS.md convention has no universal @import, so
// the block instructs the agent to read DOMAIN.md rather than importing
// it. That keeps the block static — the daemon keeps DOMAIN.md itself
// current, so this never needs rewriting as the model changes.
const agentsBlock = beginMark + `
## Domain model (managed by Tandem)

Read ` + "`DOMAIN.md`" + ` in this repository before writing code. It is the
ubiquitous language agreed with the domain experts in Tandem sessions.
Name new code constructs after its terms; where a term carries a ` + "`code:`" + `
alias, use the alias in code and the business wording everywhere else.
` + endMark

// EnsureAgentsInclude installs or refreshes the managed block in
// dir/AGENTS.md for agents that read the AGENTS.md convention (Codex,
// Cursor, Amp, opencode, Factory). Idempotent; preserves user content
// outside the block.
func EnsureAgentsInclude(dir string) error {
	return ensureManagedBlock(filepath.Join(dir, "AGENTS.md"), agentsBlock)
}
