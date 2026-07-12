// Package ptywrap spawns a command inside a PTY and gives the host a
// fully interactive passthrough terminal. All PTY output is tee'd to a
// tap writer — the feed the relay layer will encrypt and ship to guests.
//
// The security invariant lives here: this package holds the only handle
// to the child's stdin. Callers other than the host daemon get the
// output tap, never the PTY file.
package ptywrap

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// Run executes argv inside a PTY, wiring the current process's terminal
// through to it. Every byte the child writes is also copied to tap (may
// be nil). Blocks until the child exits and returns its exit code.
func Run(argv []string, tap io.Writer) (int, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return -1, err
	}
	defer ptmx.Close()

	// Propagate window size, initial and on SIGWINCH.
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	go func() {
		for range winch {
			_ = pty.InheritSize(os.Stdin, ptmx)
		}
	}()
	winch <- syscall.SIGWINCH

	// Raw mode so the child TUI sees keystrokes unmangled. Skipped when
	// stdin is not a terminal (tests, pipes).
	if term.IsTerminal(int(os.Stdin.Fd())) {
		oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return -1, err
		}
		defer term.Restore(int(os.Stdin.Fd()), oldState)
	}

	// Host stdin -> PTY. Sole writer to the child's input.
	go func() { _, _ = io.Copy(ptmx, os.Stdin) }()

	// PTY -> host stdout, tee'd to the tap.
	out := io.Writer(os.Stdout)
	if tap != nil {
		out = io.MultiWriter(os.Stdout, tap)
	}
	_, _ = io.Copy(out, ptmx) // returns on child exit (PTY EOF)

	err = cmd.Wait()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}
