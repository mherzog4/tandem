package broker

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mherzog4/tandem/internal/hostlink"
	"github.com/mherzog4/tandem/internal/relay"
)

// FuzzHandle feeds arbitrary frames into the broker's guest-message
// allowlist (the switch in handle, FR8). Every byte here is what a
// hostile guest can put on the wire; handle must drop malformed or
// unknown messages without panicking, no matter the bytes or ordering.
// The link is wired to an in-process relay once so broadcasts have
// somewhere to drain; only handle is exercised per iteration.
func FuzzHandle(f *testing.F) {
	srv := httptest.NewServer(relay.NewServer("http://relay.test"))
	f.Cleanup(srv.Close)
	wsBase := "ws" + strings.TrimPrefix(srv.URL, "http")
	// Background context: the link must live for the whole fuzz run, not a
	// fixed window, so a long -fuzztime never trips a dial deadline.
	link, err := hostlink.Connect(context.Background(), wsBase)
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { _ = link.Close() })
	b := New(link)

	ctrl := func(json string) []byte { return append([]byte{hostlink.FrameCtrl}, []byte(json)...) }
	f.Add(ctrl(`{"type":"op","author":"a","op":{"author":"a","pos":0,"del":0,"ins":"hi"}}`))
	f.Add(ctrl(`{"type":"board-move","id":"x","toIndex":-999999}`))
	f.Add(ctrl(`{"type":"react","author":"a","emoji":"👍"}`))
	f.Add(ctrl(`{"type":"unknown"}`))
	f.Add([]byte{})
	f.Add([]byte{hostlink.FramePTY, 0x00})

	f.Fuzz(func(t *testing.T, frame []byte) {
		// Must not panic on any input; drops are the expected outcome.
		b.handle(frame)
	})
}
