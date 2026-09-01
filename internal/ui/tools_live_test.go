package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/balintb/llamago/internal/ollama"
	"github.com/balintb/llamago/internal/tools"
)

// toolCapableModels lists the installed models advertising tool support.
//
// The flag is not a promise: a model can claim tools and still write the call out as prose, so the test tries them until one actually asks for something.
func toolCapableModels(t *testing.T, c *ollama.Client) []string {
	t.Helper()
	models, err := c.List(context.Background())
	if err != nil {
		t.Skipf("no server: %v", err)
	}
	var out []string
	for _, m := range models {
		info, err := c.Show(context.Background(), m.Name)
		if err != nil {
			continue
		}
		for _, cap := range info.Capabilities {
			if cap == "tools" {
				out = append(out, m.Name)
			}
		}
	}
	if len(out) == 0 {
		t.Skip("no installed model advertises tool support")
	}
	return out
}

// askForTool sends one request and reports what the model asked for.
func askForTool(t *testing.T, c *ollama.Client, req ollama.ChatRequest) ollama.Message {
	t.Helper()
	var msg ollama.Message
	if err := c.Chat(context.Background(), req, func(r ollama.ChatResponse) error {
		msg.Content += r.Message.Content
		msg.ToolCalls = append(msg.ToolCalls, r.Message.ToolCalls...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return msg
}

// The whole loop against a real model: it is offered a tool, asks for it, and answers from what came back.
func TestLiveToolRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a model into memory")
	}
	c := ollama.New("")
	reg := tools.NewRegistry()
	tools.Builtins(reg, t.TempDir())

	think := false
	base := ollama.ChatRequest{
		Think: &think, Tools: reg.Definitions(),
		Messages: []ollama.Message{{
			Role:    "user",
			Content: "What is today's date? Use a tool to find out rather than guessing.",
		}},
	}

	for _, model := range toolCapableModels(t, c) {
		req := base
		req.Model = model
		msg := askForTool(t, c, req)
		if len(msg.ToolCalls) == 0 {
			t.Logf("%s wrote the call out instead of asking for it; trying the next model", model)
			continue
		}
		t.Logf("%s asked for %s", model, msg.ToolCalls[0].Function.Name)

		res := reg.Run(context.Background(), msg.ToolCalls[0])
		if res.Err != nil {
			t.Fatalf("running %s: %v", res.Name, res.Err)
		}
		t.Logf("%s returned %q", res.Name, res.Output)

		req.Messages = append(req.Messages,
			ollama.Message{Role: "assistant", ToolCalls: msg.ToolCalls},
			res.Message(),
		)
		var final string
		if err := c.Chat(context.Background(), req, func(r ollama.ChatResponse) error {
			final += r.Message.Content
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		final = strings.TrimSpace(final)
		t.Logf("final answer: %s", final)

		if final == "" {
			t.Fatal("the model said nothing after the tool result")
		}
		// The answer has to come from the tool, not from the model's guess.
		if year := time.Now().Format("2006"); !strings.Contains(final, year) {
			t.Errorf("the answer does not mention %s, so the tool result was ignored: %q", year, final)
		}
		return
	}
	t.Skip("no installed model emitted a structured tool call")
}
