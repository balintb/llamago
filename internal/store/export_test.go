package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func demoSession() *Session {
	return &Session{
		ID: "1", Title: "Demo", Model: "llama3.2:3b", Created: time.Now(),
		System: "You are terse.",
		Turns: []Turn{
			{Role: "user", Content: "explain <script> tags"},
			{Role: "assistant", Model: "llama3.2:3b", Thinking: "considering",
				Content:   "# Heading\n\nSome `code` here.\n\n- one\n- two\n\n```go\nfunc main() {}\n```",
				EvalCount: 128, TokensPerSec: 42},
		},
	}
}

// The page must be safe to open: anything the model or the user wrote is text, never markup.
func TestHTMLEscapesContent(t *testing.T) {
	got := demoSession().HTML()
	if strings.Contains(got, "<script>") {
		t.Fatal("a <script> tag from the conversation reached the page unescaped")
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Fatal("the tag was not escaped")
	}
}

func TestHTMLRendersTheMarkdownSubset(t *testing.T) {
	got := demoSession().HTML()
	for _, want := range []string{
		"<h1>Heading</h1>",
		"<code>code</code>",
		"<li>one</li>",
		`<pre><code class="language-go">func main() {}</code></pre>`,
		"<details><summary>Reasoning</summary>",
		"You are terse.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("HTML is missing %s", want)
		}
	}
}

// Self-contained means no requests: a page that fetches a stylesheet stops rendering the day that host goes away.
func TestHTMLMakesNoRequests(t *testing.T) {
	got := demoSession().HTML()
	for _, bad := range []string{"http://", "https://", "<script", "@import"} {
		if strings.Contains(got, bad) {
			t.Errorf("the page references %q", bad)
		}
	}
}

// The JSON export is the on-disk shape, so an export and a session file can be read by the same thing.
func TestJSONExportRoundTrips(t *testing.T) {
	s := demoSession()
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var back Session
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Title != s.Title || len(back.Turns) != len(s.Turns) {
		t.Fatalf("round trip lost data: %+v", back)
	}
}

func TestUnknownFormatIsRefused(t *testing.T) {
	if _, err := demoSession().Export("pdf"); err == nil {
		t.Fatal("an unknown format was accepted")
	}
}
