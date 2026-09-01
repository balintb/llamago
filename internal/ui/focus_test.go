package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A pane drawn as focused must have a cursor in it. The two are separate pieces of state - where the keyboard is, and whether the widget believes it - and they drifted apart whenever the tab strip took the keyboard.
func TestComposerFocusAndCursorAgree(t *testing.T) {
	routes := []struct {
		name  string
		leave func(a *App)
	}{
		{"alt+4", func(a *App) { a.onKey(tea.KeyPressMsg{Code: '4', Mod: tea.ModAlt}) }},
		{"ctrl+o", func(a *App) {
			for range 3 {
				a.onKey(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
			}
		}},
		{"tab strip", func(a *App) {
			a.onKey(tea.KeyPressMsg{Code: tea.KeyEscape}) // keyboard to the tab strip
			for range 3 {
				a.onKey(tea.KeyPressMsg{Code: tea.KeyRight})
			}
			a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		}},
		{"palette", func(a *App) {
			a.openPalette()
			a.paletteIn.SetValue("Settings")
			a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		}},
	}
	for _, r := range routes {
		t.Run(r.name, func(t *testing.T) {
			a := chatWithPrompts(t, "hi")
			r.leave(a)
			if a.tab != tabSettings {
				t.Fatalf("setup: landed on tab %v, want settings", a.tab)
			}
			a.onKey(tea.KeyPressMsg{Code: tea.KeyEscape}) // back to the chat

			if a.tab != tabChat || a.focus != focusInput {
				t.Fatalf("tab = %v, focus = %v, want the composer in the chat", a.tab, a.focus)
			}
			if !a.input.Focused() {
				t.Error("the composer is drawn as focused but the widget is blurred")
			}
			if a.View().Cursor == nil {
				t.Error("the composer has no cursor, so it cannot be typed into")
			}
		})
	}
}

// Typing has to land in the composer after every one of those routes, which is what the missing focus actually cost.
func TestComposerAcceptsTypingAfterTheTabStrip(t *testing.T) {
	a := chatWithPrompts(t, "hi")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	for range 3 {
		a.onKey(tea.KeyPressMsg{Code: tea.KeyRight})
	}
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})  // into settings
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEscape}) // back to the chat

	a.onKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if got := a.input.Value(); got != "x" {
		t.Fatalf("composer = %q, want the keystroke to have landed in it", got)
	}
}
