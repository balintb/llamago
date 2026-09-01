package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/balintb/llamago/internal/config"
)

// Prompt is a reusable prompt kept by name. Text may carry {{placeholders}}, which are filled in the composer before sending.
type Prompt struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

// placeholder matches {{name}}, the blanks a saved prompt leaves to fill.
var placeholder = regexp.MustCompile(`\{\{\s*([^}]+?)\s*\}\}`)

// Placeholders lists the blanks left in s, in the order they appear and without repeats, so a prompt that mentions {{topic}} twice reports it once.
func Placeholders(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range placeholder.FindAllStringSubmatch(s, -1) {
		name := m[1]
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// promptsPath is the single file the library lives in, beside the sessions.
func promptsPath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "prompts.json"), nil
}

// LoadPrompts reads the library, sorted by name. A missing file is an empty library rather than an error: nobody has saved one yet.
func LoadPrompts() ([]Prompt, error) {
	p, err := promptsPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Prompt
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SavePrompts writes the library atomically, the way sessions are written.
func SavePrompts(prompts []Prompt) error {
	p, err := promptsPath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(prompts, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// PutPrompt adds or replaces a prompt by name and returns the new library. Names are matched case-insensitively so "Review" cannot shadow "review".
func PutPrompt(prompts []Prompt, name, text string) []Prompt {
	for i, p := range prompts {
		if strings.EqualFold(p.Name, name) {
			prompts[i].Text = text
			return prompts
		}
	}
	out := append(prompts, Prompt{Name: name, Text: text})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// FindPrompt resolves a name to a saved prompt: an exact match first, then the only one starting with it, so a long name need not be typed in full.
func FindPrompt(prompts []Prompt, name string) (Prompt, bool) {
	for _, p := range prompts {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	var found Prompt
	var n int
	for _, p := range prompts {
		if strings.HasPrefix(strings.ToLower(p.Name), strings.ToLower(name)) {
			found, n = p, n+1
		}
	}
	return found, n == 1
}

// DropPrompt removes a prompt by name, reporting whether it was there.
func DropPrompt(prompts []Prompt, name string) ([]Prompt, bool) {
	for i, p := range prompts {
		if strings.EqualFold(p.Name, name) {
			return append(prompts[:i], prompts[i+1:]...), true
		}
	}
	return prompts, false
}
