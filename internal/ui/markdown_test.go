package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Raw mode has to show what actually arrived, syntax markers and all, or it cannot answer "did the model write bad markdown, or did the renderer mangle good markdown".
func TestRawTextTogglePreservesTheSourceMarkers(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.cur.Turns[1].Content = "# Heading\n\n- one\n- two"
	a.setFocus(focusTranscript)
	a.invalidateRenders()

	if got := ansi.Strip(a.renderConversation()); strings.Contains(got, "# Heading") {
		t.Fatalf("setup: markdown should be rendered, not raw:\n%s", got)
	}

	a.onKey(tea.KeyPressMsg{Code: 'm', Text: "m"})
	if a.cfg.Markdown {
		t.Fatal("m did not turn markdown off")
	}
	got := ansi.Strip(a.renderConversation())
	if !strings.Contains(got, "# Heading") || !strings.Contains(got, "- one") {
		t.Fatalf("raw text lost its markers:\n%s", got)
	}

	a.onKey(tea.KeyPressMsg{Code: 'm', Text: "m"})
	if !a.cfg.Markdown {
		t.Fatal("m did not turn markdown back on")
	}
	if got := ansi.Strip(a.renderConversation()); strings.Contains(got, "# Heading") {
		t.Fatal("markdown did not come back")
	}
}
