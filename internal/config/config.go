// Package config loads and saves llamago's user settings.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/balintb/llamago/internal/ollama"
)

// Config is the persisted user settings, stored as JSON in the config dir.
type Config struct {
	Host          string  `json:"host"`
	Model         string  `json:"model"`
	System        string  `json:"system"`
	Temperature   float64 `json:"temperature"`
	TopP          float64 `json:"top_p"`
	TopK          int     `json:"top_k"`
	RepeatPenalty float64 `json:"repeat_penalty"`
	NumCtx        int     `json:"num_ctx"`
	NumPredict    int     `json:"num_predict"`
	KeepAlive     string  `json:"keep_alive"`
	Think         bool    `json:"think"`
	Markdown      bool    `json:"markdown"`
	Timestamps    bool    `json:"timestamps"`
	// Resume reopens the most recently used conversation on startup instead of beginning an empty one.
	Resume bool `json:"resume"`
	// AutoTitle asks the model to name a conversation after its first exchange.
	AutoTitle bool `json:"auto_title"`
	// Theme names the palette; an unknown one falls back to the default.
	Theme string `json:"theme,omitempty"`

	// Tools lets models call tools. Off until asked for: it runs programs.
	Tools bool `json:"tools"`
	// ToolSteps caps the rounds of calling in one turn, which is what stops a model that keeps calling the same tool from doing so forever.
	ToolSteps int `json:"tool_steps,omitempty"`
	// ToolOff names the tools switched off, so a tool is on unless it was deliberately turned off - including one installed after this was written.
	ToolOff map[string]bool `json:"tools_off,omitempty"`
	// Graphics overrides terminal image-protocol detection: auto, kitty, iterm2, sixel or none.
	Graphics string `json:"graphics"`
	// SaveDir is where "save image as" starts, and where it saves by default.
	SaveDir string   `json:"save_dir"`
	Stop    []string `json:"stop,omitempty"`

	// Seed makes generation reproducible. 0 is Ollama's own default and means "pick one per request", so it is left out of the request entirely.
	Seed int `json:"seed,omitempty"`

	// Presets are sampling parameters saved by name, shadowing a built-in of the same name.
	Presets map[string]Preset `json:"presets,omitempty"`

	// Hosts are Ollama servers saved by name; Host is whichever is in use.
	Hosts []Host `json:"hosts,omitempty"`
}

// Default returns the settings a first-run user gets.
func Default() Config {
	return Config{
		Temperature:   0.7,
		TopP:          0.9,
		TopK:          40,
		RepeatPenalty: 1.1,
		NumCtx:        4096,
		NumPredict:    -1,
		KeepAlive:     "5m",
		Think:         true,
		Markdown:      true,
		Graphics:      "auto",
		ToolSteps:     5,
		SaveDir:       defaultSaveDir(),
	}
}

// Options projects the config onto the subset Ollama accepts per request.
func (c Config) Options() *ollama.Options {
	o := &ollama.Options{
		Temperature:   new(c.Temperature),
		TopP:          new(c.TopP),
		TopK:          new(c.TopK),
		RepeatPenalty: new(c.RepeatPenalty),
		NumCtx:        new(c.NumCtx),
		Stop:          c.Stop,
	}
	// -1 means "until the model stops", which is the server default; sending it explicitly is harmless but noisy, so only set a real limit.
	if c.NumPredict > 0 {
		o.NumPredict = new(c.NumPredict)
	}
	// Seed 0 is the server's "random per request"; only a chosen seed is sent.
	if c.Seed != 0 {
		o.Seed = new(c.Seed)
	}
	return o
}

//go:fix inline
func ptr[T any](v T) *T { return new(v) }

// defaultSaveDir is where saved images land unless the user picks elsewhere.
func defaultSaveDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	if downloads := filepath.Join(home, "Downloads"); isDir(downloads) {
		return downloads
	}
	return home
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// Dir returns the directory llamago keeps its state in, creating it if needed. It honors XDG_CONFIG_HOME and falls back to ~/.config/llamago.
func Dir() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	dir := filepath.Join(base, "llamago")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// ToolsDir is where declared tool manifests live.
func ToolsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tools"), nil
}

func path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads the saved config, returning defaults when none exists yet. A corrupt or unreadable file is not fatal: the user still gets a working app.
func Load() Config {
	c := Default()
	p, err := path()
	if err != nil {
		return c
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return c
	}
	// Decode over the defaults so fields added in later versions keep sane values.
	if err := json.Unmarshal(b, &c); err != nil {
		return Default()
	}
	return c
}

// Save writes the config atomically so a crash mid-write can't truncate it.
func (c Config) Save() error {
	p, err := path()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Preset is a named set of sampling parameters. Only the four that change how text comes out are covered: context size and keep-alive are about the machine rather than the writing.
type Preset struct {
	Temperature   float64 `json:"temperature"`
	TopP          float64 `json:"top_p"`
	TopK          int     `json:"top_k"`
	RepeatPenalty float64 `json:"repeat_penalty"`
}

// Builtins are the presets that always exist, from most predictable to least.
var Builtins = map[string]Preset{
	"precise":  {Temperature: 0, TopP: 0.5, TopK: 10, RepeatPenalty: 1.05},
	"balanced": {Temperature: 0.7, TopP: 0.9, TopK: 40, RepeatPenalty: 1.1},
	"creative": {Temperature: 1.2, TopP: 0.95, TopK: 100, RepeatPenalty: 1.15},
}

// BuiltinOrder lists the built-ins in the order they are offered, which is by how much they let the model wander rather than alphabetical.
var BuiltinOrder = []string{"precise", "balanced", "creative"}

// Preset reads the current sampling parameters as one.
func (c Config) Preset() Preset {
	return Preset{
		Temperature:   c.Temperature,
		TopP:          c.TopP,
		TopK:          c.TopK,
		RepeatPenalty: c.RepeatPenalty,
	}
}

// Apply overwrites the sampling parameters from a preset, leaving everything else - model, context size, system prompt - alone.
func (c *Config) Apply(p Preset) {
	c.Temperature, c.TopP, c.TopK, c.RepeatPenalty = p.Temperature, p.TopP, p.TopK, p.RepeatPenalty
}

// FindPreset resolves a name against the saved presets first, so a preset saved as "precise" is the one that answers to it.
func (c Config) FindPreset(name string) (Preset, bool) {
	if p, ok := c.Presets[name]; ok {
		return p, true
	}
	p, ok := Builtins[name]
	return p, ok
}

// PresetNames lists every preset, built-ins first and saved ones after.
func (c Config) PresetNames() []string {
	out := append([]string{}, BuiltinOrder...)
	for name := range c.Presets {
		if _, builtin := Builtins[name]; !builtin {
			out = append(out, name)
		}
	}
	sort.Strings(out[len(BuiltinOrder):])
	return out
}

// MatchPreset names the preset the current settings equal, if any, so the UI can say "creative" instead of listing four numbers.
func (c Config) MatchPreset() string {
	cur := c.Preset()
	for name, p := range c.Presets {
		if p == cur {
			return name
		}
	}
	for _, name := range BuiltinOrder {
		if Builtins[name] == cur {
			return name
		}
	}
	return ""
}

// Host is a named Ollama server. The laptop's own and the workstation's generally differ in what they can hold in memory, so switching is worth one command rather than a restart with a flag.
type Host struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// FindHost resolves a name to a saved host, case-insensitively.
func (c Config) FindHost(name string) (Host, bool) {
	for _, h := range c.Hosts {
		if strings.EqualFold(h.Name, name) {
			return h, true
		}
	}
	return Host{}, false
}

// PutHost adds or updates a saved host by name.
func (c *Config) PutHost(name, url string) {
	for i, h := range c.Hosts {
		if strings.EqualFold(h.Name, name) {
			c.Hosts[i].URL = url
			return
		}
	}
	c.Hosts = append(c.Hosts, Host{Name: name, URL: url})
}

// DropHost removes a saved host, reporting whether it was there.
func (c *Config) DropHost(name string) bool {
	for i, h := range c.Hosts {
		if strings.EqualFold(h.Name, name) {
			c.Hosts = append(c.Hosts[:i], c.Hosts[i+1:]...)
			return true
		}
	}
	return false
}

// HostName is the name the active host is saved under, or empty when it is not one of them.
func (c Config) HostName(active string) string {
	for _, h := range c.Hosts {
		if h.URL == active {
			return h.Name
		}
	}
	return ""
}
