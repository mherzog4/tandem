package redact

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func scanAll(t *testing.T, input string) string {
	t.Helper()
	var out bytes.Buffer
	r := New(&out)
	// Write in awkward chunks to exercise line buffering.
	for i := 0; i < len(input); i += 7 {
		end := i + 7
		if end > len(input) {
			end = len(input)
		}
		if _, err := r.Write([]byte(input[i:end])); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(120 * time.Millisecond) // allow the lull flush
	return out.String()
}

func TestTruePositives(t *testing.T) {
	secrets := []string{
		"AKIAIOSFODNN7EXAMPLE",
		"ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		"github_pat_11ABCDEFG0123456789_abcdefghij",
		"xoxb-123456789012-abcdefghijklmnop",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9P",
		"Bearer abcdefghij1234567890abcdefghij",
	}
	for _, sec := range secrets {
		out := scanAll(t, "output "+sec+" more\n")
		if strings.Contains(out, sec) {
			t.Errorf("secret survived: %q -> %q", sec, out)
		}
		if !strings.Contains(out, Mask) {
			t.Errorf("no mask emitted for %q: %q", sec, out)
		}
	}
}

func TestEnvAssignmentKeepsName(t *testing.T) {
	out := scanAll(t, "export STRIPE_SECRET_KEY=sk_live_abc123def456\nDB_PASSWORD: hunter2hunter2\n")
	if strings.Contains(out, "sk_live_abc123def456") || strings.Contains(out, "hunter2hunter2") {
		t.Fatalf("env secret survived: %q", out)
	}
	if !strings.Contains(out, "STRIPE_SECRET_KEY=") || !strings.Contains(out, "DB_PASSWORD:") {
		t.Fatalf("variable names should survive: %q", out)
	}
}

func TestPEMBlockAcrossWrites(t *testing.T) {
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA7bq\nmoreb64lines\n-----END RSA PRIVATE KEY-----\nafter\n"
	out := scanAll(t, "before\n"+pem)
	if strings.Contains(out, "MIIEowIBAAKCAQEA7bq") || strings.Contains(out, "moreb64lines") {
		t.Fatalf("PEM interior leaked: %q", out)
	}
	if !strings.Contains(out, "before") || !strings.Contains(out, "after") {
		t.Fatalf("surrounding output lost: %q", out)
	}
}

func TestFalsePositives(t *testing.T) {
	clean := []string{
		"normal terminal output with words\n",
		"a claim can be reopened within 90 days\n",
		"git commit -m 'add key handling for keyboard events'\n",
		"the TOKEN variable is unset\n",           // no value assignment
		"PASSWORD=short\n",                        // value under 8 chars
		"eyJ short.not.jwt\n",                     // not a real JWT shape
		"AKIA1234 too short\n",                    // not a full AWS key
		"ls -la /home/user/keys/\n",
	}
	for _, c := range clean {
		out := scanAll(t, c)
		if strings.Contains(out, Mask) {
			t.Errorf("false positive on %q -> %q", c, out)
		}
	}
}

func TestCountAndCallback(t *testing.T) {
	var out bytes.Buffer
	r := New(&out)
	fired := 0
	r.OnRedact = func() { fired++ }
	r.Write([]byte("AKIAIOSFODNN7EXAMPLE and ghp_abcdefghijklmnopqrstuvwxyz0123456789\n"))
	if r.Count.Load() != 2 || fired != 2 {
		t.Fatalf("count=%d fired=%d, want 2/2", r.Count.Load(), fired)
	}
}
