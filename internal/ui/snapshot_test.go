package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/store"
)

// TestSnapshot dumps plain-text renders for visual review. Opt in with LLAMAGO_SNAPSHOT=1; it asserts nothing.
func TestSnapshot(t *testing.T) {
	if os.Getenv("LLAMAGO_SNAPSHOT") == "" {
		t.Skip("set LLAMAGO_SNAPSHOT=1")
	}
	var b strings.Builder
	dump := func(name string, setup func(a *App)) {
		a := newTestApp(118, 34)
		setup(a)
		b.WriteString("\n=== " + name + " ===\n")
		b.WriteString(ansi.Strip(render(a)))
		b.WriteString("\n")
	}
	dump("CHAT", func(a *App) {})
	dump("CHAT streaming", func(a *App) {
		a.streaming = true
		a.tps = []float64{20, 31, 44, 39, 47, 52, 48}
		a.cur.Turns = append(a.cur.Turns, Turnf("assistant", "Streaming a partial answer right now"))
		a.refreshTranscript()
	})
	dump("MODELS", func(a *App) { a.goTab(tabModels) })
	dump("RUNNING", func(a *App) { a.goTab(tabRunning) })
	dump("SETTINGS", func(a *App) { a.goTab(tabSettings) })
	dump("PALETTE", func(a *App) { a.openPalette() })
	dump("HELP", func(a *App) { a.overlay = overlayHelp })
	dump("PULL", func(a *App) { a.goTab(tabModels); a.overlay = overlayPull })
	dump("EMPTY", func(a *App) { a.cur.Turns = nil; a.refreshTranscript() })
	out := filepath.Join(os.TempDir(), "llamago-snapshot.txt")
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", out)
}

// Turnf is a tiny helper for the snapshot fixtures.
func Turnf(role, content string) store.Turn {
	return store.Turn{Role: role, Content: content, Model: "llama3.2:3b"}
}
