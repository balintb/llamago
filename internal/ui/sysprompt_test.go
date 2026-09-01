package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A session records the prompt in force when it starts, so editing the global one later cannot quietly change the persona of a thread already underway.
func TestSessionKeepsThePromptItStartedUnder(t *testing.T) {
	a := chatWithPrompts(t)
	a.cfg.System = "You are terse."
	a.input.SetValue("hello")
	a.send()

	if got := a.cur.System; got != "You are terse." {
		t.Fatalf("session prompt = %q, want it stamped at send", got)
	}

	a.cfg.System = "You are verbose."
	if got := a.systemPrompt(); got != "You are terse." {
		t.Fatalf("prompt in force = %q, want the session to keep its own", got)
	}
	if last := a.requestMessages()[0]; last.Role != "system" || last.Content != "You are terse." {
		t.Fatalf("request carried %+v, want the session's prompt", last)
	}
}

// A divergence is flagged, since it explains an answer that looks out of character.
func TestDivergentPromptIsFlaggedInTheHeader(t *testing.T) {
	a := chatWithPrompts(t)
	a.cfg.System = "You are terse."
	a.input.SetValue("hello")
	a.send()

	if got := ansi.Strip(render(a)); strings.Contains(got, "own prompt") {
		t.Fatal("flagged while the prompts still agree")
	}
	a.cfg.System = "You are verbose."
	if got := ansi.Strip(render(a)); !strings.Contains(got, "⚑ own prompt") {
		t.Fatalf("a divergent prompt is not flagged:\n%s", got)
	}
}

// Sessions started without a prompt keep following the global setting, which is both the older behaviour and the less surprising one.
func TestUnstampedSessionsFollowTheGlobalPrompt(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.cur.System = ""
	a.cfg.System = "You are terse."

	if got := a.systemPrompt(); got != "You are terse." {
		t.Fatalf("prompt = %q, want the global one", got)
	}
	if got := ansi.Strip(render(a)); strings.Contains(got, "own prompt") {
		t.Error("an unstamped session should not claim its own prompt")
	}
}

// The export reports the prompt the conversation actually ran under.
func TestExportCarriesTheSessionPrompt(t *testing.T) {
	a := chatWithPrompts(t)
	a.cfg.System = "You are terse."
	a.input.SetValue("hello")
	a.send()

	if !strings.Contains(a.cur.Markdown(), "You are terse.") {
		t.Fatal("the export lost the system prompt")
	}
}
