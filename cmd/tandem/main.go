// Command tandem is the host CLI. It wraps a terminal command (a coding
// agent like Claude Code, or any program) in a managed PTY and shares the
// session with browser guests. See prd.md.
package main

import (
	"fmt"
	"os"

	"github.com/mherzog4/tandem/internal/ptywrap"
)

// version is overridden at release time via -ldflags "-X main.version=...".
var version = "0.0.1-dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: tandem <command> [args...]")
		os.Exit(2)
	}
	if os.Args[1] == "--version" || os.Args[1] == "-v" {
		fmt.Println("tandem", version)
		return
	}
	// Output tap becomes the encrypted relay feed in issue #3.
	code, err := ptywrap.Run(os.Args[1:], nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tandem:", err)
		os.Exit(1)
	}
	os.Exit(code)
}
