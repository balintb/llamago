package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/ollama"
	"github.com/balintb/llamago/internal/store"
	"github.com/balintb/llamago/internal/theme"
)

// toolApp is a chat with tools on and a model that can call them.
func toolApp(t *testing.T) *App {
	t.Helper()
	a := chatWithPrompts(t)
	a.cfg.Tools = true
	a.cfg.Model = "llama3.2:3b"
	a.details["llama3.2:3b"] = &ollama.ShowResponse{Capabilities: []string{"tools", "completion"}}
	return a
}

// lastToolTurn is the most recent tool result. It is not the last turn: once the results are in, the model is asked to carry on, which opens a fresh assistant turn after them.
func lastToolTurn(t *testing.T, a *App) store.Turn {
	t.Helper()
	for i := len(a.cur.Turns) - 1; i >= 0; i-- {
		if a.cur.Turns[i].Role == "tool" {
			return a.cur.Turns[i]
		}
	}
	t.Fatal("no tool turn in the conversation")
	return store.Turn{}
}

func call(name string, args map[string]any) ollama.ToolCall {
	return ollama.ToolCall{Function: ollama.ToolCallFunc{Name: name, Arguments: args}}
}

// Offering tools to a model that cannot call them wastes context on a list it will never use.
func TestToolsAreOfferedOnlyWhenUsable(t *testing.T) {
	a := toolApp(t)
	if len(a.toolsForRequest()) == 0 {
		t.Fatal("no tools offered to a capable model with the setting on")
	}

	a.cfg.Tools = false
	if a.toolsForRequest() != nil {
		t.Error("tools offered with the setting off")
	}

	a.cfg.Tools = true
	a.details["llama3.2:3b"] = &ollama.ShowResponse{Capabilities: []string{"completion"}}
	if a.toolsForRequest() != nil {
		t.Error("tools offered to a model that cannot call them")
	}
}

// A switched-off tool is neither offered nor runnable, since a model may ask for one it saw in an earlier turn.
func TestSwitchedOffToolIsNeitherOfferedNorRun(t *testing.T) {
	a := toolApp(t)
	a.cfg.ToolOff = map[string]bool{"read_file": true}

	for _, d := range a.toolsForRequest() {
		if d.Function.Name == "read_file" {
			t.Fatal("a switched-off tool was offered")
		}
	}

	a.pendingCalls = []ollama.ToolCall{call("read_file", map[string]any{"path": "go.mod"})}
	a.runNextCall()

	last := lastToolTurn(t, a)
	if !last.ToolFail || !strings.Contains(last.Content, "switched off") {
		t.Fatalf("switched-off call produced %+v, want a refusal the model can read", last)
	}
}

// A safe tool runs without interrupting; an unsafe one asks first.
func TestPermissionFollowsSafety(t *testing.T) {
	a := toolApp(t)

	a.pendingCalls = []ollama.ToolCall{call("now", nil)}
	if cmd := a.runNextCall(); cmd == nil {
		t.Fatal("a safe tool did not run")
	}
	if a.overlay == overlayConfirm {
		t.Error("a safe tool asked for permission")
	}

	a.pendingCalls = []ollama.ToolCall{call("http_get", map[string]any{"url": "https://example.com"})}
	a.runNextCall()
	if a.overlay != overlayConfirm {
		t.Fatal("an unsafe tool ran without asking")
	}
	// The arguments are the decision: "fetch a URL" is not one, this URL is.
	if !strings.Contains(a.confirm.prompt, "example.com") {
		t.Errorf("the prompt hides the arguments: %q", a.confirm.prompt)
	}
}

// Allowing a tool lasts for the conversation, so the same tool does not ask again on the next call.
func TestAllowingAToolLastsTheConversation(t *testing.T) {
	a := toolApp(t)
	a.pendingCalls = []ollama.ToolCall{call("http_get", map[string]any{"url": "https://example.com"})}
	a.runNextCall()

	a.Update(toolAllowedMsg{call: call("http_get", map[string]any{"url": "https://example.com"})})
	if !a.toolAllowed["http_get"] {
		t.Fatal("the permission was not remembered")
	}

	a.overlay = overlayNone
	a.pendingCalls = []ollama.ToolCall{call("http_get", map[string]any{"url": "https://other.com"})}
	a.runNextCall()
	if a.overlay == overlayConfirm {
		t.Error("asked again for a tool already allowed in this conversation")
	}
}

// Refusing still owes the model an answer, or it waits for output that is never coming.
func TestRefusingACallTellsTheModel(t *testing.T) {
	a := toolApp(t)
	a.pendingCalls = []ollama.ToolCall{call("http_get", map[string]any{"url": "https://example.com"})}
	a.runNextCall()

	a.onKey(tea.KeyPressMsg{Code: 'n', Text: "n"})

	last := lastToolTurn(t, a)
	if !last.ToolFail {
		t.Fatalf("refusing produced %+v, want a tool turn saying so", last)
	}
	if !strings.Contains(last.Content, "did not allow") {
		t.Errorf("the model is not told why: %q", last.Content)
	}
}

// A model that keeps calling must stop somewhere.
func TestToolRoundsAreCapped(t *testing.T) {
	a := toolApp(t)
	a.cfg.ToolSteps = 2
	a.cur.Turns = append(a.cur.Turns, store.Turn{Role: "assistant", Content: "working"})
	a.toolStep = 2

	a.onToolCalls([]ollama.ToolCall{call("now", nil)})
	if len(a.pendingCalls) != 0 {
		t.Fatal("calls were queued past the cap")
	}
	if !a.toastErr || !strings.Contains(a.toast, "rounds") {
		t.Errorf("toast = %q, want it to say why it stopped", a.toast)
	}
}

// A tool turn shows what ran, with what, and what came back.
func TestToolTurnRenders(t *testing.T) {
	a := toolApp(t)
	a.cur.Turns = append(a.cur.Turns, store.Turn{
		Role: "tool", ToolName: "read_file", Content: "package main",
		ToolArgs: map[string]any{"path": "main.go"},
	})
	a.invalidateRenders()

	// Collapsed by default: what ran and how much came back, not the output.
	got := ansi.Strip(a.renderConversation())
	for _, want := range []string{"read_file", "path=main.go", "1 line"} {
		if !strings.Contains(got, want) {
			t.Errorf("the tool turn does not show %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "package main") {
		t.Errorf("the output was shown without being asked for:\n%s", got)
	}
}

// The result is not shown at all until asked for; the model still gets all of it. A file read would otherwise bury the conversation it was part of.
func TestResultIsHiddenButTheModelGetsItWhole(t *testing.T) {
	a := toolApp(t)
	body := strings.Repeat("line\n", 40)
	a.cur.Turns = append(a.cur.Turns, store.Turn{
		Role: "tool", ToolName: "read_file", Content: body,
	})
	a.invalidateRenders()

	got := ansi.Strip(a.renderConversation())
	if strings.Contains(got, "line\nline") {
		t.Fatalf("the output was shown by default:\n%s", got)
	}
	if !strings.Contains(got, "40 lines") {
		t.Fatalf("nothing says how much came back:\n%s", got)
	}
	// What travels to the model is the turn itself, untouched.
	if msgs := a.cur.Messages(""); !strings.Contains(msgs[len(msgs)-1].Content, strings.TrimSpace(body)) {
		t.Error("the model was given a summary rather than the whole result")
	}
}

// Every tool gets a checkbox, and it is on unless switched off - including a tool installed after the config was written.
func TestSettingsListsEveryToolAsACheckbox(t *testing.T) {
	a := newTestApp(100, 40)
	a.tab = tabSettings
	a.cfg.Tools = true

	names := map[string]bool{}
	for _, f := range a.settingsFields() {
		names[strings.TrimSpace(f.name)] = true
	}
	for _, want := range []string{"read_file", "list_files", "find_files", "now", "http_get"} {
		if !names[want] {
			t.Errorf("settings has no row for %q", want)
		}
	}

	// Toggling one writes it off and back on.
	for i, f := range a.settingsFields() {
		if strings.TrimSpace(f.name) == "http_get" {
			a.setIdx = i
		}
	}
	a.onKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if !a.cfg.ToolOff["http_get"] {
		t.Fatal("toggling the row did not switch the tool off")
	}
	a.onKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if a.cfg.ToolOff["http_get"] {
		t.Fatal("toggling again did not switch it back on")
	}
}

// The header has to say whether tools are actually in play. Silence is what makes a model answering "I cannot know the time" look like a broken feature rather than a model that never had the option.
func TestHeaderSaysWhetherToolsCanRun(t *testing.T) {
	a := toolApp(t)
	a.cfg.Tools = false
	if got := ansi.Strip(render(a)); strings.Contains(got, "⚒") {
		t.Error("tools advertised while switched off")
	}

	a.cfg.Tools = true
	want := fmt.Sprintf("⚒ %d", len(a.enabledTools()))
	if got := ansi.Strip(render(a)); !strings.Contains(got, want) {
		t.Errorf("the header does not say %q:\n%s", want, firstLine(got))
	}

	// The state that actually confused: on, but this model cannot use them.
	a.details["llama3.2:3b"] = &ollama.ShowResponse{Capabilities: []string{"completion", "vision"}}
	got := ansi.Strip(render(a))
	if !strings.Contains(got, "unsupported") {
		t.Fatalf("nothing says the model cannot call tools:\n%s", firstLine(got))
	}
}

// Being told "unsupported" without being told what to switch to is half an answer.
func TestUnusableNoteNamesAModelThatWorks(t *testing.T) {
	a := toolApp(t)
	a.details["llama3.2:3b"] = &ollama.ShowResponse{Capabilities: []string{"completion"}}
	a.details["huihui_ai/qwen3-abliterated:30b-a3b"] = &ollama.ShowResponse{Capabilities: []string{"tools"}}

	note := a.toolsUnusableNote()
	if !strings.Contains(note, "cannot call tools") {
		t.Fatalf("note = %q", note)
	}
	if !strings.Contains(note, "qwen3") {
		t.Errorf("note = %q, want it to name a model that can", note)
	}
}

// A long result is trimmed until asked for, then shown whole, then trimmed again.
func TestToolResultExpandsAndCollapses(t *testing.T) {
	a := toolApp(t)
	var body strings.Builder
	for i := range 30 {
		body.WriteString(fmt.Sprintf("line %d\n", i))
	}
	a.cur.Turns = append(a.cur.Turns, store.Turn{
		Role: "tool", ToolName: "read_file", Content: body.String(),
	})
	a.invalidateRenders()
	a.refreshTranscript()
	shiftUp(a) // select the tool result

	collapsed := ansi.Strip(a.renderConversation())
	if strings.Contains(collapsed, "line 0") {
		t.Fatal("the result was shown before being asked for")
	}
	if !strings.Contains(collapsed, "show") {
		t.Errorf("nothing says it can be shown:\n%s", collapsed)
	}

	a.onKey(tea.KeyPressMsg{Code: tea.KeyRight})
	expanded := ansi.Strip(a.renderConversation())
	if !strings.Contains(expanded, "line 29") {
		t.Fatalf("right did not expand the result:\n%s", expanded)
	}
	if !strings.Contains(expanded, "hide") {
		t.Error("nothing says it can be hidden again")
	}

	a.onKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	if got := ansi.Strip(a.renderConversation()); strings.Contains(got, "line 29") {
		t.Fatal("left did not collapse the result")
	}
}

// Expanding is a view of one result, not of every tool turn at once.
func TestExpandingOneResultLeavesTheOthers(t *testing.T) {
	a := toolApp(t)
	long := strings.Repeat("x\n", 30)
	a.cur.Turns = append(a.cur.Turns,
		store.Turn{Role: "tool", ToolName: "first", Content: long + "FIRSTEND"},
		store.Turn{Role: "tool", ToolName: "second", Content: long + "SECONDEND"},
	)
	a.invalidateRenders()
	a.refreshTranscript()

	// The selection lives in the transcript, so that is where the key has to go.
	a.setFocus(focusTranscript)
	a.selTurn = len(a.cur.Turns) - 1
	a.onKey(tea.KeyPressMsg{Code: tea.KeyRight})

	got := ansi.Strip(a.renderConversation())
	if !strings.Contains(got, "SECONDEND") {
		t.Fatal("the selected result did not expand")
	}
	if strings.Contains(got, "FIRSTEND") {
		t.Fatal("expanding one result expanded another")
	}
}

// The arrows do nothing on a turn that is not a tool result, rather than swallowing the key.
func TestExpandIgnoresOrdinaryTurns(t *testing.T) {
	a := toolApp(t)
	a.cur.Turns = []store.Turn{{Role: "user", Content: "hello"}}
	a.selTurn = 0
	if cmd := a.expandSelected(true); cmd != nil {
		t.Fatal("expanding a prompt did something")
	}
}

// phi4-mini advertises tool support and writes the call into its answer instead: the reply is a wall of JSON and invented filenames that reads as llamago failing, when the model simply never asked for anything.
func TestTextToolCallIsRecognised(t *testing.T) {
	wrote := []string{
		`To list the files, I can use the "list_files" function.` + "\n\n" +
			`[{"name": "list_files", "arguments": {"path": "" }}]`,
		`<|tool_call|>To read docs/report1.txt using your available functions`,
		`<tool_call>{"name": "read_file"}`,
	}
	for _, content := range wrote {
		if !looksLikeTextToolCall(content) {
			t.Errorf("not recognised as a call written as text: %.60q", content)
		}
	}

	// Ordinary answers, including ones that talk about tools, must not trip it.
	for _, content := range []string{
		"I do not have a tool for that.",
		"The list_files function would help here, if you enable it.",
		`Here is some JSON: {"name": "Lisbon", "country": "Portugal"}`,
		"",
	} {
		if looksLikeTextToolCall(content) {
			t.Errorf("ordinary answer flagged as a call: %.60q", content)
		}
	}
}

func TestMisfireNoteNamesTheProblemAndAWayOut(t *testing.T) {
	a := toolApp(t)
	a.details["huihui_ai/qwen3-abliterated:30b-a3b"] = &ollama.ShowResponse{Capabilities: []string{"tools"}}
	turn := store.Turn{
		Role: "assistant", Model: "llama3.2:3b",
		Content: `[{"name": "list_files", "arguments": {}}]`,
	}

	note := ansi.Strip(a.viewToolMisfire(&turn))
	if !strings.Contains(note, "nothing ran") {
		t.Fatalf("note = %q, want it to say nothing happened", note)
	}
	if !strings.Contains(note, "qwen3") {
		t.Errorf("note = %q, want it to name a model that works", note)
	}

	// A real call is not a misfire, and neither is anything with tools off.
	turn.ToolCalls = []ollama.ToolCall{call("list_files", nil)}
	if a.viewToolMisfire(&turn) != "" {
		t.Error("a genuine call was reported as a misfire")
	}
	turn.ToolCalls = nil
	a.cfg.Tools = false
	if a.viewToolMisfire(&turn) != "" {
		t.Error("flagged a misfire with tools switched off")
	}
}

// The note appears under the reply itself, where the confusion is.
func TestMisfireNoteRendersInTheTranscript(t *testing.T) {
	a := toolApp(t)
	a.cur.Turns = append(a.cur.Turns, store.Turn{
		Role: "assistant", Model: "llama3.2:3b",
		Content: `[{"name": "list_files", "arguments": {}}] here are your files: a.txt`,
	})
	a.invalidateRenders()

	if got := ansi.Strip(a.renderConversation()); !strings.Contains(got, "wrote that call as text") {
		t.Fatalf("no explanation under a reply that misfired:\n%s", got)
	}
}

// A call is part of the conversation: it is the question the result answers. Without it the turn is an empty header with output appearing underneath.
func TestToolCallIsVisibleInTheHistory(t *testing.T) {
	a := toolApp(t)
	a.cur.Turns = []store.Turn{
		{Role: "user", Content: "what time is it?"},
		{Role: "assistant", Model: "llama3.2:3b", ToolCalls: []ollama.ToolCall{
			call("now", nil),
			call("read_file", map[string]any{"path": "main.go"}),
		}},
		{Role: "tool", ToolName: "now", Content: "Thursday"},
	}
	a.invalidateRenders()

	got := ansi.Strip(a.renderConversation())
	if !strings.Contains(got, "now()") {
		t.Errorf("the call is not shown:\n%s", got)
	}
	if !strings.Contains(got, "read_file(path=main.go)") {
		t.Errorf("the call's arguments are not shown:\n%s", got)
	}
	// The call and its result must not look like the same thing twice.
	if strings.Count(got, "⚒") == 0 || !strings.Contains(got, "↳") {
		t.Errorf("call and result are not distinguishable:\n%s", got)
	}
}

// Long arguments would push the answer off screen, so they trim until asked for.
func TestLongCallArgumentsTrimUntilExpanded(t *testing.T) {
	a := toolApp(t)
	long := strings.Repeat("x", 200)
	a.cur.Turns = []store.Turn{
		{Role: "user", Content: "fetch it"},
		{Role: "assistant", Model: "llama3.2:3b", ToolCalls: []ollama.ToolCall{
			call("http_get", map[string]any{"url": "https://example.com/" + long}),
		}},
	}
	a.invalidateRenders()
	a.refreshTranscript()
	a.setFocus(focusTranscript)
	a.selTurn = 1

	collapsed := ansi.Strip(a.renderConversation())
	if strings.Contains(collapsed, long) {
		t.Fatal("a 200-character argument was shown in full")
	}
	if !strings.Contains(collapsed, "full arguments") {
		t.Errorf("nothing says the call can be unfolded:\n%s", collapsed)
	}

	a.onKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if got := ansi.Strip(a.renderConversation()); !strings.Contains(got, long) {
		t.Fatal("right did not unfold the call")
	}
	a.onKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	if got := ansi.Strip(a.renderConversation()); strings.Contains(got, long) {
		t.Fatal("left did not fold the call again")
	}
}

// A short result has nothing hidden, so it offers no unfold and pressing right is a no-op rather than a promise that does nothing.
func TestShortResultAlsoWaitsToBeShown(t *testing.T) {
	a := toolApp(t)
	a.cur.Turns = []store.Turn{
		{Role: "tool", ToolName: "now", Content: "Thursday, 13 August 2026, 17:37 CEST"},
	}
	a.invalidateRenders()
	a.refreshTranscript()
	a.setFocus(focusTranscript)
	a.selTurn = 0

	// Even a one-line result waits to be asked for, so every result behaves the same way rather than some appearing and some not.
	got := ansi.Strip(a.renderConversation())
	if strings.Contains(got, "Thursday") {
		t.Fatalf("a short result was shown without being asked for:\n%s", got)
	}
	if !strings.Contains(got, "1 line") || !strings.Contains(got, "show") {
		t.Fatalf("the row does not offer to show it:\n%s", got)
	}

	a.onKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if got := ansi.Strip(a.renderConversation()); !strings.Contains(got, "Thursday") {
		t.Fatalf("right did not show it:\n%s", got)
	}
}

// Watching a model think is worth seeing as it happens. Keeping it on screen afterwards is not: it is working-out rather than answer, and usually longer than the answer it produced.
//
// "Afterwards" is the moment the answer starts, not the moment the turn ends - the reasoning is finished by then, whatever the turn is still doing.
func TestReasoningFoldsWhenTheAnswerStarts(t *testing.T) {
	a := toolApp(t)
	reasoning := "First I consider this.\nThen I consider that.\nFinally I decide."
	a.cur.Turns = []store.Turn{
		{Role: "user", Content: "why?"},
		{Role: "assistant", Model: "llama3.2:3b", Thinking: reasoning},
	}
	a.streaming = true

	// Thinking, nothing said yet: it is on screen.
	a.invalidateRenders()
	if got := ansi.Strip(a.renderConversation()); !strings.Contains(got, "Finally I decide") {
		t.Fatalf("reasoning is hidden while it is still arriving:\n%s", got)
	}

	// The first word of the answer lands, still mid-turn: it folds.
	a.cur.Turns[1].Content = "Because"
	a.invalidateRenders()
	got := ansi.Strip(a.renderConversation())
	if strings.Contains(got, "Finally I decide") {
		t.Fatalf("reasoning stayed on screen once the answer started:\n%s", got)
	}
	if !strings.Contains(got, "reasoning") || !strings.Contains(got, "3 lines") {
		t.Fatalf("nothing says there is reasoning to see:\n%s", got)
	}
	if !strings.Contains(got, "Because") {
		t.Fatal("the answer was folded away with the reasoning")
	}

	// And it stays folded once the turn finishes.
	a.streaming = false
	a.invalidateRenders()
	if got := ansi.Strip(a.renderConversation()); strings.Contains(got, "Finally I decide") {
		t.Fatal("reasoning came back when the turn ended")
	}
}

func TestReasoningUnfoldsWithRight(t *testing.T) {
	a := toolApp(t)
	a.cur.Turns = []store.Turn{
		{Role: "user", Content: "why?"},
		{Role: "assistant", Model: "llama3.2:3b", Thinking: "Because of this.", Content: "Because."},
	}
	a.invalidateRenders()
	a.refreshTranscript()
	a.setFocus(focusTranscript)
	a.selTurn = 1

	if got := ansi.Strip(a.renderConversation()); !strings.Contains(got, "show") {
		t.Fatalf("nothing offers to show the reasoning:\n%s", got)
	}
	a.onKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if got := ansi.Strip(a.renderConversation()); !strings.Contains(got, "Because of this") {
		t.Fatalf("right did not show the reasoning:\n%s", got)
	}
	a.onKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	if got := ansi.Strip(a.renderConversation()); strings.Contains(got, "Because of this") {
		t.Fatal("left did not fold the reasoning away")
	}
}

// ctrl+g still removes reasoning entirely, rather than folding it.
func TestReasoningToggleStillHidesItCompletely(t *testing.T) {
	a := toolApp(t)
	a.cur.Turns = []store.Turn{
		{Role: "assistant", Model: "llama3.2:3b", Thinking: "hidden", Content: "answer"},
	}
	a.showThink = false
	a.invalidateRenders()

	if got := ansi.Strip(a.renderConversation()); strings.Contains(got, "reasoning") {
		t.Fatalf("reasoning is still mentioned with the display off:\n%s", got)
	}
}

// The selection gutter recolours decoration, never content. The token counts under a reply start with a digit, and that digit was being painted as if it were a gutter glyph.
func TestSelectionNeverPaintsContent(t *testing.T) {
	a := toolApp(t)
	a.cur.Turns = []store.Turn{
		{Role: "user", Content: "why?"},
		{Role: "assistant", Model: "llama3.2:3b", Content: "Because.",
			PromptCount: 708, EvalCount: 586},
	}
	a.invalidateRenders()
	a.refreshTranscript()
	a.setFocus(focusTranscript)
	a.selTurn = 1
	a.invalidateRenders()

	for line := range strings.SplitSeq(a.renderConversation(), "\n") {
		if !strings.Contains(ansi.Strip(line), "708 prompt") {
			continue
		}
		// The digit must not carry the selection colour, and the line must still be marked as selected - by a gutter beside it, not by painting its first character.
		if strings.Contains(line, selectionMark("7")) {
			t.Fatalf("the first digit was painted as a gutter: %q", line)
		}
		if !strings.HasPrefix(line, selectionBar()) {
			t.Fatalf("the counts lost their selection gutter: %q", line)
		}
		return
	}
	t.Fatal("the token counts are missing from the selected turn")
}

// Reasoning is faint italic by design, so its selection gutter matches it: same weight as the text and a muted amber, rather than the bold mark used beside ordinary content, which draws the eye to the margin instead of the message.
func TestReasoningGutterIsSofterThanTheRest(t *testing.T) {
	a := toolApp(t)
	a.cur.Turns = []store.Turn{
		{Role: "user", Content: "why?"},
		{Role: "assistant", Model: "llama3.2:3b", Content: "Because.",
			Thinking: "Step one.\nStep two."},
	}
	a.refreshTranscript()
	a.setFocus(focusTranscript)
	a.selTurn = 1
	a.expanded = map[int]bool{1: true}
	a.invalidateRenders()

	// Only the selected turn: the prompt above it has a violet speaker bar of its own, which is not a selection gutter.
	block, _ := a.renderTurn(&a.cur.Turns[1], 1, nil, new(int), false)

	var sawReasoning, sawBody bool
	for line := range strings.SplitSeq(block, "\n") {
		plain := ansi.Strip(line)
		switch {
		case strings.Contains(plain, "Step "):
			// Selected, reasoning carries the ordinary bar rather than its own ⋮, so a selection looks like a selection wherever it is - but in the softer tone, since the text beside it is faint.
			sawReasoning = true
			if !strings.HasPrefix(line, selectionMarkSoft("▌")) {
				t.Errorf("the reasoning gutter is not the soft bar: %q", line)
			}
			if strings.HasPrefix(line, selectionBar()) {
				t.Errorf("the reasoning gutter is the loud bar: %q", line)
			}
		case strings.Contains(plain, "Because."):
			sawBody = true
			if !strings.HasPrefix(line, selectionBar()) {
				t.Errorf("ordinary content lost the usual gutter: %q", line)
			}
		}
	}
	if !sawReasoning || !sawBody {
		t.Fatalf("expected both gutters in the render (reasoning=%v body=%v)", sawReasoning, sawBody)
	}
}

// Every palette defines the soft mark, or a theme switch would leave the reasoning gutter invisible.
func TestEverySoftMarkIsDefined(t *testing.T) {
	t.Cleanup(func() { theme.Use("midnight") })
	for _, name := range theme.Names() {
		theme.Use(name)
		if theme.AmberSoft == nil {
			t.Errorf("%s has no soft selection colour", name)
		}
		if theme.AmberSoft == theme.Amber {
			t.Errorf("%s: the soft mark is the same colour as the loud one", name)
		}
	}
}
