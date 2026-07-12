package ptywrap

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunCapturesOutputAndExitCode(t *testing.T) {
	var tap bytes.Buffer
	code, err := Run([]string{"sh", "-c", "printf hello-from-pty; exit 7"}, &tap, nil)
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
	code, err := Run([]string{"true"}, nil, nil)
	if err != nil || code != 0 {
		t.Fatalf("got code=%d err=%v, want 0, nil", code, err)
	}
}
