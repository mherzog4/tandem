package ptywrap

import (
	"fmt"
	"io"
	"os"

	"github.com/mherzog4/tandem/internal/signer"
)

// Injector is the sole bridge between network-derived text (the
// Composer buffer) and the child's stdin. Every submission must carry
// a valid host-local signature with a fresh sequence number (FR21);
// anything else is dropped and logged, never written.
type Injector struct {
	verifier *signer.Verifier
	ch       chan signer.SignedText
}

func NewInjector(v *signer.Verifier) *Injector {
	return &Injector{verifier: v, ch: make(chan signer.SignedText, 8)}
}

// Submit queues a signed submission for verification and injection.
func (inj *Injector) Submit(st signer.SignedText) {
	select {
	case inj.ch <- st:
	default: // agent is not consuming; dropping beats blocking the daemon
	}
}

// serve verifies and writes submissions until the PTY closes. Text is
// wrapped in bracketed paste so multi-line buffers land as one paste in
// agent TUIs (and modern readline), then the submit key is sent.
func (inj *Injector) serve(ptmx io.Writer) {
	for st := range inj.ch {
		if err := inj.verifier.Verify(st); err != nil {
			fmt.Fprintf(os.Stderr, "tandem: REJECTED injected input (%v)\r\n", err)
			continue
		}
		if _, err := fmt.Fprintf(ptmx, "\x1b[200~%s\x1b[201~\r", st.Text); err != nil {
			return
		}
	}
}
