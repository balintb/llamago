package ui

import (
	"context"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/balintb/llamago/internal/ollama"
	"github.com/balintb/llamago/internal/store"
)

// titlePrompt asks for a label rather than an answer. Models are eager to be helpful, so it says what not to do as clearly as what to do.
const titlePrompt = `Summarise this conversation as a title of at most five words.
Write it in the same language the conversation is in.
Reply with the title alone: no quotes, no punctuation at the end, no preamble.

`

// titleTimeout bounds the request. A title is a nicety; it must never be the reason the app feels stuck.
const titleTimeout = 20 * time.Second

// autoTitleCmd asks the model to name the conversation, once, after the first exchange. It runs against a copy of what it needs so nothing is read from the app while the request is in flight.
func (a *App) autoTitleCmd(s *store.Session) tea.Cmd {
	if !a.cfg.AutoTitle || s == nil || len(s.Turns) < 2 {
		return nil
	}
	// Only while the title is still the one derived from the prompt: a name someone chose, or one already written here, is never overwritten.
	if s.Title != store.Summarize(s.Turns[0].Content) {
		return nil
	}
	// Once per session per run. The derived-title check above stops a second attempt after a successful one, but an attempt that comes back empty would otherwise retry on every turn for the rest of the conversation.
	if a.titleTried[s.ID] {
		return nil
	}
	// The opening exchange is the material, but it is not always the first two turns: a question answered with a tool runs prompt, call, result, answer, and the answer is the part worth summarising.
	answer := ""
	for i := len(s.Turns) - 1; i > 0; i-- {
		if t := s.Turns[i]; t.Role == "assistant" && strings.TrimSpace(t.Content) != "" {
			answer = t.Content
			break
		}
	}
	if answer == "" {
		return nil
	}
	if a.titleTried == nil {
		a.titleTried = map[string]bool{}
	}
	a.titleTried[s.ID] = true

	client, model, id := a.client, a.cfg.Model, s.ID
	transcript := "User: " + clip(s.Turns[0].Content, 600) +
		"\n\nAssistant: " + clip(answer, 600)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), titleTimeout)
		defer cancel()

		var b strings.Builder
		think := false
		limit := 24
		err := client.Chat(ctx, ollama.ChatRequest{
			Model:    model,
			Messages: []ollama.Message{{Role: "user", Content: titlePrompt + transcript}},
			Think:    &think,
			Options:  &ollama.Options{NumPredict: &limit},
		}, func(r ollama.ChatResponse) error {
			b.WriteString(r.Message.Content)
			return nil
		})
		if err != nil {
			// A title nobody asked for is not worth a visible failure.
			return titledMsg{}
		}
		title := cleanTitle(b.String())
		// Multilingual models drift, and a title in a script the conversation never used is unreadable to whoever was having it. Asking for the right language is not enough on its own.
		if !titleMatchesConversation(title, transcript) {
			return titledMsg{}
		}
		return titledMsg{id: id, title: title}
	}
}

// titleScripts are the writing systems worth telling apart. Latin is deliberately absent: a Latin title for a conversation in another script is still readable, while the reverse is not.
var titleScripts = map[string]*unicode.RangeTable{
	"han":        unicode.Han,
	"hiragana":   unicode.Hiragana,
	"katakana":   unicode.Katakana,
	"hangul":     unicode.Hangul,
	"cyrillic":   unicode.Cyrillic,
	"arabic":     unicode.Arabic,
	"hebrew":     unicode.Hebrew,
	"devanagari": unicode.Devanagari,
	"greek":      unicode.Greek,
	"thai":       unicode.Thai,
}

// scriptsIn reports which of those writing systems appear in s.
func scriptsIn(s string) map[string]bool {
	out := map[string]bool{}
	for _, r := range s {
		for name, table := range titleScripts {
			if unicode.Is(table, r) {
				out[name] = true
				break
			}
		}
	}
	return out
}

// titleMatchesConversation rejects a title written in a script the conversation never used - a Chinese title for a conversation held in English, say, which is what a multilingual model produces when it drifts.
func titleMatchesConversation(title, source string) bool {
	used := scriptsIn(source)
	for name := range scriptsIn(title) {
		if !used[name] {
			return false
		}
	}
	return true
}

// cleanTitle turns whatever came back into something that fits a sidebar row. Models wrap titles in quotes, prefix them with "Title:", and occasionally explain themselves first; the first non-empty line, unwrapped, is the title.
func cleanTitle(raw string) string {
	line := ""
	for l := range strings.SplitSeq(raw, "\n") {
		if strings.TrimSpace(l) != "" {
			line = strings.TrimSpace(l)
			break
		}
	}
	for _, prefix := range []string{"title:", "Title:", "TITLE:"} {
		line = strings.TrimSpace(strings.TrimPrefix(line, prefix))
	}
	line = strings.Trim(line, ` "'“”*.`)
	line = strings.Join(strings.Fields(line), " ")
	if words := strings.Fields(line); len(words) > 8 {
		line = strings.Join(words[:8], " ")
	}
	if r := []rune(line); len(r) > 48 {
		line = strings.TrimSpace(string(r[:48]))
	}
	return line
}
