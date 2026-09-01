package ui

import (
	"strings"
	"testing"
)

func TestCopyConversationRendersTheWholeThread(t *testing.T) {
	a := chatWithPrompts(t, "first", "second")
	md := a.cur.Markdown()

	for _, want := range []string{"first", "second", "reply A", "reply B"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown is missing %q", want)
		}
	}
	if a.copyConversation() == nil {
		t.Fatal("copy returned no command")
	}
}

// With nothing said yet there is nothing to put on the clipboard, and silently copying an empty document would be worse than saying so.
func TestCopyConversationRefusesAnEmptyChat(t *testing.T) {
	a := chatWithPrompts(t)
	a.copyConversation()
	if !a.toastErr {
		t.Fatalf("toast = %q, want it to report there is nothing to copy", a.toast)
	}
}
