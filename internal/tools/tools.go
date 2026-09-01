// Package tools runs the things a model asks for: reading a file, listing a directory, fetching a page, or anything declared in a manifest.
//
// Built-in tools are written in Go and declared tools are JSON files naming a program, but both satisfy Tool, so nothing downstream knows or cares which kind it is holding.
package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/balintb/llamago/internal/ollama"
)

// Tool is anything a model can call.
type Tool interface {
	// Name is what the model calls it, and what a permission answer is remembered against.
	Name() string
	// Definition is what the model is shown: the description it decides from, and the JSON Schema of the arguments.
	Definition() ollama.Tool
	// Safe reports whether the tool may run without asking. Only tools that read, and read within reach, should say yes.
	Safe() bool
	// Run executes the call. The string is handed to the model as-is, so it should read as data rather than as prose about data.
	Run(ctx context.Context, args map[string]any) (string, error)
}

// Registry is the set of tools available to a conversation.
type Registry struct {
	byName map[string]Tool
}

func NewRegistry() *Registry { return &Registry{byName: map[string]Tool{}} }

// Add registers a tool, replacing any of the same name. Declared tools are loaded after the built-ins, so one can deliberately shadow a built-in.
func (r *Registry) Add(t Tool) { r.byName[t.Name()] = t }

// Get finds a tool by the name the model used.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}

// Len is how many tools are registered.
func (r *Registry) Len() int { return len(r.byName) }

// All lists the tools by name, so the order the model sees is stable between runs rather than following Go's map iteration.
func (r *Registry) All() []Tool {
	out := make([]Tool, 0, len(r.byName))
	for _, t := range r.byName {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Definitions is the list offered on a request.
func (r *Registry) Definitions() []ollama.Tool {
	all := r.All()
	out := make([]ollama.Tool, 0, len(all))
	for _, t := range all {
		out = append(out, t.Definition())
	}
	return out
}

// Names lists the registered names, for a status line or an error.
func (r *Registry) Names() []string {
	all := r.All()
	out := make([]string, 0, len(all))
	for _, t := range all {
		out = append(out, t.Name())
	}
	return out
}

// Result is what a call produced, ready to be shown and sent back.
type Result struct {
	Name    string
	Args    map[string]any
	Output  string
	Err     error
	Elapsed time.Duration
}

// Run executes a call and always returns a Result: an error is something the model is told about so it can adjust, not something that stops the turn.
func (r *Registry) Run(ctx context.Context, call ollama.ToolCall) Result {
	name := call.Function.Name
	res := Result{Name: name, Args: call.Function.Arguments}

	t, ok := r.Get(name)
	if !ok {
		res.Err = fmt.Errorf("no tool called %q; available: %s", name, strings.Join(r.Names(), ", "))
		return res
	}
	started := time.Now()
	out, err := t.Run(ctx, call.Function.Arguments)
	res.Output, res.Err, res.Elapsed = out, err, time.Since(started)
	return res
}

// Message is how a result goes back to the model. An error is reported as the content of an ordinary tool message rather than as a protocol failure: the model can then try different arguments, which is usually what is needed.
func (res Result) Message() ollama.Message {
	content := res.Output
	if res.Err != nil {
		content = "error: " + res.Err.Error()
	}
	if strings.TrimSpace(content) == "" {
		content = "(no output)"
	}
	return ollama.Message{Role: "tool", ToolName: res.Name, Content: content}
}

// argString reads a string argument, since a model may send a number or a bool where the schema asked for text.
func argString(args map[string]any, key string) string {
	switch v := args[key].(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strings.TrimSuffix(fmt.Sprintf("%f", v), ".000000")
	default:
		return fmt.Sprint(v)
	}
}

// argInt reads a whole-number argument, with a fallback for absent or unusable values.
func argInt(args map[string]any, key string, fallback int) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return fallback
}

// schema builds a JSON Schema object for a tool's arguments.
func schema(required []string, props map[string]any) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

// prop is one entry in a schema's properties.
func prop(kind, description string) map[string]any {
	return map[string]any{"type": kind, "description": description}
}
