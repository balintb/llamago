package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

// TestLiveCompareRace runs a real two-model race. It needs two installed models, so it skips on a single-model host.
func TestLiveCompareRace(t *testing.T) {
	if testing.Short() {
		t.Skip("loads models into memory")
	}
	a := liveApp(t, 130, 36)
	deadline := time.Now().Add(6 * time.Minute)
	pump(t, a, tea.Batch(a.connectCmd(), a.listModelsCmd()), deadline, nil)
	// Learn every model's capabilities, as the compare picker does, so the thinking flag is actually sent.
	pump(t, a, a.fetchMissingDetails(), deadline, nil)
	if len(a.models) == 0 {
		t.Skip("no models installed")
	}
	a.cfg.NumPredict = 80
	a.cfg.Think = false

	// Only chat models can race; a generation model has no /api/chat at all.
	var chat []string
	for _, m := range a.models {
		if !a.isImageModel(m.Name) {
			chat = append(chat, m.Name)
		}
	}
	if len(chat) == 0 {
		t.Skip("no chat-capable models installed")
	}
	a.setModel(chat[0])

	prompt := "Say hello in one short sentence."
	var start tea.Cmd
	if len(chat) >= 2 {
		start = a.startCompare(chat[1], prompt)
	} else {
		// Only one model is installed, so race it against itself. That still exercises what is actually risky here: two concurrent streams, and chunks routing to the right column. A genuine two-model race is left unverified rather than pulling gigabytes to test it.
		t.Log("one chat model installed; racing it against itself to exercise the plumbing")
		start = a.startSelfRace(prompt)
	}

	ended := 0
	pump(t, a, start, deadline, func(m tea.Msg) bool {
		if _, ok := m.(chatEndMsg); ok {
			ended++
		}
		return ended >= compareSides
	})

	for i, r := range a.compare {
		if r.streaming {
			t.Errorf("side %d never finished", i)
		}
		if r.err != nil {
			t.Errorf("side %d failed: %v", i, r.err)
		}
		// A finished answer is settled onto the side's thread.
		answer := lastAnswer(r)
		if strings.TrimSpace(answer.Content)+strings.TrimSpace(answer.Thinking) == "" {
			t.Errorf("side %d produced nothing", i)
		}
		t.Logf("side %d: %-40s %.1f tok/s, %d tokens, %s",
			i, r.model, answer.TokensPerSec, answer.EvalCount, shortDuration(r.elapsed))
		t.Logf("         %.70q", strings.Join(strings.Fields(answer.Content), " "))
	}
	checkFrame(t, render(a), 130, 36, "live compare")

	// Keeping a side must commit exactly that exchange.
	before := len(a.cur.Turns)
	want := len(a.compare[0].turns)
	a.keepCompareSide(0)
	if got := len(a.cur.Turns) - before; got != want {
		t.Errorf("keeping a side appended %d turns, want %d", got, want)
	}
	if a.comparing {
		t.Error("race still open after keeping a side")
	}
}

// startSelfRace sets up a two-column race using the same model on both sides. startCompare deliberately refuses that, so this drives the layer beneath it.
func (a *App) startSelfRace(prompt string) tea.Cmd {
	a.comparing = true
	a.compareIdx = 0
	a.compareFocus = compareFocusComposer
	a.compare = make([]*compareRun, compareSides)

	for i := range compareSides {
		a.compare[i] = &compareRun{model: a.cfg.Model, vp: viewport.New()}
	}
	a.layout()
	return a.sendCompare(prompt)
}
