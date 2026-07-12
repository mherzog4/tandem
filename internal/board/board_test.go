package board

import "testing"

func TestAddEditDelete(t *testing.T) {
	b := New()
	c, ok := b.Add(TypeEvent, "ClaimDenied", "marcus")
	if !ok || c.ID == "" || c.State != StateProposed {
		t.Fatalf("add: %+v ok=%v", c, ok)
	}
	if _, ok := b.Add("sticky-note", "nope", "m"); ok {
		t.Fatal("invalid type accepted")
	}
	if _, ok := b.Add(TypeEvent, "", "m"); ok {
		t.Fatal("empty text accepted")
	}

	if !b.Edit(c.ID, "ClaimDenied (by adjuster)", "priya") {
		t.Fatal("edit failed")
	}
	got := b.Cards()[0]
	if got.Text != "ClaimDenied (by adjuster)" || got.Author != "priya" {
		t.Fatalf("edit result: %+v", got)
	}

	if !b.Delete(c.ID) || len(b.Cards()) != 0 {
		t.Fatal("delete failed")
	}
	if b.Delete("missing") {
		t.Fatal("deleting missing card succeeded")
	}
}

func TestEditDemotesConfirmed(t *testing.T) {
	b := New()
	c, _ := b.Add(TypeTerm, "Reopen Window", "marcus")
	b.mu.Lock()
	b.cards[0].State = StateConfirmed
	b.mu.Unlock()
	b.Edit(c.ID, "Reopen Window (90 days)", "marcus")
	if b.Cards()[0].State != StateProposed {
		t.Fatal("edit must demote confirmed cards to proposed")
	}
}

func TestMoveWithinType(t *testing.T) {
	b := New()
	e1, _ := b.Add(TypeEvent, "ClaimFiled", "m")
	b.Add(TypeCmd, "FileClaim", "m") // interleaved other type
	e2, _ := b.Add(TypeEvent, "ClaimDenied", "m")
	e3, _ := b.Add(TypeEvent, "ClaimReopened", "m")

	// Move ClaimReopened before ClaimDenied: event order e1, e3, e2.
	if !b.Move(e3.ID, 1) {
		t.Fatal("move failed")
	}
	var events []string
	for _, c := range b.Cards() {
		if c.Type == TypeEvent {
			events = append(events, c.Text)
		}
	}
	want := []string{"ClaimFiled", "ClaimReopened", "ClaimDenied"}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("event order = %v, want %v", events, want)
		}
	}
	// Command untouched.
	if len(b.Cards()) != 4 {
		t.Fatal("card lost during move")
	}
	_ = e1
	_ = e2
}
