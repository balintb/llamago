package ollama

import (
	"context"
	"strings"
	"testing"
	"time"
)

// liveClient returns a client for a reachable local server, or skips the test. These tests exercise the real wire format, which is the part most likely to drift; they are skipped anywhere Ollama isn't running.
func liveClient(t *testing.T) *Client {
	t.Helper()
	c := New("")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := c.Version(ctx); err != nil {
		t.Skipf("no Ollama server at %s: %v", c.Host(), err)
	}
	return c
}

func TestLiveVersion(t *testing.T) {
	c := liveClient(t)
	v, err := c.Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v == "" {
		t.Fatal("empty version")
	}
	t.Logf("ollama %s at %s", v, c.Host())
}

// chatModel returns the first installed model that can hold a conversation. Image generation models expose no chat endpoint and no context length, so they have to be filtered out of tests that assume either.
func chatModel(t *testing.T, c *Client) string {
	t.Helper()
	models, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range models {
		info, err := c.Show(context.Background(), m.Name)
		if err == nil && !info.CanImage() {
			return m.Name
		}
	}
	return ""
}

func TestLiveListAndShow(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()

	models, err := c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) == 0 {
		t.Skip("no models installed")
	}
	for _, m := range models {
		t.Logf("%-40s %10s %8s %s", m.Name, HumanBytes(m.Size),
			m.Details.ParameterSize, m.Details.Family)
		if m.Name == "" || m.Size == 0 {
			t.Errorf("model decoded with empty name or size: %+v", m)
		}
	}

	name := chatModel(t, c)
	if name == "" {
		t.Skip("no chat-capable models installed")
	}
	info, err := c.Show(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: caps=%v ctx=%d params=%s think=%v vision=%v", name,
		info.Capabilities, info.ContextLength(),
		HumanCount(info.ParameterCount()), info.CanThink(), info.CanVision())
	if info.ContextLength() == 0 {
		t.Error("context length not found in model_info")
	}
}

// TestLiveImageModelCapability pins how a generation model presents itself, so the UI can tell it apart from a chat model before sending anything.
func TestLiveImageModelCapability(t *testing.T) {
	c := liveClient(t)
	models, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, m := range models {
		info, err := c.Show(context.Background(), m.Name)
		if err != nil || !info.CanImage() {
			continue
		}
		found = true
		t.Logf("%s: caps=%v", m.Name, info.Capabilities)
		if info.CanChat() {
			t.Errorf("%s reports both image generation and chat", m.Name)
		}
		if info.CanVision() {
			t.Errorf("%s reports both image generation and vision", m.Name)
		}
	}
	if !found {
		t.Skip("no image generation models installed")
	}
}

func TestLivePS(t *testing.T) {
	c := liveClient(t)
	running, err := c.PS(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range running {
		t.Logf("%-40s %10s %s expires %s", r.Name, HumanBytes(r.Size),
			r.Processor(), HumanUntil(r.ExpiresAt))
	}
}

// TestLiveChatStream is the important one: it proves the streaming decoder, the final-chunk stats and cancellation all behave against a real server.
func TestLiveChatStream(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a model into memory")
	}
	c := liveClient(t)

	name := chatModel(t, c)
	if name == "" {
		t.Skip("no chat-capable models installed")
	}

	// Loading a large model from cold can take a while.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	// Reasoning models spend their first tokens in the thinking channel and may emit no content at all under a tight budget, so give them room and accept output on either channel.
	limit := 200
	var content, thinking strings.Builder
	var chunks int
	var final ChatResponse

	err := c.Chat(ctx, ChatRequest{
		Model:    name,
		Messages: []Message{{Role: "user", Content: "Reply with exactly: hello from ollama"}},
		Options:  &Options{NumPredict: &limit},
	}, func(r ChatResponse) error {
		chunks++
		content.WriteString(r.Message.Content)
		thinking.WriteString(r.Message.Thinking)
		if r.Done {
			final = r
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if chunks < 2 {
		t.Errorf("expected a multi-chunk stream, got %d chunks", chunks)
	}
	if strings.TrimSpace(content.String())+strings.TrimSpace(thinking.String()) == "" {
		t.Error("stream produced neither content nor thinking")
	}
	if thinking.Len() > 0 {
		t.Logf("thinking channel: %d bytes, e.g. %q", thinking.Len(),
			truncateForLog(thinking.String(), 120))
	}
	if !final.Done {
		t.Error("never received a final chunk")
	}
	if final.EvalCount == 0 || final.EvalDuration == 0 {
		t.Errorf("final chunk missing stats: eval=%d dur=%d", final.EvalCount, final.EvalDuration)
	}
	t.Logf("%d chunks, %d tokens, %.1f tok/s, prompt=%d",
		chunks, final.EvalCount, final.TokensPerSecond(), final.PromptEvalCount)
	t.Logf("response: %q", truncateForLog(strings.TrimSpace(content.String()), 200))
}

// TestLiveChatCancel checks that cancelling the context stops generation promptly and reports the cancellation rather than hanging.
func TestLiveChatCancel(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a model into memory")
	}
	c := liveClient(t)
	name := chatModel(t, c)
	if name == "" {
		t.Skip("no chat-capable models installed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	var chunks int
	start := time.Now()
	err := c.Chat(ctx, ChatRequest{
		Model:    name,
		Messages: []Message{{Role: "user", Content: "Count slowly from 1 to 500, one number per line."}},
	}, func(r ChatResponse) error {
		chunks++
		if chunks == 5 {
			cancel()
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected a cancellation error")
	}
	if ctx.Err() == nil {
		t.Errorf("context not cancelled; got err %v", err)
	}
	t.Logf("cancelled after %d chunks in %s (err: %v)", chunks, time.Since(start).Round(time.Millisecond), err)
}

// truncateForLog shortens a string for readable test output.
func truncateForLog(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
