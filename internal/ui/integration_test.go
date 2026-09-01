package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/config"
	"github.com/balintb/llamago/internal/ollama"
)

// pump runs a command and feeds every message it produces back into the model, standing in for the Bubble Tea event loop. Batches are expanded and their commands run in turn. stop lets a test end the loop on a chosen message.
//
// A nil budget guard would let a stuck stream hang the suite, so pumping is bounded by both a deadline and a message count.
func pump(t *testing.T, a *App, cmd tea.Cmd, deadline time.Time, stop func(tea.Msg) bool) {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for n := 0; len(queue) > 0 && n < 200000; n++ {
		if time.Now().After(deadline) {
			t.Fatal("pump timed out")
		}
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if msg == nil {
			continue
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		// Ticks re-arm themselves forever, so drop them rather than pumping them round the loop.
		switch msg.(type) {
		case tickMsg, spinner.TickMsg, progress.FrameMsg:
			continue
		}
		_, next := a.Update(msg)
		queue = append(queue, next)
		if stop != nil && stop(msg) {
			return
		}
	}
}

// liveApp builds an App wired to a reachable server, or skips.
func liveApp(t *testing.T, w, h int) *App {
	t.Helper()
	c := ollama.New("")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := c.Version(ctx); err != nil {
		t.Skipf("no Ollama server at %s: %v", c.Host(), err)
	}

	cfg := config.Default()
	a := New(cfg)
	a.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return a
}

// TestLiveBootstrap checks that the app populates itself from a real server: version, model list and running models all land in the UI.
func TestLiveBootstrap(t *testing.T) {
	a := liveApp(t, 120, 36)
	deadline := time.Now().Add(30 * time.Second)

	pump(t, a, tea.Batch(a.connectCmd(), a.listModelsCmd(), a.psCmd()), deadline, nil)

	if a.connErr != nil {
		t.Fatalf("connect failed: %v", a.connErr)
	}
	if a.version == "" {
		t.Error("no server version recorded")
	}
	if len(a.models) == 0 {
		t.Skip("no models installed")
	}
	if a.cfg.Model == "" {
		t.Error("app did not adopt a default model")
	}

	frame := ansi.Strip(render(a))
	if !strings.Contains(frame, "llamago") {
		t.Error("header missing from the rendered frame")
	}
	t.Logf("bootstrapped: ollama %s, %d models, active=%s", a.version, len(a.models), a.cfg.Model)

	// The models tab should render the real list without breaking geometry.
	a.goTab(tabModels)
	pump(t, a, a.showSelectedModel(), deadline, nil)
	checkFrame(t, render(a), 120, 36, "live models tab")

	if m := a.selectedModel(); m != nil {
		if _, ok := a.details[m.Name]; !ok {
			t.Errorf("details never arrived for %s", m.Name)
		}
	}
}

// TestLiveChatRoundTrip drives a real generation through the UI: type a prompt, send it, stream to completion, and confirm the transcript and stats updated.
func TestLiveChatRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a model into memory")
	}
	a := liveApp(t, 120, 36)
	deadline := time.Now().Add(5 * time.Minute)

	pump(t, a, tea.Batch(a.connectCmd(), a.listModelsCmd()), deadline, nil)
	if len(a.models) == 0 {
		t.Skip("no models installed")
	}
	// Generation models have no chat endpoint, so pick one that can talk.
	pump(t, a, a.fetchMissingDetails(), deadline, nil)
	chat := a.firstChatModel()
	if a.isImageModel(a.cfg.Model) && chat == "" {
		t.Skip("no chat-capable models installed")
	}
	if a.isImageModel(a.cfg.Model) {
		a.setModel(chat)
	}
	// Keep the run short and deterministic.
	a.cfg.NumPredict = 160
	a.cfg.Think = false

	a.input.SetValue("Reply with exactly: hello from llamago")
	pump(t, a, a.send(), deadline, func(m tea.Msg) bool {
		_, done := m.(chatEndMsg)
		return done
	})

	if a.streaming {
		t.Error("still streaming after chatEndMsg")
	}
	if n := len(a.cur.Turns); n != 2 {
		t.Fatalf("expected user + assistant turns, got %d", n)
	}
	reply := a.cur.Turns[1]
	if reply.Role != "assistant" {
		t.Fatalf("last turn is %q, want assistant", reply.Role)
	}
	if reply.Err != "" {
		t.Fatalf("generation failed: %s", reply.Err)
	}
	if strings.TrimSpace(reply.Content)+strings.TrimSpace(reply.Thinking) == "" {
		t.Error("assistant turn is empty")
	}
	if reply.TokensPerSec <= 0 || reply.EvalCount <= 0 {
		t.Errorf("stats missing: %.1f tok/s, %d tokens", reply.TokensPerSec, reply.EvalCount)
	}
	if reply.TTFT <= 0 {
		t.Error("time to first token not measured")
	}

	// The session title should have been derived from the prompt.
	if a.cur.Title == "" || a.cur.Title == "New chat" {
		t.Errorf("session title not derived: %q", a.cur.Title)
	}

	checkFrame(t, render(a), 120, 36, "after live generation")
	t.Logf("reply: %.1f tok/s, %d tokens, ttft %s, title %q",
		reply.TokensPerSec, reply.EvalCount, reply.TTFT.Round(time.Millisecond), a.cur.Title)
	t.Logf("content: %q", truncate(strings.TrimSpace(reply.Content), 160))
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
