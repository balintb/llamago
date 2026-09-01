package ui

import (
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/balintb/llamago/internal/config"
	"github.com/balintb/llamago/internal/theme"
)

// setting describes one editable row. Adjusting is expressed as a closure over the config so the table stays declarative and the view stays dumb.
type setting struct {
	name  string
	help  string
	value func(c config.Config) string
	// adjust nudges the value by dir (-1 or +1); coarse is set when shift is held.
	adjust func(c *config.Config, dir int, coarse bool)
	// bar renders an optional 0..1 fill meter for continuous values.
	bar func(c config.Config) (float64, bool)
	// open is set on the fields that are edited in an overlay rather than nudged. ↵ and → run it; those fields have no adjust.
	open func(a *App) tea.Cmd
	// preview is the second line such a field shows: the value itself, which is too long to sit in a column.
	preview func(a *App) string
}

var settings = []setting{
	{
		name:  "temperature",
		help:  "randomness; 0 is deterministic, 1+ gets creative",
		value: func(c config.Config) string { return fmt.Sprintf("%.2f", c.Temperature) },
		adjust: func(c *config.Config, dir int, coarse bool) {
			c.Temperature = clampF(c.Temperature+step(dir, coarse, 0.05, 0.25), 0, 2)
		},
		bar: func(c config.Config) (float64, bool) { return c.Temperature / 2, true },
	},
	{
		name:  "top_p",
		help:  "nucleus sampling cutoff",
		value: func(c config.Config) string { return fmt.Sprintf("%.2f", c.TopP) },
		adjust: func(c *config.Config, dir int, coarse bool) {
			c.TopP = clampF(c.TopP+step(dir, coarse, 0.01, 0.1), 0, 1)
		},
		bar: func(c config.Config) (float64, bool) { return c.TopP, true },
	},
	{
		name:  "top_k",
		help:  "how many candidate tokens to consider",
		value: func(c config.Config) string { return fmt.Sprintf("%d", c.TopK) },
		adjust: func(c *config.Config, dir int, coarse bool) {
			c.TopK = clampI(c.TopK+int(step(dir, coarse, 1, 10)), 0, 200)
		},
		bar: func(c config.Config) (float64, bool) { return float64(c.TopK) / 200, true },
	},
	{
		name:  "repeat_penalty",
		help:  "discourages the model from repeating itself",
		value: func(c config.Config) string { return fmt.Sprintf("%.2f", c.RepeatPenalty) },
		adjust: func(c *config.Config, dir int, coarse bool) {
			c.RepeatPenalty = clampF(c.RepeatPenalty+step(dir, coarse, 0.01, 0.1), 0.5, 2)
		},
		bar: func(c config.Config) (float64, bool) { return (c.RepeatPenalty - 0.5) / 1.5, true },
	},
	{
		name:  "num_ctx",
		help:  "context window in tokens; larger costs more memory",
		value: func(c config.Config) string { return fmt.Sprintf("%d", c.NumCtx) },
		adjust: func(c *config.Config, dir int, coarse bool) {
			// Context sizes are meaningful in powers of two, so double or halve.
			if coarse {
				if dir > 0 {
					c.NumCtx = clampI(c.NumCtx*2, 512, 262144)
				} else {
					c.NumCtx = clampI(c.NumCtx/2, 512, 262144)
				}
				return
			}
			c.NumCtx = clampI(c.NumCtx+dir*512, 512, 262144)
		},
	},
	{
		name: "num_predict",
		help: "max tokens to generate; -1 means no limit",
		value: func(c config.Config) string {
			if c.NumPredict <= 0 {
				return "unlimited"
			}
			return fmt.Sprintf("%d", c.NumPredict)
		},
		adjust: func(c *config.Config, dir int, coarse bool) {
			c.NumPredict = clampI(c.NumPredict+int(step(dir, coarse, 64, 512)), -1, 131072)
		},
	},
	{
		name: "seed",
		help: "fix for reproducible output; random re-rolls every request",
		value: func(c config.Config) string {
			if c.Seed == 0 {
				return "random"
			}
			return fmt.Sprintf("%d", c.Seed)
		},
		adjust: func(c *config.Config, dir int, coarse bool) {
			c.Seed = clampI(c.Seed+int(step(dir, coarse, 1, 100)), 0, 1<<31-1)
		},
	},
	{
		name:  "keep_alive",
		help:  "how long a model stays loaded after use",
		value: func(c config.Config) string { return c.KeepAlive },
		adjust: func(c *config.Config, dir int, coarse bool) {
			opts := []string{"0", "30s", "5m", "15m", "1h", "24h", "-1"}
			i := 2
			for j, o := range opts {
				if o == c.KeepAlive {
					i = j
					break
				}
			}
			c.KeepAlive = opts[clampI(i+dir, 0, len(opts)-1)]
		},
	},
	{
		name:   "thinking",
		help:   "show reasoning from models that expose it",
		value:  func(c config.Config) string { return onOff(c.Think) },
		adjust: func(c *config.Config, dir int, coarse bool) { c.Think = !c.Think },
	},
	{
		name:   "timestamps",
		help:   "show when each message was sent",
		value:  func(c config.Config) string { return onOff(c.Timestamps) },
		adjust: func(c *config.Config, dir int, coarse bool) { c.Timestamps = !c.Timestamps },
	},
	{
		name:   "auto_title",
		help:   "let the model name a chat after its first exchange",
		value:  func(c config.Config) string { return onOff(c.AutoTitle) },
		adjust: func(c *config.Config, dir int, coarse bool) { c.AutoTitle = !c.AutoTitle },
	},
	{
		name:   "resume",
		help:   "reopen the last conversation on startup",
		value:  func(c config.Config) string { return onOff(c.Resume) },
		adjust: func(c *config.Config, dir int, coarse bool) { c.Resume = !c.Resume },
	},
	{
		name: "system prompt",
		help: "sent at the start of every conversation · ↵ to edit",
		value: func(c config.Config) string {
			if strings.TrimSpace(c.System) == "" {
				return "none"
			}
			return "set"
		},
		open:    func(a *App) tea.Cmd { return a.openEditor(editSystem) },
		preview: func(a *App) string { return a.systemPreview() },
	},
	{
		name: "stop sequences",
		help: "generation halts on any of these · ↵ to edit",
		value: func(c config.Config) string {
			if len(c.Stop) == 0 {
				return "none"
			}
			return fmt.Sprintf("%d", len(c.Stop))
		},
		open:    func(a *App) tea.Cmd { return a.openEditor(editStop) },
		preview: func(a *App) string { return a.stopPreview() },
	},
	{
		name:   "tools",
		help:   "let models call tools; each one can be switched off below",
		value:  func(c config.Config) string { return onOff(c.Tools) },
		adjust: func(c *config.Config, dir int, coarse bool) { c.Tools = !c.Tools },
	},
	{
		name:   "markdown",
		help:   "render responses as rich markdown",
		value:  func(c config.Config) string { return onOff(c.Markdown) },
		adjust: func(c *config.Config, dir int, coarse bool) { c.Markdown = !c.Markdown },
	},
}

// settingsFields is every row the tab offers: the fixed ones, then a checkbox per registered tool. Tools are discovered at startup and can be declared by anyone, so the list cannot be a fixed table.
func (a *App) settingsFields() []setting {
	out := append([]setting{}, settings...)
	if a.tools == nil {
		return out
	}
	for _, t := range a.tools.All() {
		name, safe := t.Name(), t.Safe()
		help := "asks before every call"
		if safe {
			help = "reads only, runs without asking"
		}
		out = append(out, setting{
			name: "  " + name,
			help: help,
			value: func(c config.Config) string {
				if c.ToolOff[name] {
					return "off"
				}
				return "on"
			},
			adjust: func(c *config.Config, dir int, coarse bool) {
				if c.ToolOff == nil {
					c.ToolOff = map[string]bool{}
				}
				c.ToolOff[name] = !c.ToolOff[name]
			},
		})
	}
	return out
}

func (a *App) onSettingsKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		return a.goTab(tabChat)
	case "j", "down":
		// Past the last field the arrows keep working, scrolling the tail into view: the connection block is not selectable and would otherwise need a second set of keys nobody knows about.
		if a.setIdx == len(a.settingsFields())-1 {
			a.setScroll = a.settingsOffset() + 1
			break
		}
		a.setIdx, a.setScroll = a.setIdx+1, 0
	case "k", "up":
		// Always a move, never an unwind. Scrolling into the tail happens at the end of the list; coming back up is a selection moving and the view following it, which is what every list does.
		a.setIdx, a.setScroll = max(a.setIdx-1, 0), 0
	case "pgdown", "ctrl+d":
		if a.setIdx == len(a.settingsFields())-1 {
			a.setScroll = a.settingsOffset() + a.settingsPage()
			break
		}
		a.setIdx, a.setScroll = min(a.setIdx+a.settingsPage(), len(a.settingsFields())-1), 0
	case "pgup", "ctrl+u":
		a.setIdx, a.setScroll = max(a.setIdx-a.settingsPage(), 0), 0
	case "g", "home":
		a.setIdx, a.setScroll = 0, 0
	case "G", "end":
		// The very bottom is tail, not a field, so this is both moves at once.
		fields := a.settingsFields()
		a.setIdx, a.setScroll = len(fields)-1, len(fields)*4
	case "left", "h":
		return a.adjustSetting(-1, false)
	case "right", "l":
		return a.adjustSetting(1, false)
	case "shift+left", "H":
		return a.adjustSetting(-1, true)
	case "shift+right", "L":
		return a.adjustSetting(1, true)
	case "enter", " ":
		if s := a.settingsFields()[a.setIdx]; s.open != nil {
			return s.open(a)
		}
		return a.adjustSetting(1, false)
	case "e":
		return a.openEditor(editSystem)
	case "s":
		return a.openEditor(editStop)
	case "r":
		model := a.cfg.Model
		a.cfg = config.Default()
		a.cfg.Model = model
		a.cfg.System = a.sysInput.Value()
		_ = a.cfg.Save()
		return a.okToast("settings reset to defaults")
	}
	return nil
}

func (a *App) adjustSetting(dir int, coarse bool) tea.Cmd {
	fields := a.settingsFields()
	if a.setIdx >= len(fields) {
		return nil
	}
	s := fields[a.setIdx]
	// Fields edited in an overlay have nothing to nudge; ← and → open them rather than doing nothing at all.
	if s.adjust == nil {
		if s.open != nil {
			return s.open(a)
		}
		return nil
	}
	s.adjust(&a.cfg, dir, coarse)
	_ = a.cfg.Save()
	if s.name == "markdown" || s.name == "timestamps" {
		a.invalidateRenders()
		a.refreshTranscript()
	}
	return nil
}

// settingsLines builds every line of the settings pane and reports which line carries the selected field, so the view can keep it on screen.
func (a *App) settingsLines() (rows []string, selLine int) {
	// Leave room for the border and the scrollbar column.
	width := a.width - 4

	rows = []string{
		theme.Label.Render("INFERENCE") +
			theme.Dim.Render("   ←→ adjust · shift+←→ bigger steps · saved automatically") +
			presetLabel(a.cfg),
		"",
	}

	const nameW = 18
	fields := a.settingsFields()
	for i, s := range fields {
		// The tool rows get a heading of their own.
		if i == len(settings) {
			rows = append(rows, "", theme.Label.Render("TOOLS")+a.toolsSummary())
		}
		selected := i == a.setIdx
		if selected {
			selLine = len(rows)
		}
		name := theme.Pad(s.name, nameW)
		if selected {
			name = lipgloss.NewStyle().Foreground(theme.Text).Bold(true).Render(name)
		} else {
			name = theme.Dim.Render(name)
		}

		value := s.value(a.cfg)
		valueStyled := lipgloss.NewStyle().Foreground(theme.Amber).Render(theme.Pad(value, 12))
		if !selected {
			valueStyled = lipgloss.NewStyle().Foreground(theme.Subtle).Render(theme.Pad(value, 12))
		}

		bar := ""
		if s.bar != nil {
			if p, ok := s.bar(a.cfg); ok {
				stops := []color.Color{theme.Faint, theme.Faint}
				if selected {
					stops = theme.Accent
				}
				bar = theme.Meter(min(28, max(8, width/3)), p, stops...) + "  "
			}
		}

		marker := "  "
		if selected {
			marker = lipgloss.NewStyle().Foreground(theme.Violet).Render("▌ ")
		}

		line := marker + name + valueStyled + bar
		if selected {
			line += theme.Dim.Render(s.help)
		}
		rows = append(rows, theme.Truncate(line, width))
		// A value too long for the column gets the line beneath it, indented to sit under the value rather than the name.
		if s.preview != nil {
			rows = append(rows, indent(s.preview(a), strings.Repeat(" ", 4)))
		}
	}

	rows = append(rows, "",
		theme.Label.Render("CONNECTION"), "",
		kv("  host", a.hostLine(), 18),
		kv("  status", a.connectionLine(), 18),
		kv("  config", theme.Dim.Render(configPath()), 18),
	)

	// A wrapped preview is several screen lines from one entry, so count what will actually be drawn rather than what was appended.
	return flatten(rows), selLine
}

func (a *App) viewSettings() string {
	h, inner := a.contentHeight(), a.settingsInner()
	rows, _ := a.settingsLines()
	content := scrollPane(rows, a.width-2-scrollbarWidth, inner, a.settingsOffset(), true)
	return panel(content, a.width, h, true)
}

// settingsInner is the height of the pane's interior.
func (a *App) settingsInner() int { return max(1, a.contentHeight()-2) }

// settingsOffset is the offset the pane is actually drawn at, which is not always the stored one: the selection pulls it back when it would fall off.
func (a *App) settingsOffset() int {
	rows, selLine := a.settingsLines()
	return scrollOffset(len(rows), a.settingsInner(), a.setScroll, selLine)
}

// settingsPage is how far the paging keys move, in fields.
func (a *App) settingsPage() int { return max(1, a.contentHeight()/3) }

// systemPreview is the system prompt as it appears under its row.
func (a *App) systemPreview() string {
	sys := strings.TrimSpace(a.cfg.System)
	if sys == "" {
		return theme.Dim.Render("none - press ") + theme.Key.Render("↵") +
			theme.Dim.Render(" to write one")
	}
	return lipgloss.NewStyle().Foreground(theme.Subtle).
		Width(max(1, a.width-10)).Render(clip(sys, 300))
}

// stopPreview is the stop list as it appears under its row.
func (a *App) stopPreview() string {
	if len(a.cfg.Stop) == 0 {
		return theme.Dim.Render("none - press ") + theme.Key.Render("↵") +
			theme.Dim.Render(" to add some")
	}
	quoted := make([]string, len(a.cfg.Stop))
	for i, q := range a.cfg.Stop {
		quoted[i] = fmt.Sprintf("%q", q)
	}
	return lipgloss.NewStyle().Foreground(theme.Subtle).
		Width(max(1, a.width-10)).Render(strings.Join(quoted, "  "))
}

// presetLabel names the preset the current numbers equal, so the settings say "creative" rather than leaving four values to be recognised.
func presetLabel(c config.Config) string {
	name := c.MatchPreset()
	if name == "" {
		return ""
	}
	return theme.Dim.Render("   preset ") + lipgloss.NewStyle().Foreground(theme.Amber).Render(name)
}

// openEditor puts the multi-line editor on screen for one of the free-text settings. Both share the textarea; only the seed value and the title differ.
func (a *App) openEditor(target editorTarget) tea.Cmd {
	a.overlay = overlaySystem
	a.editTarget = target
	if target == editStop {
		a.sysInput.SetValue(strings.Join(a.cfg.Stop, "\n"))
	} else {
		a.sysInput.SetValue(a.cfg.System)
	}
	return a.sysInput.Focus()
}

// parseStops reads the editor's contents as one sequence per line. Blank lines are dropped, but inner whitespace is kept: a stop sequence is matched literally, so trimming it would change what it matches.
func parseStops(s string) []string {
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// hostLine is the active server, named when it is one of the saved ones.
func (a *App) hostLine() string {
	out := lipgloss.NewStyle().Foreground(theme.Cyan).Render(a.client.Host())
	if name := a.cfg.HostName(a.client.Host()); name != "" {
		out += theme.Dim.Render("  " + name)
	}
	return out
}

func (a *App) connectionLine() string {
	if a.connErr != nil {
		return theme.Err.Render("offline - " + theme.Truncate(a.connErr.Error(), 60))
	}
	if a.version == "" {
		return theme.OK.Render("connected")
	}
	return theme.OK.Render("connected") + theme.Dim.Render("  ollama "+a.version)
}

func configPath() string {
	d, err := config.Dir()
	if err != nil {
		return "unavailable"
	}
	return d
}

// --- numeric helpers --------------------------------------------------------

func step(dir int, coarse bool, fine, big float64) float64 {
	if coarse {
		return float64(dir) * big
	}
	return float64(dir) * fine
}

func clampF(v, lo, hi float64) float64 { return min(max(v, lo), hi) }
func clampI(v, lo, hi int) int         { return min(max(v, lo), hi) }
