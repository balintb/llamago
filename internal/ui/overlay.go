package ui

import (
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/balintb/llamago/internal/store"
	"github.com/balintb/llamago/internal/theme"
)

// command is one entry in the command palette.
type command struct {
	title string
	hint  string
	group string
	run   func(a *App) tea.Cmd
}

// --- key routing ------------------------------------------------------------

func (a *App) onOverlayKey(msg tea.KeyPressMsg) tea.Cmd {
	switch a.overlay {
	case overlayHelp:
		// The movement keys are only claimed while there is something to scroll. Once the whole keymap is on screen the footer promises that any key closes, and that has to include the arrows.
		if limit := a.helpMaxScroll(); limit > 0 {
			switch msg.String() {
			case "up", "k":
				a.helpScroll = max(0, a.helpScroll-1)
				return nil
			case "down", "j":
				a.helpScroll = min(limit, a.helpScroll+1)
				return nil
			case "pgup", "ctrl+u":
				a.helpScroll = max(0, a.helpScroll-a.helpRows()/2)
				return nil
			case "pgdown", "ctrl+d":
				a.helpScroll = min(limit, a.helpScroll+a.helpRows()/2)
				return nil
			case "home", "g":
				a.helpScroll = 0
				return nil
			case "end", "G":
				a.helpScroll = limit
				return nil
			}
		}
		a.overlay, a.helpScroll = overlayNone, 0
		return nil

	case overlayConfirm:
		switch msg.String() {
		case "y", "Y", "enter":
			action := a.confirm.action
			a.overlay, a.confirm = overlayNone, confirmState{}
			return action
		default:
			a.overlay, a.confirm = overlayNone, confirmState{}
			// A refused tool call still owes the model an answer, or it waits for output that is never coming.
			if call := a.deniedCall; call != nil {
				a.deniedCall = nil
				return a.denyToolCall(*call)
			}
			return nil
		}

	case overlayPull:
		switch msg.String() {
		case "esc":
			a.overlay = overlayNone
			a.pullInput.Blur()
			return nil
		case "enter":
			name := a.pullTarget()
			if name == "" {
				return a.showToast("nothing to pull - type a name or pick one", true)
			}
			a.pullInput.Blur()
			return a.beginPull(name)
		case "down", "ctrl+n":
			a.pullIdx = min(a.pullIdx+1, max(0, len(a.pullChoices())-1))
			return nil
		case "up", "ctrl+p":
			a.pullIdx = max(a.pullIdx-1, 0)
			return nil
		case "tab":
			// Complete to the highlighted name, so a pick can be edited into a tag the list does not carry.
			if choices := a.pullChoices(); a.pullIdx < len(choices) {
				a.pullInput.SetValue(choices[a.pullIdx].Name)
				a.pullInput.CursorEnd()
			}
			return nil
		}
		var cmd tea.Cmd
		a.pullInput, cmd = a.pullInput.Update(msg)
		// Typing changes what is listed, so an index into the old list means nothing.
		a.pullIdx = 0
		return cmd

	case overlaySystem:
		switch msg.String() {
		case "esc":
			a.overlay = overlayNone
			a.sysInput.Blur()
			return nil
		case "ctrl+s", "alt+enter":
			a.overlay = overlayNone
			a.sysInput.Blur()
			if a.editTarget == editStop {
				a.cfg.Stop = parseStops(a.sysInput.Value())
				_ = a.cfg.Save()
				return a.okToast(fmt.Sprintf("%d stop sequences saved", len(a.cfg.Stop)))
			}
			a.cfg.System = strings.TrimSpace(a.sysInput.Value())
			_ = a.cfg.Save()
			return a.okToast("system prompt saved")
		}
		var cmd tea.Cmd
		a.sysInput, cmd = a.sysInput.Update(msg)
		return cmd

	case overlayRename:
		switch msg.String() {
		case "esc":
			a.overlay = overlayNone
			a.renameIn.Blur()
			return nil
		case "enter":
			return a.applyRename()
		}
		var cmd tea.Cmd
		a.renameIn, cmd = a.renameIn.Update(msg)
		return cmd

	case overlayNudge:
		switch msg.String() {
		case "esc":
			a.overlay = overlayNone
			a.nudgeIn.Blur()
			return nil
		case "enter":
			return a.applyNudge()
		}
		var cmd tea.Cmd
		a.nudgeIn, cmd = a.nudgeIn.Update(msg)
		return cmd

	case overlayFind:
		return a.onFindKey(msg)

	case overlayPicker:
		return a.onPickerKey(msg)

	case overlayPalette:
		return a.onPaletteKey(msg)
	}
	return nil
}

// --- command palette --------------------------------------------------------

// paletteMode selects what the palette is listing.
type paletteMode int

const (
	paletteAll paletteMode = iota
	// paletteCompare narrows the palette to models that can race the active one.
	paletteCompare
)

func (a *App) openPalette() tea.Cmd { return a.openPaletteMode(paletteAll) }

func (a *App) openPaletteMode(mode paletteMode) tea.Cmd {
	a.overlay = overlayPalette
	a.paletteMode = mode
	a.paletteIdx = 0
	a.paletteIn.SetValue("")
	a.paletteCmds = a.buildCommands()
	return a.paletteIn.Focus()
}

// askCompareOpponent takes the composer's text as the prompt and asks which model should race the active one.
func (a *App) askCompareOpponent() tea.Cmd {
	prompt := strings.TrimSpace(a.input.Value())
	if prompt == "" {
		return a.showToast("type a prompt first, then compare", true)
	}
	if len(a.models) < 2 {
		return a.showToast("need at least two models to compare", true)
	}
	a.tab = tabChat
	a.comparePrompt = prompt
	a.input.Reset()
	a.layout()
	// Warm the capability cache while the user is choosing. The thinking flag is read from these details when the request is built, and an opponent the user has never inspected would otherwise be sent no flag at all.
	return tea.Batch(a.openPaletteMode(paletteCompare), a.fetchMissingDetails())
}

func (a *App) onPaletteKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+k":
		a.overlay = overlayNone
		a.paletteIn.Blur()
		return nil
	case "down", "ctrl+n":
		a.paletteIdx = min(a.paletteIdx+1, max(0, len(a.filteredCommands())-1))
		return nil
	case "up", "ctrl+p":
		a.paletteIdx = max(a.paletteIdx-1, 0)
		return nil
	case "enter":
		matches := a.filteredCommands()
		if a.paletteIdx >= len(matches) {
			return nil
		}
		cmd := matches[a.paletteIdx]
		a.overlay = overlayNone
		a.paletteIn.Blur()
		return cmd.run(a)
	}
	var c tea.Cmd
	a.paletteIn, c = a.paletteIn.Update(msg)
	a.paletteIdx = 0
	return c
}

// buildCommands assembles the palette: fixed actions plus one entry per model.
func (a *App) buildCommands() []command {
	cmds := []command{
		{title: "New chat", hint: "ctrl+n", group: "chat", run: func(a *App) tea.Cmd {
			a.tab = tabChat
			return a.newSession()
		}},
		{title: "Regenerate last response", hint: "ctrl+e", group: "chat", run: func(a *App) tea.Cmd {
			a.tab = tabChat
			return a.regenerate()
		}},
		{title: "Regenerate with a nudge", hint: "alt+e", group: "chat", run: func(a *App) tea.Cmd {
			a.tab = tabChat
			return a.openNudge()
		}},
		{title: "Copy last response", hint: "ctrl+y", group: "chat", run: (*App).copyLast},
		{title: "Copy the whole conversation", hint: "Y", group: "chat", run: (*App).copyConversation},
		{title: "Compare two models on this prompt", hint: "ctrl+\\", group: "chat", run: (*App).askCompareOpponent},
		{title: "Find in conversation", hint: "ctrl+f", group: "chat", run: (*App).openSearch},
		{title: "Search every conversation", hint: "/find", group: "chat", run: func(a *App) tea.Cmd {
			a.tab = tabChat
			a.setComposer("/find ")
			return nil
		}},
		{title: "Export chat to markdown", hint: "ctrl+s", group: "chat", run: (*App).exportCmd},
		{title: "Export chat to HTML", hint: "/export html", group: "chat", run: func(a *App) tea.Cmd {
			return a.exportAs(store.FormatHTML)
		}},
		{title: "Export chat to JSON", hint: "/export json", group: "chat", run: func(a *App) tea.Cmd {
			return a.exportAs(store.FormatJSON)
		}},
		{title: "Toggle markdown rendering", hint: "m", group: "chat", run: (*App).toggleMarkdown},
		{title: "Toggle reasoning display", hint: "ctrl+g", group: "chat", run: func(a *App) tea.Cmd {
			a.showThink = !a.showThink
			a.refreshTranscript()
			return a.okToast("reasoning " + onOff(a.showThink))
		}},
		{title: "Toggle sidebar", hint: "ctrl+b", group: "chat", run: func(a *App) tea.Cmd {
			a.sidebar = !a.sidebar
			a.layout()
			a.refreshTranscript()
			return nil
		}},
		{title: "Attach an image", hint: "ctrl+i", group: "chat", run: func(a *App) tea.Cmd {
			a.tab = tabChat
			return a.openImagePicker()
		}},
		{title: "Inline a text file", hint: "ctrl+t", group: "chat", run: func(a *App) tea.Cmd {
			a.tab = tabChat
			return a.openTextPicker()
		}},
		{title: "Edit system prompt", hint: "", group: "chat", run: func(a *App) tea.Cmd {
			return a.openEditor(editSystem)
		}},
		{title: "Edit stop sequences", hint: "", group: "chat", run: func(a *App) tea.Cmd {
			return a.openEditor(editStop)
		}},
		{title: "Pull a model", hint: "p", group: "models", run: func(a *App) tea.Cmd {
			a.tab = tabModels
			a.overlay = overlayPull
			a.pullInput.SetValue("")
			return a.pullInput.Focus()
		}},
		{title: "Save the last prompt", hint: "/save", group: "prompts", run: func(a *App) tea.Cmd {
			a.tab = tabChat
			a.setComposer("/save ")
			return nil
		}},
		{title: "Refresh models and memory", hint: "ctrl+r", group: "models", run: func(a *App) tea.Cmd {
			a.loading = true
			return tea.Batch(a.listModelsCmd(), a.psCmd(), a.connectCmd())
		}},
		{title: "Browse models", hint: "alt+2", group: "go", run: func(a *App) tea.Cmd { return a.goTab(tabModels) }},
		{title: "Running models", hint: "alt+3", group: "go", run: func(a *App) tea.Cmd { return a.goTab(tabRunning) }},
		{title: "Settings", hint: "alt+4", group: "go", run: func(a *App) tea.Cmd { return a.goTab(tabSettings) }},
		{title: "Chat", hint: "alt+1", group: "go", run: func(a *App) tea.Cmd { return a.goTab(tabChat) }},
		{title: "Keyboard shortcuts", hint: "f1", group: "go", run: func(a *App) tea.Cmd {
			a.overlay, a.helpScroll = overlayHelp, 0
			return nil
		}},
		{title: "Quit", hint: "ctrl+q", group: "go", run: (*App).quit},
	}

	for _, h := range a.cfg.Hosts {
		if h.URL == a.client.Host() {
			continue
		}
		cmds = append(cmds, command{
			title: "Host: " + h.Name, hint: h.URL, group: "settings",
			run: func(a *App) tea.Cmd { return a.switchHost(h.URL) },
		})
	}

	for _, name := range a.cfg.PresetNames() {
		cmds = append(cmds, command{
			title: "Preset: " + name, hint: "/preset", group: "settings",
			run: func(a *App) tea.Cmd {
				p, ok := a.cfg.FindPreset(name)
				if !ok {
					return nil
				}
				a.cfg.Apply(p)
				_ = a.cfg.Save()
				return a.okToast("preset " + name)
			},
		})
	}

	for _, p := range a.library {
		cmds = append(cmds, command{
			title: p.Name, hint: "prompt", group: "prompts",
			run: func(a *App) tea.Cmd {
				a.tab = tabChat
				a.setComposer(p.Text)
				if blanks := store.Placeholders(p.Text); len(blanks) > 0 {
					return a.okToast("fill in " + strings.Join(blanks, ", "))
				}
				return nil
			},
		})
	}

	// While choosing an opponent the palette lists only models, and picking one starts the race instead of switching the active model.
	if a.paletteMode == paletteCompare {
		var only []command
		for _, m := range a.models {
			if m.Name == a.cfg.Model || a.isImageModel(m.Name) {
				continue
			}
			if a.racing(m.Name) {
				continue
			}
			name := m.Name
			title := "Race against " + m.ShortName()
			if a.comparing {
				title = "Add " + m.ShortName() + " to the race"
			}
			only = append(only, command{
				title: title,
				hint:  m.Details.ParameterSize,
				group: "model",
				run: func(a *App) tea.Cmd {
					if a.comparing {
						return a.addCompareSide(name)
					}
					return a.startCompare(name, a.comparePrompt)
				},
			})
		}
		if len(only) == 0 {
			only = append(only, command{
				title: "Pull another model first",
				group: "models",
				run: func(a *App) tea.Cmd {
					a.overlay = overlayPull
					return a.pullInput.Focus()
				},
			})
		}
		return only
	}

	// Switching model is the single most common action, so give every model its own palette entry.
	for _, m := range a.models {
		name := m.Name
		hint := ""
		if name == a.cfg.Model {
			hint = "active"
		}
		if a.isImageModel(name) {
			hint = "image gen"
		}
		cmds = append(cmds, command{
			title: "Use " + m.ShortName(),
			hint:  hint,
			group: "model",
			run: func(a *App) tea.Cmd {
				a.setModel(name)
				a.tab = tabChat
				a.layout()
				a.refreshTranscript()
				return a.okToast("now chatting with " + strings.TrimSuffix(name, ":latest"))
			},
		})
	}

	// One entry per code block, so blocks past the digit shortcuts stay reachable and searchable by their contents.
	for i, b := range a.codeBlocks() {
		n, lang, first := i+1, b.lang, firstLine(b.code)
		cmds = append(cmds, command{
			title: fmt.Sprintf("Copy code ⌗%d  %s", n, first),
			hint:  lang,
			group: "code",
			run:   func(a *App) tea.Cmd { return a.copyCodeBlock(n) },
		})
	}

	for i, img := range a.images() {
		n, ref := i+1, img
		for _, act := range []struct {
			verb string
			run  func(a *App) tea.Cmd
		}{
			{"View", func(a *App) tea.Cmd { return a.viewImageCmd(ref) }},
			{"Open", func(a *App) tea.Cmd { return a.openImageCmd(ref) }},
			{"Save", func(a *App) tea.Cmd { return a.openSaveDirPicker(ref) }},
		} {
			cmds = append(cmds, command{
				title: fmt.Sprintf("%s image 🖼%d", act.verb, n),
				group: "image",
				run:   act.run,
			})
		}
	}

	// Recent sessions, so history is reachable without the sidebar.
	for i, s := range a.sessions {
		if i >= 8 {
			break
		}
		sess := s
		cmds = append(cmds, command{
			title: "Open “" + sess.Title + "”",
			hint:  strings.TrimSuffix(sess.Model, ":latest"),
			group: "session",
			run: func(a *App) tea.Cmd {
				a.tab = tabChat
				a.sessionIdx = i + 1
				return a.openSelectedSession()
			},
		})
	}
	return cmds
}

// filteredCommands applies subsequence matching to the palette query.
func (a *App) filteredCommands() []command {
	q := strings.ToLower(strings.TrimSpace(a.paletteIn.Value()))
	if q == "" {
		return a.paletteCmds
	}
	out := make([]command, 0, len(a.paletteCmds))
	for _, c := range a.paletteCmds {
		if fuzzyMatch(strings.ToLower(c.title+" "+c.group), q) {
			out = append(out, c)
		}
	}
	return out
}

// firstLine is a one-line preview of a code block for the palette.
func firstLine(code string) string {
	for l := range strings.SplitSeq(code, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return theme.Truncate(l, 32)
		}
	}
	return "(empty)"
}

// fuzzyMatch reports whether every rune of query appears in s, in order.
func fuzzyMatch(s, query string) bool {
	i := 0
	qr := []rune(query)
	for _, r := range s {
		if i < len(qr) && r == qr[i] {
			i++
		}
	}
	return i == len(qr)
}

// --- overlay rendering ------------------------------------------------------

func (a *App) viewOverlay() string {
	switch a.overlay {
	case overlayHelp:
		return a.viewHelp()
	case overlayPalette:
		return a.viewPalette()
	case overlayConfirm:
		return a.viewConfirm()
	case overlayPull:
		return a.viewPull()
	case overlaySystem:
		return a.viewSystemEditor()
	case overlayRename:
		return a.viewRename()
	case overlayNudge:
		return a.viewNudge()
	case overlayFind:
		return a.viewFindResults()
	case overlayPicker:
		return a.viewPicker()
	}
	return ""
}

// modalInner is the usable content width inside a modal of the given total width: two cells of border plus one of padding on each side.
func modalInner(width int) int { return max(4, width-4) }

// Modal widths. layout() sizes the text inputs from these, so both must agree or the inputs render wider than the box and get clipped.
func (a *App) paletteWidth() int { return max(40, min(72, a.width-8)) }
func (a *App) pullWidth() int    { return max(40, min(64, a.width-8)) }
func (a *App) systemWidth() int  { return max(40, min(78, a.width-8)) }

// modal frames overlay content in a bordered box with a gradient title.
//
// Body lines are clipped to the interior first: lipgloss would otherwise wrap an over-long line, silently making the box taller than its caller intended.
func modal(title, body string, width int) string {
	inner := modalInner(width)
	head := theme.GradientBold(theme.Truncate(title, inner), theme.Brand...)
	body = fitBlock(body, inner, len(strings.Split(body, "\n")))
	content := head + "\n" + theme.Rule(inner) + "\n" + body
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Violet).
		Background(theme.Deep).
		Padding(0, 1).
		Width(width).
		Render(content)
}

func (a *App) paletteTitle() string {
	if a.paletteMode == paletteCompare {
		return "Race against which model?"
	}
	return "Command palette"
}

func (a *App) viewPalette() string {
	width := a.paletteWidth()
	inner := modalInner(width)
	matches := a.filteredCommands()

	// Navigation clamps the index, but filtering can shrink the list under it; keep the highlight and the counter inside the match set regardless.
	idx := min(max(a.paletteIdx, 0), max(0, len(matches)-1))

	rows := []string{a.paletteIn.View(), ""}

	// Chrome around the list: border, title, rule, input, spacer and footer.
	const chrome = 10
	maxRows := min(12, max(2, a.height-4-chrome))
	start := 0
	if idx >= maxRows {
		start = idx - maxRows + 1
	}
	// Never scroll past the final full page, or the last rows come up blank.
	start = min(start, max(0, len(matches)-maxRows))
	end := min(start+maxRows, len(matches))

	// Column widths that always sum to exactly inner: marker, badge, title, hint. Hints can be arbitrarily long (a session's model name), so the hint gets a hard column rather than whatever it happens to need.
	const (
		markerW = 1
		badgeW  = 9
		hintW   = 13 // includes one leading space separating it from the title
	)
	titleW := max(8, inner-markerW-badgeW-hintW)

	// Always emit maxRows list rows, blank-padding the remainder, so filtering and scrolling never change the palette's height.
	list := make([]string, 0, maxRows)
	for i := start; i < end; i++ {
		c := matches[i]
		badge := theme.Pad(c.group, badgeW)
		title := theme.Pad(theme.Truncate(c.title, titleW), titleW)
		hint := " " + theme.Pad(theme.Truncate(c.hint, hintW-1), hintW-1)

		if i == idx {
			list = append(list, lipgloss.NewStyle().
				Background(theme.Surface).Foreground(theme.Text).Bold(true).
				Render(" "+badge+title+hint))
			continue
		}
		list = append(list, " "+
			lipgloss.NewStyle().Foreground(groupColor(c.group)).Render(badge)+
			lipgloss.NewStyle().Foreground(theme.Subtle).Render(title)+
			theme.Dim.Render(hint))
	}
	if len(matches) == 0 && maxRows > 0 {
		list = append(list, theme.Dim.Render("  no matching commands"))
	}
	for len(list) < maxRows {
		list = append(list, "")
	}
	rows = append(rows, list...)

	// The counter line is always present, blank when there is nothing to count.
	counter := ""
	if len(matches) > 0 {
		counter = theme.Dim.Render(fmt.Sprintf("  %d of %d", idx+1, len(matches)))
	}
	rows = append(rows, "", counter, "", theme.Dim.Render("  ↑↓ move · ↵ run · esc close"))
	return modal(a.paletteTitle(), strings.Join(rows, "\n"), width)
}

func groupColor(group string) color.Color {
	switch group {
	case "chat":
		return theme.Violet
	case "models":
		return theme.Cyan
	case "model":
		return theme.Amber
	case "session":
		return theme.Teal
	case "code":
		return theme.Green
	case "image":
		return theme.Cyan
	default:
		return theme.Muted
	}
}

func (a *App) viewPull() string {
	width := a.pullWidth()
	inner := modalInner(width)
	rows := []string{
		theme.Dim.Render("Type a name from ollama.com, or pick one below."),
		"",
		a.pullInput.View(),
		"",
	}

	choices := a.pullChoices()
	// Leave room for the border, title, rule, the input and the hint line.
	room := max(1, a.height-12)
	shown := min(len(choices), room)

	// Keep the highlight on screen as it moves down a long list.
	start := 0
	if a.pullIdx >= shown {
		start = a.pullIdx - shown + 1
	}
	for i := start; i < min(len(choices), start+shown); i++ {
		e := choices[i]
		name := theme.Pad(e.Name, 20)
		size := theme.Pad(e.Size, 10)
		note := e.Purpose
		if a.installed(e.Name) {
			note = "installed · " + note
		}
		line := name + size + note
		if i == a.pullIdx {
			rows = append(rows, selectedRow(theme.Truncate(line, inner-1), inner))
			continue
		}
		body := lipgloss.NewStyle().Foreground(theme.Cyan).Render(name) +
			theme.Dim.Render(size+note)
		rows = append(rows, " "+theme.Truncate(body, inner-1))
	}

	switch {
	case len(choices) == 0:
		rows = append(rows, theme.Dim.Render("  nothing in the list matches - ↵ pulls what you typed"))
	case len(choices) > shown:
		rows = append(rows, theme.Dim.Render(fmt.Sprintf("  … %d more", len(choices)-shown)))
	}
	rows = append(rows, "", theme.Dim.Render("↑↓ pick · tab complete · ↵ pull · esc cancel"))
	return modal("Pull a model", strings.Join(rows, "\n"), width)
}

func (a *App) viewSystemEditor() string {
	width := a.systemWidth()
	title, blurb := "System prompt", "Sent as the system message at the start of every conversation."
	if a.editTarget == editStop {
		title = "Stop sequences"
		blurb = "One per line. Generation halts as soon as the model produces any of them."
	}
	rows := []string{
		theme.Dim.Render(blurb),
		"",
		a.sysInput.View(),
		"",
		theme.Dim.Render("ctrl+s save · esc cancel"),
	}
	return modal(title, strings.Join(rows, "\n"), width)
}

func (a *App) viewRename() string {
	width := max(36, min(64, a.width-8))
	rows := []string{
		a.renameIn.View(),
		"",
		theme.Dim.Render("↵ save · esc cancel"),
	}
	return modal("Rename session", strings.Join(rows, "\n"), width)
}

func (a *App) viewNudge() string {
	width := max(40, min(66, a.width-8))
	rows := []string{
		theme.Dim.Render("Answer again, with one more instruction. It steers this reply only"),
		theme.Dim.Render("and is not added to the conversation."),
		"",
		a.nudgeIn.View(),
		"",
		theme.Dim.Render("↵ regenerate · esc cancel"),
	}
	return modal("Regenerate with…", strings.Join(rows, "\n"), width)
}

func (a *App) viewConfirm() string {
	width := max(36, min(60, a.width-8))
	body := lipgloss.NewStyle().Foreground(theme.Text).Width(modalInner(width)).Render(a.confirm.prompt) +
		"\n\n" +
		theme.Err.Render("y") + theme.Dim.Render(" confirm    ") +
		theme.Key.Render("n") + theme.Dim.Render(" cancel")
	return modal("Are you sure?", body, width)
}

// helpSection groups related bindings for the help overlay.
type helpSection struct {
	title string
	keys  [][2]string
}

var helpSections = []helpSection{
	{"Global", [][2]string{
		{"ctrl+k", "command palette"},
		{"alt+1…4", "jump to a tab"},
		{"ctrl+o", "next tab"},
		{"esc", "up to the tab strip"},
		{"ctrl+r", "refresh from server"},
		{"f1", "this help"},
		{"ctrl+q", "quit"},
	}},
	{"Chat", [][2]string{
		{"↵", "send"},
		{"alt+↵", "newline"},
		{"shift+⌫", "clear the composer"},
		{"↑ / ↓", "previous / next prompt"},
		{"ctrl+c", "stop generating"},
		{"ctrl+n", "new chat"},
		{"ctrl+e", "regenerate"},
		{"alt+e", "regenerate with a nudge"},
		{"ctrl+y", "copy last response"},
		{"ctrl+i", "attach an image"},
		{"ctrl+t", "inline a text file"},
		{"ctrl+f", "find in conversation"},
		{"ctrl+a", "widen find to every chat"},
		{"ctrl+s", "export to markdown"},
		{"ctrl+g", "toggle reasoning"},
		{"/tools", "let models call tools"},
		{"ctrl+b", "toggle sidebar"},
		{"tab", "next pane"},
		{"shift+tab", "previous pane"},
		{"esc", "back to composer"},
	}},
	{"Transcript / lists", [][2]string{
		{"j / k", "move"},
		{"d / u", "half page"},
		{"g / G", "top / bottom"},
		{"shift+↑ / ↓", "select a message"},
		{"y", "copy selected message"},
		{"Y", "copy the conversation"},
		{"m", "raw text / markdown"},
		{"↵", "edit that prompt again"},
		{"r", "ask again from here"},
		{"f", "fork into a new chat"},
		{"x", "delete the exchange"},
		{"→ / ←", "show / hide details"},
		{"1…9", "copy code block"},
		{"v / o", "view / open the image"},
		{"/", "find"},
		{"n / N", "next / previous match"},
	}},
	{"Compare", [][2]string{
		{"ctrl+\\", "start / leave a race"},
		{"alt+a", "add a model to it"},
		{"↵", "ask every column"},
		{"tab", "composer / columns"},
		{"↵ on a column", "keep that thread"},
		{"y", "copy that column"},
		{"1 / 2", "keep that column"},
		{"ctrl+f", "find across columns"},
	}},
	{"Sessions", [][2]string{
		{"↵", "open"},
		{"n", "new chat"},
		{"r", "rename"},
		{"p", "pin to the top"},
		{"c", "duplicate"},
		{"d", "delete"},
	}},
	{"Models", [][2]string{
		{"↵", "chat with model"},
		{"p", "pull a model"},
		{"d", "delete a model"},
		{"u", "unload from memory"},
		{"ctrl+d / ctrl+u", "scroll the card"},
		{"y", "copy model name"},
		{"/", "filter"},
	}},
	{"Settings", [][2]string{
		{"← →", "adjust"},
		{"shift+← →", "bigger steps"},
		{"↵", "edit the selected field"},
		{"e / s", "prompt / stop sequences"},
		{"r", "reset to defaults"},
		{"g / G", "top / bottom"},
	}},
}

func (a *App) helpWidth() int { return max(44, min(78, a.width-6)) }

// helpRows is how many lines of keymap the modal shows at once. Border, title, rule and the closing hint bracket the two columns.
func (a *App) helpRows() int { return max(3, a.height-4-6) }

// helpMaxScroll is how far the keymap can be scrolled before running out.
func (a *App) helpMaxScroll() int { return max(0, len(a.helpLines())-a.helpRows()) }

// helpLines lays the keymap out in two columns and returns the rendered lines.
//
// Sections are packed by height rather than split at a fixed index: they have grown enough that a fixed split left one column half empty while the other overflowed, and the overflow was dropped in silence.
func (a *App) helpLines() []string {
	blocks := make([][]string, len(helpSections))
	for i, s := range helpSections {
		block := []string{theme.Label.Render(strings.ToUpper(s.title))}
		for _, k := range s.keys {
			block = append(block, theme.Key.Render(theme.Pad(k[0], 12))+theme.Dim.Render(k[1]))
		}
		block = append(block, "")
		blocks[i] = block
	}

	// Each section joins whichever column is shorter. One split point cannot balance sections of this many different heights - the best ordered split leaves one column taller than the screen while the other has room to spare - and a column that overflows is a section nobody finds.
	var left, right []string
	for _, block := range blocks {
		if len(left) <= len(right) {
			left = append(left, block...)
			continue
		}
		right = append(right, block...)
	}

	col := modalInner(a.helpWidth()) / 2
	return strings.Split(lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(col).Render(strings.Join(left, "\n")),
		lipgloss.NewStyle().Width(col).Render(strings.Join(right, "\n")),
	), "\n")
}

func (a *App) viewHelp() string {
	lines, rows := a.helpLines(), a.helpRows()
	// Clamp here rather than in the key handler so a resize cannot strand the view below its content.
	off := min(max(a.helpScroll, 0), max(0, len(lines)-rows))
	end := min(len(lines), off+rows)

	hint := "press any key to close"
	if len(lines) > rows {
		hint = "↑↓ scroll · any other key closes"
		if more := len(lines) - end; more > 0 {
			hint = fmt.Sprintf("↑↓ scroll · %d more · any other key closes", more)
		}
	}
	body := strings.Join(lines[off:end], "\n") + "\n" + theme.Dim.Render(hint)
	return modal("llamago - keyboard", body, a.helpWidth())
}
