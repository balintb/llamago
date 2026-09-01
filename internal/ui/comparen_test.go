package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/store"
)

func storeTurnWithTPS(tps float64) store.Turn {
	return store.Turn{Role: "assistant", Content: "answer", TokensPerSec: tps}
}

func racingApp(t *testing.T, width int) *App {
	t.Helper()
	a := newTestApp(width, 32)
	a.cfg.Model = "llama3.2:3b"
	a.comparePrompt = "hello"
	a.startCompare("huihui_ai/qwen3-abliterated:30b-a3b", "hello")
	return a
}

func TestRaceTakesMoreThanTwoModels(t *testing.T) {
	a := racingApp(t, 140)
	a.models = append(a.models, a.models[0], a.models[1])
	a.models[2].Name, a.models[3].Name = "phi4-mini", "gemma:2b"

	if got := a.addCompareSide("phi4-mini"); got == nil {
		t.Fatal("adding a third model returned nothing")
	}
	if len(a.compare) != 3 {
		t.Fatalf("%d columns, want 3", len(a.compare))
	}

	// The columns still tile the full width exactly, with no gap or overlap.
	total := 0
	for i := range a.compare {
		if x := a.compareColumnX(i); x != total {
			t.Fatalf("column %d starts at %d, want %d", i, x, total)
		}
		total += a.compareColumnWidth(i)
	}
	if total != a.compareWidth() {
		t.Fatalf("columns span %d, want the full %d", total, a.compareWidth())
	}

	if got := ansi.Strip(render(a)); !strings.Contains(got, "phi4-mini") {
		t.Error("the new column is not on screen")
	}
}

// A model cannot race itself, and a narrow window refuses another column rather than shrinking them all into uselessness.
func TestRaceRefusesDuplicatesAndOverflow(t *testing.T) {
	a := racingApp(t, 140)
	if a.addCompareSide("llama3.2:3b"); len(a.compare) != 2 {
		t.Fatal("a model already racing was added again")
	}

	narrow := racingApp(t, 70) // room for two columns, not three
	narrow.addCompareSide("phi4-mini")
	if len(narrow.compare) != 2 {
		t.Fatalf("%d columns in a 70-cell window, want 2", len(narrow.compare))
	}
	if !narrow.toastErr {
		t.Errorf("toast = %q, want it to explain the refusal", narrow.toast)
	}
}

// The verdict line names every racer rather than two of them.
func TestVerdictNamesEveryRacer(t *testing.T) {
	a := racingApp(t, 140)
	a.addCompareSide("phi4-mini")
	for i, r := range a.compare {
		r.streaming = false // the race has landed
		r.turns = append(r.turns, storeTurnWithTPS(float64(10*(i+1))))
	}
	got := ansi.Strip(a.viewCompareVerdict())
	if !strings.Contains(got, "★") {
		t.Fatalf("no winner marked:\n%s", got)
	}
	if strings.Count(got, "tok/s") != 3 {
		t.Fatalf("verdict covers %d racers, want 3:\n%s", strings.Count(got, "tok/s"), got)
	}
}
