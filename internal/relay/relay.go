// Package relay implements the stateless session relay (PRD §8.1 #2).
//
// The relay forwards frames between one host and its guests and holds no
// session content: binary WebSocket messages are opaque payloads passed
// through verbatim (ciphertext once issue #4 lands). The only messages
// the relay itself originates are text-frame presence events. Nothing is
// persisted; a relay restart drops sessions and hosts reconnect (NFR3 is
// handled host-side).
package relay

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// MaxParticipants counts the host plus guests (PRD non-goal 2: no more
// than three participants per session).
const MaxParticipants = 3

// Presence is the only relay-originated message, sent as a text frame so
// clients can distinguish it from opaque binary payloads.
type Presence struct {
	Type  string `json:"type"` // always "presence"
	Event string `json:"event"` // "join" or "leave"
	Name  string `json:"name"`
}

type session struct {
	mu     sync.Mutex
	host   *websocket.Conn
	guests map[*websocket.Conn]string // conn -> display name
}

// Server is an http.Handler serving the relay protocol.
type Server struct {
	mu       sync.Mutex
	sessions map[string]*session
	// BaseURL is the externally visible URL used to build join links.
	BaseURL string
}

func NewServer(baseURL string) *Server {
	return &Server{sessions: make(map[string]*session), BaseURL: strings.TrimSuffix(baseURL, "/")}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/ws/host":
		s.serveHost(w, r)
	case strings.HasPrefix(r.URL.Path, "/ws/join/"):
		s.serveGuest(w, r, strings.TrimPrefix(r.URL.Path, "/ws/join/"))
	case r.URL.Path == "/healthz":
		fmt.Fprintln(w, "ok")
	default:
		http.NotFound(w, r)
	}
}

func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) serveHost(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	id := newSessionID()
	sess := &session{host: conn, guests: make(map[*websocket.Conn]string)}
	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.sessions, id)
		s.mu.Unlock()
		sess.mu.Lock()
		for g := range sess.guests {
			g.Close(websocket.StatusGoingAway, "host left")
		}
		sess.mu.Unlock()
		conn.Close(websocket.StatusNormalClosure, "")
	}()

	// Tell the host its session ID and join link.
	hello, _ := json.Marshal(map[string]string{
		"type": "session", "id": id, "joinURL": s.BaseURL + "/s/" + id,
	})
	ctx := r.Context()
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		return
	}

	// Host -> all guests, verbatim.
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		sess.mu.Lock()
		for g := range sess.guests {
			_ = writeTimeout(g, typ, data)
		}
		sess.mu.Unlock()
	}
}

func (s *Server) serveGuest(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	sess := s.sessions[id]
	s.mu.Unlock()
	if sess == nil {
		http.Error(w, "no such session", http.StatusNotFound)
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "guest"
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}

	sess.mu.Lock()
	if 1+len(sess.guests) >= MaxParticipants {
		sess.mu.Unlock()
		conn.Close(websocket.StatusPolicyViolation, "session full")
		return
	}
	sess.guests[conn] = name
	s.broadcastPresenceLocked(sess, Presence{Type: "presence", Event: "join", Name: name})
	sess.mu.Unlock()

	defer func() {
		sess.mu.Lock()
		delete(sess.guests, conn)
		s.broadcastPresenceLocked(sess, Presence{Type: "presence", Event: "leave", Name: name})
		sess.mu.Unlock()
		conn.Close(websocket.StatusNormalClosure, "")
	}()

	// Guest -> host only, verbatim. Guests never reach each other directly.
	ctx := r.Context()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		sess.mu.Lock()
		host := sess.host
		sess.mu.Unlock()
		if host != nil {
			_ = writeTimeout(host, typ, data)
		}
	}
}

// broadcastPresenceLocked sends a presence event to the host and every
// guest. Caller holds sess.mu.
func (s *Server) broadcastPresenceLocked(sess *session, p Presence) {
	msg, _ := json.Marshal(p)
	_ = writeTimeout(sess.host, websocket.MessageText, msg)
	for g := range sess.guests {
		_ = writeTimeout(g, websocket.MessageText, msg)
	}
}

func writeTimeout(c *websocket.Conn, typ websocket.MessageType, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Write(ctx, typ, data)
}
