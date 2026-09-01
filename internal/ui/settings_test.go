package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/config"
)

// Stop sequences are a list, so they get the multi-line editor rather than a row that ←/→ could adjust.
func TestStopSequencesAreEditableFromSettings(t *testing.T) {
	a := newTestApp(100, 32)
	a.tab = tabSettings
	if got := ansi.Strip(render(a)); !strings.Contains(got, "stop sequences") {
		t.Fatalf("settings does not list the stop sequences field:\n%s", got)
	}
	a.onKey(tea.KeyPressMsg{Code: 's', Text: "s"})
	if a.overlay != overlaySystem || a.editTarget != editStop {
		t.Fatalf("overlay=%v target=%v, want the stop editor", a.overlay, a.editTarget)
	}
	if got := ansi.Strip(render(a)); !strings.Contains(got, "Stop sequences") {
		t.Fatal("editor is not titled for stop sequences")
	}
	a.sysInput.SetValue("<|im_end|>\n\n  END  \n")
	a.onKey(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if got := a.cfg.Stop; len(got) != 2 || got[0] != "<|im_end|>" || got[1] != "  END  " {
		t.Fatalf("stop = %q, want blank lines dropped and inner spaces kept", got)
	}
	if got := ansi.Strip(render(a)); !strings.Contains(got, `"<|im_end|>"`) {
		t.Fatal("saved sequences are not shown in settings")
	}
	// The system prompt editor must still work.
	a.onKey(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if a.editTarget != editSystem {
		t.Fatal("e no longer opens the system prompt")
	}
}

// Seed 0 is Ollama's own "random per request", so it must stay out of the request rather than being sent as a literal zero, which would pin every answer to the same seed.
func TestSeedOnlyTravelsWhenChosen(t *testing.T) {
	c := config.Default()
	if o := c.Options(); o.Seed != nil {
		t.Fatalf("seed = %v on a default config, want it omitted", *o.Seed)
	}
	c.Seed = 42
	o := c.Options()
	if o.Seed == nil || *o.Seed != 42 {
		t.Fatalf("seed = %v, want 42", o.Seed)
	}
}

func TestSeedRowAdjusts(t *testing.T) {
	a := newTestApp(100, 32)
	a.tab = tabSettings
	for i, s := range settings {
		if s.name == "seed" {
			a.setIdx = i
		}
	}
	if got := ansi.Strip(render(a)); !strings.Contains(got, "random") {
		t.Fatal("an unset seed should read as random")
	}
	a.onKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if a.cfg.Seed != 1 {
		t.Fatalf("seed = %d after one step, want 1", a.cfg.Seed)
	}
	a.onKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	if a.cfg.Seed != 0 {
		t.Fatalf("seed = %d, want it back to random", a.cfg.Seed)
	}
	// It must not go negative: -1 is not "random" to Ollama.
	a.onKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	if a.cfg.Seed != 0 {
		t.Fatalf("seed = %d, want it to hold at 0", a.cfg.Seed)
	}
}

// The pane outgrew the window, and clipping dropped the tail in silence.
func TestSettingsTailIsReachable(t *testing.T) {
	a := newTestApp(100, 22) // short enough that the tail starts off screen
	a.tab = tabSettings

	if got := ansi.Strip(render(a)); strings.Contains(got, "config") {
		t.Fatal("setup: the pane is expected to be taller than this window")
	}

	a.onKey(tea.KeyPressMsg{Code: 'G', Text: "G", Mod: tea.ModShift})
	got := ansi.Strip(render(a))
	for _, want := range []string{"CONNECTION", "host", "status", "config"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is unreachable after scrolling to the end:\n%s", want, got)
		}
	}
}

// Every field has to be reachable by moving through them, without needing to know the pane scrolls at all.
func TestMovingThroughFieldsPullsThemIntoView(t *testing.T) {
	a := newTestApp(100, 32)
	a.tab = tabSettings

	for i := range settings {
		a.setIdx = i
		rows, selLine := a.settingsLines()
		inner := a.contentHeight() - 2
		off := scrollOffset(len(rows), inner, a.setScroll, selLine)
		if selLine < off || selLine >= off+inner {
			t.Fatalf("field %q sits on line %d, outside the window [%d,%d)",
				settings[i].name, selLine, off, off+inner)
		}
	}
}

// A window tall enough for everything scrolls not at all.
func TestSettingsDoesNotScrollWhenItFits(t *testing.T) {
	a := newTestApp(100, 60)
	a.tab = tabSettings
	rows, selLine := a.settingsLines()
	if off := scrollOffset(len(rows), a.contentHeight()-2, a.setScroll, selLine); off != 0 {
		t.Fatalf("offset = %d, want the pane still when it all fits", off)
	}
	if got := ansi.Strip(render(a)); !strings.Contains(got, "config") {
		t.Error("the tail is missing from a window with room for it")
	}
}
