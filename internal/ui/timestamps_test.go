package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestTimestampsAreOffUntilAskedFor(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.cur.Turns[0].At = time.Date(2026, 8, 13, 14, 46, 0, 0, time.Local)

	if got := ansi.Strip(a.renderConversation()); strings.Contains(got, "14:46") {
		t.Fatal("timestamps showed while the setting is off")
	}
	a.cfg.Timestamps = true
	if got := ansi.Strip(a.renderConversation()); !strings.Contains(got, "14:46") {
		t.Fatalf("no timestamp in:\n%s", got)
	}
}

// A session reopened later must not claim its turns happened today, so anything but today carries its date.
func TestTimestampsCarryTheDateOnceTheyAreOld(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.cfg.Timestamps = true

	a.cur.Turns[0].At = time.Now()
	if got := ansi.Strip(a.renderConversation()); strings.Contains(got, time.Now().Format("Jan 2")) {
		t.Error("today's turn should show clock time alone")
	}
	old := time.Now().AddDate(0, 0, -8)
	a.cur.Turns[0].At = old
	if got := ansi.Strip(a.renderConversation()); !strings.Contains(got, old.Format("Jan 2 15:04")) {
		t.Errorf("an eight-day-old turn lost its date:\n%s", got)
	}
}

// A turn with no time recorded renders nothing rather than the zero date.
func TestTimestampSkipsUnstampedTurns(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.cfg.Timestamps = true
	a.cur.Turns[0].At = time.Time{}
	if got := ansi.Strip(a.renderConversation()); strings.Contains(got, "Jan 1") {
		t.Fatalf("zero time rendered:\n%s", got)
	}
}
