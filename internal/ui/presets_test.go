package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/config"
)

func TestPresetAppliesSamplingOnly(t *testing.T) {
	a := chatWithPrompts(t)
	a.cfg.NumCtx, a.cfg.Model = 8192, "llama3.2:3b"
	typeInto(a, "/preset creative")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if a.cfg.Temperature != 1.2 || a.cfg.TopK != 100 {
		t.Fatalf("sampling = %+v, want the creative preset", a.cfg.Preset())
	}
	// A preset is about the writing, not the machine.
	if a.cfg.NumCtx != 8192 || a.cfg.Model != "llama3.2:3b" {
		t.Fatalf("preset touched num_ctx (%d) or model (%q)", a.cfg.NumCtx, a.cfg.Model)
	}
}

func TestPresetSaveAndReapply(t *testing.T) {
	a := chatWithPrompts(t)
	a.cfg.Temperature, a.cfg.TopK = 0.42, 7
	typeInto(a, "/preset save mine")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	typeInto(a, "/preset precise")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if a.cfg.Temperature != 0 {
		t.Fatalf("temperature = %v, want the precise preset", a.cfg.Temperature)
	}

	typeInto(a, "/preset mine")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if a.cfg.Temperature != 0.42 || a.cfg.TopK != 7 {
		t.Fatalf("sampling = %+v, want the saved preset back", a.cfg.Preset())
	}
}

// A saved preset shadows a built-in of the same name.
func TestSavedPresetShadowsABuiltin(t *testing.T) {
	c := config.Default()
	c.Presets = map[string]config.Preset{"precise": {Temperature: 0.9}}
	p, ok := c.FindPreset("precise")
	if !ok || p.Temperature != 0.9 {
		t.Fatalf("found %+v, want the saved one to win", p)
	}
}

func TestUnknownPresetIsRefused(t *testing.T) {
	a := chatWithPrompts(t)
	was := a.cfg.Temperature
	typeInto(a, "/preset nope")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if a.cfg.Temperature != was || !a.toastErr {
		t.Fatalf("temperature = %v, toast = %q", a.cfg.Temperature, a.toast)
	}
}

// The settings tab names the preset in force rather than leaving four numbers to be recognised.
func TestSettingsNamesTheActivePreset(t *testing.T) {
	a := newTestApp(100, 32)
	a.tab = tabSettings
	a.cfg.Apply(config.Builtins["creative"])
	if got := ansi.Strip(render(a)); !strings.Contains(got, "preset creative") {
		t.Fatalf("settings does not name the preset:\n%s", got)
	}
	a.cfg.Temperature = 0.33
	if got := ansi.Strip(render(a)); strings.Contains(got, "preset creative") {
		t.Fatal("still claims a preset after the numbers diverged")
	}
}
