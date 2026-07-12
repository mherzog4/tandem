package ptywrap

import (
	"io"
	"bytes"
	"strings"
	"testing"
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
	err := copyIntercept(&out, src, 0x1c, func() { fired++ })
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
