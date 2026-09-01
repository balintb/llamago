package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/ollama"
	"github.com/balintb/llamago/internal/store"
	"github.com/balintb/llamago/internal/theme"
)

// mdThrottle bounds how often markdown is re-rendered mid-stream. Glamour is fast but not free, and re-rendering per token wastes most of a core.
const mdThrottle = 90 * time.Millisecond

// tpsSample is the interval between throughput samples feeding the sparkline.
const tpsSample = 250 * time.Millisecond

// --- key handling -----------------------------------------------------------

func (a *App) onChatKey(msg tea.KeyPressMsg) tea.Cmd {
	if a.searching {
		return a.onSearchKey(msg)
	}
	switch msg.String() {
	case "ctrl+f":
		return a.openSearch()
	case "ctrl+i":
		return a.openImagePicker()
	case "ctrl+t":
		return a.openTextPicker()
	case "ctrl+x":
		return a.clearPending()
	case "ctrl+\\":
		return a.askCompareOpponent()
	case "ctrl+s":
		return a.exportCmd()
	case "ctrl+n":
		return a.newSession()
	case "ctrl+b":
		a.sidebar = !a.sidebar
		a.layout()
		a.refreshTranscript()
		return nil
	case "ctrl+g":
		a.showThink = !a.showThink
		a.refreshTranscript()
		return a.okToast(fmt.Sprintf("thinking %s", onOff(a.showThink)))
	case "tab":
		// While a command is being typed, tab completes it rather than moving the keyboard somewhere else.
		if a.focus == focusInput && a.completeSlash() {
			return nil
		}
		a.cycleFocus(1)
		return nil
	case "shift+tab":
		a.cycleFocus(-1)
		return nil
	case "esc":
		// Escalate: clear a search, then return to the composer, and only when esc has nothing left to do here hand the keyboard to the tab strip.
		switch {
		case a.searchQuery != "":
			a.closeSearch()
		case a.focus != focusInput:
			a.setFocus(focusInput)
		default:
			a.focusTabBar()
		}
		return nil
	case "ctrl+e":
		return a.regenerate()
	case "alt+e":
		return a.openNudge()
	case "ctrl+y":
		return a.copyLast()
	}

	switch a.focus {
	case focusInput:
		return a.onComposerKey(msg)
	case focusTranscript:
		return a.onTranscriptKey(msg)
	case focusSessions:
		return a.onSessionsKey(msg)
	}
	return nil
}

func (a *App) onComposerKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		return a.send()
	case "alt+enter", "shift+enter", "ctrl+j":
		a.input.InsertRune('\n')
		a.layout()
		a.refreshTranscript()
		return nil
	case "shift+backspace":
		// Clears the whole box wherever the cursor is, which ctrl+u - the textarea's own kill-to-start - does not.
		if a.input.Value() == "" {
			return nil
		}
		a.setComposer("")
		a.histIdx, a.histDraft = -1, ""
		return nil
	case "shift+up":
		return a.selectFromComposer()
	case "up":
		// Only recall from the top row, so up still walks a multi-line draft.
		if a.composerAtTop() {
			return a.recallPrompt(1)
		}
	case "down":
		if a.composerAtBottom() {
			return a.recallPrompt(-1)
		}
	}
	before := a.input.Height()
	var cmd tea.Cmd
	a.input, cmd = a.input.Update(msg)
	// Wrapping means the composer can change height without gaining a logical line, so track the rendered height and hand the difference to the transcript.
	if a.input.Height() != before {
		a.layout()
		a.refreshTranscript()
	}
	return cmd
}

func (a *App) onTranscriptKey(msg tea.KeyPressMsg) tea.Cmd {
	// Digits copy the code block carrying that label.
	if r := msg.Code; r >= '1' && r <= '9' && msg.Mod == 0 {
		return a.copyCodeBlock(int(r - '0'))
	}
	switch msg.String() {
	case "shift+up":
		return a.moveSelection(-1)
	case "shift+down":
		return a.moveSelection(1)
	case "y":
		return a.copySelected()
	case "Y":
		return a.copyConversation()
	case "m":
		return a.toggleMarkdown()
	case "enter":
		return a.editSelected()
	case "r":
		return a.rewindTo()
	case "f":
		return a.forkSelected()
	case "x":
		return a.deleteSelected()
	case "right", "l":
		return a.expandSelected(true)
	case "left", "h":
		return a.expandSelected(false)
	case "/":
		return a.openSearch()
	case "n":
		a.stepMatch(1)
		return nil
	case "N":
		a.stepMatch(-1)
		return nil
	case "v":
		return a.viewImageAt(false)
	case "o":
		return a.viewImageAt(true)
	case "j", "down":
		a.transcript.ScrollDown(1)
	case "k", "up":
		a.transcript.ScrollUp(1)
	case "d", "pgdown":
		a.transcript.HalfPageDown()
	case "u", "pgup":
		a.transcript.HalfPageUp()
	case "g", "home":
		a.transcript.GotoTop()
	case "G", "end":
		a.transcript.GotoBottom()
	}
	a.pinBottom = a.transcript.AtBottom()
	return nil
}

func (a *App) onSessionsKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "j", "down":
		a.sessionIdx = min(a.sessionIdx+1, len(a.sessions))
	case "k", "up":
		a.sessionIdx = max(a.sessionIdx-1, 0)
	case "enter":
		return a.openSelectedSession()
	case "d":
		return a.deleteSelectedSession()
	case "r":
		return a.renameSelectedSession()
	case "p":
		return a.pinSelectedSession()
	case "c":
		return a.duplicateSelectedSession()
	case "n":
		return a.newSession()
	}
	return nil
}

// pinSelectedSession keeps a session at the top of the list. The selection follows the session rather than the row, since pinning is what moves it.
func (a *App) pinSelectedSession() tea.Cmd {
	s := a.sessionAt(a.sessionIdx)
	if s == nil {
		return a.showToast("no session to pin", true)
	}
	s.Pinned = !s.Pinned
	if err := s.Save(); err != nil {
		return a.errToast(err)
	}
	store.Sort(a.sessions)
	for i, e := range a.sessions {
		if e.ID == s.ID {
			a.sessionIdx = i + 1
			break
		}
	}
	if s.Pinned {
		return a.okToast("pinned")
	}
	return a.okToast("unpinned")
}

// renameSelectedSession opens the title editor for the highlighted session. Titles are derived from the first prompt, which is a good guess often enough to be worth keeping and wrong often enough to be worth overriding.
func (a *App) renameSelectedSession() tea.Cmd {
	s := a.sessionAt(a.sessionIdx)
	if s == nil {
		return a.showToast("no session to rename", true)
	}
	a.overlay = overlayRename
	a.renameID = s.ID
	a.renameIn.SetValue(s.Title)
	a.renameIn.CursorEnd()
	return a.renameIn.Focus()
}

// applyRename commits the overlay's text to the session it was opened for.
func (a *App) applyRename() tea.Cmd {
	title := strings.TrimSpace(a.renameIn.Value())
	a.overlay = overlayNone
	a.renameIn.Blur()
	if title == "" {
		return a.showToast("a title cannot be empty", true)
	}
	target := a.cur
	if target == nil || target.ID != a.renameID {
		target = nil
		for _, s := range a.sessions {
			if s.ID == a.renameID {
				target = s
				break
			}
		}
	}
	if target == nil {
		return a.showToast("that session is gone", true)
	}
	target.Title = title
	if err := target.Save(); err != nil {
		return a.errToast(err)
	}
	return a.okToast("renamed")
}

// onSearchKey drives the find bar. Matches re-highlight as you type.
func (a *App) onSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		a.closeSearch()
		return nil
	case "enter":
		// Commit: keep the highlights but hand the keyboard back, so n and N cycle matches instead of being typed into the query.
		return a.commitSearch()
	case "ctrl+a":
		// The usual reason to widen a search is that this conversation did not have it, so keep the query and look everywhere.
		query := a.searchIn.Value()
		a.closeSearch()
		return a.searchAll(query)
	case "ctrl+f", "down", "ctrl+n":
		a.stepMatch(1)
		return nil
	case "up", "ctrl+p":
		a.stepMatch(-1)
		return nil
	}
	var cmd tea.Cmd
	a.searchIn, cmd = a.searchIn.Update(msg)
	a.setSearchQuery(a.searchIn.Value())
	return cmd
}

// --- prompt recall ----------------------------------------------------------

// composerAtTop reports whether the cursor sits on the composer's first visual row, counting soft-wrapped rows, where up means "recall" rather than "move".
func (a *App) composerAtTop() bool {
	return a.input.Line() == 0 && a.input.LineInfo().RowOffset == 0
}

func (a *App) composerAtBottom() bool {
	li := a.input.LineInfo()
	return a.input.Line() == a.input.LineCount()-1 && li.RowOffset == li.Height-1
}

// prompts lists this session's user prompts, oldest first.
func (a *App) prompts() []string {
	if a.cur == nil {
		return nil
	}
	out := make([]string, 0, len(a.cur.Turns))
	for _, t := range a.cur.Turns {
		if t.Role == "user" && strings.TrimSpace(t.Content) != "" {
			out = append(out, t.Content)
		}
	}
	return out
}

// recallPrompt walks the composer back through earlier prompts. dir is +1 for older and -1 for newer; stepping past the newest restores the draft that recall interrupted, which is usually an empty composer.
func (a *App) recallPrompt(dir int) tea.Cmd {
	hist := a.prompts()
	if len(hist) == 0 {
		return nil
	}
	// Down does nothing until recall has actually started, so it cannot wipe a draft the user is still typing.
	if a.histIdx < 0 {
		if dir < 0 {
			return nil
		}
		a.histDraft = a.input.Value()
	}

	switch idx := a.histIdx + dir; {
	case idx < 0:
		a.histIdx = -1
		a.setComposer(a.histDraft)
		a.histDraft = ""
	case idx >= len(hist):
		// Already at the oldest; hold there rather than wrapping around.
		return nil
	default:
		a.histIdx = idx
		a.setComposer(hist[len(hist)-1-idx])
	}
	return nil
}

// setComposer replaces the composer's contents, leaving the cursor at the end. The composer grows with its content, so the frame has to be re-laid out.
func (a *App) setComposer(s string) {
	a.input.SetValue(s)
	a.layout()
	a.refreshTranscript()
}

// --- transcript selection ---------------------------------------------------

// selectFromComposer hands the keyboard to the transcript with the newest message selected.
func (a *App) selectFromComposer() tea.Cmd {
	if a.cur == nil || len(a.cur.Turns) == 0 {
		return a.showToast("no messages yet", true)
	}
	a.setFocus(focusTranscript)
	a.selTurn = len(a.cur.Turns) - 1
	a.refreshTranscript()
	a.showSelected()
	return nil
}

// moveSelection steps through messages. dir is -1 for older and +1 for newer; past the newest the selection is dropped and the composer takes over again, which is the way back out without reaching for esc.
func (a *App) moveSelection(dir int) tea.Cmd {
	if a.cur == nil || len(a.cur.Turns) == 0 {
		return nil
	}
	if a.selTurn < 0 {
		return a.selectFromComposer()
	}
	idx := a.selTurn + dir
	if idx >= len(a.cur.Turns) {
		a.setFocus(focusInput)
		a.refreshTranscript()
		return nil
	}
	a.selTurn = max(idx, 0) // hold at the oldest rather than wrapping
	a.refreshTranscript()
	a.showSelected()
	return nil
}

// showSelected scrolls the selected message's header into view. Following the tail would fight that, so the transcript unpins while a selection is live.
func (a *App) showSelected() {
	if a.selTurn < 0 || a.selTurn >= len(a.turnLines) {
		return
	}
	a.pinBottom = false
	a.transcript.EnsureVisible(a.turnLines[a.selTurn], 0, 0)
}

func (a *App) selectedTurn() *store.Turn {
	if a.cur == nil || a.selTurn < 0 || a.selTurn >= len(a.cur.Turns) {
		return nil
	}
	return &a.cur.Turns[a.selTurn]
}

// selectedText is the selected message as plain text: the raw markdown that was written, falling back to the reasoning or the error for a turn that carries nothing else. kind names it for the confirmation toast.
func (a *App) selectedText() (text, kind string, ok bool) {
	t := a.selectedTurn()
	if t == nil {
		return "", "", false
	}
	body := strings.TrimSpace(t.Content)
	if body == "" {
		body = strings.TrimSpace(t.Thinking)
	}
	if body == "" {
		body = strings.TrimSpace(t.Err)
	}
	if body == "" {
		return "", "", false
	}
	kind = "response"
	if t.Role == "user" {
		kind = "prompt"
	}
	return body, kind, true
}

// expandSelected unfolds or folds whatever the selected turn keeps out of the way: a tool result, a long call's arguments, or reasoning. One state per turn rather than one per kind, so → means the same thing wherever it is pressed.
func (a *App) expandSelected(open bool) tea.Cmd {
	t := a.selectedTurn()
	if t == nil || (t.Role != "tool" && len(t.ToolCalls) == 0 && t.Thinking == "") {
		return nil
	}
	if a.expanded == nil {
		a.expanded = map[int]bool{}
	}
	if a.expanded[a.selTurn] == open {
		return nil
	}
	a.expanded[a.selTurn] = open
	a.invalidateRenders()
	a.refreshTranscript()
	a.showSelected()
	return nil
}

// promptBefore finds the user turn that produced turn i, which is i itself when the selection is already a prompt.
func (a *App) promptBefore(i int) int {
	for ; i >= 0; i-- {
		if a.cur.Turns[i].Role == "user" {
			return i
		}
	}
	return -1
}

// selectedPrompt is the index of the prompt the selection belongs to. Replies resolve to the prompt that produced them, since a reply is not a thing that can be re-asked on its own.
func (a *App) selectedPrompt() int {
	t := a.selectedTurn()
	if t == nil {
		return -1
	}
	if t.Role == "user" {
		return a.selTurn
	}
	return a.promptBefore(a.selTurn)
}

// editSelected puts the selected message's prompt back in the composer to be reworked and sent again, leaving the conversation as it stands.
func (a *App) editSelected() tea.Cmd {
	idx := a.selectedPrompt()
	if idx < 0 {
		if a.selectedTurn() != nil {
			return a.showToast("no prompt to edit there", true)
		}
		return nil
	}
	prompt := a.cur.Turns[idx].Content
	// Focus moves first: it is what drops the selection.
	a.setFocus(focusInput)
	a.setComposer(prompt)
	a.histIdx, a.histDraft = -1, ""
	return nil
}

// rewindTo asks again from the selected message, dropping everything after it. Regenerating only ever redoes the last exchange; this is how a thread that went wrong several turns back gets cut off there.
func (a *App) rewindTo() tea.Cmd {
	if a.streaming {
		return a.showToast("still generating - ctrl+c to stop", true)
	}
	idx := a.selectedPrompt()
	if idx < 0 {
		if a.selectedTurn() != nil {
			return a.showToast("no prompt to ask again from", true)
		}
		return nil
	}
	// Past the immediate exchange enough history is at stake to be worth confirming, and the count is the useful part of that question.
	if dropped := len(a.cur.Turns) - (idx + 1); dropped > 2 {
		a.confirm = confirmState{
			prompt: fmt.Sprintf("Drop the last %d messages and ask again?", dropped),
			action: func() tea.Msg { return rewindMsg{idx: idx} },
		}
		a.overlay = overlayConfirm
		return nil
	}
	return a.rewind(idx)
}

// rewind truncates the conversation to end at the prompt at idx, then asks.
func (a *App) rewind(idx int) tea.Cmd {
	if a.cur == nil || idx < 0 || idx >= len(a.cur.Turns) {
		return nil
	}
	a.cur.Turns = a.cur.Turns[:idx+1]
	a.selTurn = -1
	a.invalidateRenders()
	return a.generate()
}

// forkSelected opens a new chat carrying the conversation up to and including the selected message, leaving this one as it stands, so a tangent costs nothing to the thread it came from.
func (a *App) forkSelected() tea.Cmd {
	if a.cur == nil || a.selTurn < 0 {
		return nil
	}
	n := a.selTurn + 1
	a.openBranch(a.cur.Turns[:n], "")
	return a.okToast(fmt.Sprintf("forked %d messages into a new chat", n))
}

// duplicateSelectedSession copies a whole session and opens the copy, leaving the original where it was. Forking does this up to a chosen message; this is the same move over the entire thread, for taking a different direction without spending the one already in hand.
func (a *App) duplicateSelectedSession() tea.Cmd {
	src := a.sessionAt(a.sessionIdx)
	if src == nil {
		return a.showToast("no session to duplicate", true)
	}
	if len(src.Turns) == 0 {
		return a.showToast("that chat is empty", true)
	}
	a.openBranch(src.Turns, src.Title+" (copy)")
	return a.okToast("duplicated")
}

// openBranch starts a new session carrying a copy of turns and switches to it, saving whatever was open first. An empty title is derived from the first prompt the way any new session's is.
//
// The turns are copied rather than resliced: sharing a backing array would let the new session's first append write into the original's.
func (a *App) openBranch(turns []store.Turn, title string) *store.Session {
	carried := make([]store.Turn, len(turns))
	copy(carried, turns)

	if a.streaming {
		a.chatFeed.stop()
		a.streaming = false
	}
	_ = a.cur.Save()
	a.rememberSession(a.cur)

	s := a.freshSession(time.Now())
	s.Turns = carried
	if title != "" {
		s.Title = title
	}
	s.Touch() // titles it from the first prompt when nothing was given
	a.cur = s
	a.sessionIdx = 0
	a.setFocus(focusInput)
	a.pinBottom = true
	_ = a.cur.Save()
	a.rememberSession(a.cur)
	a.invalidateRenders()
	a.layout()
	a.refreshTranscript()
	return s
}

// toggleMarkdown drops the rich rendering for the raw text the model actually sent, which is the only way to tell a model that wrote bad markdown from a renderer that mangled good markdown.
func (a *App) toggleMarkdown() tea.Cmd {
	a.cfg.Markdown = !a.cfg.Markdown
	_ = a.cfg.Save()
	a.invalidateRenders()
	a.refreshTranscript()
	if a.cfg.Markdown {
		return a.okToast("markdown on")
	}
	return a.okToast("markdown off - showing raw text")
}

// deleteSelected drops the selected exchange from the conversation.
//
// A prompt and the reply it drew are one unit: deleting a prompt while leaving its answer behind produces a conversation the model can no longer read sensibly, since the answer would appear to respond to the prompt before it.
func (a *App) deleteSelected() tea.Cmd {
	if a.streaming {
		return a.showToast("still generating - ctrl+c to stop", true)
	}
	t := a.selectedTurn()
	if t == nil {
		return nil
	}
	from := a.selTurn
	if t.Role != "user" {
		from = a.promptBefore(a.selTurn)
	}
	if from < 0 {
		return a.showToast("nothing to delete there", true)
	}
	// Take the prompt and the answers that followed it, stopping at the next prompt so only one exchange goes.
	to := from + 1
	for to < len(a.cur.Turns) && a.cur.Turns[to].Role != "user" {
		to++
	}

	n := to - from
	a.confirm = confirmState{
		prompt: fmt.Sprintf("Delete this exchange (%d messages)?", n),
		action: func() tea.Msg { return dropTurnsMsg{from: from, to: to} },
	}
	a.overlay = overlayConfirm
	return nil
}

// dropTurns removes a half-open range of turns and keeps the selection somewhere sensible: on what took the deleted exchange's place, or the end.
func (a *App) dropTurns(from, to int) tea.Cmd {
	if a.cur == nil || from < 0 || to > len(a.cur.Turns) || from >= to {
		return nil
	}
	a.cur.Turns = append(a.cur.Turns[:from], a.cur.Turns[to:]...)
	a.selTurn = min(from, len(a.cur.Turns)-1)
	a.cur.Touch()
	_ = a.cur.Save()
	a.invalidateRenders()
	a.layout()
	a.refreshTranscript()
	if a.selTurn >= 0 {
		a.showSelected()
	}
	return a.okToast(fmt.Sprintf("deleted %d messages", to-from))
}

// copyConversation puts the whole thread on the clipboard as the markdown the export writes - headers, reasoning and stats included - so it can be pasted somewhere without going through a file first.
//
// It is bound to Y next to y rather than to ctrl+shift+y: most terminals cannot tell ctrl+shift+letter from ctrl+letter, so that binding would silently do nothing for most people.
func (a *App) copyConversation() tea.Cmd {
	if a.cur == nil || len(a.cur.Turns) == 0 {
		return a.showToast("nothing to copy yet", true)
	}
	md := a.cur.Markdown()
	return tea.Batch(tea.SetClipboard(md),
		a.okToast(fmt.Sprintf("copied the conversation, %d messages", len(a.cur.Turns))))
}

// copySelected copies the selected message rather than its styled rendering. With nothing selected it falls back to the last response, so the key is never a dead end.
func (a *App) copySelected() tea.Cmd {
	if a.selectedTurn() == nil {
		return a.copyLast()
	}
	body, kind, ok := a.selectedText()
	if !ok {
		return a.showToast("nothing to copy in that message", true)
	}
	return tea.Batch(tea.SetClipboard(body),
		a.okToast(fmt.Sprintf("copied %s, %d lines", kind, strings.Count(body, "\n")+1)))
}

// focusRing lists the focusable panes in tab order. The session list only participates while the sidebar is actually on screen.
func (a *App) focusRing() []focus {
	ring := []focus{focusInput, focusTranscript}
	if a.sidebarVisible() {
		ring = append(ring, focusSessions)
	}
	return ring
}

// cycleFocus steps through the ring; dir is +1 for tab, -1 for shift+tab.
func (a *App) cycleFocus(dir int) {
	ring := a.focusRing()
	cur := 0
	for i, f := range ring {
		if f == a.focus {
			cur = i
			break
		}
	}
	n := len(ring)
	a.setFocus(ring[((cur+dir)%n+n)%n])
}

// setFocus moves focus to pane f, keeping the composer's own focus state in step so the cursor is only ever shown when the composer has it.
func (a *App) setFocus(f focus) {
	if f == focusSessions && !a.sidebarVisible() {
		f = focusInput
	}
	// A selection only means anything while the transcript has the keyboard, so leaving drops it however focus moved - tab, esc or a new generation.
	if f != focusTranscript {
		a.selTurn = -1
	}
	a.focus = f
	if f == focusInput {
		a.input.Focus()
		return
	}
	a.input.Blur()
}

// --- session management -----------------------------------------------------

// freshSession builds a session whose id cannot collide with one already in hand. Ids are millisecond timestamps and double as filenames, so two sessions created inside the same millisecond - forking straight after opening a chat, say - would otherwise share a file and quietly overwrite each other.
func (a *App) freshSession(now time.Time) *store.Session {
	for {
		s := store.NewSession(a.cfg.Model, now)
		if !a.sessionIDTaken(s.ID) {
			return s
		}
		now = now.Add(time.Millisecond)
	}
}

func (a *App) sessionIDTaken(id string) bool {
	if a.cur != nil && a.cur.ID == id {
		return true
	}
	for _, s := range a.sessions {
		if s.ID == id {
			return true
		}
	}
	return false
}

func (a *App) newSession() tea.Cmd {
	if a.streaming {
		a.chatFeed.stop()
		a.streaming = false
	}
	if a.cur != nil {
		_ = a.cur.Save()
		a.rememberSession(a.cur)
	}
	a.cur = a.freshSession(time.Now())
	a.sessionIdx = 0
	a.input.Reset()
	a.setFocus(focusInput)
	a.layout()
	a.refreshTranscript()
	return nil
}

// rememberSession inserts or refreshes a session at the head of the list.
func (a *App) rememberSession(s *store.Session) {
	if len(s.Turns) == 0 {
		return
	}
	for i, e := range a.sessions {
		if e.ID == s.ID {
			a.sessions = append(a.sessions[:i], a.sessions[i+1:]...)
			break
		}
	}
	a.sessions = append([]*store.Session{s}, a.sessions...)
	store.Sort(a.sessions)
}

// sessionAt maps a sidebar row onto a session; row 0 is the "New chat" action.
func (a *App) sessionAt(idx int) *store.Session {
	if idx <= 0 || idx > len(a.sessions) {
		return nil
	}
	return a.sessions[idx-1]
}

func (a *App) openSelectedSession() tea.Cmd {
	s := a.sessionAt(a.sessionIdx)
	if s == nil {
		return a.newSession()
	}
	if a.streaming {
		a.chatFeed.stop()
		a.streaming = false
	}
	if a.cur != nil {
		_ = a.cur.Save()
		a.rememberSession(a.cur)
	}
	a.cur = s
	var cmd tea.Cmd
	if s.Model != "" {
		cmd = a.setModel(s.Model)
	}
	a.setFocus(focusInput)
	a.pinBottom = true
	a.refreshTranscript()
	return cmd
}

func (a *App) deleteSelectedSession() tea.Cmd {
	s := a.sessionAt(a.sessionIdx)
	if s == nil {
		return nil
	}
	a.confirm = confirmState{
		prompt: "Delete session “" + s.Title + "”?",
		action: func() tea.Msg {
			return sessionDeletedMsg{id: s.ID, err: s.Delete()}
		},
	}
	a.overlay = overlayConfirm
	return nil
}

// forgetSession removes a session from the in-memory list after deletion.
func (a *App) forgetSession(id string) {
	for i, e := range a.sessions {
		if e.ID == id {
			a.sessions = append(a.sessions[:i], a.sessions[i+1:]...)
			break
		}
	}
	a.sessionIdx = min(a.sessionIdx, len(a.sessions))
	if a.cur != nil && a.cur.ID == id {
		a.cur = a.freshSession(time.Now())
		a.refreshTranscript()
	}
}

// --- generation -------------------------------------------------------------

// send commits the composer's contents as a user turn and starts generation.
func (a *App) send() tea.Cmd {
	if a.streaming {
		return a.showToast("already generating - ctrl+c to stop", true)
	}
	if cmd, handled := a.runSlash(a.input.Value()); handled {
		return cmd
	}
	prompt := strings.TrimSpace(a.input.Value())
	// A loaded template still holding its blanks is almost never what was meant, and finding out costs a whole generation.
	if blanks := store.Placeholders(prompt); len(blanks) > 0 {
		return a.showToast("fill in "+strings.Join(blanks, ", ")+" first", true)
	}
	// An image on its own is a perfectly good prompt.
	if prompt == "" && len(a.pending) == 0 {
		return nil
	}
	if a.cfg.Model == "" {
		return a.showToast("no model selected - press ctrl+k, then “model”", true)
	}
	if a.isImageModel(a.cfg.Model) {
		return a.showToast(shortModel(a.cfg.Model)+" generates images and cannot chat", true)
	}
	a.input.Reset()
	// The sent prompt becomes the newest history entry, so recall starts over.
	a.histIdx, a.histDraft = -1, ""
	// Reset collapses the composer back to one row; re-lay out before the transcript is rendered so it reclaims the freed rows.
	a.layout()
	a.cur.Turns = append(a.cur.Turns, store.Turn{
		Role: "user", Content: prompt, At: time.Now(), Images: attachmentNames(a.pending),
	})
	a.pending = nil
	a.cur.Model = a.cfg.Model
	if a.cur.System == "" {
		a.cur.System = a.cfg.System
	}
	a.cur.Touch()
	a.layout()
	return a.generate()
}

// regenerate drops the last assistant turn and asks again from the same prompt.
func (a *App) regenerate() tea.Cmd {
	if a.streaming || a.cur == nil {
		return nil
	}
	n := len(a.cur.Turns)
	if n == 0 || a.cur.Turns[n-1].Role != "assistant" {
		return a.showToast("nothing to regenerate", true)
	}
	// A turn that failed carries no answer to replace, so this is a retry of a request that never landed rather than a second opinion on one that did.
	if a.cur.Turns[n-1].Err != "" && a.cfg.Model == "" {
		return a.showToast("no model selected", true)
	}
	a.cur.Turns = a.cur.Turns[:n-1]
	a.invalidateRenders()
	return a.generate()
}

// openNudge asks for a one-line instruction to regenerate under. alt+e rather than ctrl+shift+e, which most terminals cannot distinguish from ctrl+e.
func (a *App) openNudge() tea.Cmd {
	if a.streaming {
		return a.showToast("still generating - ctrl+c to stop", true)
	}
	if t := a.lastTurn(); t == nil || t.Role != "assistant" {
		return a.showToast("nothing to regenerate", true)
	}
	a.overlay = overlayNudge
	a.nudgeIn.SetValue("")
	return a.nudgeIn.Focus()
}

// applyNudge regenerates under the instruction just typed. An empty one is an ordinary regenerate rather than an error.
func (a *App) applyNudge() tea.Cmd {
	a.overlay = overlayNone
	a.nudgeIn.Blur()
	a.nudge = strings.TrimSpace(a.nudgeIn.Value())
	return a.regenerate()
}

// systemPrompt is the prompt this conversation is held under: its own if it was started with one, otherwise whatever is configured now.
//
// A session stamps the prompt in force when it starts, so editing the global one later does not quietly change the persona of a thread already underway. Sessions started without any prompt keep following the global setting, which is both the older behaviour and the less surprising one.
func (a *App) systemPrompt() string {
	if a.cur != nil && a.cur.System != "" {
		return a.cur.System
	}
	return a.cfg.System
}

// requestMessages is what the next request carries: the conversation, plus any pending nudge as a trailing system message.
//
// The nudge steers one generation without joining the conversation. It is consumed here rather than stored on a turn, so it cannot influence later replies or turn up in an export.
func (a *App) requestMessages() []ollama.Message {
	msgs := a.cur.Messages(a.systemPrompt())
	if a.nudge != "" {
		msgs = append(msgs, ollama.Message{Role: "system", Content: a.nudge})
		a.nudge = ""
	}
	return msgs
}

// generate opens an assistant turn and starts streaming into it.
func (a *App) generate() tea.Cmd {
	req := ollama.ChatRequest{
		Model:     a.cfg.Model,
		Tools:     a.toolsForRequest(),
		Messages:  a.requestMessages(),
		KeepAlive: a.cfg.KeepAlive,
		Options:   a.cfg.Options(),
	}
	// Send the setting explicitly for capable models. Omitting it leaves the server on its default, which for a reasoning model means it thinks regardless of what the user asked for.
	if a.modelCanThink(a.cfg.Model) {
		think := a.cfg.Think
		req.Think = &think
	}

	a.cur.Turns = append(a.cur.Turns, store.Turn{Role: "assistant", Model: a.cfg.Model, At: time.Now()})
	a.streaming = true
	a.gen++
	a.startedAt = time.Now()
	a.sampleAt = a.startedAt
	a.ttft = 0
	a.tokens, a.sampleTok = 0, 0
	a.tps = a.tps[:0]
	a.pinBottom = true
	a.setFocus(focusInput)
	a.refreshTranscript()

	return tea.Batch(a.startChat(req, a.gen), a.spinner.Tick)
}

func (a *App) stopGeneration() tea.Cmd {
	if !a.streaming {
		return nil
	}
	a.chatFeed.stop()
	a.streaming = false
	if t := a.lastTurn(); t != nil && t.Role == "assistant" {
		if strings.TrimSpace(t.Content) == "" {
			// Nothing arrived yet - drop the empty turn instead of leaving a hole.
			a.cur.Turns = a.cur.Turns[:len(a.cur.Turns)-1]
		} else {
			t.Content += "\n\n_(stopped)_"
		}
	}
	a.invalidateRenders()
	a.refreshTranscript()
	return a.okToast("stopped")
}

func (a *App) onChatChunk(msg chatChunkMsg) tea.Cmd {
	// Chunks from a superseded generation are stale; drop them but keep draining so the producer goroutine can finish.
	if msg.gen != a.gen || !a.streaming {
		return waitChat(a.chatFeed, msg.gen, sideChat)
	}
	t := a.lastTurn()
	if t == nil {
		return waitChat(a.chatFeed, msg.gen, sideChat)
	}
	if a.ttft == 0 && (msg.chunk.Message.Content != "" || msg.chunk.Message.Thinking != "") {
		a.ttft = time.Since(a.startedAt)
	}
	t.Content += msg.chunk.Message.Content
	t.Thinking += msg.chunk.Message.Thinking
	t.ToolCalls = append(t.ToolCalls, msg.chunk.Message.ToolCalls...)

	if msg.chunk.Done {
		t.TokensPerSec = msg.chunk.TokensPerSecond()
		t.EvalCount = msg.chunk.EvalCount
		t.PromptCount = msg.chunk.PromptEvalCount
		t.TTFT = a.ttft
		t.Total = time.Duration(msg.chunk.TotalDuration)
	}
	// Sample throughput on a fixed interval so the sparkline shows the current rate rather than a cumulative average, which barely moves.
	if msg.chunk.Message.Content != "" || msg.chunk.Message.Thinking != "" {
		a.tokens++
	}
	if d := time.Since(a.sampleAt); d >= tpsSample {
		a.tps = append(a.tps, float64(a.tokens-a.sampleTok)/d.Seconds())
		if len(a.tps) > 48 {
			a.tps = a.tps[1:]
		}
		a.sampleAt, a.sampleTok = time.Now(), a.tokens
	}
	a.refreshTranscript()
	return waitChat(a.chatFeed, msg.gen, sideChat)
}

func (a *App) onChatEnd(msg chatEndMsg) tea.Cmd {
	if msg.gen != a.gen {
		return nil
	}
	a.streaming = false
	if msg.err != nil && msg.err.Error() != "context canceled" {
		if t := a.lastTurn(); t != nil && t.Role == "assistant" && t.Content == "" {
			t.Err = msg.err.Error()
		}
		a.invalidateRenders()
		a.refreshTranscript()
		return a.errToast(msg.err)
	}
	// A reply asking for tools is not the end of the turn: the calls run, the results go back, and the model answers again.
	if t := a.lastTurn(); t != nil && len(t.ToolCalls) > 0 {
		a.invalidateRenders()
		a.refreshTranscript()
		return a.onToolCalls(t.ToolCalls)
	}
	a.toolStep = 0

	a.cur.Touch()
	a.rememberSession(a.cur)
	_ = a.cur.Save()
	a.invalidateRenders()
	a.refreshTranscript()
	// A finished generation usually means a newly resident model.
	return tea.Batch(a.psCmd(), a.autoTitleCmd(a.cur))
}

func (a *App) lastTurn() *store.Turn {
	if a.cur == nil || len(a.cur.Turns) == 0 {
		return nil
	}
	return &a.cur.Turns[len(a.cur.Turns)-1]
}

func (a *App) copyLast() tea.Cmd {
	for i := len(a.cur.Turns) - 1; i >= 0; i-- {
		if t := a.cur.Turns[i]; t.Role == "assistant" && t.Content != "" {
			return tea.Batch(tea.SetClipboard(t.Content), a.okToast("copied response to clipboard"))
		}
	}
	return a.showToast("no response to copy", true)
}

// modelCanThink reports whether the server advertised a thinking capability. Details are fetched lazily, so an unknown model is assumed not to think.
func (a *App) modelCanThink(name string) bool {
	d, ok := a.details[name]
	return ok && d.CanThink()
}

// --- rendering --------------------------------------------------------------

// refreshTranscript re-renders the conversation into the viewport, keeping the view pinned to the bottom while the user hasn't scrolled away.
func (a *App) refreshTranscript() {
	if !a.ready {
		return
	}
	content := a.renderConversation()
	// Anchor short conversations to the bottom so the newest turn sits just above the composer instead of floating at the top of an empty pane.
	if a.cur != nil && len(a.cur.Turns) > 0 {
		if pad := a.transcript.Height() - lipgloss.Height(content); pad > 0 {
			content = strings.Repeat("\n", pad) + content
			for i := range a.placements {
				a.placements[i].line0 += pad
				a.placements[i].line1 += pad
			}
			for i := range a.turnLines {
				a.turnLines[i] += pad
			}
		}
	}
	// Highlighting happens on the final content, after bottom padding, so the recorded line numbers match what the viewport will scroll to.
	content, a.searchHits = highlightMatches(content, a.searchQuery, a.searchIdx, sideChat)

	a.transcript.SetContent(content)
	// Following the tail would fight the jump to a match.
	if a.pinBottom && a.searchQuery == "" {
		a.transcript.GotoBottom()
	}
}

func (a *App) renderConversation() string {
	if a.cur == nil || (len(a.cur.Turns) == 0 && len(a.pending) == 0) {
		return a.renderEmptyState()
	}
	code := a.codeBlocks()
	a.placements = a.placements[:0]
	a.turnLines = a.turnLines[:0]
	// Regenerating and stopping both truncate the conversation, so a selection made earlier can be left pointing past the end.
	if a.selTurn >= len(a.cur.Turns) {
		a.selTurn = len(a.cur.Turns) - 1
	}

	out := make([]string, 0, len(a.cur.Turns)+1)
	line, imgN := 0, 0
	for i := range a.cur.Turns {
		a.turnLines = append(a.turnLines, line)
		streaming := a.streaming && i == len(a.cur.Turns)-1
		block, placed := a.renderTurn(&a.cur.Turns[i], i, code, &imgN, streaming)
		// Placements come back relative to the block; lift them to content coordinates so a click can be resolved without re-rendering.
		for _, p := range placed {
			p.line0 += line
			p.line1 += line
			a.placements = append(a.placements, p)
		}
		out = append(out, block)
		line += lipgloss.Height(block) + 1 // the blank line between turns
	}

	if block, placed := a.renderPending(a.transcript.Width()); block != "" {
		for _, p := range placed {
			p.line0 += line
			p.line1 += line
			a.placements = append(a.placements, p)
		}
		out = append(out, block)
	}
	return strings.Join(out, "\n\n")
}

// renderEmptyState is the first thing a user sees: a wordmark and the few keys that matter most.
func (a *App) renderEmptyState() string {
	w := a.transcript.Width()
	logo := theme.GradientBold(`
 ██╗     ██╗      █████╗ ███╗   ███╗ █████╗  ██████╗  ██████╗
 ██║     ██║     ██╔══██╗████╗ ████║██╔══██╗██╔════╝ ██╔═══██╗
 ██║     ██║     ███████║██╔████╔██║███████║██║  ███╗██║   ██║
 ██║     ██║     ██╔══██║██║╚██╔╝██║██╔══██║██║   ██║██║   ██║
 ███████╗███████╗██║  ██║██║ ╚═╝ ██║██║  ██║╚██████╔╝╚██████╔╝
 ╚══════╝╚══════╝╚═╝  ╚═╝╚═╝     ╚═╝╚═╝  ╚═╝ ╚═════╝  ╚═════╝`, theme.Brand...)

	// Pad the tips to a common width so the block centers as a unit; otherwise lipgloss centers each line on its own and the keys end up ragged.
	//
	// A narrow pane gets the keys without their explanations rather than the explanations cut off mid-sentence.
	tips := [][2]string{
		{"ctrl+k", "command palette - everything lives here"},
		{"alt+2", "browse and pull models"},
		{"ctrl+n", "start a fresh chat"},
		{"f1", "full keymap"},
	}
	if w < 52 {
		tips = [][2]string{
			{"ctrl+k", "palette"},
			{"alt+2", "models"},
			{"ctrl+n", "new chat"},
			{"f1", "keys"},
		}
	}
	lines := make([]string, 0, len(tips))
	for _, t := range tips {
		lines = append(lines, theme.Key.Render(theme.Pad(t[0], 8))+
			theme.Dim.Render(theme.Truncate(t[1], max(4, w-10))))
	}
	body := blockOf(lines...)

	if a.cfg.Model == "" {
		body = blockOf(
			theme.Err.Render("No model available."),
			theme.Dim.Render("Press ")+theme.Key.Render("alt+2")+
				theme.Dim.Render(" then ")+theme.Key.Render("p")+theme.Dim.Render(" to pull one."),
			"",
		) + "\n" + body
	}

	// The wordmark where there is room for it, and the name itself where there is not: an opening screen with no name on it is just a list of keys.
	mark := smallWordmark()
	if w >= 66 && a.transcript.Height() >= 16 {
		mark = logo
	}
	ver := theme.Dim.Render(Version)
	content := mark + "\n" + ver + "\n\n" + body

	// The credit is pinned to the last line rather than sitting in the centered block, so it reads as a footer and stays out of the way of the tips. It is the least important thing here, so it is dropped rather than allowed to overflow: past the pane's height the viewport would just clip it off.
	h := max(1, a.transcript.Height())
	if h < lipgloss.Height(content)+2 {
		return lipgloss.Place(max(1, w), h, lipgloss.Center, lipgloss.Center, content)
	}
	credit := theme.Dim.Render("made with ") +
		lipgloss.NewStyle().Foreground(theme.Magenta).Render("♥") +
		theme.Dim.Render(" by balintb")
	return lipgloss.Place(max(1, w), h-1, lipgloss.Center, lipgloss.Center, content) + "\n" +
		lipgloss.PlaceHorizontal(max(1, w), lipgloss.Center, credit)
}

// smallWordmark is the name for a window too small for the block letters. The letters are spaced out so it still reads as a mark rather than as the first word of a sentence.
func smallWordmark() string {
	return theme.GradientBold("l l a m a g o", theme.Brand...)
}

// renderTurn draws one message: a speaker header plus its styled body. blocks is the conversation-wide code block list, used to label this turn's blocks with their copy numbers.
func (a *App) renderTurn(t *store.Turn, turnIdx int, blocks []codeBlock, imgN *int, streaming bool) (string, []imagePlacement) {
	width := a.transcript.Width()

	if t.Role == "tool" {
		return a.viewToolTurn(t, turnIdx), nil
	}

	if t.Role == "user" {
		// Selecting recolours the speaker bar rather than moving anything, so the message does not shift under the cursor.
		bar := lipgloss.NewStyle().Foreground(theme.Violet).Render("▌")
		if turnIdx == a.selTurn {
			bar = selectionBar()
		}
		head := bar + " " + lipgloss.NewStyle().Foreground(theme.Violet).Bold(true).Render("you") +
			a.viewStamp(t.At)
		parts := []string{head}
		if strings.TrimSpace(t.Content) != "" {
			body := lipgloss.NewStyle().Foreground(theme.Text).Width(max(1, width-2)).Render(t.Content)
			parts = append(parts, indent(body, bar+" "))
		}

		var placed []imagePlacement
		for _, name := range t.Images {
			*imgN++
			art, cols, rows := a.renderThumbnail(name, *imgN, max(8, width-2))
			// The gutter is two cells, and the caption sits under the art.
			line0 := 0
			for _, p := range parts {
				line0 += lipgloss.Height(p)
			}
			placed = append(placed, imagePlacement{
				ref:   imageRef{turn: turnIdx, name: name},
				line0: line0, line1: line0 + rows,
				col0: 2, col1: 2 + cols,
			})
			parts = append(parts, indent(art, "  "))
		}
		if line := a.viewTurnCost(t, turnIdx); line != "" {
			parts = append(parts, line)
		}
		return strings.Join(parts, "\n"), placed
	}

	// Assistant header: model name, then timing once the server reports it.
	dot := lipgloss.NewStyle().Foreground(theme.Teal).Render("●")
	if streaming {
		dot = a.spinner.View()
	}
	head := dot + " " + lipgloss.NewStyle().Foreground(theme.Teal).Bold(true).Render(shortModel(t.Model))
	if t.TokensPerSec > 0 {
		head += theme.Dim.Render(fmt.Sprintf("  %.0f tok/s · %d tokens", t.TokensPerSec, t.EvalCount))
		if t.TTFT > 0 {
			head += theme.Dim.Render(fmt.Sprintf(" · %s to first token", shortDuration(t.TTFT)))
		}
	} else if streaming {
		head += theme.Dim.Render("  " + a.liveStats())
	}
	head += a.viewStamp(t.At)

	var parts []string
	if t.Err != "" {
		// A failure is a dead end unless the way out is on screen: the only alternative anyone finds on their own is retyping the prompt.
		line := theme.Err.Render("✗ "+t.Err) + theme.Dim.Render("  ") +
			theme.Key.Render("ctrl+e") + theme.Dim.Render(" try again")
		if turnIdx != len(a.cur.Turns)-1 {
			line = theme.Err.Render("✗ "+t.Err) + theme.Dim.Render("  ") +
				theme.Key.Render("shift+↑ then r") + theme.Dim.Render(" try again")
		}
		parts = append(parts, line)
	}
	if t.Thinking != "" && a.showThink {
		// Still thinking only until the first word of the answer arrives: the reasoning is finished at that point, whatever the turn is still doing.
		thinking := streaming && strings.TrimSpace(t.Content) == ""
		parts = append(parts, a.renderThinking(t.Thinking, turnIdx, thinking, width))
	}
	if body := a.renderBody(t.Content, streaming); body != "" {
		parts = append(parts, body)
	}
	if len(parts) == 0 && streaming {
		parts = append(parts, theme.Dim.Render("thinking…"))
	}
	// What it asked for. Without this the turn is an empty header: the results appear below with nothing saying what was requested or with what.
	if calls := a.viewToolCalls(t, turnIdx); calls != "" {
		parts = append(parts, calls)
	}
	if note := a.viewToolMisfire(t); note != "" {
		parts = append(parts, note)
	}
	if line := a.viewTurnCost(t, turnIdx); line != "" {
		parts = append(parts, indent(line, "  "))
	}
	if bar := a.viewCodeBlockBar(turnIdx, blocks); bar != "" {
		parts = append(parts, bar)
	}
	block := head + "\n" + strings.Join(parts, "\n")
	if turnIdx == a.selTurn {
		block = markSelected(block)
	}
	return block, nil
}

// gutterGlyphs are the characters a line may open with that are decoration rather than content, and so may be recoloured to carry the selection.
var gutterGlyphs = map[rune]bool{
	'⋮': true, // reasoning
	'⌗': true, // code block bar
	'⚒': true, // a tool call
	'↳': true, // a tool result
	'✗': true, // a failure
	'▌': true, // a speaker bar
	'●': true, // a model
}

// markSelected draws the selection gutter down a reply. It starts on the line below the header, so the bar lands in the column the speaker dot occupies, and it replaces the blank that the reply's own left margin already leaves there rather than pushing the text right: selecting must not reflow anything.
func markSelected(block string) string {
	bar := selectionBar()
	lines := strings.Split(block, "\n")
	for i := 1; i < len(lines); i++ {
		plain := ansi.Strip(lines[i])
		if plain == "" {
			// Blank lines between paragraphs carry the gutter too.
			lines[i] = bar
			continue
		}
		mark := bar
		if first := []rune(plain)[0]; first != ' ' {
			// Reasoning carries its own faint styling, so its gutter matches it rather than shouting over it.
			if first == '⋮' {
				lines[i] = selectionMarkSoft("▌") + ansi.TruncateLeft(lines[i], 1, "")
				continue
			}
			// A line opening with a marker of its own keeps its glyph and takes the selection colour, so the gutter runs unbroken.
			//
			// Only a known marker, though: a line starting with content - the token counts under a reply, say - would otherwise have its first character painted as if it were a gutter.
			if !gutterGlyphs[first] {
				continue
			}
			mark = selectionMark(string(first))
		}
		// The mark is prepended rather than handed to TruncateLeft: that keeps the line's own opening sequence after the mark's reset, so the text behind the gutter holds its styling.
		lines[i] = mark + ansi.TruncateLeft(lines[i], 1, "")
	}
	return strings.Join(lines, "\n")
}

// viewStamp renders a turn's time for its header, or nothing while timestamps are switched off. Clock time for today and the date beyond it: a session reopened next week should not claim its turns happened this afternoon.
func (a *App) viewStamp(at time.Time) string {
	if !a.cfg.Timestamps || at.IsZero() {
		return ""
	}
	layout := "15:04"
	if now := time.Now(); at.Year() != now.Year() || at.YearDay() != now.YearDay() {
		layout = "Jan 2 15:04"
	}
	return theme.Dim.Render("  " + at.Format(layout))
}

// viewTurnCost is the token accounting for the selected message, and nothing at all for the rest: it answers "which exchange is eating the window" without putting a number on every line of the transcript.
//
// A reply reports what the server counted. A prompt has no count of its own - the server bills the whole context it was sent with - so it is estimated and labelled as such.
func (a *App) viewTurnCost(t *store.Turn, turnIdx int) string {
	if turnIdx != a.selTurn {
		return ""
	}
	if t.Role == "user" {
		n := approxTokens(t.Content) + approxTokens(t.Thinking) + len(t.Images)*tokensPerImage
		if n == 0 {
			return ""
		}
		return theme.Dim.Render(fmt.Sprintf("~%d tokens", n))
	}
	if t.EvalCount == 0 && t.PromptCount == 0 {
		return ""
	}
	parts := []string{fmt.Sprintf("%d prompt", t.PromptCount), fmt.Sprintf("%d response", t.EvalCount)}
	if t.Total > 0 {
		parts = append(parts, shortDuration(t.Total)+" total")
	}
	return theme.Dim.Render(strings.Join(parts, " · ") + " tokens")
}

// renderThinking shows the model's reasoning channel as a dimmed, quoted block while it arrives, and folds it away once it has.
//
// Watching a model think is worth seeing as it happens; keeping it on screen afterwards is not, since it is working-out rather than answer, and it is usually longer than the answer it produced.
func (a *App) renderThinking(text string, turnIdx int, thinking bool, width int) string {
	text = strings.TrimSpace(text)
	style := lipgloss.NewStyle().Foreground(theme.Faint).Italic(true).Width(max(1, width-2))
	full := func(label string) string {
		return label + "\n" + indent(style.Render(text), theme.Dim.Render("⋮ "))
	}

	// Still arriving: show it, and say so.
	if thinking {
		return full(theme.Dim.Render("⋮ reasoning…"))
	}

	glyph := theme.Dim.Render("⋮")
	if turnIdx == a.selTurn {
		glyph = selectionMarkSoft("▌")
	}
	if a.expanded[turnIdx] {
		label := glyph + theme.Dim.Render(" reasoning")
		if turnIdx == a.selTurn {
			label += "  " + theme.Key.Render("←") + theme.Dim.Render(" hide")
		}
		return full(label)
	}

	label := glyph + theme.Dim.Render(" reasoning  "+plural(len(strings.Split(text, "\n")), "line"))
	if turnIdx == a.selTurn {
		label += "  " + theme.Key.Render("→") + theme.Dim.Render(" show")
	}
	return label
}

// renderBody renders message content as markdown, with a cache for settled turns and a time-based throttle while streaming.
func (a *App) renderBody(content string, streaming bool) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	if !a.cfg.Markdown || a.md == nil {
		return lipgloss.NewStyle().Foreground(theme.Text).
			Width(max(1, a.transcript.Width())).Render(content)
	}

	if !streaming {
		key := fmt.Sprintf("%d\x00%s", a.mdWidth, content)
		if cached, ok := a.renderCache[key]; ok {
			return cached
		}
		out := a.markdown(content)
		a.renderCache[key] = out
		return out
	}

	// Mid-stream: re-render on a timer, otherwise reuse the last frame with the new tail appended so text still appears immediately.
	if time.Since(a.lastRender) < mdThrottle {
		if cached, ok := a.renderCache["\x00live"]; ok {
			return cached
		}
	}
	a.lastRender = time.Now()
	out := a.markdown(content)
	a.renderCache["\x00live"] = out
	return out
}

func (a *App) markdown(content string) string {
	out, err := a.md.Render(content)
	if err != nil {
		return lipgloss.NewStyle().Foreground(theme.Text).
			Width(max(1, a.transcript.Width())).Render(content)
	}
	// Glamour pads with a leading and trailing blank line; the transcript already spaces turns apart.
	return strings.Trim(out, "\n")
}

// liveStats is the running throughput readout shown while generating.
func (a *App) liveStats() string {
	if a.startedAt.IsZero() {
		return ""
	}
	s := shortDuration(time.Since(a.startedAt))
	if n := len(a.tps); n > 0 {
		s += fmt.Sprintf(" · %.0f tok/s %s", a.tps[n-1], sparkline(a.tps))
	}
	return s
}

// --- chat layout ------------------------------------------------------------

func (a *App) viewChat() string {
	bodyWidth := a.bodyWidth()

	transcript := panel(a.transcriptView(), bodyWidth, a.transcriptPanelHeight(),
		a.focus == focusTranscript)
	composer := panel(a.composerView(), bodyWidth, a.inputPanelHeight(),
		a.focus == focusInput)

	body := lipgloss.JoinVertical(lipgloss.Left, transcript, composer, a.viewComposerHint(bodyWidth))
	if !a.sidebarVisible() {
		return body
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, a.viewSidebar(), body)
}

// attachScrollbar pins a one-cell scrollbar column to the right edge of a rendered view. The gutter is reserved permanently and drawn blank when everything fits, so text never reflows as the bar appears and disappears.
func attachScrollbar(view string, width, height int, bar []string) string {
	lines := strings.Split(view, "\n")
	out := make([]string, max(0, height))
	for i := range out {
		var line string
		if i < len(lines) {
			line = lines[i]
		}
		out[i] = theme.Pad(line, width) + bar[i]
	}
	return strings.Join(out, "\n")
}

// transcriptView renders the transcript with its scrollbar.
func (a *App) transcriptView() string {
	h := a.transcript.Height()
	bar := scrollbarColumn(h, a.transcript.TotalLineCount(), a.transcript.YOffset(),
		a.focus == focusTranscript)
	return attachScrollbar(a.transcript.View(), a.transcript.Width(), h, bar)
}

// composerView renders the composer with its scrollbar.
//
// Whether there is anything to scroll comes from the height the widget settled on, not from its scroll percentage: below the cap it sized itself to the content, so the text fits by construction. The percentage cannot carry that decision on its own because the widget reports 0 for content that fits as well as for content parked at the top.
//
// The textarea exposes no total line count, so the thumb is a single row placed by the percentage, which is accurate once the content has overflowed. Content that lands on exactly the cap therefore shows a thumb it does not strictly need; that is the one imprecision, and it errs toward showing the affordance.
func (a *App) composerView() string {
	// Restyle before measuring: command mode only changes colour, but the style has to be in place before the view is rendered from it.
	styleComposer(&a.input, slashRecognized(a.input.Value()))
	h := a.input.Height()
	scrollable := h >= a.input.MaxHeight
	bar := scrollbarFromPercent(h, a.input.ScrollPercent(), scrollable, a.focus == focusInput)
	return attachScrollbar(a.input.View(), a.input.Width(), h, bar)
}

// scrollbarGlyphs returns the styled track and thumb. The track is dashed so it reads as a scrollbar rather than a second pane border.
func scrollbarGlyphs(focused bool) (track, thumb string) {
	thumbColor := theme.Muted
	if focused {
		thumbColor = theme.Violet
	}
	return lipgloss.NewStyle().Foreground(theme.Faint).Render("┊"),
		lipgloss.NewStyle().Foreground(thumbColor).Render("█")
}

// blankColumn is an empty gutter of the given height.
func blankColumn(height int) []string {
	col := make([]string, max(0, height))
	for i := range col {
		col[i] = " "
	}
	return col
}

// scrollbarFromPercent builds a single-row thumb positioned by percent, for widgets that report only how far they are scrolled.
func scrollbarFromPercent(height int, percent float64, scrollable, focused bool) []string {
	if height <= 0 || !scrollable {
		return blankColumn(height)
	}
	track, thumb := scrollbarGlyphs(focused)
	pos := int(percent*float64(height-1) + 0.5)
	pos = min(max(pos, 0), height-1)

	col := make([]string, height)
	for i := range col {
		col[i] = track
		if i == pos {
			col[i] = thumb
		}
	}
	return col
}

// scrollbarColumn builds the scrollbar as one glyph per visible row. The thumb is sized by the share of content on screen and positioned by the offset.
func scrollbarColumn(height, total, offset int, focused bool) []string {
	// Nothing to scroll: leave the gutter empty rather than draw a full-height thumb, which would read as "scrollable" at a glance.
	if height <= 0 || total <= height {
		return blankColumn(height)
	}
	track, thumb := scrollbarGlyphs(focused)

	col := make([]string, height)
	size := max(1, height*height/total)
	pos := 0
	if span := total - height; span > 0 {
		pos = offset * (height - size) / span
	}
	pos = min(max(pos, 0), height-size)

	for i := range col {
		if i >= pos && i < pos+size {
			col[i] = thumb
		} else {
			col[i] = track
		}
	}
	return col
}

// viewComposerHint is the line under the composer: model, token estimate and live generation state.
func (a *App) viewComposerHint(width int) string {
	if a.searching || a.searchQuery != "" {
		return a.viewSearchBar(width)
	}
	var left string
	switch {
	case isSlash(a.input.Value()) && !a.streaming:
		left = a.viewSlashHints(width)
	case len(store.Placeholders(a.input.Value())) > 0 && !a.streaming:
		left = theme.Dim.Render("fill in ") +
			theme.Key.Render(strings.Join(store.Placeholders(a.input.Value()), ", "))
	case len(a.pending) > 0 && !a.streaming:
		left = a.viewPendingChips()
	case a.streaming:
		left = a.spinner.View() + " " + theme.Dim.Render("generating · "+a.liveStats())
	case a.cfg.Model != "":
		left = theme.Dim.Render("↵ send · alt+↵ newline · ↑ history")
	default:
		left = theme.Err.Render("no model selected")
	}

	return spread(" "+left, a.viewContextMeter()+" ", width)
}

// viewSidebar lists saved sessions, newest first.
func (a *App) viewSidebar() string {
	h := a.contentHeight()
	inner := sidebarWidth - 2

	rows := []string{theme.Label.Render("SESSIONS"), ""}

	newRow := "＋ New chat"
	if a.sessionIdx == 0 && a.focus == focusSessions {
		rows = append(rows, selectedRow(newRow, inner))
	} else {
		rows = append(rows, " "+lipgloss.NewStyle().Foreground(theme.Green).Render(newRow))
	}

	// Reserve the rows already used plus one for the overflow marker.
	limit := max(0, h-len(rows)-3)
	for i, s := range a.sessions {
		if i >= limit {
			rows = append(rows, theme.Dim.Render(fmt.Sprintf(" … %d more", len(a.sessions)-i)))
			break
		}
		label := theme.Truncate(s.Title, inner-2)
		if s.Pinned {
			label = theme.Truncate("★ "+s.Title, inner-2)
		}
		current := a.cur != nil && s.ID == a.cur.ID
		switch {
		case a.focus == focusSessions && a.sessionIdx == i+1:
			rows = append(rows, selectedRow(label, inner))
		case current:
			rows = append(rows, " "+lipgloss.NewStyle().Foreground(theme.Amber).Render("▸ "+label))
		default:
			rows = append(rows, " "+theme.Dim.Render("  "+label))
		}
	}

	content := strings.Join(rows, "\n")
	return panel(content, sidebarWidth, h, a.focus == focusSessions)
}

// selectionMark styles a gutter glyph in the selection colour, which is one neither speaker uses, so the mark reads as "this message" rather than as something about whoever wrote it.
func selectionMark(glyph string) string {
	return lipgloss.NewStyle().Foreground(theme.Amber).Bold(true).Render(glyph)
}

// selectionMarkSoft is the same mark against faint content. Reasoning is dim italic by design, and a bold amber gutter beside it draws the eye to the margin rather than to the message the margin is marking.
func selectionMarkSoft(glyph string) string {
	return lipgloss.NewStyle().Foreground(theme.AmberSoft).Render(glyph)
}

// selectionBar is the gutter marking the selected message, borrowing the speaker-bar idiom.
func selectionBar() string { return selectionMark("▌") }

func selectedRow(label string, width int) string {
	return lipgloss.NewStyle().Foreground(theme.Text).Bold(true).
		Background(theme.Surface).Width(width).Render(" " + theme.Truncate(label, width-1))
}

// --- small helpers ----------------------------------------------------------

// blockOf joins lines into a left-aligned block of uniform width, so the whole block can be centered as a unit.
func blockOf(lines ...string) string {
	w := 0
	for _, l := range lines {
		w = max(w, lipgloss.Width(l))
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = theme.Pad(l, w)
	}
	return strings.Join(out, "\n")
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func shortModel(name string) string {
	if name == "" {
		return "assistant"
	}
	return strings.TrimSuffix(name, ":latest")
}

func shortDuration(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return "0ms"
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}

// sparkline draws a compact throughput history using block glyphs.
func sparkline(values []float64) string {
	if len(values) < 2 {
		return ""
	}
	const glyphs = "▁▂▃▄▅▆▇█"
	hi := 0.0
	for _, v := range values {
		hi = max(hi, v)
	}
	if hi <= 0 {
		return ""
	}
	// Keep the tail; the sparkline is a "recently" indicator, not a full history.
	if len(values) > 24 {
		values = values[len(values)-24:]
	}
	runes := []rune(glyphs)
	var b strings.Builder
	for _, v := range values {
		idx := min(int(v/hi*float64(len(runes)-1)), len(runes)-1)
		b.WriteRune(runes[max(idx, 0)])
	}
	return lipgloss.NewStyle().Foreground(theme.Teal).Render(b.String())
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
