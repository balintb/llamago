package ui

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/balintb/llamago/internal/config"
	"github.com/balintb/llamago/internal/ollama"
	"github.com/balintb/llamago/internal/store"
	"github.com/balintb/llamago/internal/theme"
	"github.com/balintb/llamago/internal/tools"
)

// loadTools builds the registry: the built-ins first, then whatever is declared in the tools directory, which may deliberately replace one of them.
//
// A manifest that does not make sense is reported once and skipped. Failing to start over a malformed tool file would be a poor trade.
func (a *App) loadTools() {
	root, err := os.Getwd()
	if err != nil {
		root = "."
	}
	r := tools.NewRegistry()
	tools.Builtins(r, root)

	if dir, err := config.ToolsDir(); err == nil {
		manifests, errs := tools.Load(dir)
		for _, m := range manifests {
			r.Add(m)
		}
		a.toolErrs = errs
	}
	a.tools = r
}

// toolCapableInstalled names a model that could actually use tools, for when the current one cannot. Being told "unsupported" without being told what to switch to is only half an answer.
func (a *App) toolCapableInstalled() []string {
	var out []string
	for _, m := range a.models {
		if a.modelCanTools(m.Name) {
			out = append(out, shortModel(m.Name))
		}
	}
	return out
}

// toolsUnusableNote explains why tools will not run, or is empty when they will.
func (a *App) toolsUnusableNote() string {
	if !a.cfg.Tools || a.modelCanTools(a.cfg.Model) {
		return ""
	}
	note := shortModel(a.cfg.Model) + " cannot call tools"
	if others := a.toolCapableInstalled(); len(others) > 0 {
		note += " - try " + strings.Join(others, " or ")
	}
	return note
}

// toolListing is the one-line answer to "/tools", naming what is available and what is switched off.
func (a *App) toolListing() string {
	if !a.cfg.Tools {
		return "tools are off - /tools on"
	}
	enabled := a.enabledTools()
	names := make([]string, 0, len(enabled))
	for _, t := range enabled {
		names = append(names, t.Name())
	}
	if len(names) == 0 {
		return "every tool is switched off"
	}
	out := "tools: " + strings.Join(names, ", ")
	if note := a.toolsUnusableNote(); note != "" {
		out += " - " + note
	}
	return out
}

// toolsSummary is the note beside the TOOLS heading: whether the mechanism is on at all, and whether this model can use it. A row of checkboxes above a model that cannot call anything would be a puzzle.
func (a *App) toolsSummary() string {
	switch {
	case !a.cfg.Tools:
		return theme.Dim.Render("   off - turn on to let models call these")
	case !a.modelCanTools(a.cfg.Model):
		return lipgloss.NewStyle().Foreground(theme.Amber).Render("   " + a.toolsUnusableNote())
	case len(a.toolErrs) > 0:
		return theme.Err.Render(fmt.Sprintf("   %d manifests could not be loaded", len(a.toolErrs)))
	default:
		return theme.Dim.Render(fmt.Sprintf("   %d available", len(a.enabledTools())))
	}
}

// enabledTools is what a request may offer: everything registered except what was switched off.
func (a *App) enabledTools() []tools.Tool {
	if a.tools == nil {
		return nil
	}
	var out []tools.Tool
	for _, t := range a.tools.All() {
		if !a.cfg.ToolOff[t.Name()] {
			out = append(out, t)
		}
	}
	return out
}

// toolsForRequest is the list offered to the model, or nothing at all when the setting is off or the model cannot use them. Offering tools to a model without the capability wastes context on a list it will never call.
func (a *App) toolsForRequest() []ollama.Tool {
	if !a.cfg.Tools || a.tools == nil || !a.modelCanTools(a.cfg.Model) {
		return nil
	}
	enabled := a.enabledTools()
	out := make([]ollama.Tool, 0, len(enabled))
	for _, t := range enabled {
		out = append(out, t.Definition())
	}
	return out
}

// modelCanTools reports whether the active model advertises tool support.
func (a *App) modelCanTools(name string) bool {
	d, ok := a.details[name]
	if !ok {
		return false
	}
	return slices.Contains(d.Capabilities, "tools")
}

// --- running ----------------------------------------------------------------

// onToolCalls is where a reply asking for tools lands. Calls needing permission stop here and wait; the rest run straight away.
func (a *App) onToolCalls(calls []ollama.ToolCall) tea.Cmd {
	if a.toolStep >= max(1, a.cfg.ToolSteps) {
		return a.finishToolRound(fmt.Sprintf(
			"stopped after %d rounds of tool calls", a.toolStep))
	}
	a.pendingCalls = calls
	return a.runNextCall()
}

// runNextCall takes the next queued call, asking first when it needs asking.
func (a *App) runNextCall() tea.Cmd {
	for len(a.pendingCalls) > 0 {
		call := a.pendingCalls[0]
		a.pendingCalls = a.pendingCalls[1:]

		if a.cfg.ToolOff[call.Function.Name] {
			return a.recordToolResult(tools.Result{
				Name: call.Function.Name, Args: call.Function.Arguments,
				Err: fmt.Errorf("%s is switched off", call.Function.Name),
			})
		}
		t, ok := a.tools.Get(call.Function.Name)
		if !ok {
			// Unknown tools still produce a result, so the model learns the name was wrong rather than waiting on an answer that never comes.
			return a.recordToolResult(a.tools.Run(context.Background(), call))
		}
		if t.Safe() || a.toolAllowed[call.Function.Name] {
			return a.runToolCmd(call)
		}
		a.askToolPermission(call)
		return nil
	}
	// Every call in this round has an answer; ask the model to carry on.
	return a.continueAfterTools()
}

// askToolPermission puts the call in front of the user before it runs.
func (a *App) askToolPermission(call ollama.ToolCall) {
	a.confirm = confirmState{
		prompt: "Run " + describeCall(call) + "?",
		action: func() tea.Msg { return toolAllowedMsg{call: call} },
	}
	a.overlay = overlayConfirm
	a.deniedCall = &call
}

// describeCall renders a call the way it is shown when permission is asked. The arguments are the point: "read a file" is not a decision, "read ~/.ssh/id_rsa" is.
func describeCall(call ollama.ToolCall) string {
	if len(call.Function.Arguments) == 0 {
		return call.Function.Name + "()"
	}
	keys := make([]string, 0, len(call.Function.Arguments))
	for k := range call.Function.Arguments {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, call.Function.Arguments[k]))
	}
	return fmt.Sprintf("%s(%s)", call.Function.Name, strings.Join(parts, ", "))
}

// runToolCmd executes a call off the update loop, since a tool may take seconds.
func (a *App) runToolCmd(call ollama.ToolCall) tea.Cmd {
	registry := a.tools
	return func() tea.Msg {
		return toolResultMsg{result: registry.Run(context.Background(), call)}
	}
}

// denyToolCall answers the model as though the tool had refused, which is true: it was refused, and the model should say what it would do instead rather than wait for output that is not coming.
func (a *App) denyToolCall(call ollama.ToolCall) tea.Cmd {
	return a.recordToolResult(tools.Result{
		Name: call.Function.Name,
		Args: call.Function.Arguments,
		Err:  fmt.Errorf("the user did not allow this call"),
	})
}

// recordToolResult stores what a call produced and moves on to the next.
func (a *App) recordToolResult(res tools.Result) tea.Cmd {
	content := res.Output
	if res.Err != nil {
		content = res.Err.Error()
	}
	a.cur.Turns = append(a.cur.Turns, store.Turn{
		Role: "tool", ToolName: res.Name, ToolArgs: res.Args,
		Content: content, ToolFail: res.Err != nil, At: time.Now(),
		Total: res.Elapsed,
	})
	a.invalidateRenders()
	a.refreshTranscript()
	return a.runNextCall()
}

// continueAfterTools asks the model to answer now that the results are in.
func (a *App) continueAfterTools() tea.Cmd {
	a.toolStep++
	return a.generate()
}

// finishToolRound abandons the round with a note in the transcript, for when the step cap is reached.
func (a *App) finishToolRound(why string) tea.Cmd {
	a.pendingCalls = nil
	a.toolStep = 0
	if t := a.lastTurn(); t != nil && t.Role == "assistant" {
		t.Content = strings.TrimSpace(t.Content + "\n\n_(" + why + ")_")
	}
	a.invalidateRenders()
	a.refreshTranscript()
	return a.showToast(why, true)
}

// textToolCall matches a tool call a model wrote into its answer instead of asking for one properly: either a leaked special token, or the JSON it was trained to emit arriving as prose because the template never parsed it back.
var textToolCall = regexp.MustCompile(
	`<\|?tool_call\|?>|\{\s*"name"\s*:\s*"[a-zA-Z_][a-zA-Z0-9_]*"\s*,\s*"arguments"\s*:`)

// looksLikeTextToolCall reports whether a reply is a call in disguise.
func looksLikeTextToolCall(content string) bool {
	return textToolCall.MatchString(content)
}

// viewToolMisfire is the note under a reply that tried to call a tool in prose. Without it the answer is a wall of JSON and invented filenames that reads as llamago failing, when the model simply never asked for anything.
func (a *App) viewToolMisfire(t *store.Turn) string {
	if len(t.ToolCalls) > 0 || !a.cfg.Tools || !looksLikeTextToolCall(t.Content) {
		return ""
	}
	model := shortModel(t.Model)
	note := model + " wrote that call as text rather than asking for it, so nothing ran"
	if others := a.toolCapableInstalled(); len(others) > 0 {
		var better []string
		for _, name := range others {
			if name != model {
				better = append(better, name)
			}
		}
		if len(better) > 0 {
			note += " - try " + strings.Join(better, " or ")
		}
	}
	return lipgloss.NewStyle().Foreground(theme.Amber).Render("⚒ " + note)
}

// --- rendering --------------------------------------------------------------

// viewToolCalls draws what an assistant turn asked to have run. A call is part of the conversation - it is the question the result below answers - so it is shown rather than left as an empty turn with output appearing under it.
func (a *App) viewToolCalls(t *store.Turn, turnIdx int) string {
	if len(t.ToolCalls) == 0 {
		return ""
	}
	expanded := a.expanded[turnIdx]
	lines := make([]string, 0, len(t.ToolCalls))
	trimmed := false
	for _, c := range t.ToolCalls {
		text := describeCall(c)
		// Long arguments - a pasted URL, a body of text - would otherwise push the answer off screen.
		if !expanded && lipgloss.Width(text) > toolCallWidth {
			text = theme.Truncate(text, toolCallWidth)
			trimmed = true
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(theme.Cyan).Render("⚒ "+text))
	}
	if turnIdx == a.selTurn && trimmed {
		lines = append(lines, theme.Key.Render("→")+theme.Dim.Render(" full arguments"))
	}
	return strings.Join(lines, "\n")
}

// toolCallWidth is how much of a call is shown before it is trimmed.
const toolCallWidth = 60

// viewToolTurn draws a tool result: what ran, with what, and what came back.
func (a *App) viewToolTurn(t *store.Turn, turnIdx int) string {
	width := a.transcript.Width()

	// A different glyph from the call above it: ⚒ is the model asking, ↳ is what came back, so a pair reads as one exchange rather than as two of the same.
	icon, tint := "↳", theme.Teal
	if t.ToolFail {
		icon, tint = "✗", theme.Red
	}
	glyph := lipgloss.NewStyle().Foreground(tint).Render(icon)
	// Collapsed, the header is the whole turn, so the selection has to land on it: there is no body underneath to carry the gutter.
	if turnIdx == a.selTurn && !a.expanded[turnIdx] {
		glyph = selectionMark(icon)
	}
	head := glyph + lipgloss.NewStyle().Foreground(tint).Render(" "+t.ToolName) +
		theme.Dim.Render("  "+argSummary(t.ToolArgs))
	if t.Total > 0 {
		head += theme.Dim.Render("  " + shortDuration(t.Total))
	}
	head += a.viewStamp(t.At)

	body := strings.TrimSpace(t.Content)
	lines := strings.Split(body, "\n")

	// Collapsed by default, down to nothing: what ran is worth seeing in the history, what it returned is usually not, and a file read would otherwise bury the conversation it was part of.
	if !a.expanded[turnIdx] {
		head += theme.Dim.Render("  " + plural(len(lines), "line"))
		if body == "" {
			head = strings.TrimSuffix(head, theme.Dim.Render("  "+plural(len(lines), "line"))) +
				theme.Dim.Render("  no output")
		} else if turnIdx == a.selTurn {
			head += "  " + theme.Key.Render("→") + theme.Dim.Render(" show")
		}
		return head
	}
	if turnIdx == a.selTurn {
		head += "  " + theme.Key.Render("←") + theme.Dim.Render(" hide")
	}

	style := lipgloss.NewStyle().Foreground(theme.Faint).Width(max(1, width-2))
	block := head + "\n" + indent(style.Render(body), theme.Dim.Render("  "))
	if turnIdx == a.selTurn {
		block = markSelected(block)
	}
	return block
}

// plural renders a count with its unit, so "1 line" rather than "1 lines".
func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// argSummary is the one-line form of a call's arguments.
func argSummary(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, args[k]))
	}
	return theme.Truncate(strings.Join(parts, " "), 60)
}
