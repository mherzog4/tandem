// Package hostlink is the host daemon's connection to the relay. It
// registers a session, mints the E2E session key, and streams the PTY
// tap to guests as sealed binary frames (FR5). The relay never sees
// plaintext or the key: the key rides the join link's URL fragment.
//
// The link survives network blips (NFR3): writes never fail, output is
// buffered locally while disconnected, and the sender goroutine redials
// with the relay's resume token and replays scrollback. Guest joins and
// rejoins also trigger a scrollback replay so refreshes restore history.
package hostlink

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/mherzog4/tandem/internal/e2e"
)

// Frame envelope inside the sealed plaintext: first byte tags the kind.
const (
	FramePTY    = 0x00 // raw PTY output bytes
	FrameCtrl   = 0x01 // JSON control message (resize, shutter)
	FrameReplay = 0x02 // scrollback snapshot: guest resets, then renders body
)

// ScrollbackMax bounds the replay buffer. Trimming can cut mid escape
// sequence; xterm.js tolerates a garbled first fragment.
// ponytail: byte-oriented ring, switch to line-aware trimming if the
// first-line artifacts ever bother anyone.
const ScrollbackMax = 2 << 20

const outboundBuf = 1024

type Link struct {
	relayURL string
	cipher   *e2e.Cipher
	JoinURL  string // includes #k=<session key> for guests

	sessionID   string
	resumeToken string
	// joinURL is the bare link from the relay hello; JoinURL adds the
	// key fragment, which the relay must never learn.
	joinURL string

	// Incoming carries decrypted guest->host frames (CRDT ops in M1).
	// Frames that fail to open are dropped: they are relay noise or
	// forgery, never trusted input.
	Incoming chan []byte

	outbound chan []byte // sealed frames awaiting send
	done     chan struct{}
	closeOne sync.Once

	mu         sync.Mutex
	scrollback []byte
	shuttered  bool
}

// Connect dials the relay's /ws/host endpoint, waits for the session
// hello, and mints a fresh session key (a new key every session is the
// rotation policy). relayURL is the ws:// or wss:// base.
func Connect(ctx context.Context, relayURL string) (*Link, error) {
	key := e2e.NewKey()
	cipher, err := e2e.NewCipher(key)
	if err != nil {
		return nil, err
	}
	l := &Link{
		relayURL: relayURL,
		cipher:   cipher,
		Incoming: make(chan []byte, 64),
		outbound: make(chan []byte, outboundBuf),
		done:     make(chan struct{}),
	}
	conn, err := l.dial(ctx, false)
	if err != nil {
		return nil, err
	}
	l.JoinURL = l.joinURL + "#" + e2e.FragmentParam + "=" + e2e.EncodeKey(key)
	go l.run(conn)
	return l, nil
}

// dial connects (or reconnects) and processes the hello. resume=true
// reclaims the existing session ID with the resume token.
func (l *Link) dial(ctx context.Context, resume bool) (*websocket.Conn, error) {
	url := l.relayURL + "/ws/host"
	if resume {
		url += "?resume=" + l.sessionID + "&token=" + l.resumeToken
	}
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("dial relay: %w", err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		conn.Close(websocket.StatusProtocolError, "no hello")
		return nil, fmt.Errorf("read session hello: %w", err)
	}
	var hello struct {
		Type        string `json:"type"`
		ID          string `json:"id"`
		JoinURL     string `json:"joinURL"`
		ResumeToken string `json:"resumeToken"`
	}
	if err := json.Unmarshal(data, &hello); err != nil || hello.Type != "session" {
		conn.Close(websocket.StatusProtocolError, "bad hello")
		return nil, fmt.Errorf("bad session hello %q", data)
	}
	l.sessionID = hello.ID
	l.resumeToken = hello.ResumeToken
	l.joinURL = hello.JoinURL
	return conn, nil
}

var errClosed = fmt.Errorf("hostlink closed")

// run owns the connection: it forwards the outbound queue, restarts the
// reader per connection, and redials forever on failure.
func (l *Link) run(conn *websocket.Conn) {
	for {
		readerDone := make(chan struct{})
		go l.readLoop(conn, readerDone)

		err := l.writeLoop(conn)
		conn.Close(websocket.StatusGoingAway, "")
		<-readerDone
		if err == errClosed {
			close(l.Incoming)
			return
		}

		// Redial with resume until it works or the link is closed.
		for {
			select {
			case <-l.done:
				close(l.Incoming)
				return
			case <-time.After(time.Second):
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			c, err := l.dial(ctx, true)
			cancel()
			if err != nil {
				continue
			}
			conn = c
			l.enqueueReplay() // guests may have missed frames
			break
		}
	}
}

func (l *Link) writeLoop(conn *websocket.Conn) error {
	for {
		select {
		case <-l.done:
			return errClosed
		case frame := <-l.outbound:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := conn.Write(ctx, websocket.MessageBinary, frame)
			cancel()
			if err != nil {
				// Frame stays lost from the live stream; the reconnect
				// replay covers guests.
				return err
			}
		}
	}
}

func (l *Link) readLoop(conn *websocket.Conn, done chan struct{}) {
	defer close(done)
	for {
		typ, data, err := conn.Read(context.Background())
		if err != nil {
			return
		}
		if typ == websocket.MessageText {
			var p struct{ Type, Event, Name string }
			if json.Unmarshal(data, &p) == nil && p.Type == "presence" && p.Event == "join" {
				// New or refreshed guest: replay scrollback so they see
				// history, not a blank screen. Broadcast resets existing
				// guests too — idempotent since replay starts with reset.
				l.enqueueReplay()
				// A guest joining mid-shutter missed the state change;
				// re-broadcast so they show the paused card.
				l.mu.Lock()
				shuttered := l.shuttered
				l.mu.Unlock()
				if shuttered {
					_ = l.WriteControl(map[string]any{"type": "shutter", "on": true})
				}
			}
			continue
		}
		plain, err := l.cipher.Open(data)
		if err != nil {
			continue // tampered or foreign frame: drop, never deliver
		}
		// Latency pings (FR3) are echoed here so guests can measure the
		// real round trip without involving the daemon.
		if len(plain) > 1 && plain[0] == FrameCtrl {
			var ctrl struct {
				Type string          `json:"type"`
				T    json.RawMessage `json:"t"`
			}
			if json.Unmarshal(plain[1:], &ctrl) == nil && ctrl.Type == "ping" {
				_ = l.WriteControl(map[string]any{"type": "pong", "t": ctrl.T})
				continue
			}
		}
		select {
		case l.Incoming <- plain:
		default: // ponytail: drop on backpressure; revisit if M1 CRDT ops ever hit this
		}
	}
}

// Write records PTY output in the scrollback and queues one sealed
// frame. It never fails and never blocks on the network, so the PTY
// tee keeps flowing during relay outages (NFR3). Satisfies io.Writer.
//
// While the privacy shutter is on (FR4), output is discarded entirely:
// not sent, not recorded. Shuttered content can therefore never leak
// through a later scrollback replay.
func (l *Link) Write(p []byte) (int, error) {
	l.mu.Lock()
	if l.shuttered {
		l.mu.Unlock()
		return len(p), nil
	}
	l.scrollback = append(l.scrollback, p...)
	if over := len(l.scrollback) - ScrollbackMax; over > 0 {
		l.scrollback = l.scrollback[over:]
	}
	l.mu.Unlock()
	l.enqueue(FramePTY, p)
	return len(p), nil
}

// SetShuttered toggles the privacy shutter. Guests are told the state;
// on unshutter they also get a fresh replay so their view repaints from
// the (shutter-free) scrollback.
func (l *Link) SetShuttered(on bool) {
	l.mu.Lock()
	l.shuttered = on
	l.mu.Unlock()
	_ = l.WriteControl(map[string]any{"type": "shutter", "on": on})
	if !on {
		l.enqueueReplay()
	}
}

// WriteControl seals and queues a JSON control message to guests.
func (l *Link) WriteControl(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	l.enqueue(FrameCtrl, body)
	return nil
}

func (l *Link) enqueueReplay() {
	l.mu.Lock()
	snap := make([]byte, len(l.scrollback))
	copy(snap, l.scrollback)
	l.mu.Unlock()
	l.enqueue(FrameReplay, snap)
}

func (l *Link) enqueue(kind byte, body []byte) {
	frame := make([]byte, 1+len(body))
	frame[0] = kind
	copy(frame[1:], body)
	sealed := l.cipher.Seal(frame)
	select {
	case l.outbound <- sealed:
	default:
		// Queue full (long outage): drop the live frame; scrollback
		// replay on reconnect makes guests whole.
	}
}

func (l *Link) Close() error {
	l.closeOne.Do(func() { close(l.done) })
	return nil
}
