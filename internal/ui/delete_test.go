package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// confirmWith answers the pending confirmation and delivers its command.
func confirmWith(t *testing.T, a *App) {
	t.Helper()
	if a.overlay != overlayConfirm {
		t.Fatalf("overlay = %v, want a confirmation", a.overlay)
	}
	cmd := a.onKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("confirming produced no action")
	}
	a.Update(cmd())
}

// A prompt and its reply go together: leaving the answer behind would make it appear to respond to the prompt before it.
func TestDeleteTakesTheWholeExchange(t *testing.T) {
	a := chatWithPrompts(t, "first", "second", "third")
	a.selTurn = 2 // the "second" prompt
	a.deleteSelected()
	confirmWith(t, a)

	if n := len(a.cur.Turns); n != 4 {
		t.Fatalf("turns = %d, want the prompt and its reply gone", n)
	}
	for _, turn := range a.cur.Turns {
		if turn.Content == "second" || turn.Content == "reply B" {
			t.Fatalf("%q survived the deletion", turn.Content)
		}
	}
	if a.cur.Turns[0].Content != "first" || a.cur.Turns[2].Content != "third" {
		t.Fatal("the surrounding exchanges were disturbed")
	}
}

// Selecting the reply deletes the same pair as selecting its prompt.
func TestDeleteFromTheReplyTakesItsPrompt(t *testing.T) {
	a := chatWithPrompts(t, "first", "second")
	a.selTurn = 3 // the reply to "second"
	a.deleteSelected()
	confirmWith(t, a)

	if n := len(a.cur.Turns); n != 2 {
		t.Fatalf("turns = %d, want both halves gone", n)
	}
}

// Declining leaves the conversation alone.
func TestDeleteCanBeDeclined(t *testing.T) {
	a := chatWithPrompts(t, "first")
	was := len(a.cur.Turns)
	a.selTurn = 0
	a.deleteSelected()
	a.onKey(tea.KeyPressMsg{Code: 'n', Text: "n"})

	if len(a.cur.Turns) != was {
		t.Fatalf("turns = %d, want %d after declining", len(a.cur.Turns), was)
	}
}

// The selection must not be left pointing past the end.
func TestDeleteLeavesTheSelectionInRange(t *testing.T) {
	a := chatWithPrompts(t, "first", "second")
	a.selTurn = 2
	a.deleteSelected()
	confirmWith(t, a)

	if a.selTurn >= len(a.cur.Turns) {
		t.Fatalf("selection = %d, out of range for %d turns", a.selTurn, len(a.cur.Turns))
	}
	if a.selectedTurn() == nil && a.selTurn >= 0 {
		t.Fatal("the selection resolves to nothing")
	}
}
