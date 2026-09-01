// Package theme holds llamago's palette, gradients and shared styles.
package theme

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Palette is a complete set of the colours every view draws from. Themes are whole palettes rather than overrides: a half-swapped palette is how a theme ends up with unreadable pairings nobody chose.
type Palette struct {
	Name string

	// Hex values, converted on apply. Keeping them as strings is what lets a palette be written as a literal without a call on every field.
	Violet, Indigo, Magenta, Cyan, Teal, Green, Amber, Orange, Red string
	Text, Subtle, Muted, Faint, Surface, Deep                      string
	// AmberSoft marks a selection on content that is itself faint - reasoning, mainly - where full amber shouts over the text it is marking.
	AmberSoft string
}

// Palettes are the themes on offer, in the order they are cycled.
var Palettes = []Palette{
	{
		Name:   "midnight",
		Violet: "#A78BFA", Indigo: "#818CF8", Magenta: "#F472B6",
		Cyan: "#22D3EE", Teal: "#2DD4BF", Green: "#34D399",
		Amber: "#FBBF24", Orange: "#FB923C", Red: "#F87171",
		Text: "#E5E7EB", Subtle: "#9CA3AF", Muted: "#6B7280",
		Faint: "#4B5563", Surface: "#1E2030", Deep: "#16161E",
		AmberSoft: "#A8813C",
	},
	{
		// Same hues, darkened enough to hold their own against a white background: the light theme is not the dark one with the text inverted, which is how light themes end up washed out.
		Name:   "daylight",
		Violet: "#6D28D9", Indigo: "#4338CA", Magenta: "#BE185D",
		Cyan: "#0E7490", Teal: "#0F766E", Green: "#15803D",
		Amber: "#B45309", Orange: "#C2410C", Red: "#B91C1C",
		Text: "#111827", Subtle: "#374151", Muted: "#4B5563",
		Faint: "#9CA3AF", Surface: "#E5E7EB", Deep: "#F9FAFB",
		AmberSoft: "#8A6D1F",
	},
	{
		Name:   "ember",
		Violet: "#F59E0B", Indigo: "#F97316", Magenta: "#EF4444",
		Cyan: "#FCD34D", Teal: "#FBBF24", Green: "#A3E635",
		Amber: "#FDE68A", Orange: "#FB923C", Red: "#DC2626",
		Text: "#FEF3C7", Subtle: "#FCD34D", Muted: "#B45309",
		Faint: "#78350F", Surface: "#1C1917", Deep: "#0C0A09",
		AmberSoft: "#C9A227",
	},
}

// The active colours. Every view reads these, so switching a theme is a matter of reassigning them and rebuilding the styles derived from them.
var (
	Violet  color.Color
	Indigo  color.Color
	Magenta color.Color
	Cyan    color.Color
	Teal    color.Color
	Green   color.Color
	Amber   color.Color
	Orange  color.Color
	Red     color.Color

	Text    color.Color
	Subtle  color.Color
	Muted   color.Color
	Faint   color.Color
	Surface color.Color
	Deep    color.Color

	AmberSoft color.Color
)

// Brand is the gradient used for the wordmark and other hero text.
var Brand []color.Color

// Accent is the gradient used for progress bars and meters.
var Accent []color.Color

// Core styles shared across every view.
var (
	Title lipgloss.Style
	Dim   lipgloss.Style
	Label lipgloss.Style
	Key   lipgloss.Style
	Err   lipgloss.Style
	OK    lipgloss.Style

	// Panel is the resting border for a pane; Active marks the focused one.
	Panel  lipgloss.Style
	Active lipgloss.Style
)

// Current is the palette in use.
var Current = Palettes[0]

func init() { apply(Palettes[0]) }

// Names lists the available themes.
func Names() []string {
	out := make([]string, len(Palettes))
	for i, p := range Palettes {
		out[i] = p.Name
	}
	return out
}

// Use switches to a named theme, reporting whether there was one. Views read the package variables directly, so nothing else has to be told.
func Use(name string) bool {
	for _, p := range Palettes {
		if p.Name == name {
			apply(p)
			return true
		}
	}
	return false
}

// Next cycles to the following theme and returns its name, for a key that flips through them without needing to know what they are called.
func Next() string {
	for i, p := range Palettes {
		if p.Name == Current.Name {
			apply(Palettes[(i+1)%len(Palettes)])
			return Current.Name
		}
	}
	apply(Palettes[0])
	return Current.Name
}

// apply installs a palette and rebuilds everything derived from it.
func apply(p Palette) {
	Current = p
	hex := lipgloss.Color
	Violet, Indigo, Magenta = hex(p.Violet), hex(p.Indigo), hex(p.Magenta)
	Cyan, Teal, Green = hex(p.Cyan), hex(p.Teal), hex(p.Green)
	Amber, Orange, Red = hex(p.Amber), hex(p.Orange), hex(p.Red)
	Text, Subtle, Muted = hex(p.Text), hex(p.Subtle), hex(p.Muted)
	Faint, Surface, Deep = hex(p.Faint), hex(p.Surface), hex(p.Deep)
	AmberSoft = hex(p.AmberSoft)

	Brand = []color.Color{Violet, Magenta, Orange}
	Accent = []color.Color{Indigo, Cyan, Teal}

	Title = lipgloss.NewStyle().Bold(true).Foreground(Text)
	Dim = lipgloss.NewStyle().Foreground(Muted)
	Label = lipgloss.NewStyle().Foreground(Subtle)
	Key = lipgloss.NewStyle().Foreground(Violet).Bold(true)
	Err = lipgloss.NewStyle().Foreground(Red)
	OK = lipgloss.NewStyle().Foreground(Green)
	Panel = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(Faint)
	Active = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(Violet)
}

// Gradient colors each visible cell of s along the stops, left to right. ANSI sequences already in s are preserved and don't consume gradient steps.
func Gradient(s string, stops ...color.Color) string {
	if len(stops) == 0 {
		return s
	}
	runes := []rune(ansi.Strip(s))
	if len(runes) == 0 {
		return s
	}
	if len(runes) == 1 {
		return lipgloss.NewStyle().Foreground(stops[0]).Render(s)
	}
	ramp := lipgloss.Blend1D(len(runes), stops...)

	var b strings.Builder
	for i, r := range runes {
		b.WriteString(lipgloss.NewStyle().Foreground(ramp[i]).Render(string(r)))
	}
	return b.String()
}

// GradientBold renders s as a bold gradient.
func GradientBold(s string, stops ...color.Color) string {
	if len(stops) == 0 {
		return s
	}
	runes := []rune(ansi.Strip(s))
	if len(runes) < 2 {
		return lipgloss.NewStyle().Bold(true).Foreground(stops[0]).Render(s)
	}
	ramp := lipgloss.Blend1D(len(runes), stops...)

	var b strings.Builder
	for i, r := range runes {
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(ramp[i]).Render(string(r)))
	}
	return b.String()
}

// Rule draws a horizontal divider that fades out toward its right edge.
func Rule(width int) string {
	if width <= 0 {
		return ""
	}
	ramp := lipgloss.Blend1D(max(width, 2), Faint, Deep)
	var b strings.Builder
	for i := range width {
		b.WriteString(lipgloss.NewStyle().Foreground(ramp[i]).Render("─"))
	}
	return b.String()
}

// Meter renders a compact fixed-width bar filled to percent along Accent.
func Meter(width int, percent float64, stops ...color.Color) string {
	if width <= 0 {
		return ""
	}
	if len(stops) == 0 {
		stops = Accent
	}
	percent = min(max(percent, 0), 1)
	filled := int(percent * float64(width))
	ramp := lipgloss.Blend1D(max(width, 2), stops...)

	var b strings.Builder
	for i := range width {
		if i < filled {
			b.WriteString(lipgloss.NewStyle().Foreground(ramp[i]).Render("█"))
		} else {
			b.WriteString(lipgloss.NewStyle().Foreground(Faint).Render("─"))
		}
	}
	return b.String()
}

// Truncate shortens s to width display cells, adding an ellipsis when cut.
func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "…")
}

// Pad right-pads s with spaces to exactly width display cells.
func Pad(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return Truncate(s, width)
	}
	return s + strings.Repeat(" ", width-w)
}

// Badge renders a small pill-shaped label in the given color.
func Badge(text string, c color.Color) string {
	return lipgloss.NewStyle().Foreground(c).Render("▏" + text)
}

// FamilyColor maps a model family to a stable accent color so that the same model is always the same hue across every view.
func FamilyColor(family string) color.Color {
	switch strings.ToLower(family) {
	case "llama":
		return Violet
	case "qwen2", "qwen3", "qwen":
		return Magenta
	case "gemma", "gemma2", "gemma3":
		return Cyan
	case "phi2", "phi3", "phi":
		return Teal
	case "mistral", "mixtral":
		return Orange
	case "deepseek2", "deepseek":
		return Indigo
	case "bert", "nomic-bert":
		return Green
	default:
		return Amber
	}
}
