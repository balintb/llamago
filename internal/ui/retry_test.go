package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/store"
)

// failedChat is a conversation whose last reply never arrived.
func failedChat(t *testing.T) *App {
	t.Helper()
	a := chatWithPrompts(t, "first")
	a.cur.Turns[1].Content = ""
	a.cur.Turns[1].Err = "connection refused"
	a.invalidateRenders()
	a.refreshTranscript()
	return a
}

// A failure with no visible way forward leaves retyping as the only move anyone discovers.
func TestFailedTurnOffersTheWayOut(t *testing.T) {
	a := failedChat(t)
	got := ansi.Strip(a.renderConversation())
	if !strings.Contains(got, "connection refused") {
		t.Fatal("the error is not shown")
	}
	if !strings.Contains(got, "try again") {
		t.Fatalf("the failed turn offers no way forward:\n%s", got)
	}
}

// An older failure cannot be reached with ctrl+e, so it points at the keys that do reach it.
func TestOlderFailureNamesTheKeysThatReachIt(t *testing.T) {
	a := failedChat(t)
	a.cur.Turns = append(a.cur.Turns,
		store.Turn{Role: "user", Content: "second"},
		store.Turn{Role: "assistant", Content: "fine"})
	a.invalidateRenders()

	if got := ansi.Strip(a.renderConversation()); !strings.Contains(got, "shift+↑ then r") {
		t.Fatalf("an older failure does not name how to retry it:\n%s", got)
	}
}

// Retrying replaces the failed turn rather than stacking another one under it.
func TestRetryReplacesTheFailedTurn(t *testing.T) {
	a := failedChat(t)
	a.onKey(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})

	if n := len(a.cur.Turns); n != 2 {
		t.Fatalf("turns = %d, want the failed reply replaced in place", n)
	}
	if last := a.cur.Turns[1]; last.Err != "" {
		t.Fatalf("the retry kept the error %q", last.Err)
	}
}

// Rewind reaches a failure buried in the conversation.
func TestRewindRetriesAnOlderFailure(t *testing.T) {
	a := failedChat(t)
	a.cur.Turns = append(a.cur.Turns,
		store.Turn{Role: "user", Content: "second"},
		store.Turn{Role: "assistant", Content: "fine"})
	a.selTurn = 1 // the failed reply
	a.rewindTo()

	// Three messages would be dropped, so it asks first.
	if a.overlay != overlayConfirm {
		t.Fatalf("overlay = %v, want a confirmation before dropping the tail", a.overlay)
	}
	cmd := a.onKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("confirming produced no action")
	}
	a.Update(cmd()) // the runtime would deliver this

	if n := len(a.cur.Turns); n != 2 {
		t.Fatalf("turns = %d, want the conversation cut back to the retry", n)
	}
	if a.cur.Turns[1].Err != "" {
		t.Error("the retried turn kept its error")
	}
}
