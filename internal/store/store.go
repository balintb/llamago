// Package store persists chat sessions to disk as one JSON file per session.
package store

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/balintb/llamago/internal/config"
	"github.com/balintb/llamago/internal/ollama"
)

// Turn is one exchange in a session, with the stats the server reported.
type Turn struct {
	Role     string    `json:"role"`
	Content  string    `json:"content"`
	Thinking string    `json:"thinking,omitempty"`
	Model    string    `json:"model,omitempty"`
	At       time.Time `json:"at"`
	// Images are attachment names, resolved through AttachmentPath.
	Images []string `json:"images,omitempty"`

	// ToolCalls is what an assistant turn asked to have run. ToolName and ToolArgs belong to a turn with Role "tool", which carries what one call produced; Content is its output, or the error if it failed.
	ToolCalls []ollama.ToolCall `json:"tool_calls,omitempty"`
	ToolName  string            `json:"tool_name,omitempty"`
	ToolArgs  map[string]any    `json:"tool_args,omitempty"`
	ToolFail  bool              `json:"tool_failed,omitempty"`

	TokensPerSec float64       `json:"tokens_per_sec,omitempty"`
	EvalCount    int           `json:"eval_count,omitempty"`
	PromptCount  int           `json:"prompt_count,omitempty"`
	TTFT         time.Duration `json:"ttft,omitempty"`
	Total        time.Duration `json:"total,omitempty"`
	Err          string        `json:"error,omitempty"`
}

// Session is a saved conversation.
type Session struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Model   string    `json:"model"`
	System  string    `json:"system"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	// Pinned keeps a session at the top of the list regardless of how long ago it was last touched.
	Pinned bool   `json:"pinned,omitempty"`
	Turns  []Turn `json:"turns"`
}

// NewSession returns an empty session stamped with the current time. The ID doubles as the filename, so it must sort chronologically and be path-safe.
func NewSession(model string, now time.Time) *Session {
	return &Session{
		ID:      now.Format("20060102-150405.000"),
		Title:   "New chat",
		Model:   model,
		Created: now,
		Updated: now,
	}
}

// Messages converts the session's turns into the wire format, dropping failed turns so a stopped or errored response never poisons later context.
func (s *Session) Messages(system string) []ollama.Message {
	msgs := make([]ollama.Message, 0, len(s.Turns)+1)
	if system = strings.TrimSpace(system); system != "" {
		msgs = append(msgs, ollama.Message{Role: "system", Content: system})
	}
	for _, t := range s.Turns {
		// A tool result goes back as itself: dropping it would leave the model with its own call and no answer to it, which reads as a failure it then apologises for.
		if t.Role == "tool" {
			msgs = append(msgs, ollama.Message{
				Role: "tool", ToolName: t.ToolName, Content: t.Content,
			})
			continue
		}
		// An assistant turn that only asked for tools carries no text, and has to travel anyway: the tool results after it answer these calls.
		if len(t.ToolCalls) > 0 {
			msgs = append(msgs, ollama.Message{
				Role: t.Role, Content: t.Content, ToolCalls: t.ToolCalls,
			})
			continue
		}
		// A turn with only images is still worth sending; a failed one is not.
		if t.Err != "" || (strings.TrimSpace(t.Content) == "" && len(t.Images) == 0) {
			continue
		}
		msgs = append(msgs, ollama.Message{
			Role: t.Role, Content: t.Content, Images: encodeAttachments(t.Images),
		})
	}
	return msgs
}

// encodeAttachments loads attachments as base64, which is how Ollama takes them. An unreadable attachment is skipped rather than failing the request.
func encodeAttachments(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		data, err := AttachmentData(n)
		if err != nil {
			continue
		}
		out = append(out, base64.StdEncoding.EncodeToString(data))
	}
	return out
}

// Touch refreshes the update stamp and derives a title from the first prompt.
func (s *Session) Touch() {
	s.Updated = time.Now()
	if s.Title != "" && s.Title != "New chat" {
		return
	}
	for _, t := range s.Turns {
		if t.Role != "user" {
			continue
		}
		s.Title = Summarize(t.Content)
		return
	}
}

// Summarize turns the first line of a prompt into a short session title. It is exported so callers can tell a derived title from one someone chose.
func Summarize(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		s = s[:i]
	}
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "New chat"
	}
	if r := []rune(s); len(r) > 42 {
		return strings.TrimSpace(string(r[:42])) + "…"
	}
	return s
}

func dir() (string, error) {
	base, err := config.Dir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(base, "sessions")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	return d, nil
}

// Save writes the session to disk atomically. Empty sessions are skipped so that opening the app doesn't litter the history with blank chats.
func (s *Session) Save() error {
	if len(s.Turns) == 0 {
		return nil
	}
	d, err := dir()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(d, s.ID+".json")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Delete removes the session's file. A session never saved is not an error.
func (s *Session) Delete() error {
	d, err := dir()
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(d, s.ID+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Markdown renders the session as a portable markdown document.
func (s *Session) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", s.Title)
	fmt.Fprintf(&b, "- Model: `%s`\n", s.Model)
	fmt.Fprintf(&b, "- Started: %s\n", s.Created.Format(time.RFC1123))
	fmt.Fprintf(&b, "- Turns: %d\n", len(s.Turns))
	if sys := strings.TrimSpace(s.System); sys != "" {
		fmt.Fprintf(&b, "\n## System prompt\n\n> %s\n", strings.ReplaceAll(sys, "\n", "\n> "))
	}
	b.WriteString("\n---\n")

	for _, t := range s.Turns {
		switch t.Role {
		case "user":
			b.WriteString("\n## You\n\n")
		default:
			fmt.Fprintf(&b, "\n## %s\n\n", orElse(t.Model, "Assistant"))
		}
		// Reasoning is supporting detail, so quote it rather than inline it.
		if think := strings.TrimSpace(t.Thinking); think != "" {
			fmt.Fprintf(&b, "<details><summary>Reasoning</summary>\n\n> %s\n\n</details>\n\n",
				strings.ReplaceAll(think, "\n", "\n> "))
		}
		if t.Err != "" {
			fmt.Fprintf(&b, "**Error:** %s\n", t.Err)
		}
		if body := strings.TrimSpace(t.Content); body != "" {
			b.WriteString(body + "\n")
		}
		if t.TokensPerSec > 0 {
			fmt.Fprintf(&b, "\n<sub>%d tokens · %.0f tok/s", t.EvalCount, t.TokensPerSec)
			if t.TTFT > 0 {
				fmt.Fprintf(&b, " · %s to first token", t.TTFT.Round(time.Millisecond))
			}
			b.WriteString("</sub>\n")
		}
	}
	return b.String()
}

func orElse(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// Slug turns a title into a filesystem-safe name.
func Slug(title string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case !prevDash && b.Len() > 0:
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "chat"
	}
	if r := []rune(out); len(r) > 48 {
		return strings.Trim(string(r[:48]), "-")
	}
	return out
}

// Load reads every saved session, newest first. Unreadable or corrupt files are skipped rather than failing the whole load.
func Load() ([]*Session, error) {
	d, err := dir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(d)
	if err != nil {
		return nil, err
	}
	var out []*Session
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(d, e.Name()))
		if err != nil {
			continue
		}
		var s Session
		if json.Unmarshal(b, &s) != nil || s.ID == "" {
			continue
		}
		out = append(out, &s)
	}
	Sort(out)
	return out, nil
}

// Sort orders sessions the way the sidebar lists them: pinned first, then most recently touched.
func Sort(sessions []*Session) {
	sort.SliceStable(sessions, func(i, j int) bool {
		if sessions[i].Pinned != sessions[j].Pinned {
			return sessions[i].Pinned
		}
		return sessions[i].Updated.After(sessions[j].Updated)
	})
}

// --- attachments ------------------------------------------------------------

// attachmentsDir is where copies of attached images live. Sessions reference them by name rather than embedding base64, which would bloat every session file and make the history unreadable.
func attachmentsDir() (string, error) {
	base, err := config.Dir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(base, "attachments")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	return d, nil
}

// SaveAttachment copies an image into the attachment store and returns the name to record on a turn. Naming by content digest means attaching the same file twice costs nothing and never collides.
func SaveAttachment(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	dir, err := attachmentsDir()
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%x%s", sha256.Sum256(data), strings.ToLower(filepath.Ext(path)))
	dest := filepath.Join(dir, name)
	if _, err := os.Stat(dest); err == nil {
		return name, nil // already stored
	}
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	return name, os.Rename(tmp, dest)
}

// AttachmentPath resolves a stored attachment name to a full path.
func AttachmentPath(name string) (string, error) {
	dir, err := attachmentsDir()
	if err != nil {
		return "", err
	}
	// Names come from session files, so refuse anything that could climb out of the attachments directory.
	if name == "" || name != filepath.Base(name) {
		return "", fmt.Errorf("bad attachment name %q", name)
	}
	return filepath.Join(dir, name), nil
}

// AttachmentData reads a stored attachment.
func AttachmentData(name string) ([]byte, error) {
	p, err := AttachmentPath(name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}
