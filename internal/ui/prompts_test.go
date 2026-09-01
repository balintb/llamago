package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/store"
)

func TestPlaceholdersAreListedOnceInOrder(t *testing.T) {
	got := store.Placeholders("Review this {{language}} for {{concern}}, {{language}} only")
	want := []string{"language", "concern"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("placeholders = %v, want %v", got, want)
	}
	if n := len(store.Placeholders("no blanks here")); n != 0 {
		t.Fatalf("found %d placeholders in plain text", n)
	}
}

// The composer holds "/save name" at that moment, so the prompt has to come from the conversation instead.
func TestSaveTakesTheLastPromptNotTheComposer(t *testing.T) {
	a := chatWithPrompts(t, "explain the borrow checker")
	typeInto(a, "/save borrow")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	p, ok := store.FindPrompt(a.library, "borrow")
	if !ok {
		t.Fatal("nothing was saved")
	}
	if p.Text != "explain the borrow checker" {
		t.Fatalf("saved %q, want the last prompt", p.Text)
	}
}

// A selection cannot be live while /save is typed - focus returning to the composer drops it - so the last prompt is the only source there is.
func TestSaveIgnoresAStaleSelection(t *testing.T) {
	a := chatWithPrompts(t, "older question", "newer question")
	shiftUp(a)
	shiftUp(a)
	shiftUp(a) // select the older prompt, then type, which returns focus
	a.setFocus(focusInput)
	typeInto(a, "/save recent")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if p, _ := store.FindPrompt(a.library, "recent"); p.Text != "newer question" {
		t.Fatalf("saved %q, want the most recent prompt", p.Text)
	}
}

func TestPromptLoadsIntoTheComposer(t *testing.T) {
	a := chatWithPrompts(t)
	a.library = []store.Prompt{{Name: "review", Text: "Review this {{language}} code"}}

	typeInto(a, "/prompt rev")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if got := a.input.Value(); got != "Review this {{language}} code" {
		t.Fatalf("composer = %q, want the template", got)
	}
	if got := ansi.Strip(render(a)); !strings.Contains(got, "fill in") || !strings.Contains(got, "language") {
		t.Fatalf("the blanks are not named under the composer:\n%s", got)
	}
}

// Sending a template with its blanks intact wastes a whole generation.
func TestSendRefusesUnfilledBlanks(t *testing.T) {
	a := chatWithPrompts(t)
	a.cfg.Model = "llama3.2:3b"
	typeInto(a, "Review this {{language}} code")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if len(a.cur.Turns) != 0 {
		t.Fatal("a template with blanks was sent")
	}
	if !a.toastErr || !strings.Contains(a.toast, "language") {
		t.Fatalf("toast = %q, want it to name the blank", a.toast)
	}

	typeInto(a, "Review this Go code")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(a.cur.Turns) == 0 {
		t.Fatal("a filled template was refused")
	}
}

func TestPutAndDropPrompt(t *testing.T) {
	var lib []store.Prompt
	lib = store.PutPrompt(lib, "review", "first")
	lib = store.PutPrompt(lib, "Review", "second")
	if len(lib) != 1 {
		t.Fatalf("library has %d entries, want names matched case-insensitively", len(lib))
	}
	if lib[0].Text != "second" {
		t.Fatalf("text = %q, want the replacement", lib[0].Text)
	}
	if _, ok := store.DropPrompt(lib, "missing"); ok {
		t.Error("dropping an absent prompt reported success")
	}
	lib, ok := store.DropPrompt(lib, "REVIEW")
	if !ok || len(lib) != 0 {
		t.Fatalf("drop left %d entries, want it gone", len(lib))
	}
}

// A saved library survives a reload.
func TestPromptsRoundTripThroughDisk(t *testing.T) {
	want := []store.Prompt{{Name: "a", Text: "one"}, {Name: "b", Text: "two {{x}}"}}
	if err := store.SavePrompts(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadPrompts()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Text != "two {{x}}" {
		t.Fatalf("loaded %+v, want %+v", got, want)
	}
	_ = store.SavePrompts(nil)
}
