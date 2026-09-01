package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/balintb/llamago/internal/store"
	"github.com/balintb/llamago/internal/theme"
)

// globalHit is one match found outside the open conversation.
type globalHit struct {
	session *store.Session
	turn    int
	snippet string
}

// maxGlobalHits bounds the result list. Past this the list stops being something to read and the count says how much was left out.
const maxGlobalHits = 60

// searchAll looks through every loaded session, newest first, and opens the results list. The open conversation is included: "everywhere" that quietly skipped the chat in front of you would be a strange kind of everywhere.
func (a *App) searchAll(query string) tea.Cmd {
	query = strings.TrimSpace(query)
	if query == "" {
		return a.showToast("what should I look for? /find <text>", true)
	}

	seen := map[string]bool{}
	var order []*store.Session
	if a.cur != nil {
		order, seen[a.cur.ID] = append(order, a.cur), true
	}
	for _, s := range a.sessions {
		if !seen[s.ID] {
			order = append(order, s)
		}
	}

	needle := strings.ToLower(query)
	a.findHits = a.findHits[:0]
	total := 0
	for _, s := range order {
		for i, t := range s.Turns {
			if !strings.Contains(strings.ToLower(t.Content), needle) {
				continue
			}
			total++
			if len(a.findHits) < maxGlobalHits {
				a.findHits = append(a.findHits, globalHit{
					session: s, turn: i, snippet: snippetAround(t.Content, needle),
				})
			}
		}
	}
	a.findQuery, a.findIdx = query, 0
	if total == 0 {
		return a.showToast(fmt.Sprintf("%q is in none of your conversations", query), true)
	}
	a.findTotal = total
	a.overlay = overlayFind
	return nil
}

// snippetAround pulls a one-line excerpt with the match near the middle, so a result reads as a sentence rather than as the start of a paragraph.
func snippetAround(content, needle string) string {
	flat := strings.Join(strings.Fields(content), " ")
	at := strings.Index(strings.ToLower(flat), needle)
	if at < 0 {
		return theme.Truncate(flat, 70)
	}
	const lead = 24
	start := max(0, at-lead)
	out := flat[start:]
	if start > 0 {
		out = "…" + out
	}
	return theme.Truncate(out, 70)
}

// openHit jumps to the selected result: its session becomes the conversation and the query stays live, so the match is highlighted on arrival.
func (a *App) openHit() tea.Cmd {
	if a.findIdx < 0 || a.findIdx >= len(a.findHits) {
		return nil
	}
	hit := a.findHits[a.findIdx]
	a.overlay = overlayNone

	var cmd tea.Cmd
	if a.cur == nil || hit.session.ID != a.cur.ID {
		if a.streaming {
			a.chatFeed.stop()
			a.streaming = false
		}
		_ = a.cur.Save()
		a.rememberSession(a.cur)
		a.cur = hit.session
		if hit.session.Model != "" {
			cmd = a.setModel(hit.session.Model)
		}
	}

	a.tab = tabChat
	a.setFocus(focusInput)
	a.pinBottom = false
	a.setSearchQuery(a.findQuery)
	a.searchIn.SetValue(a.findQuery)
	a.invalidateRenders()
	a.layout()
	a.refreshTranscript()
	// Scroll the matching turn into view rather than the first match in the session, which is rarely the one that was picked.
	if hit.turn < len(a.turnLines) {
		a.transcript.EnsureVisible(a.turnLines[hit.turn], 0, 0)
	}
	return cmd
}

func (a *App) onFindKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+f":
		a.overlay = overlayNone
		return nil
	case "down", "j", "ctrl+n", "tab":
		a.findIdx = min(a.findIdx+1, len(a.findHits)-1)
	case "up", "k", "ctrl+p", "shift+tab":
		a.findIdx = max(a.findIdx-1, 0)
	case "home", "g":
		a.findIdx = 0
	case "end", "G":
		a.findIdx = len(a.findHits) - 1
	case "enter":
		return a.openHit()
	}
	return nil
}

func (a *App) viewFindResults() string {
	width := max(48, min(96, a.width-8))
	inner := modalInner(width)
	// Leave room for the border, title, rule, the count and the hint line.
	rows := max(3, min(len(a.findHits), a.height-10))

	// Keep the selection on screen without a scrollbar: the window follows it.
	start := 0
	if a.findIdx >= rows {
		start = a.findIdx - rows + 1
	}

	var lines []string
	for i := start; i < min(len(a.findHits), start+rows); i++ {
		h := a.findHits[i]
		who := "you"
		if h.session.Turns[h.turn].Role != "user" {
			who = shortModel(h.session.Turns[h.turn].Model)
		}
		title := theme.Truncate(h.session.Title, 24)
		line := theme.Pad(title, 26) + theme.Pad(who, 14) + h.snippet
		if i == a.findIdx {
			lines = append(lines, selectedRow(theme.Truncate(line, inner-1), inner))
			continue
		}
		lines = append(lines, " "+theme.Dim.Render(theme.Truncate(line, inner-1)))
	}

	count := fmt.Sprintf("%d matches", a.findTotal)
	if a.findTotal > len(a.findHits) {
		count = fmt.Sprintf("%d matches, showing %d", a.findTotal, len(a.findHits))
	}
	body := theme.Dim.Render(count) + "\n\n" +
		strings.Join(lines, "\n") + "\n\n" +
		theme.Dim.Render("↑↓ move · ↵ open · esc close")
	return modal("Found “"+theme.Truncate(a.findQuery, 30)+"”", body, width)
}
