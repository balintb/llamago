package store

import (
	"encoding/json"
	"testing"

	"github.com/balintb/llamago/internal/ollama"
)

func toolSession() *Session {
	return &Session{
		ID: "1", Title: "Tools",
		Turns: []Turn{
			{Role: "user", Content: "what is in main.go"},
			{Role: "assistant", ToolCalls: []ollama.ToolCall{{
				Function: ollama.ToolCallFunc{Name: "read_file", Arguments: map[string]any{"path": "main.go"}},
			}}},
			{Role: "tool", ToolName: "read_file", Content: "package main", ToolArgs: map[string]any{"path": "main.go"}},
			{Role: "assistant", Content: "It declares package main."},
		},
	}
}

// The model has to see its own call and the answer to it. Dropping either leaves it looking at a question it asked and no reply, which it apologises for instead of continuing.
func TestToolTurnsTravelBackToTheModel(t *testing.T) {
	msgs := toolSession().Messages("")
	if len(msgs) != 4 {
		t.Fatalf("%d messages, want all four turns", len(msgs))
	}
	if len(msgs[1].ToolCalls) != 1 || msgs[1].ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("the call did not survive: %+v", msgs[1])
	}
	if msgs[2].Role != "tool" || msgs[2].ToolName != "read_file" || msgs[2].Content != "package main" {
		t.Fatalf("the result did not survive: %+v", msgs[2])
	}
}

// An assistant turn carrying only tool calls has no text, and the usual empty-content rule would drop it.
func TestCallOnlyTurnIsNotDroppedAsEmpty(t *testing.T) {
	s := toolSession()
	s.Turns = s.Turns[:2]
	if msgs := s.Messages(""); len(msgs) != 2 {
		t.Fatalf("%d messages, want the empty assistant turn kept for its calls", len(msgs))
	}
}

// A conversation reopened later still shows what was run.
func TestToolTurnsRoundTripThroughJSON(t *testing.T) {
	b, err := json.Marshal(toolSession())
	if err != nil {
		t.Fatal(err)
	}
	var back Session
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if got := back.Turns[1].ToolCalls[0].Function.Arguments["path"]; got != "main.go" {
		t.Fatalf("arguments = %v, want them preserved", got)
	}
	if back.Turns[2].ToolName != "read_file" {
		t.Fatal("the tool name did not survive the round trip")
	}
}
