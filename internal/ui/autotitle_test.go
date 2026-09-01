package ui

import (
	"testing"

	"github.com/balintb/llamago/internal/ollama"
	"github.com/balintb/llamago/internal/store"
)

// Models wrap titles in quotes, prefix them, and sometimes explain themselves first. The first non-empty line, unwrapped, is the title.
func TestCleanTitle(t *testing.T) {
	for raw, want := range map[string]string{
		`"Go concurrency basics"`:              "Go concurrency basics",
		"Title: Borrow checker\n":              "Borrow checker",
		"\n\n  Rust ownership rules  \n more ": "Rust ownership rules",
		"**Bold title**":                       "Bold title",
		"A title that keeps going well past any reasonable number of words here": "A title that keeps going well past any",
		"": "",
	} {
		if got := cleanTitle(raw); got != want {
			t.Errorf("cleanTitle(%q) = %q, want %q", raw, got, want)
		}
	}
}

// Titling runs while the title is still the derived one, and never over a name someone chose.
func TestAutoTitleOnlyFiresOnceAndOnlyOnDerivedTitles(t *testing.T) {
	a := chatWithPrompts(t, "explain the borrow checker")
	a.cfg.AutoTitle = true
	a.cur.Title = store.Summarize(a.cur.Turns[0].Content)

	if a.autoTitleCmd(a.cur) == nil {
		t.Fatal("no attempt on a fresh conversation with a derived title")
	}
	// Once tried, it does not try again: an attempt that came back empty would otherwise fire on every later turn for the rest of the conversation.
	if a.autoTitleCmd(a.cur) != nil {
		t.Error("tried a second time for the same session")
	}

	b := chatWithPrompts(t, "explain the borrow checker")
	b.cfg.AutoTitle = true
	b.cur.Title = "My own name"
	if b.autoTitleCmd(b.cur) != nil {
		t.Error("titled over a name someone chose")
	}
}

// A question answered with a tool runs prompt, call, result, answer. Counting turns rather than exchanges meant those conversations were never titled - which is every conversation once tools are switched on.
func TestAutoTitleSurvivesAToolExchange(t *testing.T) {
	a := chatWithPrompts(t)
	a.cfg.AutoTitle = true
	a.cur.Turns = []store.Turn{
		{Role: "user", Content: "what files are here?"},
		{Role: "assistant", ToolCalls: []ollama.ToolCall{{
			Function: ollama.ToolCallFunc{Name: "list_files"}}}},
		{Role: "tool", ToolName: "list_files", Content: "main.go"},
		{Role: "assistant", Content: "There is one file, main.go."},
	}
	a.cur.Title = store.Summarize(a.cur.Turns[0].Content)

	if a.autoTitleCmd(a.cur) == nil {
		t.Fatal("a conversation that used a tool was never offered a title")
	}
}

// There has to be an answer to summarise: a turn carrying only tool calls is not one.
func TestAutoTitleWaitsForAnAnswer(t *testing.T) {
	a := chatWithPrompts(t)
	a.cfg.AutoTitle = true
	a.cur.Turns = []store.Turn{
		{Role: "user", Content: "what files are here?"},
		{Role: "assistant", ToolCalls: []ollama.ToolCall{{
			Function: ollama.ToolCallFunc{Name: "list_files"}}}},
	}
	a.cur.Title = store.Summarize(a.cur.Turns[0].Content)

	if a.autoTitleCmd(a.cur) != nil {
		t.Fatal("titled a conversation with no answer in it yet")
	}
}

func TestAutoTitleIsOptIn(t *testing.T) {
	a := chatWithPrompts(t, "first")
	a.cfg.AutoTitle = false
	if a.autoTitleCmd(a.cur) != nil {
		t.Fatal("titled with the setting off")
	}
}

// A failed or empty attempt must leave the derived title alone.
func TestEmptyTitleMessageChangesNothing(t *testing.T) {
	a := chatWithPrompts(t, "first")
	was := a.cur.Title
	a.Update(titledMsg{})
	a.Update(titledMsg{id: a.cur.ID, title: ""})
	if a.cur.Title != was {
		t.Fatalf("title = %q, want it untouched at %q", a.cur.Title, was)
	}

	a.Update(titledMsg{id: a.cur.ID, title: "Borrow checker"})
	if a.cur.Title != "Borrow checker" {
		t.Fatalf("title = %q, want the model's", a.cur.Title)
	}
}

// The exact failure from the screenshot: an English conversation titled "Lisbon's温和气候" by a Chinese-trained model that drifted.
func TestTitleInAnotherScriptIsRejected(t *testing.T) {
	english := "User: what is the weather like in lisboa\n\nAssistant: Lisbon! The capital of Portugal is known for its mild climate."

	if titleMatchesConversation("Lisbon's温和气候", english) {
		t.Error("a half-Chinese title was accepted for an English conversation")
	}
	if titleMatchesConversation("里斯本天气", english) {
		t.Error("a Chinese title was accepted for an English conversation")
	}
	if !titleMatchesConversation("Lisbon weather", english) {
		t.Error("an English title was rejected for an English conversation")
	}
}

// A conversation held in another script keeps titles in it: the check is about drift, not about preferring English.
func TestTitleKeepsTheConversationsOwnScript(t *testing.T) {
	cases := []struct{ title, source string }{
		{"里斯本天气", "User: 里斯本的天气怎么样\n\nAssistant: 里斯本气候温和"},
		{"Погода в Лиссабоне", "User: какая погода в Лиссабоне"},
		{"リスボンの天気", "User: リスボンの天気はどうですか"},
		{"Καιρός Λισαβόνα", "User: πώς είναι ο καιρός στη Λισαβόνα"},
	}
	for _, c := range cases {
		if !titleMatchesConversation(c.title, c.source) {
			t.Errorf("title %q rejected for a conversation in the same script", c.title)
		}
	}
}

// Accents and punctuation are not a script change.
func TestAccentedTitlesAreFine(t *testing.T) {
	source := "User: qual é o clima em Lisboa\n\nAssistant: Lisboa tem um clima ameno."
	for _, title := range []string{"Clima ameno de Lisboa", "Lisboa: clima e estações", "Café & croissants"} {
		if !titleMatchesConversation(title, source) {
			t.Errorf("title %q rejected; accents are not another script", title)
		}
	}
}

// A title the check refuses leaves the derived one in place rather than replacing it with nothing.
func TestRejectedTitleLeavesTheDerivedOne(t *testing.T) {
	a := chatWithPrompts(t, "what is the weather like in lisboa")
	was := a.cur.Title

	a.Update(titledMsg{}) // what the command sends when it refuses a title
	if a.cur.Title != was {
		t.Fatalf("title = %q, want the derived one kept at %q", a.cur.Title, was)
	}
}
