// Package redact masks likely secrets in the terminal stream before it
// leaves the host (FR23). It sits between the PTY tap and the hostlink,
// strictly pre-encryption: guests receive masked bytes, the host's own
// terminal shows originals.
//
// Scanning is line-buffered: bytes accumulate until a newline, 4 KiB,
// or a 50 ms lull, then each completed segment is pattern-scanned and
// forwarded. This keeps the guest view interactive while never emitting
// an unscanned token that a chunk boundary could have split.
// ponytail: tokens interleaved with ANSI color codes mid-token can
// evade the line scan; handle if real agents ever emit that.
package redact

import (
	"bytes"
	"io"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

const (
	flushAfter = 50 * time.Millisecond
	maxBuffer  = 4 << 10
)

// Mask is what guests see in place of a detected secret.
const Mask = "•••[redacted]"

var patterns = []*regexp.Regexp{
	// AWS access key IDs.
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	// GitHub tokens (classic + fine-grained prefixes).
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,255}\b`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,255}\b`),
	// Slack tokens.
	regexp.MustCompile(`\bxox[baprs]-[0-9A-Za-z-]{10,}\b`),
	// JWTs.
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,}\b`),
	// Bearer headers.
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{20,}`),
	// Env-style assignments whose name smells secret. Keeps the name,
	// masks the value.
	regexp.MustCompile(`(?i)([A-Z0-9_]*(?:KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIALS?)[A-Z0-9_]*\s*[=:]\s*)("[^"]{8,}"|'[^']{8,}'|[^\s"']{8,})`),
}

var pemBegin = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)
var pemEnd = regexp.MustCompile(`-----END [A-Z ]*PRIVATE KEY-----`)

// Redactor is an io.Writer that scans and forwards to the next writer.
type Redactor struct {
	next io.Writer

	mu    sync.Mutex
	buf   []byte
	inPEM bool
	timer *time.Timer

	// Count of redactions performed; the daemon surfaces it.
	Count atomic.Int64
	// OnRedact, if set, fires once per masked secret (host indicator).
	OnRedact func()
}

func New(next io.Writer) *Redactor { return &Redactor{next: next} }

func (r *Redactor) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)

	// Emit every completed line; keep the trailing partial.
	for {
		nl := bytes.IndexByte(r.buf, '\n')
		if nl < 0 {
			break
		}
		if err := r.emit(r.buf[:nl+1]); err != nil {
			return 0, err
		}
		r.buf = r.buf[nl+1:]
	}
	if len(r.buf) >= maxBuffer {
		if err := r.emit(r.buf); err != nil {
			return 0, err
		}
		r.buf = nil
	}
	if len(r.buf) > 0 {
		if r.timer == nil {
			r.timer = time.AfterFunc(flushAfter, r.timedFlush)
		} else {
			r.timer.Reset(flushAfter)
		}
	}
	return len(p), nil
}

func (r *Redactor) timedFlush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) > 0 {
		_ = r.emit(r.buf)
		r.buf = nil
	}
}

// emit scans one segment and forwards it. Caller holds r.mu.
func (r *Redactor) emit(seg []byte) error {
	out := r.scan(seg)
	_, err := r.next.Write(out)
	return err
}

func (r *Redactor) scan(seg []byte) []byte {
	s := string(seg)

	// PEM block state machine: everything between BEGIN/END PRIVATE KEY
	// lines is masked, across writes.
	if r.inPEM {
		if pemEnd.MatchString(s) {
			r.inPEM = false
			r.hit()
			return []byte(Mask + " [private key block]\r\n")
		}
		return nil // swallow interior PEM lines
	}
	if loc := pemBegin.FindStringIndex(s); loc != nil {
		r.inPEM = !pemEnd.MatchString(s[loc[1]:])
		r.hit()
		prefix := s[:loc[0]]
		if r.inPEM {
			return []byte(prefix + Mask + " [private key block]")
		}
		// Single-segment PEM: mask through the END marker.
		endLoc := pemEnd.FindStringIndex(s)
		return []byte(prefix + Mask + " [private key block]" + s[endLoc[1]:])
	}

	for i, re := range patterns {
		if i == len(patterns)-1 {
			// Assignment rule: keep the variable name, mask the value.
			s = re.ReplaceAllStringFunc(s, func(m string) string {
				r.hit()
				return re.ReplaceAllString(m, "${1}"+Mask)
			})
			continue
		}
		s = re.ReplaceAllStringFunc(s, func(string) string {
			r.hit()
			return Mask
		})
	}
	return []byte(s)
}

func (r *Redactor) hit() {
	r.Count.Add(1)
	if r.OnRedact != nil {
		r.OnRedact()
	}
}
