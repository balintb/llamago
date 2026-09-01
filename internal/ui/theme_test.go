package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/config"
	"github.com/balintb/llamago/internal/theme"
)

func TestThemeSwitchRepaints(t *testing.T) {
	t.Cleanup(func() { theme.Use("midnight") })

	a := chatWithPrompts(t, "first")
	a.toast = ""
	before := render(a)

	runSlashLine(a, "/theme daylight")
	a.toast = "" // the confirmation would otherwise be the difference
	if theme.Current.Name != "daylight" {
		t.Fatalf("theme = %q, want daylight", theme.Current.Name)
	}
	if a.cfg.Theme != "daylight" {
		t.Error("the choice was not saved")
	}

	after := render(a)
	if before == after {
		t.Fatal("the frame is unchanged after switching palettes")
	}
	// Only the colours changed: the words are the same.
	if ansi.Strip(before) != ansi.Strip(after) {
		t.Fatal("switching palettes changed the text, not just the colours")
	}
}

// Cached renderings hold the palette they were made with, so they have to go.
func TestThemeSwitchClearsCachedRenderings(t *testing.T) {
	t.Cleanup(func() { theme.Use("midnight") })

	a := chatWithPrompts(t, "first")
	a.cur.Turns[1].Content = "# Heading"
	a.refreshTranscript()
	before := a.renderConversation()

	runSlashLine(a, "/theme ember")
	if got := a.renderConversation(); got == before {
		t.Fatal("a cached rendering survived the palette change")
	}
}

func TestUnknownThemeIsRefused(t *testing.T) {
	t.Cleanup(func() { theme.Use("midnight") })

	a := chatWithPrompts(t)
	runSlashLine(a, "/theme neon")
	if theme.Current.Name != "midnight" {
		t.Fatalf("theme = %q, want it unchanged", theme.Current.Name)
	}
	if !a.toastErr || !strings.Contains(a.toast, "daylight") {
		t.Errorf("toast = %q, want it to list the themes", a.toast)
	}
}

// /theme with no name cycles, so the palettes can be flipped through without knowing what they are called.
func TestBareThemeCommandCycles(t *testing.T) {
	t.Cleanup(func() { theme.Use("midnight") })

	a := chatWithPrompts(t)
	theme.Use("midnight")
	seen := map[string]bool{}
	for range theme.Names() {
		runSlashLine(a, "/theme")
		seen[theme.Current.Name] = true
	}
	if len(seen) != len(theme.Names()) {
		t.Fatalf("cycling visited %d themes, want %d", len(seen), len(theme.Names()))
	}
}

// A theme chosen last time is in force before the first frame is built.
func TestConfiguredThemeAppliesAtStartup(t *testing.T) {
	t.Cleanup(func() { theme.Use("midnight") })

	cfg := config.Default()
	cfg.Theme = "ember"
	_ = New(cfg)
	if theme.Current.Name != "ember" {
		t.Fatalf("theme = %q, want the configured one", theme.Current.Name)
	}
}
