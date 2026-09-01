package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/ollama"
)

// down presses the down arrow n times.
func downN(a *App, n int) {
	for range n {
		a.onKey(tea.KeyPressMsg{Code: tea.KeyDown})
	}
}

// The arrows are the only navigation anyone tries first, so they have to reach everything - including the tail, which holds no selectable field.
func TestArrowsReachTheSettingsTail(t *testing.T) {
	for _, h := range []int{18, 24, 32} {
		a := newTestApp(100, h)
		a.tab = tabSettings
		downN(a, 60)

		got := ansi.Strip(render(a))
		for _, want := range []string{"CONNECTION", "config"} {
			if !strings.Contains(got, want) {
				t.Errorf("at height %d, %q is unreachable with the arrows:\n%s", h, want, got)
			}
		}
	}
}

// And back up again, without needing a different key.
func TestArrowsComeBackFromTheSettingsTail(t *testing.T) {
	a := newTestApp(100, 24)
	a.tab = tabSettings
	downN(a, 60)
	for range 60 {
		a.onKey(tea.KeyPressMsg{Code: tea.KeyUp})
	}
	if a.setIdx != 0 || a.setScroll != 0 {
		t.Fatalf("idx = %d, scroll = %d, want both back at the top", a.setIdx, a.setScroll)
	}
	if got := ansi.Strip(render(a)); !strings.Contains(got, "INFERENCE") {
		t.Error("the top of the pane is not back in view")
	}
}

// The system prompt and stop sequences are fields now, so they are selected with the arrows and opened with enter rather than needing their own keys.
func TestPromptAndStopAreSelectableFields(t *testing.T) {
	a := newTestApp(100, 32)
	a.tab = tabSettings

	idx := map[string]int{}
	for i, s := range settings {
		idx[s.name] = i
	}
	for _, name := range []string{"system prompt", "stop sequences"} {
		i, ok := idx[name]
		if !ok {
			t.Fatalf("%q is not a field", name)
		}
		a.setIdx = i
		a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		if a.overlay != overlaySystem {
			t.Fatalf("enter on %q opened %v, want the editor", name, a.overlay)
		}
		a.overlay = overlayNone
	}
	// The shortcut keys still work as accelerators.
	a.onKey(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if a.editTarget != editSystem {
		t.Error("e no longer opens the system prompt")
	}
}

// Several resident models produce more cards than fit.
func TestRunningPaneScrollsToTheLastCard(t *testing.T) {
	a := newTestApp(100, 24)
	a.tab = tabRunning
	a.running = nil
	for i := range 6 {
		a.running = append(a.running, ollama.RunningModel{
			Name:      string(rune('a'+i)) + "-model:7b",
			Size:      3_000_000_000,
			SizeVRAM:  2_000_000_000,
			ExpiresAt: time.Now().Add(5 * time.Minute),
		})
	}

	if got := ansi.Strip(render(a)); strings.Contains(got, "f-model") {
		t.Fatal("setup: the last card is expected to start off screen")
	}
	a.runIdx = len(a.running) - 1
	if got := ansi.Strip(render(a)); !strings.Contains(got, "f-model") {
		t.Fatalf("selecting the last model does not bring it into view:\n%s", got)
	}
}

// A chatty model card outgrows its column and used to lose its tail.
func TestModelDetailScrolls(t *testing.T) {
	a := newTestApp(100, 20)
	a.tab = tabModels
	a.modelIdx = 0
	// The list is sorted, so seed the card that is actually on screen.
	sel := a.selectedModel()
	if sel == nil {
		t.Fatal("setup: no model selected")
	}
	a.details[sel.Name] = &ollama.ShowResponse{
		Details:      sel.Details,
		Capabilities: []string{"completion", "tools", "thinking"},
		System:       strings.Repeat("a long system prompt. ", 12),
		Parameters:   "top_k 20\ntop_p 0.95\nrepeat_penalty 1\nstop \"<|im_start|>\"\nstop \"<|im_end|>\"\ntemperature 0.6",
	}

	before := ansi.Strip(render(a))
	if strings.Contains(before, "PARAMETERS") {
		t.Fatal("setup: the card is expected to be taller than its pane")
	}

	a.onKey(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if ansi.Strip(render(a)) == before {
		t.Fatal("the model card did not scroll")
	}
	// Keep going to the end: the tail has to be reachable, not merely nearer.
	for range 10 {
		a.onKey(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	}
	if got := ansi.Strip(render(a)); !strings.Contains(got, "PARAMETERS") {
		t.Fatalf("the tail of the card is unreachable:\n%s", got)
	}

	// And back up to the top.
	for range 20 {
		a.onKey(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	}
	if got := ansi.Strip(render(a)); got != before {
		t.Error("scrolling back up did not restore the top of the card")
	}
	// Moving to another model starts its card at the top.
	a.onKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if a.detailScroll != 0 {
		t.Fatalf("detail scroll = %d, want it reset for the new card", a.detailScroll)
	}
}

// Up moves the selection rather than unwinding the scroll: from anywhere, pressing it repeatedly walks back to the top with no press doing nothing.
func TestUpAlwaysMovesAndReachesTheTop(t *testing.T) {
	for _, h := range []int{18, 22, 26} {
		a := newTestApp(100, h)
		a.tab = tabSettings

		// Down to the very bottom, tail included.
		for range 80 {
			before := ansi.Strip(render(a))
			a.onKey(tea.KeyPressMsg{Code: tea.KeyDown})
			if ansi.Strip(render(a)) == before {
				break
			}
		}

		// Now up until nothing moves. Breaking early on a wasted press is what makes the check below meaningful.
		ups := 0
		for range 80 {
			before := ansi.Strip(render(a))
			a.onKey(tea.KeyPressMsg{Code: tea.KeyUp})
			if ansi.Strip(render(a)) == before {
				break
			}
			ups++
		}
		if ups == 0 {
			t.Fatalf("height %d: up never moved anything", h)
		}
		if a.setIdx != 0 || a.setScroll != 0 {
			t.Errorf("height %d: idx = %d, scroll = %d after %d ups, want the top",
				h, a.setIdx, a.setScroll, ups)
		}
	}
}

// From the last field, up selects the one above it straight away, rather than spending presses scrolling the tail back out of view first.
func TestUpFromTheLastFieldMovesTheSelection(t *testing.T) {
	a := newTestApp(100, 20)
	a.tab = tabSettings

	fields := a.settingsFields()
	last := len(fields) - 1
	a.setIdx = last
	// Scroll the tail into view, the way pressing down at the end does.
	a.onKey(tea.KeyPressMsg{Code: tea.KeyDown})
	a.onKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if a.setScroll == 0 {
		t.Fatal("setup: expected the tail to be scrolled into view")
	}

	a.onKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if a.setIdx != last-1 {
		t.Fatalf("selection = %q, want %q: up should move, not unwind",
			fields[a.setIdx].name, fields[last-1].name)
	}
}

// Paging up from the end moves the selection back by a page, and shows it.
func TestPageUpMovesOutOfTheTail(t *testing.T) {
	a := newTestApp(100, 20)
	a.tab = tabSettings
	a.onKey(tea.KeyPressMsg{Code: 'G', Text: "G", Mod: tea.ModShift})
	was := a.setIdx

	before := ansi.Strip(render(a))
	a.onKey(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if ansi.Strip(render(a)) == before {
		t.Fatal("page up from the tail changed nothing")
	}
	if a.setIdx >= was {
		t.Fatalf("selection = %d, want it moved back from %d", a.setIdx, was)
	}
}
