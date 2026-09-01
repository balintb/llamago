package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/store"
)

// searchApp seeds one open chat and one saved chat, each mentioning a different distinctive word.
func searchApp(t *testing.T) (*App, *store.Session) {
	t.Helper()
	a := chatWithPrompts(t, "tell me about goroutines")
	a.cur.Title = "open chat"

	old := a.freshSession(time.Now().Add(-time.Hour))
	old.Title = "archived chat"
	old.Turns = []store.Turn{
		{Role: "user", Content: "explain the borrow checker in Rust"},
		{Role: "assistant", Model: "llama3.2:3b", Content: "The borrow checker enforces ownership."},
	}
	a.sessions = []*store.Session{old}
	return a, old
}

func TestFindReachesArchivedConversations(t *testing.T) {
	a, old := searchApp(t)
	typeInto(a, "/find borrow checker")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if a.overlay != overlayFind {
		t.Fatalf("overlay = %v, want the results list", a.overlay)
	}
	if a.findTotal != 2 {
		t.Fatalf("found %d matches, want both turns of the archived chat", a.findTotal)
	}
	got := ansi.Strip(render(a))
	if !strings.Contains(got, "archived chat") || !strings.Contains(got, "borrow checker") {
		t.Fatalf("results do not show the session and snippet:\n%s", got)
	}

	// Opening a result switches to that conversation with the query still live.
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if a.cur != old {
		t.Fatalf("open chat is %q, want the archived one", a.cur.Title)
	}
	if a.searchQuery != "borrow checker" {
		t.Fatalf("query = %q, want it kept so the match is highlighted", a.searchQuery)
	}
	if a.overlay != overlayNone {
		t.Error("the results list stayed open")
	}
}

// "Everywhere" has to include the conversation in front of you.
func TestFindIncludesTheOpenConversation(t *testing.T) {
	a, _ := searchApp(t)
	typeInto(a, "/find goroutines")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if a.findTotal == 0 {
		t.Fatal("the open conversation was skipped")
	}
	if a.findHits[0].session != a.cur {
		t.Error("the open conversation should be searched first")
	}
}

func TestFindReportsNothingFound(t *testing.T) {
	a, _ := searchApp(t)
	typeInto(a, "/find kubernetes")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if a.overlay == overlayFind {
		t.Fatal("opened an empty results list")
	}
	if !a.toastErr || !strings.Contains(a.toast, "kubernetes") {
		t.Fatalf("toast = %q, want it to name what was not found", a.toast)
	}
}

// ctrl+a in the find bar widens the search that just came up empty here.
func TestFindBarWidensToEveryChat(t *testing.T) {
	a, _ := searchApp(t)
	a.openSearch()
	a.searchIn.SetValue("borrow")
	a.setSearchQuery("borrow")
	a.onKey(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})

	if a.overlay != overlayFind {
		t.Fatalf("overlay = %v, want the widened search", a.overlay)
	}
	if a.findQuery != "borrow" {
		t.Fatalf("query = %q, want the one being typed", a.findQuery)
	}
	if a.searching {
		t.Error("the find bar stayed open behind the results")
	}
}

func TestSnippetCentresOnTheMatch(t *testing.T) {
	long := strings.Repeat("padding ", 20) + "NEEDLE" + strings.Repeat(" trailing", 20)
	got := snippetAround(long, "needle")
	if !strings.Contains(got, "NEEDLE") {
		t.Fatalf("snippet lost the match: %q", got)
	}
	if !strings.HasPrefix(got, "…") {
		t.Errorf("snippet %q should mark that it starts mid-sentence", got)
	}
}
