package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The opening screen must always carry the name. Without the block letters it used to carry none at all, which leaves a list of keys and no clue what they belong to.
func TestOpeningScreenAlwaysNamesItself(t *testing.T) {
	for _, sz := range [][2]int{
		{120, 40}, // room for the block letters
		{100, 30},
		{60, 24},  // too narrow for them
		{100, 14}, // too short for them
		{40, 10},  // too small for anything much
	} {
		a := newTestApp(sz[0], sz[1])
		a.cur.Turns = nil
		a.sidebar = false
		a.layout()

		got := ansi.Strip(a.renderEmptyState())
		flat := strings.ReplaceAll(got, " ", "")
		if !strings.Contains(flat, "llamago") && !strings.Contains(got, "██") {
			t.Errorf("%dx%d: the opening screen does not name the app:\n%s", sz[0], sz[1], got)
		}
	}
}

// The block letters are 62 cells wide, so below that they have to give way rather than being clipped into nonsense.
func TestBlockLettersGiveWayWhenTheyDoNotFit(t *testing.T) {
	wide := newTestApp(120, 40)
	wide.cur.Turns = nil
	wide.sidebar = false
	wide.layout()
	if got := ansi.Strip(wide.renderEmptyState()); !strings.Contains(got, "██") {
		t.Fatalf("the block letters are missing from a window with room:\n%s", got)
	}

	for _, sz := range [][2]int{{60, 24}, {100, 14}} {
		a := newTestApp(sz[0], sz[1])
		a.cur.Turns = nil
		a.sidebar = false
		a.layout()

		got := ansi.Strip(a.renderEmptyState())
		if strings.Contains(got, "██") {
			t.Errorf("%dx%d: the block letters were drawn without room for them", sz[0], sz[1])
		}
		if !strings.Contains(got, "l l a m a g o") {
			t.Errorf("%dx%d: no wordmark in their place:\n%s", sz[0], sz[1], got)
		}
	}
}

// Whatever it draws, it must not overflow the pane.
func TestOpeningScreenFitsItsPane(t *testing.T) {
	for _, sz := range [][2]int{{120, 40}, {60, 24}, {40, 10}, {30, 8}} {
		a := newTestApp(sz[0], sz[1])
		a.cur.Turns = nil
		a.sidebar = false
		a.layout()

		for line := range strings.SplitSeq(a.renderEmptyState(), "\n") {
			if w := ansi.StringWidth(line); w > a.transcript.Width() {
				t.Errorf("%dx%d: a line is %d cells, pane is %d", sz[0], sz[1], w, a.transcript.Width())
				break
			}
		}
	}
}
