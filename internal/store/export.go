package store

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/balintb/llamago/internal/config"
)

// Format is an export format.
type Format string

const (
	FormatMarkdown Format = "md"
	FormatJSON     Format = "json"
	FormatHTML     Format = "html"
)

// Formats lists what Export accepts, for error messages and completion.
var Formats = []Format{FormatMarkdown, FormatJSON, FormatHTML}

// Export writes the session in the given format and returns the path. Files land in the exports directory alongside the saved sessions.
func (s *Session) Export(f Format) (string, error) {
	var body []byte
	switch f {
	case FormatMarkdown:
		body = []byte(s.Markdown())
	case FormatHTML:
		body = []byte(s.HTML())
	case FormatJSON:
		// The on-disk shape is already the useful one: whatever reads an export can read a session file, and vice versa.
		b, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			return "", err
		}
		body = b
	default:
		return "", fmt.Errorf("unknown format %q", f)
	}

	base, err := config.Dir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "exports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, Slug(s.Title)+"-"+s.Created.Format("20060102-150405")+"."+string(f))
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// ExportMarkdown writes the session as markdown.
func (s *Session) ExportMarkdown() (string, error) { return s.Export(FormatMarkdown) }

// HTML renders the session as a self-contained page: no scripts, no fonts and no requests, so it can be mailed or committed and still read the same in five years.
//
// Code blocks carry a language class, the hook a highlighter would use if the page is ever dropped into one; nothing here depends on that happening.
func (s *Session) HTML() string {
	var b strings.Builder
	fmt.Fprintf(&b, `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s</title>
<style>%s</style>
</head><body>
<header><h1>%s</h1><p class="meta">%s · %s · %d messages</p></header>
`, html.EscapeString(s.Title), htmlStyle,
		html.EscapeString(s.Title), html.EscapeString(s.Model),
		s.Created.Format(time.RFC1123), len(s.Turns))

	if sys := strings.TrimSpace(s.System); sys != "" {
		fmt.Fprintf(&b, "<section class=\"system\"><h2>System prompt</h2>%s</section>\n",
			markdownToHTML(sys))
	}

	for _, t := range s.Turns {
		role, who := "user", "You"
		if t.Role != "user" {
			role, who = "assistant", orElse(t.Model, "Assistant")
		}
		fmt.Fprintf(&b, "<article class=%q><h2>%s</h2>\n", role, html.EscapeString(who))
		if think := strings.TrimSpace(t.Thinking); think != "" {
			fmt.Fprintf(&b, "<details><summary>Reasoning</summary>%s</details>\n",
				markdownToHTML(think))
		}
		if t.Err != "" {
			fmt.Fprintf(&b, "<p class=\"error\">%s</p>\n", html.EscapeString(t.Err))
		}
		if body := strings.TrimSpace(t.Content); body != "" {
			b.WriteString(markdownToHTML(body) + "\n")
		}
		if t.TokensPerSec > 0 {
			fmt.Fprintf(&b, "<p class=\"stats\">%d tokens · %.0f tok/s</p>\n",
				t.EvalCount, t.TokensPerSec)
		}
		b.WriteString("</article>\n")
	}
	b.WriteString("</body></html>\n")
	return b.String()
}

// markdownToHTML converts the subset that actually turns up in replies: fenced code, headings, bullets and paragraphs. Everything else is escaped and left as written, which is honest about what was said rather than guessing.
func markdownToHTML(src string) string {
	var b strings.Builder
	var para, list []string

	flush := func() {
		if len(para) > 0 {
			fmt.Fprintf(&b, "<p>%s</p>\n", inlineHTML(strings.Join(para, " ")))
			para = nil
		}
		if len(list) > 0 {
			b.WriteString("<ul>\n")
			for _, item := range list {
				fmt.Fprintf(&b, "<li>%s</li>\n", inlineHTML(item))
			}
			b.WriteString("</ul>\n")
			list = nil
		}
	}

	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "```"):
			flush()
			lang := strings.TrimSpace(strings.TrimPrefix(line, "```"))
			var code []string
			for i++; i < len(lines) && !strings.HasPrefix(lines[i], "```"); i++ {
				code = append(code, lines[i])
			}
			class := ""
			if lang != "" {
				class = fmt.Sprintf(" class=\"language-%s\"", html.EscapeString(lang))
			}
			fmt.Fprintf(&b, "<pre><code%s>%s</code></pre>\n", class,
				html.EscapeString(strings.Join(code, "\n")))
		case strings.HasPrefix(line, "#"):
			flush()
			level := len(line) - len(strings.TrimLeft(line, "#"))
			text := strings.TrimSpace(strings.TrimLeft(line, "#"))
			level = min(max(level, 1), 6)
			fmt.Fprintf(&b, "<h%d>%s</h%d>\n", level, inlineHTML(text), level)
		case strings.HasPrefix(strings.TrimSpace(line), "- "),
			strings.HasPrefix(strings.TrimSpace(line), "* "):
			if len(para) > 0 {
				flush()
			}
			list = append(list, strings.TrimSpace(line)[2:])
		case strings.TrimSpace(line) == "":
			flush()
		default:
			if len(list) > 0 {
				flush()
			}
			para = append(para, line)
		}
	}
	flush()
	return b.String()
}

// inlineHTML escapes a line and turns `code` spans into markup. Emphasis is left as written: guessing at nested asterisks is how a renderer starts mangling the text it was meant to preserve.
func inlineHTML(s string) string {
	escaped := html.EscapeString(s)
	parts := strings.Split(escaped, "`")
	for i := 1; i < len(parts); i += 2 {
		parts[i] = "<code>" + parts[i] + "</code>"
	}
	return strings.Join(parts, "")
}

const htmlStyle = `
:root{color-scheme:light dark}
body{max-width:46rem;margin:2rem auto;padding:0 1rem;
 font:16px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif}
h1{font-size:1.5rem;margin-bottom:.25rem}
h2{font-size:.8rem;text-transform:uppercase;letter-spacing:.08em;opacity:.7;margin:0 0 .5rem}
.meta,.stats{font-size:.8rem;opacity:.6}
article{border-left:3px solid;padding:.75rem 0 .75rem 1rem;margin:1.5rem 0}
article.user{border-color:#a78bfa}
article.assistant{border-color:#2dd4bf}
.system{border:1px dashed;padding:.75rem 1rem;border-radius:.5rem;opacity:.8}
.error{color:#f87171}
pre{overflow-x:auto;padding:.75rem;border-radius:.5rem;background:#0000000d}
code{font:.9em ui-monospace,SFMono-Regular,Menlo,monospace}
@media(prefers-color-scheme:dark){pre{background:#ffffff14}}
details{margin:.5rem 0;opacity:.75}
summary{cursor:pointer;font-size:.85rem}
`
