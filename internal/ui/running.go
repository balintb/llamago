package ui

import (
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/balintb/llamago/internal/ollama"
	"github.com/balintb/llamago/internal/theme"
)

func (a *App) onRunningKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		return a.goTab(tabChat)
	case "g", "home":
		a.runIdx = 0
	case "G", "end":
		a.runIdx = max(0, len(a.running)-1)
	case "j", "down":
		a.runIdx = min(a.runIdx+1, max(0, len(a.running)-1))
	case "k", "up":
		a.runIdx = max(a.runIdx-1, 0)
	case "u", "x":
		if r := a.selectedRunning(); r != nil {
			return a.unloadCmd(r.Name)
		}
	case "enter":
		if r := a.selectedRunning(); r != nil {
			cmd := a.setModel(r.Name)
			a.tab = tabChat
			a.layout()
			a.refreshTranscript()
			return tea.Batch(cmd, a.okToast("now chatting with "+strings.TrimSuffix(r.Name, ":latest")))
		}
	}
	return nil
}

func (a *App) selectedRunning() *ollama.RunningModel {
	if a.runIdx < 0 || a.runIdx >= len(a.running) {
		return nil
	}
	return &a.running[a.runIdx]
}

// viewRunning shows what Ollama currently holds in memory: the closest thing to a live resource monitor the API exposes.
func (a *App) viewRunning() string {
	h := a.contentHeight()
	width := a.width

	if len(a.running) == 0 {
		empty := lipgloss.JoinVertical(lipgloss.Center,
			theme.Dim.Render("Nothing loaded in memory."),
			"",
			theme.Dim.Render("Models load on first use and stay resident for ")+
				theme.Key.Render(a.cfg.KeepAlive)+theme.Dim.Render("."),
		)
		return panel(lipgloss.Place(width-2, h-2, lipgloss.Center, lipgloss.Center, empty), width, h, false)
	}

	var totalVRAM, totalSize int64
	for _, r := range a.running {
		totalVRAM += r.SizeVRAM
		totalSize += r.Size
	}

	rows := []string{
		theme.Label.Render(fmt.Sprintf("%d MODEL(S) RESIDENT", len(a.running))) +
			theme.Dim.Render(fmt.Sprintf("  ·  %s total · %s on GPU",
				ollama.HumanBytes(totalSize), ollama.HumanBytes(totalVRAM))),
		"",
	}

	selLine := 0
	for i, r := range a.running {
		if i == a.runIdx {
			selLine = len(flatten(rows))
		}
		rows = append(rows, a.runningCard(r, i == a.runIdx, width-5), "")
	}

	// Cards are several lines each, so a handful of resident models outgrows the pane; scrolling keeps the rest reachable rather than cutting it.
	lines := flatten(rows)
	inner := max(1, h-2)
	off := scrollOffset(len(lines), inner, 0, selLine)
	return panel(scrollPane(lines, width-2-scrollbarWidth, inner, off, true), width, h, true)
}

// runningCard renders one resident model with a GPU/CPU split meter and the countdown until Ollama evicts it.
func (a *App) runningCard(r ollama.RunningModel, selected bool, width int) string {
	marker := "  "
	name := lipgloss.NewStyle().Foreground(theme.FamilyColor(r.Details.Family)).Bold(true).
		Render(strings.TrimSuffix(r.Name, ":latest"))
	if selected {
		marker = lipgloss.NewStyle().Foreground(theme.Violet).Render("▌ ")
		name = lipgloss.NewStyle().Foreground(theme.Text).Bold(true).
			Render(strings.TrimSuffix(r.Name, ":latest"))
	}

	head := marker + name
	if r.Name == a.cfg.Model {
		head += " " + lipgloss.NewStyle().Foreground(theme.Amber).Render("★ active")
	}

	meterWidth := max(10, min(32, width-44))
	// Green when fully on GPU, amber when partially offloaded to CPU.
	stops := theme.Accent
	if r.GPUPercent() < 0.99 {
		stops = []color.Color{theme.Amber, theme.Orange}
	}
	meter := theme.Meter(meterWidth, r.GPUPercent(), stops...)

	detail := fmt.Sprintf("%s  %s  %s",
		theme.Pad(ollama.HumanBytes(r.Size), 10),
		meter,
		r.Processor(),
	)

	extra := []string{
		theme.Dim.Render("expires " + ollama.HumanUntil(r.ExpiresAt)),
	}
	if r.Details.ParameterSize != "" {
		extra = append(extra, theme.Dim.Render(r.Details.ParameterSize))
	}
	if r.Details.QuantizationLevel != "" {
		extra = append(extra, theme.Dim.Render(r.Details.QuantizationLevel))
	}
	if r.ContextLength > 0 {
		extra = append(extra, theme.Dim.Render(fmt.Sprintf("%s ctx", ollama.HumanCount(int64(r.ContextLength)))))
	}

	return head + "\n" +
		"    " + detail + "\n" +
		"    " + strings.Join(extra, theme.Dim.Render(" · "))
}
