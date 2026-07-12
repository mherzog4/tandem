// Package signer implements the host-local input signature (FR21).
//
// Threat model: the Composer buffer is assembled from network input
// (guest ops via the relay). Before any of it may touch the PTY's
// stdin, it must pass a signature check against a key that never
// leaves the host process. Even if the relay — or any network layer —
// is fully compromised, it cannot mint input the injector will accept:
// the signing chokepoint is the only bridge between network-derived
// text and the shell.
//
// Signed payloads carry a monotonic sequence number; the verifier
// refuses any sequence at or below the last accepted one, so captured
// submissions cannot be replayed.
package signer

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
)

// SignedText is a submission the injector can verify.
type SignedText struct {
	Seq  uint64
	Text string
	Sig  []byte
}

// Signer holds the private half; it lives in the host daemon only.
type Signer struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
	seq  uint64
}

func New() (*Signer, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Signer{priv: priv, pub: pub}, nil
}

// Public returns the verify key to hand the injector.
func (s *Signer) Public() ed25519.PublicKey { return s.pub }

// Sign stamps text with the next sequence number.
func (s *Signer) Sign(text string) SignedText {
	s.seq++
	return SignedText{Seq: s.seq, Text: text, Sig: ed25519.Sign(s.priv, payload(s.seq, text))}
}

// Verifier checks submissions; it holds only the public key.
type Verifier struct {
	pub     ed25519.PublicKey
	lastSeq uint64
}

func NewVerifier(pub ed25519.PublicKey) *Verifier { return &Verifier{pub: pub} }

var (
	ErrBadSignature = errors.New("input signature invalid")
	ErrReplay       = errors.New("input sequence replayed or stale")
)

// Verify returns nil only for an authentic, never-before-seen
// submission, and then advances the replay floor.
func (v *Verifier) Verify(st SignedText) error {
	if !ed25519.Verify(v.pub, payload(st.Seq, st.Text), st.Sig) {
		return ErrBadSignature
	}
	if st.Seq <= v.lastSeq {
		return ErrReplay
	}
	v.lastSeq = st.Seq
	return nil
}

func payload(seq uint64, text string) []byte {
	buf := make([]byte, 8+len(text))
	binary.BigEndian.PutUint64(buf, seq)
	copy(buf[8:], text)
	return buf
}
