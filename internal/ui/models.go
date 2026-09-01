package ui

import (
	"fmt"
	"image/color"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/balintb/llamago/internal/ollama"
	"github.com/balintb/llamago/internal/theme"
)

// --- key handling -----------------------------------------------------------

func (a *App) onModelsKey(msg tea.KeyPressMsg) tea.Cmd {
	if a.modelSearchOn {
		switch msg.String() {
		case "esc":
			a.modelSearchOn = false
			a.modelSearch.Blur()
			a.modelSearch.SetValue("")
			a.clampModelIdx()
			return nil
		case "enter":
			a.modelSearchOn = false
			a.modelSearch.Blur()
			a.clampModelIdx()
			return nil
		}
		var cmd tea.Cmd
		a.modelSearch, cmd = a.modelSearch.Update(msg)
		a.modelIdx = 0
		return tea.Batch(cmd, a.showSelectedModel())
	}

	switch msg.String() {
	case "esc":
		// Nothing else uses esc here once the filter is closed, so make it the way back to the chat.
		return a.goTab(tabChat)
	case "ctrl+d":
		a.detailScroll += max(1, a.contentHeight()/3)
	case "ctrl+u":
		a.detailScroll = max(0, a.detailScroll-max(1, a.contentHeight()/3))
	case "j", "down":
		a.modelIdx = min(a.modelIdx+1, max(0, len(a.visibleModels())-1))
		return a.showSelectedModel()
	case "k", "up":
		a.modelIdx, a.detailScroll = max(a.modelIdx-1, 0), 0
		return a.showSelectedModel()
	case "g", "home":
		a.modelIdx = 0
		return a.showSelectedModel()
	case "G", "end":
		a.modelIdx = max(0, len(a.visibleModels())-1)
		return a.showSelectedModel()
	case "/":
		a.modelSearchOn = true
		return a.modelSearch.Focus()
	case "enter":
		m := a.selectedModel()
		if m == nil {
			return nil
		}
		cmd := a.setModel(m.Name)
		a.tab = tabChat
		a.layout()
		a.refreshTranscript()
		if a.isImageModel(m.Name) {
			return tea.Batch(cmd, a.showToast(m.ShortName()+" generates images and cannot chat", true))
		}
		return tea.Batch(cmd, a.okToast("now chatting with "+m.ShortName()))
	case "p", "n":
		a.overlay = overlayPull
		a.pullInput.SetValue("")
		return a.pullInput.Focus()
	case "d", "x":
		m := a.selectedModel()
		if m == nil {
			return nil
		}
		name := m.Name
		a.confirm = confirmState{
			prompt: fmt.Sprintf("Delete %s (%s)?", name, ollama.HumanBytes(m.Size)),
			action: a.deleteModelCmd(name),
		}
		a.overlay = overlayConfirm
		return nil
	case "u":
		if m := a.selectedModel(); m != nil {
			return a.unloadCmd(m.Name)
		}
	case "y":
		if m := a.selectedModel(); m != nil {
			return tea.Batch(tea.SetClipboard(m.Name), a.okToast("copied "+m.Name))
		}
	}
	return nil
}

// --- selection --------------------------------------------------------------

// visibleModels applies the current filter and sorts by size, largest first.
func (a *App) visibleModels() []ollama.Model {
	q := strings.ToLower(strings.TrimSpace(a.modelSearch.Value()))
	out := make([]ollama.Model, 0, len(a.models))
	for _, m := range a.models {
		if q == "" || strings.Contains(strings.ToLower(m.Name), q) ||
			strings.Contains(strings.ToLower(m.Details.Family), q) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Size > out[j].Size })
	return out
}

func (a *App) selectedModel() *ollama.Model {
	v := a.visibleModels()
	if a.modelIdx < 0 || a.modelIdx >= len(v) {
		return nil
	}
	return &v[a.modelIdx]
}

// showSelectedModel lazily fetches details for the highlighted model once.
func (a *App) showSelectedModel() tea.Cmd {
	m := a.selectedModel()
	if m == nil {
		return nil
	}
	if _, ok := a.details[m.Name]; ok {
		return nil
	}
	return a.showCmd(m.Name)
}

// isLoaded reports whether a model is currently resident in memory.
func (a *App) isLoaded(name string) bool {
	for _, r := range a.running {
		if r.Name == name || r.Model == name {
			return true
		}
	}
	return false
}

// --- pull -------------------------------------------------------------------

func (a *App) onPullChunk(msg pullChunkMsg) tea.Cmd {
	a.pullStatus = msg.progress.Status
	if d := msg.progress.Digest; d != "" && msg.progress.Total > 0 {
		found := false
		for i := range a.pullLayers {
			if a.pullLayers[i].digest == d {
				a.pullLayers[i].completed = msg.progress.Completed
				a.pullLayers[i].total = msg.progress.Total
				found = true
				break
			}
		}
		if !found {
			a.pullLayers = append(a.pullLayers, pullLayer{
				digest: d, total: msg.progress.Total, completed: msg.progress.Completed,
			})
		}
	}
	return tea.Batch(waitPull(a.pullFeed, msg.name), a.progress.SetPercent(a.pullPercent()))
}

func (a *App) onPullEnd(msg pullEndMsg) tea.Cmd {
	a.pulling = false
	a.pullLayers = nil
	a.pullStatus = ""
	if msg.err != nil {
		if msg.err.Error() == "context canceled" {
			return a.showToast("cancelled pull of "+msg.name, true)
		}
		return a.errToast(fmt.Errorf("pull %s: %w", msg.name, msg.err))
	}
	return tea.Batch(a.okToast("pulled "+msg.name), a.listModelsCmd())
}

// pullPercent is overall progress across every layer, weighted by byte count.
func (a *App) pullPercent() float64 {
	var total, done int64
	for _, l := range a.pullLayers {
		total += l.total
		done += l.completed
	}
	if total == 0 {
		return 0
	}
	return float64(done) / float64(total)
}

func (a *App) beginPull(name string) tea.Cmd {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if a.pulling {
		return a.showToast("a pull is already running", true)
	}
	a.pulling = true
	a.pullName = name
	a.pullStatus = "connecting"
	a.pullLayers = nil
	a.overlay = overlayNone
	return tea.Batch(a.startPull(name), a.progress.SetPercent(0), a.okToast("pulling "+name+"…"))
}

// --- view -------------------------------------------------------------------

func (a *App) viewModels() string {
	// The header takes a line of content plus a blank spacer.
	const headerRows = 2
	paneHeight := a.contentHeight() - headerRows
	listWidth := max(30, a.width*45/100)
	detailWidth := a.width - listWidth

	list := a.viewModelList(listWidth-2, paneHeight-2)
	detail := a.viewModelDetail(detailWidth-2, paneHeight-2)

	return lipgloss.JoinVertical(lipgloss.Left,
		a.viewModelsHeader(a.width),
		lipgloss.JoinHorizontal(lipgloss.Top,
			panel(list, listWidth, paneHeight, !a.modelSearchOn),
			panel(detail, detailWidth, paneHeight, false),
		),
	)
}

func (a *App) viewModelsHeader(width int) string {
	var left string
	if a.modelSearchOn || a.modelSearch.Value() != "" {
		left = theme.Key.Render("  filter ") + a.modelSearch.View()
	} else {
		n := len(a.models)
		var total int64
		for _, m := range a.models {
			total += m.Size
		}
		left = theme.Label.Render(fmt.Sprintf("  %d models", n)) +
			theme.Dim.Render(" · "+ollama.HumanBytes(total)+" on disk")
	}

	right := ""
	if a.pulling {
		right = a.spinner.View() + " " +
			lipgloss.NewStyle().Foreground(theme.Cyan).Render(a.pullName) + " " +
			a.progress.View() + " " +
			theme.Dim.Render(fmt.Sprintf("%3.0f%% %s", a.pullPercent()*100, a.pullStatus))
	}
	return spread(left, right+" ", width) + "\n"
}

func (a *App) viewModelList(width, height int) string {
	models := a.visibleModels()
	if len(models) == 0 {
		if a.loading {
			return a.spinner.View() + theme.Dim.Render(" loading models…")
		}
		if a.connErr != nil {
			return theme.Err.Render("cannot reach ollama") + "\n\n" +
				theme.Dim.Render(a.client.Host()) + "\n\n" +
				theme.Dim.Render("start it with ") + theme.Key.Render("ollama serve")
		}
		if a.modelSearch.Value() != "" {
			return theme.Dim.Render("no models match that filter")
		}
		return theme.Dim.Render("no models installed") + "\n\n" +
			theme.Dim.Render("press ") + theme.Key.Render("p") + theme.Dim.Render(" to pull one")
	}

	// Scroll the window so the cursor stays visible.
	start := 0
	if a.modelIdx >= height {
		start = a.modelIdx - height + 1
	}
	end := min(start+height, len(models))

	rows := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		rows = append(rows, a.modelRow(models[i], i == a.modelIdx, width))
	}
	return strings.Join(rows, "\n")
}

// modelRow is a single line: status marker, name, and size.
func (a *App) modelRow(m ollama.Model, selected bool, width int) string {
	mark := " "
	switch {
	case m.Name == a.cfg.Model:
		mark = lipgloss.NewStyle().Foreground(theme.Amber).Render("★")
	case a.isLoaded(m.Name):
		mark = lipgloss.NewStyle().Foreground(theme.Green).Render("●")
	}

	size := ollama.HumanBytes(m.Size)
	params := m.Details.ParameterSize
	meta := size
	if params != "" {
		meta = params + " · " + size
	}

	nameWidth := max(8, width-lipgloss.Width(meta)-4)
	name := theme.Truncate(m.ShortName(), nameWidth)

	if selected {
		row := fmt.Sprintf(" %s %s%s%s", mark, name,
			strings.Repeat(" ", max(1, nameWidth-lipgloss.Width(name)+1)), meta)
		return lipgloss.NewStyle().Background(theme.Surface).Foreground(theme.Text).
			Bold(true).Width(width).Render(theme.Truncate(row, width))
	}

	nameStyled := lipgloss.NewStyle().Foreground(theme.FamilyColor(m.Details.Family)).Render(name)
	pad := strings.Repeat(" ", max(1, nameWidth-lipgloss.Width(name)+1))
	return fmt.Sprintf(" %s %s%s%s", mark, nameStyled, pad, theme.Dim.Render(meta))
}

func (a *App) viewModelDetail(width, height int) string {
	m := a.selectedModel()
	if m == nil {
		return theme.Dim.Render("select a model to inspect it")
	}

	title := theme.GradientBold(theme.Truncate(m.ShortName(), width), theme.Violet, theme.Magenta)
	rows := []string{title, ""}

	lw := min(14, max(6, width/3))
	rows = append(rows,
		kv("tag", lipgloss.NewStyle().Foreground(theme.Amber).Render(m.Tag()), lw),
		kv("size", ollama.HumanBytes(m.Size), lw),
		kv("family", lipgloss.NewStyle().Foreground(theme.FamilyColor(m.Details.Family)).Render(orDash(m.Details.Family)), lw),
		kv("parameters", orDash(m.Details.ParameterSize), lw),
		kv("quantization", orDash(m.Details.QuantizationLevel), lw),
		kv("format", orDash(m.Details.Format), lw),
		kv("modified", ollama.HumanSince(m.ModifiedAt), lw),
		kv("digest", theme.Truncate(strings.TrimPrefix(m.Digest, "sha256:"), 12), lw),
	)

	d, ok := a.details[m.Name]
	if !ok {
		rows = append(rows, "", a.spinner.View()+theme.Dim.Render(" loading details…"))
		return strings.Join(rows, "\n")
	}

	if n := d.ContextLength(); n > 0 {
		rows = append(rows, kv("context", fmt.Sprintf("%s tokens", ollama.HumanCount(int64(n))), lw))
	}
	if n := d.ParameterCount(); n > 0 {
		rows = append(rows, kv("weights", ollama.HumanCount(n), lw))
	}

	if len(d.Capabilities) > 0 {
		badges := make([]string, 0, len(d.Capabilities))
		for _, c := range d.Capabilities {
			badges = append(badges, theme.Badge(c, capabilityColor(c)))
		}
		rows = append(rows, "", theme.Label.Render("CAPABILITIES"), strings.Join(badges, " "))
	}

	if a.isLoaded(m.Name) {
		rows = append(rows, "", theme.OK.Render("● loaded in memory"))
	}

	if sys := strings.TrimSpace(d.System); sys != "" {
		rows = append(rows, "", theme.Label.Render("SYSTEM PROMPT"),
			lipgloss.NewStyle().Foreground(theme.Subtle).Width(max(1, width)).
				Render(clip(sys, 320)))
	}

	if params := strings.TrimSpace(d.Parameters); params != "" {
		rows = append(rows, "", theme.Label.Render("PARAMETERS"), theme.Dim.Render(clip(params, 240)))
	}

	// A chatty card - long system prompt, many parameters - outgrows the pane, so it scrolls rather than losing its tail. There is nothing to select inside it, so nothing has to be kept in view.
	lines := flatten(rows)
	off := scrollOffset(len(lines), height, a.detailScroll, -1)
	return scrollPane(lines, max(1, width-scrollbarWidth), height, off, false)
}

// capabilityColor tints a capability badge by what it unlocks.
func capabilityColor(c string) color.Color {
	switch c {
	case "thinking":
		return theme.Magenta
	case "vision":
		return theme.Cyan
	case "tools":
		return theme.Amber
	case "embedding":
		return theme.Green
	default:
		return theme.Subtle
	}
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return theme.Dim.Render("·")
	}
	return s
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}
