package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/balintb/llamago/internal/theme"
)

// pulseFrames cycle a soft glow on the connection dot so an idle app still feels alive.
var pulseFrames = []string{"●", "◉", "●", "○"}

// View renders the whole frame: header, active tab, status bar, and any overlay composited on top.
func (a *App) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = a.windowTitle()

	if !a.ready {
		v.SetContent("")
		return v
	}
	if a.width < 50 || a.height < 14 {
		v.SetContent(lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center,
			theme.Err.Render("terminal too small")+"\n"+
				theme.Dim.Render(fmt.Sprintf("%d×%d - need at least 50×14", a.width, a.height))))
		return v
	}

	var body string
	switch {
	case a.tab == tabChat && a.comparing:
		body = a.viewCompare()
	default:
		body = a.viewTab()
	}

	frame := lipgloss.JoinVertical(lipgloss.Left, a.viewHeader(), body, a.viewStatus())
	return a.finish(v, frame)
}

// viewTab renders the active tab's body.
func (a *App) viewTab() string {
	switch a.tab {
	case tabChat:
		return a.viewChat()
	case tabModels:
		return a.viewModels()
	case tabRunning:
		return a.viewRunning()
	case tabSettings:
		return a.viewSettings()
	}
	return ""
}

// finish composites any overlay and settles the cursor.
func (a *App) finish(v tea.View, frame string) tea.View {
	if a.overlay != overlayNone {
		over, ox, oy := a.placeOverlay(a.viewOverlay())
		frame = composite(frame, over, ox, oy, a.width, a.height)
		if c := a.overlayCursor(); c != nil {
			c.X += ox
			c.Y += oy
			v.Cursor = c
		}
	} else if c := a.cursor(); c != nil {
		v.Cursor = c
	}

	v.SetContent(frame)
	return v
}

func (a *App) windowTitle() string {
	if a.streaming {
		return "llamago · generating…"
	}
	if a.cfg.Model != "" {
		return "llamago · " + a.cfg.Model
	}
	return "llamago"
}

// cursor maps the focused text widget's local cursor onto absolute screen coordinates so the terminal draws a real, native cursor.
func (a *App) cursor() *tea.Cursor {
	// The tab strip owns the keyboard; no text widget should show a cursor.
	if a.tabBarFocus {
		return nil
	}
	switch {
	case a.tab == tabChat && a.comparing && a.searching:
		c := a.searchIn.Cursor()
		if c == nil {
			return nil
		}
		x, y := a.compareSearchBarOrigin()
		c.X += x
		c.Y += y
		c.Color = theme.Magenta
		return c
	case a.tab == tabChat && a.comparing:
		if a.compareFocus != compareFocusComposer {
			return nil
		}
		c := a.input.Cursor()
		if c == nil {
			return nil
		}
		x, y := a.compareInputOrigin()
		c.X = clampI(x+max(c.X, 0), x, x+max(0, a.input.Width()-1))
		c.Y = clampI(y+max(c.Y, 0), y, y+max(0, a.inputHeight()-1))
		c.Color = theme.Magenta
		return c
	case a.tab == tabChat && a.focus == focusInput:
		c := a.input.Cursor()
		if c == nil {
			return nil
		}
		x, y := a.inputOrigin()
		// The composer scrolls internally, and the widget only syncs its scroll offset to the cursor while handling input. Clamp to the interior so a momentarily stale offset can never paint the cursor over the border or into the pane below.
		c.X = clampI(x+max(c.X, 0), x, x+max(0, a.input.Width()-1))
		c.Y = clampI(y+max(c.Y, 0), y, y+max(0, a.inputHeight()-1))
		c.Color = theme.Magenta
		return c
	case a.tab == tabChat && a.searching:
		c := a.searchIn.Cursor()
		if c == nil {
			return nil
		}
		x, y := a.searchBarOrigin()
		c.X += x
		c.Y += y
		c.Color = theme.Magenta
		return c
	case a.tab == tabModels && a.modelSearchOn:
		c := a.modelSearch.Cursor()
		if c == nil {
			return nil
		}
		c.X += 10
		c.Y += headerHeight + 1
		c.Color = theme.Magenta
		return c
	}
	return nil
}

// placeOverlay clamps a modal to the frame and returns it with the origin it should be drawn at. Clamping keeps it clear of the header and status bar: at that size, centering leaves exactly those rows exposed.
func (a *App) placeOverlay(over string) (clamped string, x, y int) {
	clamped = fitBlock(over, max(4, a.width-4), max(3, a.height-4))
	ow, oh := lipgloss.Size(clamped)
	return clamped, max(0, (a.width-ow)/2), max(0, (a.height-oh)/2)
}

// composite draws an overlay on top of the base frame at (x, y).
//
// Positioning must go through a Compositor: a Layer's own x/y/z are only read when the compositor flattens the hierarchy. Handing a Layer straight to Canvas.Compose draws it at the canvas origin and clears everything else.
func composite(base, over string, x, y, width, height int) string {
	comp := lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(over).X(x).Y(y).Z(1),
	)
	canvas := lipgloss.NewCanvas(width, height)
	canvas.Compose(comp)
	return canvas.Render()
}

// Where a modal's body starts inside its box: past the left border and padding, and past the top border, title and rule.
const (
	modalBodyX = 2
	modalBodyY = 3
)

// overlayCursor is the focused modal input's cursor, in box-relative cells. The inputs draw no cursor of their own (virtual cursors are off), so without this you would be typing blind in the palette and the prompt editors.
func (a *App) overlayCursor() *tea.Cursor {
	var (
		c   *tea.Cursor
		row int // the input's row within the modal body
	)
	switch a.overlay {
	case overlayPalette:
		c, row = a.paletteIn.Cursor(), 0
	case overlayPull:
		c, row = a.pullInput.Cursor(), 2
	case overlaySystem:
		c, row = a.sysInput.Cursor(), 2
	case overlayRename:
		c, row = a.renameIn.Cursor(), 0
	case overlayNudge:
		c, row = a.nudgeIn.Cursor(), 3
	default:
		return nil
	}
	if c == nil {
		return nil
	}
	c.X += modalBodyX
	c.Y += modalBodyY + row
	c.Color = theme.Magenta
	return c
}

// --- header -----------------------------------------------------------------

// headerLogo and headerStatus are the two pieces left of the tab strip. They are separate so the click handler can measure where the tabs begin from the same strings the header draws, rather than from a copy that drifts.
func headerLogo() string {
	return lipgloss.NewStyle().Foreground(theme.Violet).Render("◆ ") +
		theme.GradientBold("llamago", theme.Brand...)
}

// headerStatus is the connection indicator: a pulsing dot when healthy, a red one when not.
func (a *App) headerStatus() string {
	if a.connErr != nil {
		return theme.Err.Render("● offline")
	}
	dot := lipgloss.NewStyle().Foreground(theme.Green).Render(pulseFrames[a.frame%len(pulseFrames)])
	label := "connected"
	if a.version != "" {
		label = "v" + a.version
	}
	return dot + " " + theme.Dim.Render(label)
}

// tabsOriginX is the column the tab strip starts at: the leading space, then the logo and the status with the gaps the header puts between them.
func (a *App) tabsOriginX() int {
	return 1 + lipgloss.Width(headerLogo()) + 2 + lipgloss.Width(a.headerStatus()) + 3
}

// tabSpans is the screen rectangle of each tab's label, in tab order, so a click can be turned back into the tab it landed on.
func (a *App) tabSpans() []rect {
	x := a.tabsOriginX()
	out := make([]rect, 0, len(tabNames))
	for _, chip := range a.tabChips() {
		w := lipgloss.Width(chip)
		out = append(out, rect{x0: x, y0: 0, x1: x + w, y1: 1})
		x += w + 1 // the separator between chips
	}
	return out
}

func (a *App) viewHeader() string {
	logo := headerLogo()
	status := a.headerStatus()
	tabs := a.viewTabs()

	// Right side: active model and its context window.
	right := theme.Dim.Render("no model")
	if a.cfg.Model != "" {
		name := lipgloss.NewStyle().Foreground(theme.Amber).Render(a.cfg.Model)
		right = name + theme.Dim.Render(fmt.Sprintf(" · %dk ctx", max(1, a.cfg.NumCtx/1024)))
	}
	// Say whether tools are actually in play. Switched on but unusable by this model is the state worth naming: without it, a model answering "I cannot know the time" looks like a broken feature rather than a model that never had the option.
	if a.cfg.Tools {
		switch {
		case !a.modelCanTools(a.cfg.Model):
			right += theme.Dim.Render(" · ") +
				lipgloss.NewStyle().Foreground(theme.Amber).Render("⚒ unsupported")
		case len(a.enabledTools()) == 0:
			right += theme.Dim.Render(" · ⚒ none on")
		default:
			right += theme.Dim.Render(fmt.Sprintf(" · ⚒ %d", len(a.enabledTools())))
		}
	}
	// Flag a conversation running under a prompt other than the current one, so a surprising answer has a visible explanation.
	if a.cur != nil && a.cur.System != "" && a.cur.System != a.cfg.System {
		right += theme.Dim.Render(" · ") + lipgloss.NewStyle().Foreground(theme.Amber).Render("⚑ own prompt")
	}

	left := logo + theme.Dim.Render("  ") + status + theme.Dim.Render("   ") + tabs
	line := spread(" "+left, right+" ", a.width)
	return line + "\n" + theme.Rule(a.width)
}

func (a *App) viewTabs() string {
	return strings.Join(a.tabChips(), theme.Dim.Render(" "))
}

// tabChips renders each tab label. The strip and the click targets are measured from these same strings, so they cannot disagree about where a tab is.
func (a *App) tabChips() []string {
	parts := make([]string, 0, len(tabNames))
	for i, name := range tabNames {
		switch {
		case tab(i) == a.tab && a.tabBarFocus:
			// Focused: fill the chip so it reads as a selection being moved.
			parts = append(parts, lipgloss.NewStyle().
				Foreground(theme.Deep).Background(theme.Violet).Bold(true).
				Render(" "+name+" "))
		case tab(i) == a.tab:
			parts = append(parts, lipgloss.NewStyle().Foreground(theme.Text).Bold(true).
				Render("▸ "+name))
		default:
			parts = append(parts, theme.Dim.Render("  "+name))
		}
	}
	return parts
}

// --- status bar -------------------------------------------------------------

func (a *App) viewStatus() string {
	// A live toast outranks the key hints.
	if a.toast != "" {
		icon, style := "✓", theme.OK
		if a.toastErr {
			icon, style = "✗", theme.Err
		}
		return theme.Pad(" "+style.Render(icon+" "+theme.Truncate(a.toast, a.width-4)), a.width)
	}

	if a.tabBarFocus {
		hints := []string{"←→ switch tab", "↵ open", "esc back", "ctrl+k palette"}
		return theme.Pad(theme.Truncate(" "+joinHints(hints), a.width), a.width)
	}

	var hints []string
	switch a.tab {
	case tabChat:
		switch {
		case a.comparing:
			hints = []string{"↵ ask all", "alt+a add model", "ctrl+f find", "tab column", "ctrl+\\ exit"}
		case a.streaming:
			hints = []string{"ctrl+c stop", "tab focus"}
		case a.focus == focusSessions:
			// The session keys are only live while the sidebar has the keyboard, and were reachable but invisible until now.
			hints = []string{"↵ open", "n new", "r rename", "p pin", "c duplicate", "d delete"}
		case a.selTurn >= 0:
			// A live selection replaces the general hints: these are the keys that act on it.
			hints = []string{"shift+↑↓ select", "↵ edit", "y copy", "r rewind", "f fork", "x delete"}
		default:
			// ↵ send lives on the composer hint line, right under the input.
			hints = []string{"ctrl+i image", "ctrl+f find", "ctrl+s export", "tab focus"}
		}
	case tabModels:
		hints = []string{"↑↓ move", "↵ use", "p pull", "d delete", "/ filter", "esc chat"}
	case tabRunning:
		hints = []string{"↑↓ move", "u unload", "ctrl+r refresh", "esc chat"}
	case tabSettings:
		hints = []string{"↑↓ move", "←→ adjust", "↵ edit", "g/G ends", "esc chat"}
	}
	hints = append(hints, "ctrl+k palette", "f1 help")

	return theme.Pad(theme.Truncate(" "+joinHints(hints), a.width), a.width)
}

// joinHints renders "key description" pairs as a separated hint line.
func joinHints(hints []string) string {
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		k, rest, ok := strings.Cut(h, " ")
		if !ok {
			parts = append(parts, theme.Dim.Render(h))
			continue
		}
		parts = append(parts, theme.Key.Render(k)+" "+theme.Dim.Render(rest))
	}
	return strings.Join(parts, theme.Dim.Render(" · "))
}

// --- shared widgets ---------------------------------------------------------

// panel wraps content in a rounded border, tinted when focused. width and height are the totals the pane occupies, border included.
//
// The content is force-fitted to the interior first. lipgloss would otherwise wrap an over-long line and treat height as a minimum, so a single wide row could silently grow the pane and push the rest of the frame off screen.
func panel(content string, width, height int, focused bool) string {
	s := theme.Panel
	if focused {
		s = theme.Active
	}
	content = fitBlock(content, max(1, width-2), max(1, height-2))
	return s.Width(width).Height(height).Render(content)
}

// fitBlock clips a block of text to at most w cells wide and h lines tall.
func fitBlock(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for i, l := range lines {
		if lipgloss.Width(l) > w {
			lines[i] = theme.Truncate(l, w)
		}
	}
	return strings.Join(lines, "\n")
}

// spread lays left and right out on one line of exactly width cells, pushing right to the far edge and truncating left if they collide.
func spread(left, right string, width int) string {
	if width <= 0 {
		return ""
	}
	lw, rw := lipgloss.Width(left), lipgloss.Width(right)
	if lw+rw > width {
		// Drop the right side entirely before mangling the left.
		if rw+8 > width {
			return theme.Pad(theme.Truncate(left, width), width)
		}
		left = theme.Truncate(left, width-rw)
		lw = lipgloss.Width(left)
	}
	return left + strings.Repeat(" ", max(0, width-lw-rw)) + right
}

// kv renders an aligned "label  value" row for the detail panes.
func kv(label, value string, labelWidth int) string {
	return theme.Dim.Render(theme.Pad(label, labelWidth)) + value
}
