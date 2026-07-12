package ptywrap

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mherzog4/tandem/internal/signer"
)

func TestRunCapturesOutputAndExitCode(t *testing.T) {
	var tap bytes.Buffer
	code, err := Run([]string{"sh", "-c", "printf hello-from-pty; exit 7"}, Options{Tap: &tap})
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
	if !strings.Contains(tap.String(), "hello-from-pty") {
		t.Fatalf("tap missing child output, got %q", tap.String())
	}
}

func TestRunZeroExit(t *testing.T) {
	code, err := Run([]string{"true"}, Options{})
	if err != nil || code != 0 {
		t.Fatalf("got code=%d err=%v, want 0, nil", code, err)
	}
}

func TestCopyInterceptSwallowsKey(t *testing.T) {
	var out bytes.Buffer
	fired := 0
	src := bytes.NewReader([]byte("ab\x1ccd\x1c\x1ce"))
	err := copyIntercept(&out, src, map[byte]func(){0x1c: func() { fired++ }})
	if err != io.EOF && err != nil {
		t.Fatal(err)
	}
	if out.String() != "abcde" {
		t.Fatalf("forwarded %q, want abcde", out.String())
	}
	if fired != 3 {
		t.Fatalf("intercept fired %d times, want 3", fired)
	}
}

// TestInjectorEndToEnd wraps cat: a signed submission reaches the child
// (echoed to the tap in bracketed paste); forged and replayed ones never
// do (FR21).
func TestInjectorEndToEnd(t *testing.T) {
	s, err := signer.New()
	if err != nil {
		t.Fatal(err)
	}
	inj := NewInjector(signer.NewVerifier(s.Public()))

	var tap syncBuffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		// cat exits when the timeout kills it via sh.
		Run([]string{"sh", "-c", "cat & CPID=$!; sleep 2; kill $CPID"}, Options{Tap: &tap, Injector: inj})
	}()

	time.Sleep(300 * time.Millisecond)
	inj.Submit(s.Sign("legit prompt"))

	// Forgery: signed by a different key.
	other, _ := signer.New()
	inj.Submit(other.Sign("FORGED"))

	// Replay: reuse the first (already accepted) submission.
	first := s.Sign("second legit")
	inj.Submit(first)
	inj.Submit(first)

	<-done
	out := tap.String()
	if !strings.Contains(out, "legit prompt") || !strings.Contains(out, "second legit") {
		t.Fatalf("signed submissions missing from child output: %q", out)
	}
	if strings.Contains(out, "FORGED") {
		t.Fatalf("forged submission reached the PTY: %q", out)
	}
	if strings.Count(out, "second legit") > 2 { // echo shows paste + cat output
		t.Fatalf("replayed submission reached the PTY: %q", out)
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
