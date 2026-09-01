// llamago: a TUI for Ollama
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/balintb/llamago/internal/config"
	"github.com/balintb/llamago/internal/ollama"
	"github.com/balintb/llamago/internal/ui"
)

// version is stamped by the release build:
//
//	go build -ldflags "-X main.version=1.2.3"
//
// It is deliberately empty by default. Release artifacts get the tag this way, but ldflags never reach a `go install module@version` build, so anything left unstamped falls back to what the toolchain itself recorded.
var version = ""

// resolveVersion reports the running build's version, preferring the stamped value, then the module version Go records for `go install`ed builds, then the revision it embeds in any build made from a checkout.
func resolveVersion() string {
	v := version
	if v == "" {
		v = versionFromBuild()
	}
	if v == "" {
		return "dev"
	}
	// -ldflags may carry "1.2.3" while the module version carries "v1.2.3"; settle on one form. Bare revisions have no dot and stay as they are.
	if v[0] >= '0' && v[0] <= '9' && strings.Contains(v, ".") {
		v = "v" + v
	}
	return v
}

// isPseudoVersion reports whether v is one of Go's synthetic v0.0.0-20060102150405-abcdef123456 versions, recognised by the 14-digit timestamp the toolchain embeds in the middle.
func isPseudoVersion(v string) bool {
	for part := range strings.SplitSeq(v, "-") {
		if len(part) != 14 {
			continue
		}
		if strings.IndexFunc(part, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
			return true
		}
	}
	return false
}

func versionFromBuild() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	// A tagged install reports the tag. Older toolchains report "(devel)" for a build from source; newer ones synthesize a pseudo-version, which says no more than the bare revision does and is far too long for a splash screen.
	if v := bi.Main.Version; v != "" && v != "(devel)" && !isPseudoVersion(v) {
		return v
	}
	var rev string
	var dirty bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return ""
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if dirty {
		rev += "-dirty"
	}
	return rev
}

func main() {
	var (
		host        = flag.String("host", "", "Ollama host (default $OLLAMA_HOST or http://127.0.0.1:11434)")
		model       = flag.String("model", "", "model to start with")
		system      = flag.String("system", "", "system prompt for this run")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Println("llamago", resolveVersion())
		fmt.Println("https://github.com/balintb/llamago")
		return
	}

	cfg := config.Load()
	if *host != "" {
		cfg.Host = *host
	}
	if *model != "" {
		cfg.Model = *model
	}
	if *system != "" {
		cfg.System = *system
	}

	// Fail fast with a useful message rather than dropping the user into an empty TUI that just says "offline".
	client := ollama.New(cfg.Host)
	if _, err := client.Version(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "llamago: cannot reach Ollama at %s\n", client.Host())
		fmt.Fprintf(os.Stderr, "  %v\n\n", err)
		fmt.Fprintln(os.Stderr, "Start it with:  ollama serve")
		fmt.Fprintln(os.Stderr, "Or point elsewhere:  llamago -host http://otherhost:11434")
		os.Exit(1)
	}

	ui.Version = resolveVersion()
	if _, err := tea.NewProgram(ui.New(cfg)).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "llamago:", err)
		os.Exit(1)
	}
}

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintln(out, strings.TrimSpace(`
llamago - a terminal client for Ollama

Usage:
  llamago [flags]

Flags:`))
	flag.PrintDefaults()
	fmt.Fprintln(out, "\nOnce running, press ctrl+k for the command palette or f1 for the full keymap.")
}
