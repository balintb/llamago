package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// helpText is the keymap as it renders at the given size.
func helpText(t *testing.T, w, h int) string {
	t.Helper()
	a := newTestApp(w, h)
	a.overlay = overlayHelp
	return ansi.Strip(render(a))
}

// Every binding must be reachable. It cannot all fit on one screen any more, so the invariant is that scrolling reaches all of it - which is what catches a section packed off the end and dropped in silence.
func TestHelpShowsEveryBinding(t *testing.T) {
	a := newTestApp(120, 44)
	a.overlay = overlayHelp

	var seen strings.Builder
	for off := 0; ; off++ {
		a.helpScroll = off
		seen.WriteString(ansi.Strip(render(a)))
		if off >= a.helpMaxScroll() {
			break
		}
	}
	view := seen.String()

	for _, s := range helpSections {
		if !strings.Contains(view, strings.ToUpper(s.title)) {
			t.Errorf("section %q is unreachable in the help", s.title)
		}
		for _, k := range s.keys {
			if !strings.Contains(view, k[1]) {
				t.Errorf("binding %q (%s) is unreachable in the help", k[1], k[0])
			}
		}
	}
}

// Descriptions have to fit the column they are rendered into, or they are cut off mid-word instead of wrapping.
func TestHelpDescriptionsFitTheColumn(t *testing.T) {
	a := newTestApp(100, 32)
	limit := modalInner(a.helpWidth())/2 - 12 // the key column is padded to 12
	for _, s := range helpSections {
		for _, k := range s.keys {
			if n := len([]rune(k[1])); n > limit {
				t.Errorf("%q is %d cells, column fits %d", k[1], n, limit)
			}
		}
	}
}

// Where it cannot all fit, the rest must stay reachable rather than vanish.
func TestHelpScrollsWhenItCannotFit(t *testing.T) {
	short := helpText(t, 100, 32)
	if !strings.Contains(short, "scroll") {
		t.Fatal("a clipped keymap should say it scrolls")
	}
	if strings.Contains(short, "reset to defaults") {
		t.Fatal("setup: expected the last section to start off screen")
	}

	a := newTestApp(100, 32)
	a.overlay = overlayHelp
	for range a.helpMaxScroll() {
		a.onKey(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if a.overlay != overlayHelp {
		t.Fatal("scrolling closed the help")
	}
	if got := ansi.Strip(render(a)); !strings.Contains(got, "reset to defaults") {
		t.Error("scrolling to the end did not reveal the last binding")
	}

	// Anything that is not a movement key still closes on one press.
	a.onKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if a.overlay != overlayNone || a.helpScroll != 0 {
		t.Fatalf("overlay = %v, scroll = %d, want closed and reset", a.overlay, a.helpScroll)
	}
}

// With the whole keymap on screen the footer promises that any key closes, so the arrows must not be quietly claimed for scrolling.
func TestHelpClosesOnArrowsWhenNothingScrolls(t *testing.T) {
	const tall = 70 // tall enough that the whole keymap fits at once
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyDown},
		{Code: tea.KeyUp},
		{Code: 'j', Text: "j"},
		{Code: 'G', Text: "G", Mod: tea.ModShift},
		{Code: tea.KeyPgDown},
	} {
		a := newTestApp(120, tall)
		a.overlay = overlayHelp
		if a.helpMaxScroll() != 0 {
			t.Fatalf("setup: the keymap should fit at 120x%d, scrolls by %d", tall, a.helpMaxScroll())
		}
		a.onKey(key)
		if a.overlay != overlayNone {
			t.Errorf("%s left the help open even though nothing scrolls", key.String())
		}
	}
}

// While it does scroll, those same keys must move rather than close.
func TestHelpArrowsScrollWhenClipped(t *testing.T) {
	a := newTestApp(100, 32)
	a.overlay = overlayHelp
	if a.helpMaxScroll() == 0 {
		t.Fatal("setup: the keymap should be clipped at 100x32")
	}
	a.onKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if a.overlay != overlayHelp || a.helpScroll != 1 {
		t.Fatalf("overlay = %v, scroll = %d, want it open and moved", a.overlay, a.helpScroll)
	}
}
