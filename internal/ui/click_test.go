package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/store"
)

func clickAt(a *App, x, y int) {
	a.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y})
}

// Which pane has the keyboard is otherwise only reachable by tab, and a click that changes nothing reads as an unresponsive window.
func TestClickingAPaneFocusesIt(t *testing.T) {
	a := chatWithPrompts(t, "first", "second")
	a.sessions = []*store.Session{a.cur}
	a.layout()

	mid := func(r rect) (int, int) { return (r.x0 + r.x1) / 2, (r.y0 + r.y1) / 2 }

	x, y := mid(a.transcriptRect())
	clickAt(a, x, y)
	if a.focus != focusTranscript {
		t.Fatalf("focus = %v after clicking the transcript", a.focus)
	}

	x, y = mid(a.composerRect())
	clickAt(a, x, y)
	if a.focus != focusInput {
		t.Fatalf("focus = %v after clicking the composer", a.focus)
	}

	x, y = mid(a.sidebarRect())
	clickAt(a, x, y)
	if a.focus != focusSessions {
		t.Fatalf("focus = %v after clicking the sidebar", a.focus)
	}
}

// The panes must not overlap, or a click lands in whichever happens to be checked first.
func TestPaneRectsDoNotOverlap(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.layout()

	rects := map[string]rect{
		"sidebar":    a.sidebarRect(),
		"transcript": a.transcriptRect(),
		"composer":   a.composerRect(),
	}
	for aName, ra := range rects {
		for bName, rb := range rects {
			if aName >= bName {
				continue
			}
			for x := ra.x0; x < ra.x1; x++ {
				for y := ra.y0; y < ra.y1; y++ {
					if rb.contains(x, y) {
						t.Fatalf("%s and %s both claim (%d,%d)", aName, bName, x, y)
					}
				}
			}
		}
	}
}

// Clicking a list is clicking a row, so the highlight follows the pointer.
func TestClickingASessionRowSelectsIt(t *testing.T) {
	a := chatWithPrompts(t, "first")
	older := a.freshSession(a.cur.Created)
	older.Title, older.Turns = "older", a.cur.Turns
	a.sessions = []*store.Session{a.cur, older}
	a.layout()

	r := a.sidebarRect()
	// Rows start below the border, the heading and a blank line: New chat, then one row per session.
	newChatY := r.y0 + 1 + 2
	clickAt(a, 2, newChatY)
	if a.sessionIdx != 0 {
		t.Fatalf("row = %d, want the New chat row", a.sessionIdx)
	}
	clickAt(a, 2, newChatY+2)
	if a.sessionIdx != 2 {
		t.Fatalf("row = %d, want the second session", a.sessionIdx)
	}
	if got := a.sessionAt(a.sessionIdx); got != older {
		t.Fatalf("selected %v, want the row that was clicked", got)
	}

	// A click below the last row leaves the selection alone rather than jumping to something that is not there.
	was := a.sessionIdx
	clickAt(a, 2, r.y1-1)
	if a.sessionIdx != was {
		t.Fatalf("row = %d, want it unchanged when clicking empty space", a.sessionIdx)
	}
}

// Click handling started out as image click-to-save, and that still works.
func TestClickingAnImageStillOpensTheSavePicker(t *testing.T) {
	a := appWithImage(t, 100, 32)
	a.setFocus(focusInput)
	r := a.transcriptRect()

	var found bool
	for _, p := range a.placements {
		clickAt(a, r.x0+1+p.col0, r.y0+1+p.line0-a.transcript.YOffset())
		found = true
		break
	}
	if !found {
		t.Skip("no image placement to click")
	}
	if a.overlay != overlayPicker {
		t.Fatalf("overlay = %v, want the save picker", a.overlay)
	}
	if a.focus != focusTranscript {
		t.Error("clicking the transcript did not focus it")
	}
}

// The tab strip is a row of buttons; it was reachable only through esc and the arrows, which nobody discovers by looking at it.
func TestClickingATabSwitchesToIt(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.layout()

	spans := a.tabSpans()
	if len(spans) != len(tabNames) {
		t.Fatalf("%d spans for %d tabs", len(spans), len(tabNames))
	}
	for i, name := range tabNames {
		a.tab = tabChat
		mid := (spans[i].x0 + spans[i].x1) / 2
		clickAt(a, mid, 0)
		if a.tab != tab(i) {
			t.Errorf("clicking %q landed on tab %v", name, a.tab)
		}
	}
}

// The spans have to line up with what is drawn, or clicks land on the tab next to the one under the pointer.
func TestTabSpansMatchTheRenderedStrip(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.layout()
	line := ansi.Strip(strings.Split(render(a), "\n")[0])

	for i, span := range a.tabSpans() {
		if span.x1 > len([]rune(line)) {
			t.Fatalf("span for %q runs past the header", tabNames[i])
		}
		got := string([]rune(line)[span.x0:span.x1])
		if !strings.Contains(got, tabNames[i]) {
			t.Errorf("span for %q covers %q instead", tabNames[i], got)
		}
	}
}

// Clicking the tab you are already on hands the keyboard back to its content, rather than leaving it on the strip where the arrows still move tabs.
func TestClickingTheCurrentTabReturnsTheKeyboard(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.layout()
	a.focusTabBar()
	if !a.tabBarFocus {
		t.Fatal("setup: expected the keyboard on the tab strip")
	}

	span := a.tabSpans()[int(tabChat)]
	clickAt(a, (span.x0+span.x1)/2, 0)
	if a.tabBarFocus {
		t.Fatal("the keyboard stayed on the strip")
	}
	if a.tab != tabChat {
		t.Fatalf("tab = %v, want it unchanged", a.tab)
	}
}

// A click on the rule under the strip is not a click on a tab.
func TestClickingBelowTheStripDoesNothing(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.layout()
	span := a.tabSpans()[int(tabModels)]

	clickAt(a, (span.x0+span.x1)/2, 1)
	if a.tab != tabChat {
		t.Fatalf("tab = %v, want the click on the rule ignored", a.tab)
	}
}
