package ui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/balintb/llamago/internal/config"
	"github.com/balintb/llamago/internal/store"
	"github.com/balintb/llamago/internal/theme"
)

// slashCommand is one composer command. Everything here is reachable from the palette too; these exist because typing "/temp 0.2" beats opening a menu once you know what you want.
type slashCommand struct {
	name string
	arg  string // shown after the name when it takes one
	help string
	run  func(a *App, arg string) tea.Cmd
}

var slashCommands = []slashCommand{
	{"model", "<name>", "switch model", func(a *App, arg string) tea.Cmd {
		if arg == "" {
			return a.showToast("which model? try /model "+shortModel(a.cfg.Model), true)
		}
		name, ok := a.matchModel(arg)
		if !ok {
			return a.showToast("no installed model matches "+arg, true)
		}
		return tea.Batch(a.setModel(name), a.okToast("model is now "+shortModel(name)))
	}},
	{"system", "<text>", "set the system prompt", func(a *App, arg string) tea.Cmd {
		a.cfg.System = arg
		_ = a.cfg.Save()
		if arg == "" {
			return a.okToast("system prompt cleared")
		}
		return a.okToast("system prompt set")
	}},
	{"temp", "<0-2>", "set temperature", func(a *App, arg string) tea.Cmd {
		v, err := strconv.ParseFloat(arg, 64)
		if err != nil || v < 0 || v > 2 {
			return a.showToast("temperature is a number from 0 to 2", true)
		}
		a.cfg.Temperature = v
		_ = a.cfg.Save()
		return a.okToast(fmt.Sprintf("temperature %.2f", v))
	}},
	{"seed", "<n>", "fix the seed, 0 for random", func(a *App, arg string) tea.Cmd {
		v, err := strconv.Atoi(arg)
		if err != nil || v < 0 {
			return a.showToast("seed is a whole number, 0 for random", true)
		}
		a.cfg.Seed = v
		_ = a.cfg.Save()
		if v == 0 {
			return a.okToast("seed is random again")
		}
		return a.okToast(fmt.Sprintf("seed %d", v))
	}},
	{"think", "on|off", "toggle reasoning", func(a *App, arg string) tea.Cmd {
		switch strings.ToLower(arg) {
		case "on", "":
			a.cfg.Think = true
		case "off":
			a.cfg.Think = false
		default:
			return a.showToast("/think takes on or off", true)
		}
		_ = a.cfg.Save()
		return a.okToast("thinking " + onOff(a.cfg.Think))
	}},
	{"prompt", "<name>", "load a saved prompt", func(a *App, arg string) tea.Cmd {
		if arg == "" {
			return a.showToast(a.promptNames(), len(a.library) == 0)
		}
		p, ok := store.FindPrompt(a.library, arg)
		if !ok {
			return a.showToast("no saved prompt matches "+arg, true)
		}
		a.setComposer(p.Text)
		if blanks := store.Placeholders(p.Text); len(blanks) > 0 {
			return a.okToast(fmt.Sprintf("%s - fill %s", p.Name, strings.Join(blanks, ", ")))
		}
		return a.okToast("loaded " + p.Name)
	}},
	{"save", "<name>", "save the last prompt", func(a *App, arg string) tea.Cmd {
		if arg == "" {
			return a.showToast("what should it be called? /save <name>", true)
		}
		text := a.promptToSave()
		if text == "" {
			return a.showToast("no prompt to save yet - send one first", true)
		}
		a.library = store.PutPrompt(a.library, arg, text)
		if err := store.SavePrompts(a.library); err != nil {
			return a.errToast(err)
		}
		return a.okToast("saved as " + arg)
	}},
	{"forget", "<name>", "delete a saved prompt", func(a *App, arg string) tea.Cmd {
		next, ok := store.DropPrompt(a.library, arg)
		if !ok {
			return a.showToast("no saved prompt called "+arg, true)
		}
		a.library = next
		if err := store.SavePrompts(a.library); err != nil {
			return a.errToast(err)
		}
		return a.okToast("forgot " + arg)
	}},
	{"preset", "<name>", "apply sampling settings", func(a *App, arg string) tea.Cmd {
		name, saveAs, _ := strings.Cut(arg, " ")
		switch {
		case name == "":
			return a.okToast("presets: " + strings.Join(a.cfg.PresetNames(), ", "))
		case name == "save":
			if saveAs = strings.TrimSpace(saveAs); saveAs == "" {
				return a.showToast("what should it be called? /preset save <name>", true)
			}
			if a.cfg.Presets == nil {
				a.cfg.Presets = map[string]config.Preset{}
			}
			a.cfg.Presets[saveAs] = a.cfg.Preset()
			_ = a.cfg.Save()
			return a.okToast("saved preset " + saveAs)
		}
		p, ok := a.cfg.FindPreset(name)
		if !ok {
			return a.showToast("no preset called "+name, true)
		}
		a.cfg.Apply(p)
		_ = a.cfg.Save()
		return a.okToast("preset " + name)
	}},
	{"host", "<name|url>", "switch Ollama server", func(a *App, arg string) tea.Cmd {
		verb, rest, _ := strings.Cut(arg, " ")
		rest = strings.TrimSpace(rest)
		switch verb {
		case "":
			return a.okToast(a.hostSummary())
		case "add":
			name, url, ok := strings.Cut(rest, " ")
			if !ok || name == "" || url == "" {
				return a.showToast("/host add <name> <url>", true)
			}
			a.cfg.PutHost(name, url)
			_ = a.cfg.Save()
			return a.okToast("saved host " + name)
		case "forget":
			if !a.cfg.DropHost(rest) {
				return a.showToast("no host called "+rest, true)
			}
			_ = a.cfg.Save()
			return a.okToast("forgot " + rest)
		}
		if h, ok := a.cfg.FindHost(verb); ok {
			return a.switchHost(h.URL)
		}
		if strings.Contains(verb, "://") {
			return a.switchHost(verb)
		}
		return a.showToast("no host called "+verb+" - /host add <name> <url> first", true)
	}},
	{"find", "<text>", "search every conversation", func(a *App, arg string) tea.Cmd {
		return a.searchAll(arg)
	}},
	{"theme", "<name>", "switch the palette", func(a *App, arg string) tea.Cmd {
		if arg == "" {
			return a.applyTheme(theme.Next())
		}
		if !theme.Use(arg) {
			return a.showToast("themes are "+strings.Join(theme.Names(), ", "), true)
		}
		return a.applyTheme(arg)
	}},
	{"tools", "on|off", "let models call tools", func(a *App, arg string) tea.Cmd {
		switch strings.ToLower(arg) {
		case "on":
			a.cfg.Tools = true
		case "off":
			a.cfg.Tools = false
		case "":
			return a.okToast(a.toolListing())
		default:
			return a.showToast("/tools takes on or off", true)
		}
		_ = a.cfg.Save()
		if note := a.toolsUnusableNote(); note != "" {
			return a.showToast("tools on, but "+note, true)
		}
		return a.okToast("tools " + onOff(a.cfg.Tools))
	}},
	{"clear", "", "start a new chat", func(a *App, arg string) tea.Cmd {
		return a.newSession()
	}},
	{"export", "md|json|html", "write the chat to a file", func(a *App, arg string) tea.Cmd {
		if arg == "" {
			return a.exportCmd()
		}
		for _, f := range store.Formats {
			if string(f) == strings.ToLower(arg) {
				return a.exportAs(f)
			}
		}
		return a.showToast("formats are md, json and html", true)
	}},
	{"help", "", "show the keymap", func(a *App, arg string) tea.Cmd {
		a.overlay, a.helpScroll = overlayHelp, 0
		return nil
	}},
}

// isSlash reports whether the composer holds a command rather than a prompt. A lone "/" counts, so the completions appear as soon as one is typed.
func isSlash(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), "/")
}

// slashRecognized reports whether the composer holds a command that exists, as opposed to one still being typed or one that was mistyped. It is what the composer's colour tracks: the colour appearing is the confirmation that the command is real, which a half-typed prefix has not earned.
func slashRecognized(text string) bool {
	if !isSlash(text) {
		return false
	}
	name, _, _ := strings.Cut(strings.TrimSpace(text)[1:], " ")
	name = strings.ToLower(strings.TrimSpace(name))
	for _, c := range slashCommands {
		if c.name == name {
			return true
		}
	}
	return false
}

// runSlash executes what the composer holds. The second return is false when the text is not a command at all, which sends it as an ordinary prompt.
func (a *App) runSlash(text string) (tea.Cmd, bool) {
	if !isSlash(text) {
		return nil, false
	}
	name, arg, _ := strings.Cut(strings.TrimSpace(text)[1:], " ")
	name = strings.ToLower(strings.TrimSpace(name))
	arg = strings.TrimSpace(arg)

	for _, c := range slashCommands {
		if c.name == name {
			a.input.Reset()
			a.histIdx, a.histDraft = -1, ""
			a.layout()
			a.refreshTranscript()
			return c.run(a, arg), true
		}
	}
	// An unknown command is not sent to the model: "/mdoel llama3" reaching a model as a prompt is a confusing way to learn about a typo.
	return a.showToast("no such command: /"+name, true), true
}

// slashMatches lists the commands the composer's text could still become.
func slashMatches(text string) []slashCommand {
	if !isSlash(text) {
		return nil
	}
	name, _, hasArg := strings.Cut(strings.TrimSpace(text)[1:], " ")
	name = strings.ToLower(name)

	var out []slashCommand
	for _, c := range slashCommands {
		// Once an argument is being typed, only the exact command still applies.
		if hasArg {
			if c.name == name {
				return []slashCommand{c}
			}
			continue
		}
		if strings.HasPrefix(c.name, name) {
			out = append(out, c)
		}
	}
	return out
}

// completeSlash fills in the longest unambiguous command name, the way a shell would. It returns false when there is nothing to add.
func (a *App) completeSlash() bool {
	matches := slashMatches(a.input.Value())
	if len(matches) != 1 {
		return false
	}
	c := matches[0]
	value := "/" + c.name
	if c.arg != "" {
		value += " "
	}
	if value == a.input.Value() {
		return false
	}
	a.setComposer(value)
	return true
}

// viewSlashHints lists the matching commands under the composer, in place of the usual send hint. width is the room the line has, so a long library of commands ends in a count rather than being cut off mid-name.
func (a *App) viewSlashHints(width int) string {
	matches := slashMatches(a.input.Value())
	if len(matches) == 0 {
		return theme.Err.Render("unknown command")
	}
	// One match gets its argument and description; a list of them all only fits as bare names, and a truncated list is worse than a terse one.
	if len(matches) == 1 {
		c := matches[0]
		out := theme.Key.Render("/" + c.name)
		if c.arg != "" {
			out += theme.Dim.Render(" " + c.arg)
		}
		return out + theme.Dim.Render("  "+c.help)
	}
	// The context meter holds the right end of this line; leave it room.
	budget := max(16, width-24)
	parts := make([]string, 0, len(matches))
	used := 0
	for i, c := range matches {
		name := "/" + c.name
		if i > 0 && used+len(name)+3 > budget {
			parts = append(parts, theme.Dim.Render(fmt.Sprintf("+%d more", len(matches)-i)))
			break
		}
		used += len(name) + 3
		parts = append(parts, theme.Key.Render(name))
	}
	return strings.Join(parts, theme.Dim.Render(" · "))
}

// promptToSave is what /save stores: the most recent prompt.
//
// Neither the composer nor the selection can be the source. The composer holds the /save command itself at that moment, and a selection is dropped as soon as the keyboard returns to the composer, so one can never be live while the command is being typed.
func (a *App) promptToSave() string {
	if a.cur == nil {
		return ""
	}
	for i := len(a.cur.Turns) - 1; i >= 0; i-- {
		if t := a.cur.Turns[i]; t.Role == "user" && strings.TrimSpace(t.Content) != "" {
			return strings.TrimSpace(t.Content)
		}
	}
	return ""
}

// hostSummary lists the saved servers and marks the one in use.
func (a *App) hostSummary() string {
	if len(a.cfg.Hosts) == 0 {
		return "one host: " + a.client.Host() + " - /host add <name> <url> to save more"
	}
	parts := make([]string, 0, len(a.cfg.Hosts))
	for _, h := range a.cfg.Hosts {
		if h.URL == a.client.Host() {
			parts = append(parts, h.Name+" (in use)")
			continue
		}
		parts = append(parts, h.Name)
	}
	return "hosts: " + strings.Join(parts, ", ")
}

// promptNames is the library as one line, for when /prompt is given no name.
func (a *App) promptNames() string {
	if len(a.library) == 0 {
		return "no saved prompts - type one, then /save <name>"
	}
	names := make([]string, len(a.library))
	for i, p := range a.library {
		names[i] = p.Name
	}
	return "saved prompts: " + strings.Join(names, ", ")
}

// matchModel resolves what was typed to an installed model: an exact name first, then the only one containing it.
func (a *App) matchModel(want string) (string, bool) {
	want = strings.ToLower(want)
	for _, m := range a.models {
		if strings.ToLower(m.Name) == want {
			return m.Name, true
		}
	}
	var found string
	for _, m := range a.models {
		if strings.Contains(strings.ToLower(m.Name), want) {
			if found != "" {
				return "", false // ambiguous
			}
			found = m.Name
		}
	}
	return found, found != ""
}
