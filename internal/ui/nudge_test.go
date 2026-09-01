package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/store"
)

func TestNudgeRidesAlongWithoutJoiningTheConversation(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.onKey(tea.KeyPressMsg{Code: 'e', Mod: tea.ModAlt})
	if a.overlay != overlayNudge {
		t.Fatalf("overlay = %v, want the nudge prompt", a.overlay)
	}
	if got := ansi.Strip(render(a)); !strings.Contains(got, "Regenerate with") {
		t.Fatal("the nudge modal is not on screen")
	}

	a.nudgeIn.SetValue("  shorter  ")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	// The stale reply is dropped and a fresh one opened; the nudge is spent on that request and never stored.
	if a.nudge != "" {
		t.Errorf("nudge = %q, want it cleared once used", a.nudge)
	}
	for _, turn := range a.cur.Turns {
		if strings.Contains(turn.Content, "shorter") {
			t.Error("the nudge was written into the conversation")
		}
	}
	if strings.Contains(a.cur.Markdown(), "shorter") {
		t.Error("the nudge leaked into the export")
	}
}

// The nudge reaches the model as a trailing system message, and only once.
func TestNudgeReachesTheRequestOnce(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.nudge = "in Go"

	msgs := a.requestMessages()
	if last := msgs[len(msgs)-1]; last.Role != "system" || last.Content != "in Go" {
		t.Fatalf("last message = %+v, want the nudge as a system message", last)
	}
	if a.nudge != "" {
		t.Error("the nudge survived the request it was spent on")
	}
	// The next request must not carry it again.
	again := a.requestMessages()
	if last := again[len(again)-1]; last.Role == "system" && last.Content == "in Go" {
		t.Error("the nudge rode along a second time")
	}
}

// An empty instruction is an ordinary regenerate, not an error.
func TestEmptyNudgeJustRegenerates(t *testing.T) {
	a := chatWithPrompts(t, "first")
	before := len(a.cur.Turns)
	a.onKey(tea.KeyPressMsg{Code: 'e', Mod: tea.ModAlt})
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if a.overlay != overlayNone {
		t.Fatal("the modal stayed open")
	}
	if len(a.cur.Turns) != before {
		t.Fatalf("turns = %d, want %d: the reply replaced in place", len(a.cur.Turns), before)
	}
	if a.toastErr {
		t.Errorf("an empty nudge reported an error: %q", a.toast)
	}
}

// With no reply to replace there is nothing to regenerate.
func TestNudgeRefusedWithoutAReply(t *testing.T) {
	a := chatWithPrompts(t)
	a.cur.Turns = []store.Turn{{Role: "user", Content: "unanswered"}}
	a.onKey(tea.KeyPressMsg{Code: 'e', Mod: tea.ModAlt})
	if a.overlay == overlayNudge {
		t.Fatal("offered to regenerate a prompt that has no reply")
	}
}
