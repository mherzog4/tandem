package e2e

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	key := NewKey()
	c, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("terminal frame \x1b[2J with escapes")
	frame := c.Seal(msg)
	if bytes.Contains(frame, []byte("terminal frame")) {
		t.Fatal("plaintext visible in sealed frame")
	}
	got, err := c.Open(frame)
	if err != nil || !bytes.Equal(got, msg) {
		t.Fatalf("open: got %q err=%v", got, err)
	}
}

func TestTamperRejected(t *testing.T) {
	c, _ := NewCipher(NewKey())
	frame := c.Seal([]byte("payload"))
	frame[len(frame)-1] ^= 0x01
	if _, err := c.Open(frame); err == nil {
		t.Fatal("tampered frame accepted")
	}
	// Wrong key also rejected.
	c2, _ := NewCipher(NewKey())
	if _, err := c2.Open(c.Seal([]byte("payload"))); err == nil {
		t.Fatal("frame opened with wrong key")
	}
	// Truncated frame rejected, no panic.
	if _, err := c.Open([]byte{1, 2, 3}); err == nil {
		t.Fatal("truncated frame accepted")
	}
}

func TestKeyEncoding(t *testing.T) {
	key := NewKey()
	got, err := DecodeKey(EncodeKey(key))
	if err != nil || !bytes.Equal(got, key) {
		t.Fatalf("round trip failed: %v", err)
	}
	if _, err := DecodeKey("too-short"); err == nil {
		t.Fatal("short key accepted")
	}
}
