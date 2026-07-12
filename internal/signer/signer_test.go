package signer

import "testing"

func TestSignVerify(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	v := NewVerifier(s.Public())

	st := s.Sign("run the plan")
	if err := v.Verify(st); err != nil {
		t.Fatal(err)
	}

	// Replay of the same submission is refused.
	if err := v.Verify(st); err != ErrReplay {
		t.Fatalf("replay: got %v", err)
	}

	// Tampered text fails the signature.
	st2 := s.Sign("innocent")
	st2.Text = "rm -rf /"
	if err := v.Verify(st2); err != ErrBadSignature {
		t.Fatalf("tamper: got %v", err)
	}

	// A different key (fake relay minting input) fails.
	other, _ := New()
	forged := other.Sign("forged input")
	if err := v.Verify(forged); err != ErrBadSignature {
		t.Fatalf("forgery: got %v", err)
	}

	// Sequence must strictly advance even after failures.
	st3 := s.Sign("next")
	if err := v.Verify(st3); err != nil {
		t.Fatal(err)
	}
}
