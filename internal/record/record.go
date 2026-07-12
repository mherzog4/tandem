// Package record captures a replayable session cast (FR19). It writes
// asciinema v2 format — the de-facto terminal-recording standard, so
// casts also play in any asciinema player — with extra event lines for
// Composer edits and Board changes so the web player can scrub all
// three timelines together.
//
// The recorder sits on the REDACTED guest stream, so secrets masked
// for guests stay masked in the recording (FR19 + FR23). Recording
// only happens when the host declared --record (FR24 consent).
package record

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Recorder writes an asciinema v2 cast. Safe for concurrent use.
type Recorder struct {
	mu    sync.Mutex
	w     io.Writer
	start time.Time
	// now is injectable for tests; defaults to time.Since(start).
	now func() float64
}

// New writes the asciinema v2 header to w and returns a recorder.
// cols/rows are the initial terminal size.
func New(w io.Writer, cols, rows uint16) (*Recorder, error) {
	r := &Recorder{w: w}
	r.start = time.Time{} // real start stamped on first event via now()
	header := map[string]any{
		"version":   2,
		"width":     cols,
		"height":    rows,
		"timestamp": 0, // absolute wall-clock omitted (Date.now unavailable at build)
		"env":       map[string]string{"TERM": "xterm-256color"},
	}
	line, _ := json.Marshal(header)
	if _, err := fmt.Fprintf(w, "%s\n", line); err != nil {
		return nil, err
	}
	return r, nil
}

// SetClock overrides the timestamp source (tests).
func (r *Recorder) SetClock(now func() float64) {
	r.mu.Lock()
	r.now = now
	r.mu.Unlock()
}

func (r *Recorder) elapsed() float64 {
	if r.now != nil {
		return r.now()
	}
	if r.start.IsZero() {
		r.start = time.Now()
	}
	return time.Since(r.start).Seconds()
}

// event writes one cast line: [time, code, data].
func (r *Recorder) event(code, data string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	line, _ := json.Marshal([]any{r.elapsed(), code, data})
	_, _ = fmt.Fprintf(r.w, "%s\n", line)
}

// Output records terminal output ("o" per asciinema). Satisfies
// io.Writer so it tees the redacted guest stream.
func (r *Recorder) Output(p []byte) { r.event("o", string(p)) }
func (r *Recorder) Write(p []byte) (int, error) {
	r.Output(p)
	return len(p), nil
}

// Composer records the buffer text after an edit (custom code "c").
func (r *Recorder) Composer(text string) { r.event("c", text) }

// Board records a board-state snapshot as JSON (custom code "b").
func (r *Recorder) Board(cardsJSON string) { r.event("b", cardsJSON) }
