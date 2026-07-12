// Command tandem is the host CLI. It wraps a terminal command (a coding
// agent like Claude Code, or any program) in a managed PTY and shares the
// session with browser guests. See prd.md.
package main

import (
	"fmt"
	"os"
)

// version is overridden at release time via -ldflags "-X main.version=...".
var version = "0.0.1-dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println("tandem", version)
		return
	}
	fmt.Fprintln(os.Stderr, "usage: tandem <command> [args...]   (PTY wrapping lands in issue #2)")
	os.Exit(2)
}
