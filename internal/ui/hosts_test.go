package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/balintb/llamago/internal/ollama"
)

func runSlashLine(a *App, line string) {
	typeInto(a, line)
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})
}

func TestHostAddSwitchAndForget(t *testing.T) {
	a := chatWithPrompts(t)
	runSlashLine(a, "/host add box http://10.0.0.5:11434")
	if h, ok := a.cfg.FindHost("box"); !ok || h.URL != "http://10.0.0.5:11434" {
		t.Fatalf("saved %+v, want the host", h)
	}

	runSlashLine(a, "/host box")
	if a.client.Host() != "http://10.0.0.5:11434" {
		t.Fatalf("client points at %q, want the new host", a.client.Host())
	}
	if a.cfg.Host != "http://10.0.0.5:11434" {
		t.Error("the switch was not persisted")
	}

	runSlashLine(a, "/host forget box")
	if _, ok := a.cfg.FindHost("box"); ok {
		t.Error("the host was not forgotten")
	}
}

// Everything on screen came from the old server, so none of it may survive the switch: models, memory and the version all describe one machine.
func TestSwitchingHostDropsTheOldServersState(t *testing.T) {
	a := chatWithPrompts(t)
	a.version = "0.32.8"
	a.details["llama3.2:3b"] = &ollama.ShowResponse{}
	if len(a.models) == 0 {
		t.Fatal("setup: expected seeded models")
	}

	a.switchHost("http://elsewhere:11434")

	if len(a.models) != 0 || len(a.running) != 0 {
		t.Error("models or memory survived the switch")
	}
	if len(a.details) != 0 {
		t.Error("cached model details survived the switch")
	}
	if a.version != "" {
		t.Errorf("version = %q, want it cleared until the new server answers", a.version)
	}
}

// A bare URL is accepted; an unknown name is not, since it is probably a typo.
func TestHostAcceptsUrlsAndRefusesUnknownNames(t *testing.T) {
	a := chatWithPrompts(t)
	runSlashLine(a, "/host http://direct:11434")
	if a.client.Host() != "http://direct:11434" {
		t.Fatalf("client = %q, want the literal URL", a.client.Host())
	}

	was := a.client.Host()
	runSlashLine(a, "/host workstaion")
	if a.client.Host() != was {
		t.Error("a typo switched the host")
	}
	if !a.toastErr || !strings.Contains(a.toast, "workstaion") {
		t.Errorf("toast = %q, want it to name the unknown host", a.toast)
	}
}
