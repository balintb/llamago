package ui

import (
	"testing"
	"time"

	"github.com/balintb/llamago/internal/store"
)

// resumeApp builds an app with saved sessions, as Init sees them.
func resumeApp(t *testing.T, resume bool) (*App, *store.Session, *store.Session) {
	t.Helper()
	a := chatWithPrompts(t)
	a.cfg.Resume = resume

	older := a.freshSession(time.Now().Add(-2 * time.Hour))
	older.Title, older.Updated = "older", time.Now().Add(-2*time.Hour)
	older.Turns = []store.Turn{{Role: "user", Content: "old"}}

	newest := a.freshSession(time.Now().Add(-time.Hour))
	newest.Title, newest.Updated = "newest", time.Now().Add(-time.Minute)
	newest.Turns = []store.Turn{{Role: "user", Content: "recent"}}

	a.sessions = []*store.Session{older, newest}
	return a, older, newest
}

func TestResumeOpensTheLastConversation(t *testing.T) {
	a, _, newest := resumeApp(t, true)
	a.resumeLast()
	if a.cur != newest {
		t.Fatalf("resumed %q, want the most recently updated", a.cur.Title)
	}
}

func TestResumeIsOptIn(t *testing.T) {
	a, _, _ := resumeApp(t, false)
	was := a.cur
	a.resumeLast()
	if a.cur != was {
		t.Fatal("resumed with the setting off")
	}
}

// Pinning reorders the list, so "last" has to mean most recently updated rather than whatever sits at the top.
func TestResumeIgnoresPinOrder(t *testing.T) {
	a, older, newest := resumeApp(t, true)
	older.Pinned = true
	store.Sort(a.sessions)
	if a.sessions[0] != older {
		t.Fatal("setup: the pinned session should sort first")
	}
	a.resumeLast()
	if a.cur != newest {
		t.Fatalf("resumed %q, want the most recently updated rather than the pinned one", a.cur.Title)
	}
}

// A session already carrying turns outranks anything saved.
func TestResumeLeavesAnActiveChatAlone(t *testing.T) {
	a, _, _ := resumeApp(t, true)
	a.cur.Turns = []store.Turn{{Role: "user", Content: "already typing"}}
	was := a.cur
	a.resumeLast()
	if a.cur != was {
		t.Fatal("resume clobbered a conversation in progress")
	}
}

// Resuming adopts the model the conversation was held with.
func TestResumeAdoptsTheSessionModel(t *testing.T) {
	a, _, newest := resumeApp(t, true)
	newest.Model = "phi4-mini"
	a.resumeLast()
	if a.cfg.Model != "phi4-mini" {
		t.Fatalf("model = %q, want the resumed session's", a.cfg.Model)
	}
}
