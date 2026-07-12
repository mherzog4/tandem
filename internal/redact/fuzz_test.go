package redact

import (
	"io"
	"testing"
)

// FuzzWrite streams arbitrary terminal output (all attacker-influenced)
// through the redactor. The redactor deliberately rewrites content (it
// collapses PEM blocks, masks secrets), so the invariant is not that
// bytes are preserved but that the io.Writer contract holds: Write never
// panics and always reports the full input length, no matter how the
// stream is chunked across the secret-spanning buffer.
func FuzzWrite(f *testing.F) {
	f.Add([]byte("plain line\n"), 3)
	f.Add([]byte("export API_KEY=supersecretvalue123\n"), 1)
	f.Add([]byte("-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\n"), 5)
	f.Add([]byte("no newline at all"), 0)

	f.Fuzz(func(t *testing.T, data []byte, splits int) {
		r := New(io.Discard)
		total := 0
		for _, chunk := range splitInto(data, splits) {
			n, err := r.Write(chunk)
			if err != nil {
				t.Fatalf("Write error: %v", err)
			}
			if n != len(chunk) {
				t.Fatalf("Write returned %d, want %d", n, len(chunk))
			}
			total += n
		}
		if total != len(data) {
			t.Fatalf("total written %d, want %d", total, len(data))
		}
	})
}

// splitInto cuts data into up to n+1 roughly-equal chunks so a secret can
// straddle a Write boundary.
func splitInto(data []byte, n int) [][]byte {
	if n <= 0 || len(data) < 2 {
		return [][]byte{data}
	}
	if n > len(data) {
		n = len(data)
	}
	step := len(data) / (n + 1)
	if step == 0 {
		step = 1
	}
	var chunks [][]byte
	for i := 0; i < len(data); i += step {
		end := i + step
		if end > len(data) {
			end = len(data)
		}
		chunks = append(chunks, data[i:end])
	}
	return chunks
}
