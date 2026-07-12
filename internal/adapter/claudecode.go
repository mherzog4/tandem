// Package adapter wires the confirmed domain model into specific
// agents' context (FR15). The Claude Code adapter manages a delimited
// include block in the wrapped repo's CLAUDE.md; Claude Code reads
// CLAUDE.md (and its @imports) at conversation start, which pulls
// DOMAIN.md into context. Mid-session domain changes are surfaced by
// the daemon's submit path instead (a re-read note), since CLAUDE.md
// is not re-read every turn.
package adapter

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	beginMark = "<!-- tandem:begin — managed by tandem, do not edit this block -->"
	endMark   = "<!-- tandem:end -->"

	includeBlock = beginMark + `
@DOMAIN.md

The imported DOMAIN.md is this repository's ubiquitous language,
agreed with the domain experts in Tandem sessions. Name new code
constructs after its terms; where a term carries a ` + "`code:`" + ` alias,
use the alias in code and the business wording everywhere else.
` + endMark
)

// EnsureClaudeInclude installs or refreshes the managed block in
// dir/CLAUDE.md. Idempotent: repeated calls produce identical content.
// User content outside the block is preserved byte-for-byte; a missing
// CLAUDE.md is created containing only the block.
func EnsureClaudeInclude(dir string) error {
	path := filepath.Join(dir, "CLAUDE.md")
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(old)

	if b := strings.Index(content, beginMark); b >= 0 {
		if e := strings.Index(content, endMark); e > b {
			updated := content[:b] + includeBlock + content[e+len(endMark):]
			if updated == content {
				return nil // already current
			}
			return os.WriteFile(path, []byte(updated), 0o644)
		}
		// Begin marker without end: user mangled the block. Leave their
		// file alone rather than guessing at repairs.
		return nil
	}

	sep := ""
	if len(content) > 0 && !strings.HasSuffix(content, "\n\n") {
		sep = "\n"
		if !strings.HasSuffix(content, "\n") {
			sep = "\n\n"
		}
	}
	return os.WriteFile(path, []byte(content+sep+includeBlock+"\n"), 0o644)
}

// IsClaude reports whether argv looks like a Claude Code invocation.
func IsClaude(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	base := filepath.Base(argv[0])
	return base == "claude" || strings.HasPrefix(base, "claude-")
}
