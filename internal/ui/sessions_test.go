package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/store"
)

// focusSidebar puts the keyboard on the session list with row idx highlighted.
func focusSidebar(a *App, idx int) {
	a.setFocus(focusSessions)
	a.sessionIdx = idx
}

func TestRenameSessionFromTheSidebar(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.sessions = []*store.Session{a.cur}
	focusSidebar(a, 1)

	a.onKey(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if a.overlay != overlayRename {
		t.Fatalf("overlay = %v, want the rename editor", a.overlay)
	}
	if got := a.renameIn.Value(); got != a.cur.Title {
		t.Fatalf("editor seeded with %q, want the current title %q", got, a.cur.Title)
	}
	if got := ansi.Strip(render(a)); !strings.Contains(got, "Rename session") {
		t.Fatal("the rename modal is not on screen")
	}

	a.renameIn.SetValue("  Borrow checker notes  ")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if a.overlay != overlayNone {
		t.Fatal("the modal stayed open after saving")
	}
	if got := a.cur.Title; got != "Borrow checker notes" {
		t.Fatalf("title = %q, want it trimmed and applied", got)
	}
}

// An empty title would leave a blank row in the sidebar, so it is refused and the old one kept.
func TestRenameRefusesAnEmptyTitle(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.sessions = []*store.Session{a.cur}
	was := a.cur.Title
	focusSidebar(a, 1)

	a.onKey(tea.KeyPressMsg{Code: 'r', Text: "r"})
	a.renameIn.SetValue("   ")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if a.cur.Title != was {
		t.Fatalf("title = %q, want it unchanged at %q", a.cur.Title, was)
	}
}

// esc leaves the title alone.
func TestRenameCancels(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.sessions = []*store.Session{a.cur}
	was := a.cur.Title
	focusSidebar(a, 1)

	a.onKey(tea.KeyPressMsg{Code: 'r', Text: "r"})
	a.renameIn.SetValue("discarded")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEscape})

	if a.overlay != overlayNone || a.cur.Title != was {
		t.Fatalf("overlay = %v, title = %q, want cancelled", a.overlay, a.cur.Title)
	}
}

// The rename targets the session it was opened for, by id, even if the selection moves underneath it.
func TestRenameStaysOnItsOwnSession(t *testing.T) {
	a := chatWithPrompts(t, "first")
	other := a.freshSession(a.cur.Created.Add(-time.Hour))
	other.Title, other.Turns = "another chat", a.cur.Turns
	a.sessions = []*store.Session{a.cur, other}
	focusSidebar(a, 2) // the other session

	a.onKey(tea.KeyPressMsg{Code: 'r', Text: "r"})
	a.sessionIdx = 1 // selection moves while the modal is open
	a.renameIn.SetValue("renamed")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if other.Title != "renamed" {
		t.Errorf("other session title = %q, want renamed", other.Title)
	}
	if a.cur.Title == "renamed" {
		t.Error("the rename landed on the wrong session")
	}
}

func TestPinFloatsASessionToTheTop(t *testing.T) {
	a := chatWithPrompts(t, "first")
	older := a.freshSession(a.cur.Created.Add(-time.Hour))
	older.Title, older.Turns, older.Updated = "older chat", a.cur.Turns, a.cur.Updated.Add(-time.Hour)
	a.cur.Updated = time.Now()
	a.sessions = []*store.Session{a.cur, older}
	store.Sort(a.sessions)
	if a.sessions[0] != a.cur {
		t.Fatal("setup: the newest session should sort first")
	}

	focusSidebar(a, 2) // the older one
	a.onKey(tea.KeyPressMsg{Code: 'p', Text: "p"})

	if !older.Pinned {
		t.Fatal("the session was not pinned")
	}
	if a.sessions[0] != older {
		t.Fatalf("pinned session is at index %d, want the top", indexOf(a.sessions, older))
	}
	// The highlight follows the session, not the row it used to be in.
	if got := a.sessionAt(a.sessionIdx); got != older {
		t.Fatalf("selection landed on %q, want it to follow the pinned session", got.Title)
	}
	if got := ansi.Strip(render(a)); !strings.Contains(got, "★") {
		t.Error("pinned sessions are not marked in the sidebar")
	}

	// And unpinning drops it back by recency.
	a.onKey(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if older.Pinned || a.sessions[0] != a.cur {
		t.Fatal("unpinning did not restore the order")
	}
}

// A new turn re-sorts the list, and must not float an unpinned session above a pinned one.
func TestPinnedSessionsOutrankRecency(t *testing.T) {
	a := chatWithPrompts(t, "first")
	pinned := a.freshSession(a.cur.Created.Add(-time.Hour))
	pinned.Title, pinned.Turns, pinned.Pinned = "kept", a.cur.Turns, true
	pinned.Updated = time.Now().Add(-24 * time.Hour)
	a.sessions = []*store.Session{pinned}

	a.cur.Updated = time.Now()
	a.rememberSession(a.cur)

	if a.sessions[0] != pinned {
		t.Fatalf("order = %v, want the pinned session first", titlesOf(a.sessions))
	}
}

func indexOf(list []*store.Session, want *store.Session) int {
	for i, s := range list {
		if s == want {
			return i
		}
	}
	return -1
}

func titlesOf(list []*store.Session) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = s.Title
	}
	return out
}

func TestDuplicateCopiesTheWholeSessionAndOpensIt(t *testing.T) {
	a := chatWithPrompts(t, "first", "second")
	a.cur.Title = "Original"
	a.sessions = []*store.Session{a.cur}
	src := a.cur
	focusSidebar(a, 1)

	a.onKey(tea.KeyPressMsg{Code: 'c', Text: "c"})

	if a.cur == src {
		t.Fatal("duplicate did not switch to the copy")
	}
	if got := a.cur.Title; got != "Original (copy)" {
		t.Fatalf("title = %q, want it marked as a copy", got)
	}
	if len(a.cur.Turns) != len(src.Turns) {
		t.Fatalf("copy has %d turns, want all %d", len(a.cur.Turns), len(src.Turns))
	}
	if a.cur.ID == src.ID {
		t.Fatal("the copy shares the original's id, so it shares its file")
	}
	// Appending to the copy must not reach into the original.
	a.cur.Turns = append(a.cur.Turns, store.Turn{Role: "user", Content: "tangent"})
	if src.Turns[3].Content == "tangent" {
		t.Fatal("the copy shares the original's backing array")
	}
	if len(src.Turns) != 4 {
		t.Fatalf("original now has %d turns, want it untouched", len(src.Turns))
	}
}

// An empty chat has nothing to copy, and Save skips it, so the copy would be a row that vanishes on restart.
func TestDuplicateRefusesAnEmptySession(t *testing.T) {
	a := chatWithPrompts(t)
	a.sessions = []*store.Session{a.cur}
	was := a.cur
	focusSidebar(a, 1)

	a.onKey(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if a.cur != was {
		t.Fatal("duplicated an empty session")
	}
}

// The session keys only work while the sidebar has the keyboard, so that is exactly when the status bar has to name them.
func TestSidebarHintsNameTheSessionKeys(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.sessions = []*store.Session{a.cur}

	if got := ansi.Strip(render(a)); strings.Contains(got, "p pin") {
		t.Error("session keys advertised while the composer has focus")
	}

	focusSidebar(a, 1)
	got := ansi.Strip(render(a))
	for _, want := range []string{"↵ open", "n new", "r rename", "p pin", "c duplicate", "d delete"} {
		if !strings.Contains(got, want) {
			t.Errorf("sidebar hints are missing %q", want)
		}
	}
}
