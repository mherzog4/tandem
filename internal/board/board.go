// Package board is the authoritative Domain Board state (FR12/FR16):
// the EventStorming-grammar cards both parties build during a session.
// Four card types only — richer notation becomes an engineer-only tool
// and defeats the purpose (PRD §8.2).
//
// Like the composer, state is host-authoritative: guests propose
// mutations through the broker allowlist and everyone converges on the
// broadcast state. Slice order is meaningful for events: it is the
// process timeline (FR16) and serializes as the ordered event list.
package board

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
)

// The four EventStorming card types.
const (
	TypeEvent = "event"   // Domain Events (ClaimDenied)
	TypeCmd   = "command" // Commands (DenyClaim)
	TypeActor = "actor"   // Actors/Roles (Adjuster)
	TypeTerm  = "term"    // Terms/Rules (Reopen Window)
)

func ValidType(t string) bool {
	return t == TypeEvent || t == TypeCmd || t == TypeActor || t == TypeTerm
}

// Card states: proposed until the host confirms (FR13, issue #18).
const (
	StateProposed  = "proposed"
	StateConfirmed = "confirmed"
)

type Card struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Text   string `json:"text"`
	Author string `json:"author"`
	State  string `json:"state"`
	// CodeName is the engineer-facing alias (PRD risk 5); the business
	// wording in Text always wins for display. Populated in issue #18.
	CodeName string `json:"codeName,omitempty"`
}

type Board struct {
	mu    sync.Mutex
	cards []Card
}

func New() *Board { return &Board{} }

func newID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Add appends a proposed card and returns it with its assigned ID.
func (b *Board) Add(cardType, text, author string) (Card, bool) {
	if !ValidType(cardType) || text == "" || author == "" {
		return Card{}, false
	}
	c := Card{ID: newID(), Type: cardType, Text: text, Author: author, State: StateProposed}
	b.mu.Lock()
	b.cards = append(b.cards, c)
	b.mu.Unlock()
	return c, true
}

// Edit rewrites a card's text. Editing a confirmed card demotes it to
// proposed — wording changes need re-confirmation (FR13).
func (b *Board) Edit(id, text, author string) bool {
	if text == "" || author == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.cards {
		if b.cards[i].ID == id {
			b.cards[i].Text = text
			b.cards[i].Author = author
			b.cards[i].State = StateProposed
			return true
		}
	}
	return false
}

// Move repositions a card within the global order (drag ordering of
// events expresses process flow, FR16). toIndex counts positions among
// cards of the SAME type; other types are unaffected.
func (b *Board) Move(id string, toIndex int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	from := -1
	for i := range b.cards {
		if b.cards[i].ID == id {
			from = i
			break
		}
	}
	if from < 0 || toIndex < 0 {
		return false
	}
	card := b.cards[from]
	rest := append(append([]Card{}, b.cards[:from]...), b.cards[from+1:]...)

	// Find the global position of the toIndex-th same-type card.
	insert := len(rest)
	seen := 0
	for i := range rest {
		if rest[i].Type == card.Type {
			if seen == toIndex {
				insert = i
				break
			}
			seen++
		}
	}
	b.cards = append(append(append([]Card{}, rest[:insert]...), card), rest[insert:]...)
	return true
}

// Delete removes a card.
func (b *Board) Delete(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.cards {
		if b.cards[i].ID == id {
			b.cards = append(b.cards[:i], b.cards[i+1:]...)
			return true
		}
	}
	return false
}

// Confirm marks a card authoritative (host-only; the broker gates this
// behind the host capability token, FR13).
func (b *Board) Confirm(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.cards {
		if b.cards[i].ID == id {
			b.cards[i].State = StateConfirmed
			return true
		}
	}
	return false
}

// SetAlias records the engineer-facing code name (PRD risk 5). The
// business wording in Text is untouched — the stakeholder's language
// always wins for display.
func (b *Board) SetAlias(id, codeName string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.cards {
		if b.cards[i].ID == id {
			b.cards[i].CodeName = codeName
			return true
		}
	}
	return false
}

// Load replaces the board contents (session preload, FR20).
func (b *Board) Load(cards []Card) {
	b.mu.Lock()
	b.cards = append([]Card{}, cards...)
	b.mu.Unlock()
}

// Cards returns the board in order (a copy).
func (b *Board) Cards() []Card {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Card, len(b.cards))
	copy(out, b.cards)
	return out
}
