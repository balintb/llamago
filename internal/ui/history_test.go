package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/store"
)

// chatWithPrompts seeds a session with n user prompts and their replies.
func chatWithPrompts(t *testing.T, prompts ...string) *App {
	t.Helper()
	a := newTestApp(100, 32)
	a.cur.Turns = nil
	for i, p := range prompts {
		a.cur.Turns = append(a.cur.Turns,
			store.Turn{Role: "user", Content: p, At: time.Now()},
			store.Turn{Role: "assistant", Model: "llama3.2:3b", Content: "reply " + string(rune('A'+i)), At: time.Now()},
		)
	}
	a.setFocus(focusInput)
	a.refreshTranscript()
	return a
}

func up(a *App)      { a.onKey(tea.KeyPressMsg{Code: tea.KeyUp}) }
func down(a *App)    { a.onKey(tea.KeyPressMsg{Code: tea.KeyDown}) }
func shiftUp(a *App) { a.onKey(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift}) }
func shiftDn(a *App) { a.onKey(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift}) }

func TestRecallWalksPromptsAndBack(t *testing.T) {
	a := chatWithPrompts(t, "first", "second", "third")

	// Up walks back from the newest prompt and holds at the oldest.
	for _, want := range []string{"third", "second", "first", "first"} {
		up(a)
		if got := a.input.Value(); got != want {
			t.Fatalf("after up: composer = %q, want %q", got, want)
		}
	}
	// Down retraces and lands back on the empty composer it started from.
	for _, want := range []string{"second", "third", ""} {
		down(a)
		if got := a.input.Value(); got != want {
			t.Fatalf("after down: composer = %q, want %q", got, want)
		}
	}
	if a.histIdx != -1 {
		t.Fatalf("recall index = %d, want -1 once back at the draft", a.histIdx)
	}
}

func TestRecallRestoresTheDraft(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.input.SetValue("half typed")

	up(a)
	if got := a.input.Value(); got != "first" {
		t.Fatalf("composer = %q, want the recalled prompt", got)
	}
	down(a)
	if got := a.input.Value(); got != "half typed" {
		t.Fatalf("composer = %q, want the draft back", got)
	}
}

// Down before any recall must not wipe what is being typed.
func TestDownLeavesAnUntouchedDraftAlone(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.input.SetValue("still typing")

	down(a)
	if got := a.input.Value(); got != "still typing" {
		t.Fatalf("composer = %q, want the draft untouched", got)
	}
}

// Up inside a multi-line draft moves the cursor; only the top row recalls.
func TestUpMovesWithinAMultilineDraft(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.input.SetValue("line one\nline two")

	up(a)
	if got := a.input.Value(); got != "line one\nline two" {
		t.Fatalf("composer = %q, want the draft intact while the cursor moves", got)
	}
	if a.input.Line() != 0 {
		t.Fatalf("cursor row = %d, want 0 after moving up", a.input.Line())
	}
	// Now on the top row, so the next up recalls.
	up(a)
	if got := a.input.Value(); got != "first" {
		t.Fatalf("composer = %q, want the recalled prompt from the top row", got)
	}
}

func TestSendResetsRecall(t *testing.T) {
	a := chatWithPrompts(t, "first")
	up(a)
	a.input.SetValue("something new")
	a.send()

	if a.histIdx != -1 || a.histDraft != "" {
		t.Fatalf("recall state = (%d, %q), want reset after send", a.histIdx, a.histDraft)
	}
	up(a)
	if got := a.input.Value(); got != "something new" {
		t.Fatalf("composer = %q, want the just-sent prompt as newest history", got)
	}
}

func TestShiftUpSelectsFromTheComposer(t *testing.T) {
	a := chatWithPrompts(t, "first", "second")

	shiftUp(a)
	if a.focus != focusTranscript {
		t.Fatalf("focus = %v, want the transcript", a.focus)
	}
	if want := len(a.cur.Turns) - 1; a.selTurn != want {
		t.Fatalf("selection = %d, want the newest turn %d", a.selTurn, want)
	}

	// Older, one message at a time, prompts and replies alike.
	shiftUp(a)
	if a.selTurn != 2 || a.cur.Turns[a.selTurn].Content != "second" {
		t.Fatalf("selection = %d (%q), want the second prompt", a.selTurn, a.cur.Turns[a.selTurn].Content)
	}
	// And it holds at the oldest rather than wrapping.
	for range 5 {
		shiftUp(a)
	}
	if a.selTurn != 0 {
		t.Fatalf("selection = %d, want to hold at the oldest turn", a.selTurn)
	}
}

func TestShiftDownPastTheNewestReturnsToTheComposer(t *testing.T) {
	a := chatWithPrompts(t, "first")

	shiftUp(a)
	shiftDn(a)
	if a.focus != focusInput {
		t.Fatalf("focus = %v, want the composer back", a.focus)
	}
	if a.selTurn != -1 {
		t.Fatalf("selection = %d, want it dropped on the way out", a.selTurn)
	}
}

func TestLeavingTheTranscriptDropsTheSelection(t *testing.T) {
	a := chatWithPrompts(t, "first")
	shiftUp(a)
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if a.selTurn != -1 {
		t.Fatalf("selection = %d, want it dropped when focus leaves", a.selTurn)
	}
}

func TestCopySelectedTakesTheRawMessage(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.cur.Turns[1].Content = "# heading\n\nbody text"

	shiftUp(a) // the assistant reply
	text, kind, ok := a.selectedText()
	if !ok || text != "# heading\n\nbody text" || kind != "response" {
		t.Fatalf("selected = (%q, %q, %v), want the raw markdown of the reply", text, kind, ok)
	}
	if a.copySelected() == nil {
		t.Fatal("copy returned no command")
	}

	shiftUp(a) // the prompt
	if text, kind, _ := a.selectedText(); text != "first" || kind != "prompt" {
		t.Fatalf("selected = (%q, %q), want the prompt", text, kind)
	}
}

// A turn that only ever produced reasoning still has something worth copying.
func TestCopySelectedFallsBackToReasoning(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.cur.Turns[1].Content = ""
	a.cur.Turns[1].Thinking = "considering it"

	shiftUp(a)
	if text, _, ok := a.selectedText(); !ok || text != "considering it" {
		t.Fatalf("selected = (%q, %v), want the reasoning", text, ok)
	}
}

// A selection left pointing past the end must not survive a truncation.
func TestSelectionSurvivesTurnsBeingDropped(t *testing.T) {
	a := chatWithPrompts(t, "first", "second")
	shiftUp(a)
	a.cur.Turns = a.cur.Turns[:1]
	a.refreshTranscript()

	if a.selTurn >= len(a.cur.Turns) {
		t.Fatalf("selection = %d, out of range for %d turns", a.selTurn, len(a.cur.Turns))
	}
	if a.selectedTurn() == nil && a.selTurn >= 0 {
		t.Fatalf("selection = %d resolves to no turn", a.selTurn)
	}
}

func TestSelectedMessageIsMarkedInTheTranscript(t *testing.T) {
	a := chatWithPrompts(t, "first")
	before := a.renderConversation()

	shiftUp(a)
	after := a.renderConversation()
	if before == after {
		t.Fatal("selecting a message changed nothing in the render")
	}
	// The fill must not push the header past the pane width.
	for line := range strings.SplitSeq(after, "\n") {
		if w := lipgloss.Width(line); w > a.transcript.Width() {
			t.Fatalf("line %q is %d cells wide, pane is %d", ansi.Strip(line), w, a.transcript.Width())
		}
	}
}

func TestEditSelectedLoadsThePromptWithoutTouchingHistory(t *testing.T) {
	a := chatWithPrompts(t, "first", "second")
	before := len(a.cur.Turns)

	shiftUp(a) // the reply to "second"
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if got := a.input.Value(); got != "second" {
		t.Fatalf("composer = %q, want the prompt behind the selected reply", got)
	}
	if a.focus != focusInput {
		t.Fatalf("focus = %v, want the composer", a.focus)
	}
	if len(a.cur.Turns) != before {
		t.Fatalf("turns = %d, want the conversation untouched at %d", len(a.cur.Turns), before)
	}
}

// Rewinding within the last exchange is as cheap as regenerate, so it acts at once; reaching further back asks first.
func TestRewindActsOnTheLastExchangeAndConfirmsBeyondIt(t *testing.T) {
	a := chatWithPrompts(t, "first", "second", "third")

	shiftUp(a) // the newest reply
	a.rewindTo()
	if a.overlay == overlayConfirm {
		t.Fatal("rewinding the last exchange should not stop to confirm")
	}
	// The stale reply is dropped and a fresh one opened in its place, so the count returns to six with an empty turn waiting on the model.
	if n := len(a.cur.Turns); n != 6 {
		t.Fatalf("turns = %d, want 6", n)
	}
	if last := a.cur.Turns[5]; last.Role != "assistant" || last.Content != "" {
		t.Fatalf("last turn = %+v, want a fresh empty reply", last)
	}
	if a.cur.Turns[4].Content != "third" {
		t.Fatalf("prompt = %q, want the rewind to keep it", a.cur.Turns[4].Content)
	}

	a = chatWithPrompts(t, "first", "second", "third")
	a.selTurn = 0 // the very first prompt
	a.rewindTo()
	if a.overlay != overlayConfirm {
		t.Fatal("rewinding past the last exchange should confirm")
	}
	if n := len(a.cur.Turns); n != 6 {
		t.Fatalf("turns = %d, want 6: nothing dropped before the answer", n)
	}
	if !strings.Contains(a.confirm.prompt, "5") {
		t.Fatalf("confirm asked %q, want the number of messages at stake", a.confirm.prompt)
	}
}

func TestRewindTruncatesToTheSelectedPrompt(t *testing.T) {
	a := chatWithPrompts(t, "first", "second", "third")
	a.rewind(2) // the "second" prompt

	if n := len(a.cur.Turns); n != 4 {
		t.Fatalf("turns = %d, want 4: through the prompt plus the new reply", n)
	}
	if got := a.cur.Turns[2].Content; got != "second" {
		t.Fatalf("last prompt = %q, want it kept", got)
	}
	if a.selTurn != -1 {
		t.Fatalf("selection = %d, want it dropped by the rewind", a.selTurn)
	}
}

func TestForkCopiesUpToTheSelectionAndKeepsTheOriginal(t *testing.T) {
	a := chatWithPrompts(t, "first", "second")
	origID, origLen := a.cur.ID, len(a.cur.Turns)

	a.selTurn = 1 // the reply to "first"
	a.forkSelected()

	if a.cur.ID == origID {
		t.Fatal("fork stayed in the same session")
	}
	if n := len(a.cur.Turns); n != 2 {
		t.Fatalf("forked turns = %d, want 2 up to the selection", n)
	}
	if a.cur.Title != "first" {
		t.Fatalf("forked title = %q, want it taken from the first prompt", a.cur.Title)
	}
	var orig *store.Session
	for _, s := range a.sessions {
		if s.ID == origID {
			orig = s
		}
	}
	if orig == nil {
		t.Fatal("the original session is not in the sidebar")
	}
	if len(orig.Turns) != origLen {
		t.Fatalf("original now has %d turns, want it untouched at %d", len(orig.Turns), origLen)
	}
	// Appending to the fork must not reach back into the original's turns.
	a.cur.Turns = append(a.cur.Turns, store.Turn{Role: "user", Content: "tangent"})
	if orig.Turns[2].Content != "second" {
		t.Fatalf("original turn 2 = %q, want %q: the fork shares its backing array",
			orig.Turns[2].Content, "second")
	}
}

// Selecting is a change of colour, never of layout: the text must stay in the exact column it occupied, gutter glyphs included.
func TestSelectingDoesNotMoveContent(t *testing.T) {
	a := chatWithPrompts(t, "Write a haiku about Go")
	a.cur.Turns[1].Content = "Goroutines flow free\n\n- 5/7/5 syllables\n\n```go\nfunc main() {}\n```"
	a.cur.Turns[1].Thinking = "counting syllables"
	a.refreshTranscript()

	before := strings.Split(ansi.Strip(a.renderConversation()), "\n")
	shiftUp(a) // the reply
	after := strings.Split(ansi.Strip(a.renderConversation()), "\n")

	if len(before) != len(after) {
		t.Fatalf("line count changed from %d to %d", len(before), len(after))
	}
	for i := range before {
		b, s := before[i], after[i]
		if b == s {
			continue
		}
		// Two differences are allowed: the first cell becoming the gutter, and a key hint appended to the end of a selected line. What must not happen is existing text moving, so the after-line has to start with the before-line, minus that first cell.
		if strings.TrimSpace(b) == "" && strings.TrimSpace(s) == "▌" {
			continue
		}
		br, sr := []rune(b), []rune(s)
		was := strings.TrimRight(string(br[1:]), " ")
		now := strings.TrimRight(string(sr[1:]), " ")
		if !strings.HasPrefix(now, was) {
			t.Fatalf("line %d shifted:\n  before %q\n  after  %q", i, b, s)
		}
	}
}

// Session ids are millisecond timestamps and double as filenames, so two sessions born in the same millisecond would share a file on disk.
func TestNewSessionsNeverShareAnID(t *testing.T) {
	a := chatWithPrompts(t, "first")
	now := time.Now()

	first := a.freshSession(now)
	a.sessions = append(a.sessions, first)
	second := a.freshSession(now) // the very same instant

	if first.ID == second.ID {
		t.Fatalf("two sessions share the id %q, so they share a file", first.ID)
	}
	if a.freshSession(now).ID == a.cur.ID {
		t.Fatal("a new session collided with the open one")
	}
}
