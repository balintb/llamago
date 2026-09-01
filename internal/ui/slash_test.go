package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func typeInto(a *App, s string) {
	a.input.SetValue(s)
	a.layout()
	a.refreshTranscript()
}

func TestSlashCommandsRunInsteadOfSending(t *testing.T) {
	a := chatWithPrompts(t)
	typeInto(a, "/temp 0.25")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if a.cfg.Temperature != 0.25 {
		t.Fatalf("temperature = %v, want 0.25", a.cfg.Temperature)
	}
	if len(a.cur.Turns) != 0 {
		t.Fatal("the command was sent to the model as a prompt")
	}
	if a.input.Value() != "" {
		t.Fatalf("composer = %q, want it cleared", a.input.Value())
	}
}

// A typo must not reach the model: learning about it from a reply is a poor way to find out.
func TestUnknownSlashCommandIsRefused(t *testing.T) {
	a := chatWithPrompts(t)
	typeInto(a, "/mdoel llama3")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if len(a.cur.Turns) != 0 {
		t.Fatal("an unknown command was sent as a prompt")
	}
	if !a.toastErr || !strings.Contains(a.toast, "mdoel") {
		t.Fatalf("toast = %q, want it to name the unknown command", a.toast)
	}
}

// Text that merely contains a slash is an ordinary prompt.
func TestOnlyALeadingSlashIsACommand(t *testing.T) {
	a := chatWithPrompts(t)
	a.cfg.Model = "llama3.2:3b"
	typeInto(a, "what does 10/2 mean")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if len(a.cur.Turns) == 0 {
		t.Fatal("an ordinary prompt was swallowed as a command")
	}
}

func TestSlashModelMatchesAnInstalledModel(t *testing.T) {
	a := chatWithPrompts(t)
	typeInto(a, "/model qwen3")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if a.cfg.Model != "huihui_ai/qwen3-abliterated:30b-a3b" {
		t.Fatalf("model = %q, want the substring match resolved", a.cfg.Model)
	}
}

// An ambiguous fragment is refused rather than resolved arbitrarily.
func TestAmbiguousModelIsRefused(t *testing.T) {
	a := chatWithPrompts(t)
	was := a.cfg.Model
	typeInto(a, "/model 3b")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if a.cfg.Model != was {
		t.Fatalf("model = %q, want it unchanged on an ambiguous match", a.cfg.Model)
	}
}

func TestSlashHintsAndCompletion(t *testing.T) {
	a := chatWithPrompts(t)
	// A narrow list is shown in full.
	typeInto(a, "/s")
	got := ansi.Strip(render(a))
	for _, want := range []string{"/system", "/seed", "/save"} {
		if !strings.Contains(got, want) {
			t.Errorf("hints are missing %q", want)
		}
	}

	// The whole library does not fit, so it ends in a count rather than being cut off mid-name.
	typeInto(a, "/")
	got = ansi.Strip(render(a))
	if !strings.Contains(got, "/model") {
		t.Error("hints do not start at the first command")
	}
	if !strings.Contains(got, "more") {
		t.Errorf("a list too long to fit should say how many are left:\n%s", got)
	}

	// Tab completes an unambiguous prefix rather than moving focus.
	typeInto(a, "/temp")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := a.input.Value(); got != "/temp " {
		t.Fatalf("composer = %q, want the completed command", got)
	}
	if a.focus != focusInput {
		t.Fatal("tab moved focus while completing a command")
	}

	// Ambiguous prefixes leave the text alone and keep listing.
	typeInto(a, "/s")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if a.input.Value() != "/s" {
		t.Fatalf("composer = %q, want an ambiguous prefix left alone", a.input.Value())
	}
	if a.focus == focusInput {
		t.Fatal("tab should still move focus when there is nothing to complete")
	}
}

// The colour tracks recognition, not the slash: it is the confirmation that what you typed is real, so a prefix and a typo must not earn it.
func TestOnlyRecognisedCommandsAreColoured(t *testing.T) {
	cases := map[string]bool{
		"/theme":        true,
		"/theme ember":  true,
		"/THEME":        true,
		"  /theme":      true,
		"/the":          false,
		"/":             false,
		"/mdoel llama3": false,
		"what is 10/2":  false,
		"":              false,
	}
	for text, want := range cases {
		if got := slashRecognized(text); got != want {
			t.Errorf("slashRecognized(%q) = %v, want %v", text, got, want)
		}
	}
}

// And the composer actually renders in a different colour. Comparing two different strings would pass on the text alone, which is how a version of this that styled nothing at all went unnoticed: the textarea styles the line the cursor is on with CursorLine, so setting Text alone did nothing.
func TestComposerRecoloursOnRecognition(t *testing.T) {
	a := chatWithPrompts(t)
	a.input.SetValue("/theme")

	styleComposer(&a.input, false)
	plain := a.input.View()
	styleComposer(&a.input, true)
	lit := a.input.View()

	if plain == lit {
		t.Fatal("the same text renders identically whether or not it is a command")
	}
	if ansi.Strip(plain) != ansi.Strip(lit) {
		t.Fatal("the text itself changed; only the colour should have")
	}
	if !strings.Contains(lit, "/theme") {
		t.Fatal("the command is missing from the styled view")
	}
}

// The composer picks its styling from recognition, not from the slash.
func TestComposerStylingFollowsRecognition(t *testing.T) {
	a := chatWithPrompts(t)

	typeInto(a, "/theme")
	recognised := a.composerView()
	typeInto(a, "/theme")
	if a.composerView() != recognised {
		t.Fatal("the same command rendered differently twice")
	}

	// Same length, one recognised and one not: any difference is the colour.
	typeInto(a, "/themd")
	if a.composerView() == recognised {
		t.Fatal("an unknown command is styled as a recognised one")
	}
}

// shift+backspace empties the composer wherever the cursor happens to be.
func TestShiftBackspaceClearsTheComposer(t *testing.T) {
	a := chatWithPrompts(t)
	typeInto(a, "half a thought\nand another line")
	// Put the cursor somewhere other than the end, where a kill-to-start would leave the rest behind.
	a.input.CursorUp()

	a.onKey(tea.KeyPressMsg{Code: tea.KeyBackspace, Mod: tea.ModShift})
	if got := a.input.Value(); got != "" {
		t.Fatalf("composer = %q, want it emptied", got)
	}
	if a.input.Height() != 1 {
		t.Errorf("composer is %d rows tall, want it collapsed back to one", a.input.Height())
	}
}

// Plain backspace still deletes one character.
func TestPlainBackspaceStillDeletesOneCharacter(t *testing.T) {
	a := chatWithPrompts(t)
	typeInto(a, "abc")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got := a.input.Value(); got != "ab" {
		t.Fatalf("composer = %q, want one character gone", got)
	}
}

// Clearing starts over, so a half-walked recall does not resume mid-history.
func TestClearingResetsPromptRecall(t *testing.T) {
	a := chatWithPrompts(t, "first", "second")
	up(a) // recall "second"
	a.onKey(tea.KeyPressMsg{Code: tea.KeyBackspace, Mod: tea.ModShift})

	if a.histIdx != -1 || a.histDraft != "" {
		t.Fatalf("recall state = (%d, %q), want it reset", a.histIdx, a.histDraft)
	}
	up(a)
	if got := a.input.Value(); got != "second" {
		t.Fatalf("composer = %q, want recall to start from the newest again", got)
	}
}
