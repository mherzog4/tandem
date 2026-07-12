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

// Options tune a Run. All fields are optional.
type Options struct {
	// Tap receives a copy of every byte the child writes.
	Tap io.Writer
	// OnResize fires with the PTY dimensions at start and on every
	// window-size change, so the share layer can keep guest renderers
	// in sync.
	OnResize func(cols, rows uint16)
	// Intercepts maps stdin bytes the host daemon claims for itself
	// (e.g. 0x1C, Ctrl-\, for the privacy shutter). Intercepted bytes
	// are NOT forwarded to the child; the handler fires instead.
	Intercepts map[byte]func()
	// Injector, if set, is the only path by which network-derived text
	// (the Composer buffer) may reach the child's stdin. Every
	// submission is signature-verified first (FR21).
	Injector *Injector
	// OnHostInput fires whenever the host's own keystrokes flow to the
	// child. The mirror layer uses it to yield while the host types.
	OnHostInput func()
}

// Run executes argv inside a PTY, wiring the current process's terminal
// through to it. Blocks until the child exits and returns its exit code.
func Run(argv []string, opts Options) (int, error) {
	tap, onResize := opts.Tap, opts.OnResize
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
			if onResize != nil {
				if sz, err := pty.GetsizeFull(ptmx); err == nil {
					onResize(sz.Cols, sz.Rows)
				}
			}
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

	// Host stdin -> PTY: the host's own keystrokes, forwarded directly.
	go func() {
		var dst io.Writer = ptmx
		if opts.OnHostInput != nil {
			dst = writerFunc(func(p []byte) (int, error) {
				opts.OnHostInput()
				return ptmx.Write(p)
			})
		}
		if len(opts.Intercepts) > 0 {
			_ = copyIntercept(dst, os.Stdin, opts.Intercepts)
		} else {
			_, _ = io.Copy(dst, os.Stdin)
		}
	}()

	// Signed-submission path: the only other writer to the child's
	// stdin, gated by signature verification.
	if opts.Injector != nil {
		go opts.Injector.serve(ptmx)
	}

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

// copyIntercept forwards src to dst byte-stream style, swallowing every
// occurrence of an intercepted key and firing its handler instead. The
// child never sees intercepted bytes, so the hotkeys work regardless of
// what the wrapped TUI binds.
func copyIntercept(dst io.Writer, src io.Reader, keys map[byte]func()) error {
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			for {
				i := bytesIndexFunc(chunk, keys)
				if i < 0 {
					break
				}
				if i > 0 {
					if _, werr := dst.Write(chunk[:i]); werr != nil {
						return werr
					}
				}
				keys[chunk[i]]()
				chunk = chunk[i+1:]
			}
			if len(chunk) > 0 {
				if _, werr := dst.Write(chunk); werr != nil {
					return werr
				}
			}
		}
		if err != nil {
			return err
		}
	}
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// bytesIndexFunc returns the first index whose byte has a handler.
func bytesIndexFunc(b []byte, keys map[byte]func()) int {
	for i, c := range b {
		if _, ok := keys[c]; ok {
			return i
		}
	}
	return -1
}
