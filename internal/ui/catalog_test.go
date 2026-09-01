package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/config"
)

func openPull(t *testing.T, a *App) {
	t.Helper()
	a.tab = tabModels
	a.onKey(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if a.overlay != overlayPull {
		t.Fatalf("overlay = %v, want the pull picker", a.overlay)
	}
}

// Ollama publishes nothing to browse, so the list is ours - and it has to say what each model is for, or the names alone are no help to anyone choosing.
func TestPullPickerListsTheCatalogue(t *testing.T) {
	a := newTestApp(100, 32)
	openPull(t, a)

	got := ansi.Strip(render(a))
	for _, want := range []string{"llama3.2:3b", "~2.0 GB", "small and quick", "gemma3:4b"} {
		if !strings.Contains(got, want) {
			t.Errorf("the picker does not show %q:\n%s", want, got)
		}
	}
	// A model already on the server should say so rather than offering a download that would do nothing.
	if !strings.Contains(got, "installed") {
		t.Errorf("nothing marks the installed model:\n%s", got)
	}
}

// Typing filters by what a model is for, not just by its name: someone wanting to describe a photo does not know which model is called what.
func TestPullFilterMatchesPurposeAndTags(t *testing.T) {
	a := newTestApp(100, 32)
	openPull(t, a)

	a.pullInput.SetValue("vision")
	names := choiceNames(a)
	if len(names) == 0 {
		t.Fatal("filtering by a capability found nothing")
	}
	for _, n := range names {
		if n != "gemma3:4b" && n != "llava:7b" {
			t.Errorf("%q matched a search for vision", n)
		}
	}

	a.pullInput.SetValue("code")
	if got := choiceNames(a); len(got) == 0 || !contains(got, "qwen2.5-coder:7b") {
		t.Errorf("searching for code found %v", got)
	}
}

// Enter pulls what is highlighted.
func TestEnterPullsTheHighlightedEntry(t *testing.T) {
	a := newTestApp(100, 32)
	openPull(t, a)

	a.onKey(tea.KeyPressMsg{Code: tea.KeyDown})
	a.onKey(tea.KeyPressMsg{Code: tea.KeyDown})
	want := a.pullChoices()[2].Name
	if got := a.pullTarget(); got != want {
		t.Fatalf("target = %q, want the highlighted %q", got, want)
	}
}

// A name published after this list was written still has to be pullable, so text matching nothing is taken literally rather than refused.
func TestTypingAnUnlistedNameStillPulls(t *testing.T) {
	a := newTestApp(100, 32)
	openPull(t, a)

	a.pullInput.SetValue("some-new-model:70b")
	if len(a.pullChoices()) != 0 {
		t.Fatal("setup: expected the filter to match nothing")
	}
	if got := a.pullTarget(); got != "some-new-model:70b" {
		t.Fatalf("target = %q, want what was typed", got)
	}
	if got := ansi.Strip(render(a)); !strings.Contains(got, "pulls what you typed") {
		t.Errorf("the picker does not say what enter will do:\n%s", got)
	}
}

// The list would go stale with the release it shipped in, so it can be extended and corrected without one.
func TestModelsJSONExtendsAndOverrides(t *testing.T) {
	dir, err := config.Dir()
	if err != nil {
		t.Skip(err)
	}
	path := filepath.Join(dir, "models.json")
	t.Cleanup(func() { os.Remove(path) })

	body := `[
	  {"name":"brand-new:12b","size":"~8 GB","purpose":"published last week","tags":["tools"]},
	  {"name":"llama3.2:3b","size":"~2.0 GB","purpose":"my own note"}
	]`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	var found, overridden bool
	for _, e := range loadCatalog() {
		if e.Name == "brand-new:12b" {
			found = true
		}
		if e.Name == "llama3.2:3b" {
			if e.Purpose != "my own note" {
				t.Errorf("built-in entry kept %q, want the override", e.Purpose)
			}
			if overridden {
				t.Error("the overridden entry appears twice")
			}
			overridden = true
		}
	}
	if !found {
		t.Error("an added model is missing from the list")
	}
	if !overridden {
		t.Error("the built-in entry disappeared instead of being replaced")
	}
}

func choiceNames(a *App) []string {
	out := make([]string, 0, len(a.pullChoices()))
	for _, e := range a.pullChoices() {
		out = append(out, e.Name)
	}
	return out
}

func contains(list []string, want string) bool {
	return slices.Contains(list, want)
}
