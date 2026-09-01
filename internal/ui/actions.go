package ui

import (
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/ollama"
	"github.com/balintb/llamago/internal/store"
	"github.com/balintb/llamago/internal/theme"
)

// applyTheme records the palette and rebuilds everything that captured a colour when it was made: the markdown renderer, the cached turn renderings and the composer styles all hold the old palette until they are rebuilt.
func (a *App) applyTheme(name string) tea.Cmd {
	a.cfg.Theme = name
	_ = a.cfg.Save()
	// layout rebuilds the renderer whenever the width it was built for changed; zeroing that is what makes it rebuild for a palette change too.
	a.md, a.mdWidth = nil, 0
	a.invalidateRenders()
	clear(a.thumbCache)
	styleTextarea(&a.sysInput)
	a.layout()
	a.refreshTranscript()
	return a.okToast("theme " + name)
}

// --- context window accounting ----------------------------------------------

// approxTokens is the usual rough four-characters-per-token rule. It is only used for text the server has not yet counted for us.
func approxTokens(s string) int { return (len(s) + 3) / 4 }

// tokensPerImage is a rough cost for one attachment. Vision models expand an image into hundreds of tokens and the real figure depends on the model and the resolution, so this only has to stop the meter reading near zero for a prompt that is mostly picture. The server's own count replaces it as soon as a reply arrives.
const tokensPerImage = 400

// contextUsage estimates the prompt size of the next request.
//
// Every response carries the server's own prompt_eval_count, which is exact. The most recent one therefore anchors the count, and only the turns after it have to be approximated. exact reports whether any approximation was needed.
func (a *App) contextUsage() (used int, exact bool) {
	if a.cur == nil {
		return 0, true
	}
	anchor := -1
	for i := len(a.cur.Turns) - 1; i >= 0; i-- {
		if t := a.cur.Turns[i]; t.PromptCount > 0 {
			// The reported prompt covers the system message and everything up to that turn; its own output is what the next prompt will also carry.
			anchor, used = i, t.PromptCount+t.EvalCount
			break
		}
	}
	if anchor < 0 {
		used = approxTokens(a.cfg.System)
	}
	for i := anchor + 1; i < len(a.cur.Turns); i++ {
		t := a.cur.Turns[i]
		used += approxTokens(t.Content) + approxTokens(t.Thinking) +
			len(t.Images)*tokensPerImage
	}
	// Queued attachments are about to be sent, so count them too.
	used += len(a.pending) * tokensPerImage
	exact = anchor == len(a.cur.Turns)-1 && len(a.pending) == 0
	return used, exact
}

// viewContextMeter renders the context budget: a bar that warms to amber and then red as the conversation approaches num_ctx, past which Ollama silently drops the oldest turns.
func (a *App) viewContextMeter() string {
	used, exact := a.contextUsage()
	limit := max(1, a.cfg.NumCtx)
	pct := float64(used) / float64(limit)

	stops := theme.Accent
	switch {
	case pct >= 0.95:
		stops = []color.Color{theme.Red, theme.Red}
	case pct >= 0.75:
		stops = []color.Color{theme.Amber, theme.Orange}
	}

	label := fmt.Sprintf("%s/%s ctx", ollama.HumanCount(int64(used)), ollama.HumanCount(int64(limit)))
	if !exact {
		label = "~" + label
	}
	style := theme.Dim
	if pct >= 0.95 {
		style = theme.Err
		label += " - trimming"
	}
	return theme.Meter(10, pct, stops...) + " " + style.Render(label)
}

// --- code blocks ------------------------------------------------------------

// codeBlock is one fenced block lifted out of an assistant response.
type codeBlock struct {
	turn int    // index of the turn it came from
	lang string // fence info string, may be empty
	code string
}

// codeBlocks returns every fenced code block in the conversation, in order. Numbering runs from the top so a block keeps its label as the chat grows.
func (a *App) codeBlocks() []codeBlock {
	if a.cur == nil {
		return nil
	}
	var out []codeBlock
	for i, t := range a.cur.Turns {
		if t.Role != "assistant" {
			continue
		}
		for _, b := range extractCodeBlocks(t.Content) {
			b.turn = i
			out = append(out, b)
		}
	}
	return out
}

// extractCodeBlocks scans markdown for ``` fenced blocks. An unterminated fence runs to the end of the text, which is what a mid-stream response looks like.
func extractCodeBlocks(md string) []codeBlock {
	var (
		out    []codeBlock
		body   []string
		lang   string
		inside bool
	)
	for line := range strings.SplitSeq(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inside {
				out = append(out, codeBlock{lang: lang, code: strings.Join(body, "\n")})
				body, lang, inside = nil, "", false
				continue
			}
			inside = true
			lang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			continue
		}
		if inside {
			body = append(body, line)
		}
	}
	if inside && len(body) > 0 {
		out = append(out, codeBlock{lang: lang, code: strings.Join(body, "\n")})
	}
	return out
}

// copyCodeBlock puts block n (1-based, as labelled) on the clipboard.
func (a *App) copyCodeBlock(n int) tea.Cmd {
	blocks := a.codeBlocks()
	if n < 1 || n > len(blocks) {
		return a.showToast(fmt.Sprintf("no code block %d", n), true)
	}
	b := blocks[n-1]
	lines := strings.Count(b.code, "\n") + 1
	what := fmt.Sprintf("block %d", n)
	if b.lang != "" {
		what += " (" + b.lang + ")"
	}
	return tea.Batch(tea.SetClipboard(b.code),
		a.okToast(fmt.Sprintf("copied %s, %d lines", what, lines)))
}

// viewCodeBlockBar is the hint line under a response that contains code, listing each block's copy label.
func (a *App) viewCodeBlockBar(turnIdx int, blocks []codeBlock) string {
	var parts []string
	for i, b := range blocks {
		if b.turn != turnIdx {
			continue
		}
		label := fmt.Sprintf("⌗%d", i+1)
		if b.lang != "" {
			label += " " + b.lang
		}
		parts = append(parts, lipgloss.NewStyle().Foreground(theme.Teal).Render(label))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, theme.Dim.Render(" · ")) +
		theme.Dim.Render("   tab to the transcript, then press the number to copy")
}

// --- transcript search ------------------------------------------------------

// searchHit is one match: which pane it landed in, its line, and the cell columns it spans on that line. side is sideChat for the main transcript, or a comparison column index.
type searchHit struct {
	side             int
	line, start, end int
}

var (
	searchMatchStyle   = lipgloss.NewStyle().Foreground(theme.Deep).Background(theme.Amber)
	searchCurrentStyle = lipgloss.NewStyle().Foreground(theme.Deep).Background(theme.Magenta).Bold(true)
)

// lineMatches finds every case-insensitive occurrence of query in one line of plain text, returned as cell columns.
//
// Columns, not byte offsets: the highlighter addresses text by display cell, so a multi-byte rune earlier in the line would otherwise shift every match after it onto the wrong characters.
func lineMatches(plain, query string) [][2]int {
	if query == "" || plain == "" {
		return nil
	}
	hay, needle := strings.ToLower(plain), strings.ToLower(query)
	// Lowercasing can change byte length for some scripts, which would make the offsets lie. Fall back to a case-sensitive search when that happens.
	if len(hay) != len(plain) || len(needle) != len(query) {
		hay, needle = plain, query
	}

	var out [][2]int
	for i := 0; i+len(needle) <= len(hay); {
		j := strings.Index(hay[i:], needle)
		if j < 0 {
			break
		}
		b := i + j
		start := ansi.StringWidth(plain[:b])
		out = append(out, [2]int{start, start + ansi.StringWidth(plain[b:b+len(needle)])})
		i = b + len(needle)
	}
	return out
}

// highlightMatches paints every occurrence of query in the rendered transcript and reports where they landed. The match at index current is emphasised.
//
// This is done here rather than through the viewport's own highlighter, which walks byte offsets over ANSI-stripped text while indexing the styled string for line breaks; on styled content the two disagree and it marks the wrong characters.
func highlightMatches(content, query string, current, side int) (string, []searchHit) {
	if query == "" {
		return content, nil
	}
	lines := strings.Split(content, "\n")
	var hits []searchHit

	for li, line := range lines {
		spans := lineMatches(ansi.Strip(line), query)
		if len(spans) == 0 {
			continue
		}
		ranges := make([]lipgloss.Range, 0, len(spans))
		for _, sp := range spans {
			style := searchMatchStyle
			if len(hits) == current {
				style = searchCurrentStyle
			}
			hits = append(hits, searchHit{side: side, line: li, start: sp[0], end: sp[1]})
			ranges = append(ranges, lipgloss.NewRange(sp[0], sp[1], style))
		}
		lines[li] = lipgloss.StyleRanges(line, ranges...)
	}
	return strings.Join(lines, "\n"), hits
}

// openSearch focuses the find bar over the hint line, in either view.
func (a *App) openSearch() tea.Cmd {
	a.searching = true
	if a.comparing {
		// The composer must not keep taking keystrokes behind the find bar.
		a.input.Blur()
	} else {
		a.setFocus(focusTranscript)
	}
	a.searchIn.SetValue(a.searchQuery)
	a.searchIn.CursorEnd()
	return a.searchIn.Focus()
}

// refreshSearchable re-renders whichever view the search is running over.
func (a *App) refreshSearchable() {
	if a.comparing {
		a.refreshCompare()
		return
	}
	a.refreshTranscript()
}

// commitSearch closes the input but keeps the highlights, handing the keyboard back to the transcript so n and N can cycle matches instead of being typed into the query.
func (a *App) commitSearch() tea.Cmd {
	a.searching = false
	a.searchIn.Blur()
	if a.comparing {
		// Hand the keyboard to the column holding the current match, which is where n and N are bound.
		a.compareFocus = compareFocusComposer
		if len(a.searchHits) > 0 {
			if side := a.searchHits[min(a.searchIdx, len(a.searchHits)-1)].side; side >= 0 {
				a.compareFocus = side + 1
				a.compareIdx = side
			}
		}
	} else {
		a.setFocus(focusTranscript)
	}
	if a.searchQuery != "" && len(a.searchHits) == 0 {
		return a.showToast("no matches for "+a.searchQuery, true)
	}
	return nil
}

// closeSearch drops the query and its highlights.
func (a *App) closeSearch() {
	a.searching = false
	a.searchIn.Blur()
	a.searchIn.SetValue("")
	a.searchQuery = ""
	a.searchHits = nil
	a.searchIdx = 0
	a.refreshSearchable()
	if a.comparing && a.compareFocus == compareFocusComposer {
		a.input.Focus()
	}
}

// setSearchQuery re-highlights for a new query and jumps to the first match.
func (a *App) setSearchQuery(q string) {
	a.searchQuery = q
	a.searchIdx = 0
	a.refreshSearchable()
	a.showSearchHit()
}

// stepMatch moves the selection by delta, wrapping at both ends.
func (a *App) stepMatch(delta int) {
	n := len(a.searchHits)
	if n == 0 {
		return
	}
	a.searchIdx = ((a.searchIdx+delta)%n + n) % n
	a.refreshSearchable()
	a.showSearchHit()
}

// showSearchHit scrolls the current match into view, in whichever pane holds it.
func (a *App) showSearchHit() {
	if len(a.searchHits) == 0 {
		return
	}
	h := a.searchHits[min(a.searchIdx, len(a.searchHits)-1)]
	if h.side >= 0 {
		if h.side < len(a.compare) && a.compare[h.side] != nil {
			a.compare[h.side].vp.EnsureVisible(h.line, h.start, h.end)
			// Point the highlight at the column the match is in, so it is obvious which side matched.
			a.compareIdx = h.side
		}
		return
	}
	a.transcript.EnsureVisible(h.line, h.start, h.end)
	a.pinBottom = a.transcript.AtBottom()
}

// searchScopeLabel names what is being searched, since compare mode spans both columns rather than one transcript.
func (a *App) searchScopeLabel() string {
	if !a.comparing || len(a.searchHits) == 0 {
		return ""
	}
	perSide := make([]int, len(a.compare))
	for _, h := range a.searchHits {
		if h.side >= 0 && h.side < len(perSide) {
			perSide[h.side]++
		}
	}
	return fmt.Sprintf(" (%d|%d)", perSide[0], perSide[1])
}

// viewSearchBar replaces the composer hint while a search is open.
func (a *App) viewSearchBar(width int) string {
	label := " search "
	if !a.searching {
		label = " found  "
	}
	left := theme.Key.Render(label) + a.searchIn.View()

	var right string
	switch {
	case a.searchQuery == "":
		right = theme.Dim.Render("type to search · esc close")
	case len(a.searchHits) == 0:
		right = theme.Err.Render("no matches")
	case a.searching:
		right = theme.Dim.Render(fmt.Sprintf("%d of %d%s · ↵ to cycle with n/N",
			a.searchIdx+1, len(a.searchHits), a.searchScopeLabel()))
	default:
		right = theme.Dim.Render(fmt.Sprintf("%d of %d%s · n/N cycle · esc clear",
			a.searchIdx+1, len(a.searchHits), a.searchScopeLabel()))
	}
	return spread(left, right+" ", width)
}

// --- export -----------------------------------------------------------------

// exportCmd writes the current conversation to a markdown file.
func (a *App) exportCmd() tea.Cmd { return a.exportAs(store.FormatMarkdown) }

// exportAs writes the conversation in one of the supported formats.
func (a *App) exportAs(f store.Format) tea.Cmd {
	if a.cur == nil || len(a.cur.Turns) == 0 {
		return a.showToast("nothing to export yet", true)
	}
	s := a.cur
	return func() tea.Msg {
		path, err := s.Export(f)
		if err != nil {
			return actionMsg{err: fmt.Errorf("export: %w", err)}
		}
		return actionMsg{text: "exported to " + path}
	}
}
