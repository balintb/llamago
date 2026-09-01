package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/store"
)

// Every modal that takes typing has to place a cursor: the inputs draw none of their own, so a missing case here means typing blind.
func TestEveryTextOverlayShowsACursor(t *testing.T) {
	cases := []struct {
		name string
		open func(a *App)
	}{
		{"palette", func(a *App) { a.openPalette() }},
		{"rename", func(a *App) {
			a.sessions = []*store.Session{a.cur}
			focusSidebar(a, 1)
			a.onKey(tea.KeyPressMsg{Code: 'r', Text: "r"})
		}},
		{"nudge", func(a *App) { a.onKey(tea.KeyPressMsg{Code: 'e', Mod: tea.ModAlt}) }},
		{"system prompt", func(a *App) { a.openEditor(editSystem) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := chatWithPrompts(t, "first")
			tc.open(a)
			if a.overlay == overlayNone {
				t.Fatalf("setup: %s did not open", tc.name)
			}
			v := a.View()
			if v.Cursor == nil {
				t.Fatalf("%s has no cursor, so it is typed blind", tc.name)
			}
			if v.Cursor.X <= 0 || v.Cursor.Y <= 0 {
				t.Fatalf("cursor at (%d,%d), want it inside the modal", v.Cursor.X, v.Cursor.Y)
			}
		})
	}
}

// The cursor has to sit on the line the text is actually drawn on, which is what catches a wrong row offset.
func TestRenameCursorSitsOnTheTitleItIsEditing(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.cur.Title = "Findable"
	a.sessions = []*store.Session{a.cur}
	focusSidebar(a, 1)
	a.onKey(tea.KeyPressMsg{Code: 'r', Text: "r"})

	v := a.View()
	lines := strings.Split(ansi.Strip(v.Content), "\n")
	if v.Cursor.Y >= len(lines) {
		t.Fatalf("cursor row %d is off the frame", v.Cursor.Y)
	}
	if got := lines[v.Cursor.Y]; !strings.Contains(got, "Findable") {
		t.Fatalf("cursor is on %q, want the row holding the title", strings.TrimSpace(got))
	}
}

func TestNudgeCursorSitsOnItsInput(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.onKey(tea.KeyPressMsg{Code: 'e', Mod: tea.ModAlt})
	a.nudgeIn.SetValue("shorter")

	v := a.View()
	lines := strings.Split(ansi.Strip(v.Content), "\n")
	if got := lines[v.Cursor.Y]; !strings.Contains(got, "shorter") {
		t.Fatalf("cursor is on %q, want the row holding the instruction", strings.TrimSpace(got))
	}
}
