package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The accounting belongs to the selected message only; on every message it would be noise.
func TestTokenCostShowsOnlyOnTheSelection(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.cur.Turns[1].PromptCount = 19
	a.cur.Turns[1].EvalCount = 128
	a.refreshTranscript()

	if got := ansi.Strip(a.renderConversation()); strings.Contains(got, "19 prompt") {
		t.Fatal("cost showed with nothing selected")
	}
	shiftUp(a) // the reply
	got := ansi.Strip(a.renderConversation())
	if !strings.Contains(got, "19 prompt · 128 response tokens") {
		t.Fatalf("reply cost missing:\n%s", got)
	}
}

// A prompt has no server-side count of its own, so it is estimated and marked.
func TestPromptCostIsMarkedAsAnEstimate(t *testing.T) {
	a := chatWithPrompts(t, strings.Repeat("word ", 20))
	shiftUp(a)
	shiftUp(a) // the prompt
	got := ansi.Strip(a.renderConversation())
	if !strings.Contains(got, "~25 tokens") {
		t.Fatalf("prompt estimate missing or wrong:\n%s", got)
	}
}

// A turn the server never reported on has nothing to say.
func TestNoCostLineWithoutCounts(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.cur.Turns[1].PromptCount, a.cur.Turns[1].EvalCount = 0, 0
	shiftUp(a)
	if got := ansi.Strip(a.renderConversation()); strings.Contains(got, "tokens") {
		t.Fatalf("cost line rendered without counts:\n%s", got)
	}
}
