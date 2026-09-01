package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/balintb/llamago/internal/config"
)

// catalogEntry is one model worth suggesting: what it is called, roughly how much disk it wants, and what it is for.
//
// Ollama publishes no API for browsing its library - the one endpoint that answers returns a handful of frontier models measured in hundreds of gigabytes - so this list is ours. Sizes are approximate and marked as such; the pull reports the real figure as it downloads.
type catalogEntry struct {
	Name    string `json:"name"`
	Size    string `json:"size"`
	Purpose string `json:"purpose"`
	// Tags name what the model can do, in the same words the Models tab uses for a model already installed.
	Tags []string `json:"tags,omitempty"`
}

// catalog is the built-in list: small enough to run on a laptop, current enough to be worth suggesting, and ordered from the most generally useful down.
var catalog = []catalogEntry{
	{"llama3.2:3b", "~2.0 GB", "small and quick, a good default", []string{"tools"}},
	{"qwen3:4b", "~2.6 GB", "reasons and calls tools at a small size", []string{"tools", "thinking"}},
	{"gemma3:4b", "~3.3 GB", "reads images as well as text", []string{"vision"}},
	{"phi4-mini", "~2.5 GB", "small, strong at instructions", []string{"tools"}},
	{"qwen2.5-coder:7b", "~4.7 GB", "written for code", []string{"tools"}},
	{"llama3.1:8b", "~4.7 GB", "general purpose, a step up in quality", []string{"tools"}},
	{"qwen3:8b", "~5.2 GB", "the 4b with more room to think", []string{"tools", "thinking"}},
	{"deepseek-r1:8b", "~5.2 GB", "shows its reasoning at length", []string{"thinking"}},
	{"mistral:7b", "~4.1 GB", "fast and even-tempered", []string{"tools"}},
	{"llava:7b", "~4.7 GB", "describes images in detail", []string{"vision"}},
	{"gpt-oss:20b", "~14 GB", "large, for a machine with memory to spare", []string{"tools", "thinking"}},
	{"nomic-embed-text", "~274 MB", "embeddings rather than chat", []string{"embedding"}},
}

// loadCatalog returns the built-in list plus anything in models.json, which is how the list stays current without a new release: a name published after this was written can be added by hand.
func loadCatalog() []catalogEntry {
	out := append([]catalogEntry{}, catalog...)

	dir, err := config.Dir()
	if err != nil {
		return out
	}
	b, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil {
		return out
	}
	var extra []catalogEntry
	if json.Unmarshal(b, &extra) != nil {
		return out
	}
	// A hand-written entry replaces a built-in of the same name rather than appearing twice.
	for _, e := range extra {
		if strings.TrimSpace(e.Name) == "" {
			continue
		}
		replaced := false
		for i := range out {
			if out[i].Name == e.Name {
				out[i], replaced = e, true
				break
			}
		}
		if !replaced {
			out = append(out, e)
		}
	}
	return out
}

// matchesFilter reports whether an entry answers what has been typed, matching the purpose and tags as well as the name: "vision" should find the models that read images, not just one called vision.
func (e catalogEntry) matchesFilter(q string) bool {
	if q == "" {
		return true
	}
	q = strings.ToLower(q)
	if strings.Contains(strings.ToLower(e.Name), q) ||
		strings.Contains(strings.ToLower(e.Purpose), q) {
		return true
	}
	for _, t := range e.Tags {
		if strings.Contains(strings.ToLower(t), q) {
			return true
		}
	}
	return false
}

// pullChoices is the catalogue filtered by what has been typed.
func (a *App) pullChoices() []catalogEntry {
	q := strings.TrimSpace(a.pullInput.Value())
	var out []catalogEntry
	for _, e := range loadCatalog() {
		if e.matchesFilter(q) {
			out = append(out, e)
		}
	}
	return out
}

// installed reports whether a catalogue name is already on the server, so the list can say so rather than offering a download that would be a no-op.
func (a *App) installed(name string) bool {
	for _, m := range a.models {
		if m.Name == name || strings.TrimPrefix(m.Name, "library/") == name ||
			strings.TrimSuffix(m.Name, ":latest") == name {
			return true
		}
	}
	return false
}

// pullTarget is what enter downloads: the highlighted entry, or the text typed when it matches nothing in the list. A name published after this list was written still has to be pullable.
func (a *App) pullTarget() string {
	typed := strings.TrimSpace(a.pullInput.Value())
	choices := a.pullChoices()
	if a.pullIdx >= 0 && a.pullIdx < len(choices) {
		return choices[a.pullIdx].Name
	}
	return typed
}
