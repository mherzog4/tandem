package e2e

import (
	"bytes"
	"testing"
)

// FuzzOpen throws arbitrary bytes at the decrypt boundary — every frame
// the relay forwards is attacker-controlled. Open must reject tampered or
// malformed frames with an error and never panic.
func FuzzOpen(f *testing.F) {
	key := make([]byte, KeySize)
	c, _ := NewCipher(key)
	f.Add([]byte{})
	f.Add(make([]byte, 11)) // shorter than a nonce
	f.Add(make([]byte, 64))

	f.Fuzz(func(t *testing.T, frame []byte) {
		if _, err := c.Open(frame); err == nil {
			// A random frame decrypting cleanly would mean the AEAD tag
			// was forgeable, which must never happen.
			t.Fatalf("Open accepted an unauthenticated frame of %d bytes", len(frame))
		}
	})
}

// FuzzSealOpenRoundtrip checks that any plaintext survives Seal then Open
// unchanged, so redaction/composer bytes are never corrupted in transit.
func FuzzSealOpenRoundtrip(f *testing.F) {
	f.Add([]byte("hello"))
	f.Add([]byte{})
	f.Add([]byte{0x00, 0xff, 0x1d})

	key := make([]byte, KeySize)
	c, _ := NewCipher(key)
	f.Fuzz(func(t *testing.T, plaintext []byte) {
		got, err := c.Open(c.Seal(plaintext))
		if err != nil {
			t.Fatalf("round-trip failed: %v", err)
		}
		if !bytes.Equal(got, plaintext) {
			t.Fatalf("round-trip changed bytes: got %x want %x", got, plaintext)
		}
	})
}

// FuzzDecodeKey feeds arbitrary strings to the key parser (the join-link
// fragment is user-controlled); it must error, not panic, on bad input.
func FuzzDecodeKey(f *testing.F) {
	f.Add(EncodeKey(make([]byte, KeySize)))
	f.Add("")
	f.Add("not base64!!")
	f.Add("YWJj")

	f.Fuzz(func(t *testing.T, s string) {
		key, err := DecodeKey(s)
		if err == nil && len(key) != KeySize {
			t.Fatalf("DecodeKey accepted a %d-byte key", len(key))
		}
	})
}
