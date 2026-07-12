// Package e2e implements the session encryption between host and guests
// (FR5). The relay only ever sees sealed frames.
//
// Scheme:
//   - One 32-byte AES-256-GCM session key, generated host-side per
//     session. Key rotation happens naturally: every session (and every
//     host reconnect that creates a new session) mints a fresh key.
//   - The key travels to guests in the join link's URL *fragment*
//     ("#k=<base64url>"). Fragments are never sent in HTTP requests, so
//     the relay cannot see the key even though it serves the link.
//   - Frame format: 12-byte random nonce || GCM ciphertext+tag.
//
// AES-GCM over XChaCha20-Poly1305 because the browser guest client uses
// native WebCrypto, which ships AES-GCM but not XChaCha20 — this keeps
// the guest free of JS crypto libraries.
//
// Random 96-bit nonces are safe at session scale: collision odds stay
// negligible below ~2^32 frames and a terminal session is orders of
// magnitude smaller.
//
// FR21 (host input signing) will layer Ed25519 signatures inside sealed
// frames in issue #12; sealing alone already authenticates
// host<->guest traffic against the relay.
package e2e

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// KeySize is the AES-256 key length in bytes.
const KeySize = 32

// FragmentParam is the URL-fragment parameter carrying the session key.
const FragmentParam = "k"

// NewKey returns a fresh random session key.
func NewKey() []byte {
	k := make([]byte, KeySize)
	if _, err := rand.Read(k); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return k
}

// EncodeKey renders a key for embedding in a URL fragment.
func EncodeKey(key []byte) string { return base64.RawURLEncoding.EncodeToString(key) }

// DecodeKey parses EncodeKey output.
func DecodeKey(s string) ([]byte, error) {
	k, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decode session key: %w", err)
	}
	if len(k) != KeySize {
		return nil, fmt.Errorf("session key is %d bytes, want %d", len(k), KeySize)
	}
	return k, nil
}

// Cipher seals and opens session frames.
type Cipher struct {
	aead cipher.AEAD
}

func NewCipher(key []byte) (*Cipher, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Seal encrypts one frame: 12-byte random nonce || ciphertext+tag.
func (c *Cipher) Seal(plaintext []byte) []byte {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil)
}

// Open decrypts a Seal-formatted frame, rejecting anything tampered.
func (c *Cipher) Open(frame []byte) ([]byte, error) {
	if len(frame) < c.aead.NonceSize() {
		return nil, errors.New("frame shorter than nonce")
	}
	nonce, ct := frame[:c.aead.NonceSize()], frame[c.aead.NonceSize():]
	return c.aead.Open(nil, nonce, ct, nil)
}
