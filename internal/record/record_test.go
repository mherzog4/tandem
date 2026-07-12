package record

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestCastFormat(t *testing.T) {
	var buf bytes.Buffer
	r, err := New(&buf, 120, 40)
	if err != nil {
		t.Fatal(err)
	}
	tick := 0.0
	r.SetClock(func() float64 { tick += 1; return tick })

	r.Write([]byte("hello \x1b[31mworld\x1b[0m"))
	r.Composer("a claim can be reopened")
	r.Board(`[{"id":"e1","type":"event","text":"ClaimDenied"}]`)

	lines := bufio.NewScanner(strings.NewReader(buf.String()))

	// Header.
	lines.Scan()
	var header map[string]any
	if err := json.Unmarshal(lines.Bytes(), &header); err != nil {
		t.Fatalf("header: %v", err)
	}
	if header["version"].(float64) != 2 || header["width"].(float64) != 120 {
		t.Fatalf("bad header: %v", header)
	}

	// Events: [time, code, data].
	want := []struct{ code, data string }{
		{"o", "hello \x1b[31mworld\x1b[0m"},
		{"c", "a claim can be reopened"},
		{"b", `[{"id":"e1","type":"event","text":"ClaimDenied"}]`},
	}
	for i, w := range want {
		if !lines.Scan() {
			t.Fatalf("missing event %d", i)
		}
		var ev []any
		if err := json.Unmarshal(lines.Bytes(), &ev); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
		if ev[0].(float64) != float64(i+1) {
			t.Fatalf("event %d time = %v", i, ev[0])
		}
		if ev[1].(string) != w.code || ev[2].(string) != w.data {
			t.Fatalf("event %d = %v %v, want %s %s", i, ev[1], ev[2], w.code, w.data)
		}
	}
	if lines.Scan() {
		t.Fatalf("unexpected extra line: %q", lines.Text())
	}
}

func TestRedactedContentStaysRedacted(t *testing.T) {
	// The recorder tees the same (already-redacted) bytes it's given;
	// it never sees an unmasked secret. Verify it records verbatim.
	var buf bytes.Buffer
	r, _ := New(&buf, 80, 24)
	r.Write([]byte("key=•••[redacted]"))
	if strings.Contains(buf.String(), "sk_live") {
		t.Fatal("recorder invented plaintext")
	}
	if !strings.Contains(buf.String(), "redacted") {
		t.Fatalf("masked marker missing: %s", buf.String())
	}
}
