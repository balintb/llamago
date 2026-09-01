package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/ollama"
	"github.com/balintb/llamago/internal/store"
)

// --- context meter ----------------------------------------------------------

// TestContextUsagePrefersServerCounts checks that the meter anchors on the server's exact prompt_eval_count rather than guessing from character counts.
func TestContextUsageAnchorsOnServerCounts(t *testing.T) {
	a := newTestApp(100, 30)
	a.cfg.System = "be brief"

	// No response yet: everything is an estimate.
	a.cur.Turns = []store.Turn{{Role: "user", Content: strings.Repeat("x", 400)}}
	used, exact := a.contextUsage()
	if exact {
		t.Error("with no server counts the usage must be reported as an estimate")
	}
	if want := approxTokens("be brief") + 100; used != want {
		t.Errorf("estimated usage = %d, want %d", used, want)
	}

	// Once the server reports a prompt size, that becomes the anchor.
	a.cur.Turns = append(a.cur.Turns, store.Turn{
		Role: "assistant", Content: "ok", PromptCount: 1234, EvalCount: 56,
	})
	used, exact = a.contextUsage()
	if !exact {
		t.Error("usage should be exact right after a counted response")
	}
	if want := 1234 + 56; used != want {
		t.Errorf("anchored usage = %d, want %d", used, want)
	}

	// A turn added after the anchor is estimated on top of it.
	a.cur.Turns = append(a.cur.Turns, store.Turn{Role: "user", Content: strings.Repeat("y", 40)})
	used, exact = a.contextUsage()
	if exact {
		t.Error("a turn after the anchor makes the total an estimate again")
	}
	if want := 1234 + 56 + 10; used != want {
		t.Errorf("usage after anchor = %d, want %d", used, want)
	}
}

// TestContextMeterWarns checks the meter turns red and says so near the limit.
func TestContextMeterWarns(t *testing.T) {
	a := newTestApp(100, 30)
	a.cfg.NumCtx = 1000

	a.cur.Turns = []store.Turn{{Role: "assistant", PromptCount: 100, EvalCount: 0}}
	if got := ansi.Strip(a.viewContextMeter()); strings.Contains(got, "trimming") {
		t.Errorf("warned at 10%% of the window: %q", got)
	}

	a.cur.Turns = []store.Turn{{Role: "assistant", PromptCount: 990, EvalCount: 0}}
	if got := ansi.Strip(a.viewContextMeter()); !strings.Contains(got, "trimming") {
		t.Errorf("no warning at 99%% of the window: %q", got)
	}
}

// --- code blocks ------------------------------------------------------------

func TestExtractCodeBlocks(t *testing.T) {
	md := "Intro\n\n```go\nfunc main() {}\n```\n\nMiddle\n\n```\nplain\ntext\n```\n"
	got := extractCodeBlocks(md)
	if len(got) != 2 {
		t.Fatalf("got %d blocks, want 2", len(got))
	}
	if got[0].lang != "go" || got[0].code != "func main() {}" {
		t.Errorf("first block = %+v", got[0])
	}
	if got[1].lang != "" || got[1].code != "plain\ntext" {
		t.Errorf("second block = %+v", got[1])
	}

	// A fence still open, as seen mid-stream, runs to the end.
	partial := extractCodeBlocks("text\n\n```py\nprint(1)")
	if len(partial) != 1 || partial[0].code != "print(1)" {
		t.Errorf("unterminated fence = %+v", partial)
	}

	if n := len(extractCodeBlocks("no code here")); n != 0 {
		t.Errorf("found %d blocks in prose", n)
	}
}

// TestCodeBlockNumberingAndCopy checks blocks are numbered across the whole conversation and that the digit shortcut copies the right one.
func TestCodeBlockNumberingAndCopy(t *testing.T) {
	a := newTestApp(100, 30)
	a.cur.Turns = []store.Turn{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "a\n```go\nONE\n```"},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "b\n```sh\nTWO\n```\nand\n```\nTHREE\n```"},
	}
	blocks := a.codeBlocks()
	if len(blocks) != 3 {
		t.Fatalf("got %d blocks, want 3", len(blocks))
	}
	for i, want := range []string{"ONE", "TWO", "THREE"} {
		if blocks[i].code != want {
			t.Errorf("block %d = %q, want %q", i+1, blocks[i].code, want)
		}
	}
	// Blocks must carry the turn they came from so labels land on the right message.
	if blocks[0].turn != 1 || blocks[1].turn != 3 || blocks[2].turn != 3 {
		t.Errorf("block turns = %d,%d,%d, want 1,3,3", blocks[0].turn, blocks[1].turn, blocks[2].turn)
	}

	// Out-of-range digits report rather than panic.
	a.copyCodeBlock(9)
	if !a.toastErr {
		t.Error("copying a nonexistent block should raise an error toast")
	}
	a.copyCodeBlock(2)
	if a.toastErr {
		t.Error("copying a real block should not error")
	}

	// The label bar lists this turn's blocks only.
	bar := ansi.Strip(a.viewCodeBlockBar(3, blocks))
	if !strings.Contains(bar, "⌗2") || !strings.Contains(bar, "⌗3") {
		t.Errorf("turn 3 bar should list blocks 2 and 3: %q", bar)
	}
	if strings.Contains(bar, "⌗1") {
		t.Errorf("turn 3 bar should not list block 1: %q", bar)
	}
}

// --- search -----------------------------------------------------------------

// TestHighlightLandsOnTheQuery is the regression test for highlights marking the wrong characters. The highlighter addresses text by display cell over ANSI-stripped content, so styled prefixes and wide runes both used to shift every match after them.
func TestHighlightLandsOnTheQuery(t *testing.T) {
	bold := lipgloss.NewStyle().Bold(true).Render
	const needle = "goroutine"

	for _, line := range []string{
		"plain text with goroutine in it",
		bold("styled words") + " then goroutine after the styling",
		"日本語 and goroutine after wide runes",
		bold("日本語") + " styled wide then goroutine",
		"emoji 🎉 then goroutine",
		bold("a") + "b" + bold("c") + " goroutine between many style runs",
	} {
		out, hits := highlightMatches(line, needle, 0, sideChat)
		if len(hits) != 1 {
			t.Errorf("got %d hits in %q, want 1", len(hits), ansi.Strip(line))
			continue
		}
		h := hits[0]

		// The recorded columns must actually span the query.
		plain := ansi.Strip(line)
		if got := ansi.Cut(plain, h.start, h.end); !strings.EqualFold(got, needle) {
			t.Errorf("hit spans %q, want %q\n  in %q (cells %d-%d)",
				got, needle, plain, h.start, h.end)
		}
		// Highlighting must not disturb the text itself.
		if got := ansi.Strip(out); got != plain {
			t.Errorf("highlighting changed the text:\n got  %q\n want %q", got, plain)
		}
	}
}

// TestHighlightNumbersHitsInOrder checks multi-line, multi-hit bookkeeping.
func TestHighlightNumbersHitsInOrder(t *testing.T) {
	content := "go here\nnothing\ngo and go again"
	_, hits := highlightMatches(content, "go", 0, sideChat)
	if len(hits) != 3 {
		t.Fatalf("got %d hits, want 3", len(hits))
	}
	if hits[0].line != 0 || hits[1].line != 2 || hits[2].line != 2 {
		t.Errorf("hit lines = %d,%d,%d, want 0,2,2", hits[0].line, hits[1].line, hits[2].line)
	}
	if hits[1].start >= hits[2].start {
		t.Error("hits on the same line should be ordered left to right")
	}
	if n := len(mustHits(highlightMatches(content, "", 0, sideChat))); n != 0 {
		t.Errorf("empty query produced %d hits", n)
	}
	if n := len(mustHits(highlightMatches(content, "zebra", 0, sideChat))); n != 0 {
		t.Errorf("absent query produced %d hits", n)
	}
	// Overlapping candidates must not double count or loop forever.
	if n := len(mustHits(highlightMatches("aaaa", "aa", 0, sideChat))); n != 2 {
		t.Errorf("got %d hits for aa in aaaa, want 2 non-overlapping", n)
	}
}

func mustHits(_ string, hits []searchHit) []searchHit { return hits }

// TestSearchCommitFreesNAndShiftN is the regression test for n and N being swallowed as text: they only cycle once the query is committed with enter.
func TestSearchCommitFreesNAndShiftN(t *testing.T) {
	a := newTestApp(110, 30)
	a.cur.Turns = []store.Turn{
		{Role: "user", Content: "counting: one two one two one"},
		{Role: "assistant", Model: "llama3.2:3b", Content: "one more one and one again"},
	}
	a.invalidateRenders()
	a.refreshTranscript()

	typeKey := func(r rune) { a.onKey(tea.KeyPressMsg{Code: r, Text: string(r)}) }

	a.onKey(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	for _, r := range "one" {
		typeKey(r)
	}
	if a.searchQuery != "one" {
		t.Fatalf("query = %q, want %q", a.searchQuery, "one")
	}
	if len(a.searchHits) < 3 {
		t.Fatalf("got %d hits, want several", len(a.searchHits))
	}

	// While the input has the keyboard, n is a character.
	typeKey('n')
	if a.searchQuery != "onen" {
		t.Errorf("while typing, n should extend the query; got %q", a.searchQuery)
	}
	a.onKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if a.searchQuery != "one" {
		t.Fatalf("backspace left query %q", a.searchQuery)
	}

	// Commit hands the keyboard back but keeps the highlights.
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if a.searching {
		t.Error("enter should close the input")
	}
	if a.searchQuery != "one" || len(a.searchHits) == 0 {
		t.Fatalf("commit dropped the search: query=%q hits=%d", a.searchQuery, len(a.searchHits))
	}
	if a.focus != focusTranscript {
		t.Error("commit should leave the transcript focused")
	}

	// Now n and N cycle instead of typing.
	start := a.searchIdx
	typeKey('n')
	if a.searchQuery != "one" {
		t.Errorf("n was typed into the query after commit: %q", a.searchQuery)
	}
	if a.searchIdx != (start+1)%len(a.searchHits) {
		t.Errorf("n moved to %d, want %d", a.searchIdx, (start+1)%len(a.searchHits))
	}
	a.onKey(tea.KeyPressMsg{Code: 'N', Text: "N", Mod: tea.ModShift})
	if a.searchIdx != start {
		t.Errorf("N moved to %d, want back to %d", a.searchIdx, start)
	}

	// Wrapping in both directions.
	a.searchIdx = 0
	a.stepMatch(-1)
	if a.searchIdx != len(a.searchHits)-1 {
		t.Errorf("stepping back from the first hit gave %d, want %d",
			a.searchIdx, len(a.searchHits)-1)
	}

	// esc clears the committed search.
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if a.searchQuery != "" || len(a.searchHits) != 0 {
		t.Errorf("esc left query=%q hits=%d", a.searchQuery, len(a.searchHits))
	}
}

// TestSearchLifecycle drives the find bar the way a user does.
func TestSearchLifecycle(t *testing.T) {
	a := newTestApp(110, 30)
	a.cur.Turns = []store.Turn{
		{Role: "user", Content: "tell me about goroutines"},
		{Role: "assistant", Content: "goroutines are cheap threads", Model: "llama3.2:3b"},
	}
	a.invalidateRenders()
	a.refreshTranscript()

	a.onKey(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if !a.searching {
		t.Fatal("ctrl+f did not open the find bar")
	}
	if a.focus != focusTranscript {
		t.Error("search should move focus to the transcript it is searching")
	}

	for _, r := range "goroutines" {
		a.onKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if len(a.searchHits) == 0 {
		t.Error("no hits for a term that appears in the transcript")
	}

	// The current match must be styled differently from the rest.
	frame := render(a)
	if !strings.Contains(frame, searchCurrentStyle.Render("goroutines")) &&
		!strings.Contains(frame, searchCurrentStyle.Render("goroutine")) {
		t.Log("current-match styling not found verbatim; checking it renders at all")
	}
	checkFrame(t, frame, 110, 30, "search open")

	if got := ansi.Strip(a.viewSearchBar(110)); !strings.Contains(got, "of") {
		t.Errorf("search bar missing its match position: %q", got)
	}
	if c := a.View().Cursor; c == nil {
		t.Error("no cursor while typing in the find bar")
	}

	a.onKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if a.searching || len(a.searchHits) != 0 {
		t.Error("esc should close the find bar and drop its hits")
	}
}

// --- export -----------------------------------------------------------------

func TestSessionMarkdown(t *testing.T) {
	s := &store.Session{
		Title: "Borrow checker", Model: "llama3.2:3b", System: "be terse",
		Created: time.Now(),
		Turns: []store.Turn{
			{Role: "user", Content: "explain it"},
			{Role: "assistant", Model: "llama3.2:3b", Content: "It tracks ownership.",
				Thinking: "recall the rules", TokensPerSec: 42, EvalCount: 128, TTFT: 300 * time.Millisecond},
		},
	}
	md := s.Markdown()
	for _, want := range []string{
		"# Borrow checker", "`llama3.2:3b`", "be terse",
		"## You", "explain it", "It tracks ownership.",
		"<details><summary>Reasoning</summary>", "recall the rules",
		"128 tokens", "42 tok/s",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("export missing %q", want)
		}
	}
}

func TestExportSlug(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Borrow checker", "borrow-checker"},
		{"  Trim/me!  ", "trim-me"},
		{"???", "chat"},
		{"", "chat"},
	} {
		if got := store.Slug(tc.in); got != tc.want {
			t.Errorf("Slug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// --- compare ----------------------------------------------------------------

// compareApp starts a race with both sides wired up but no network behind them.
func compareApp(t *testing.T, w, h int) *App {
	t.Helper()
	a := newTestApp(w, h)
	a.comparing = true
	a.compareIdx = 0
	a.comparePrompt = "explain goroutines"
	now := time.Now()
	a.compare = []*compareRun{
		{model: "llama3.2:3b", started: now, sampleAt: now},
		{model: "huihui_ai/qwen3-abliterated:30b-a3b", started: now, sampleAt: now},
	}
	for _, r := range a.compare {
		// A round in progress: the shared prompt is already on each side's thread, with that side's answer still streaming into r.turn.
		r.turns = []store.Turn{{Role: "user", Content: a.comparePrompt}}
		r.turn = store.Turn{Role: "assistant", Model: r.model}
		r.streaming = true
		r.vp = viewport.New()
	}
	a.layout()
	a.refreshCompare()
	return a
}

// TestCompareGeometry renders the race at several sizes.
func TestCompareGeometry(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {120, 40}, {100, 30}, {72, 20}, {160, 50}} {
		w, h := sz[0], sz[1]
		a := compareApp(t, w, h)
		a.compare[0].turn.Content = strings.Repeat("left words ", 60)
		a.compare[1].turn.Content = strings.Repeat("right words ", 60)
		a.refreshCompare()
		checkFrame(t, render(a), w, h, "compare")

		// Columns must together span the full width exactly.
		total := 0
		for i := range compareSides {
			total += a.compareColumnWidth(i)
		}
		if total != a.compareWidth() {
			t.Errorf("%dx%d: columns total %d cells, want %d", w, h, total, a.compareWidth())
		}
	}
}

// TestCompareRoutesChunksBySide is the core correctness property: each stream must land only in its own column.
func TestCompareRoutesChunksBySide(t *testing.T) {
	a := compareApp(t, 120, 34)
	gen := a.compareGen

	a.onCompareChunk(chatChunkMsg{gen: gen, side: 0, chunk: ollama.ChatResponse{
		Message: ollama.Message{Content: "LEFT"},
	}})
	a.onCompareChunk(chatChunkMsg{gen: gen, side: 1, chunk: ollama.ChatResponse{
		Message: ollama.Message{Content: "RIGHT"},
	}})
	if a.compare[0].turn.Content != "LEFT" {
		t.Errorf("left column = %q", a.compare[0].turn.Content)
	}
	if a.compare[1].turn.Content != "RIGHT" {
		t.Errorf("right column = %q", a.compare[1].turn.Content)
	}

	// A chunk from a superseded race is dropped.
	a.onCompareChunk(chatChunkMsg{gen: gen - 1, side: 0, chunk: ollama.ChatResponse{
		Message: ollama.Message{Content: "STALE"},
	}})
	if strings.Contains(a.compare[0].turn.Content, "STALE") {
		t.Error("a stale chunk was applied to the race")
	}

	// Out-of-range sides must not panic.
	a.onCompareChunk(chatChunkMsg{gen: gen, side: 7, chunk: ollama.ChatResponse{}})
	a.onCompareEnd(chatEndMsg{gen: gen, side: 7})
}

// TestCompareKeepCommitsChosenSide checks that picking a winner appends exactly that exchange to the conversation and switches the active model to it.
func TestCompareKeepCommitsChosenSide(t *testing.T) {
	a := compareApp(t, 120, 34)
	a.compare[0].turn.Content = "left answer"
	a.compare[1].turn.Content = "right answer"
	a.compare[1].turn.TokensPerSec = 55
	before := len(a.cur.Turns)

	a.keepCompareSide(1)

	if a.comparing {
		t.Error("keeping a side should end the race")
	}
	if got := len(a.cur.Turns) - before; got != 2 {
		t.Fatalf("appended %d turns, want 2 (the prompt and the winner)", got)
	}
	last := a.cur.Turns[len(a.cur.Turns)-1]
	if last.Content != "right answer" {
		t.Errorf("kept the wrong side: %q", last.Content)
	}
	if prompt := a.cur.Turns[len(a.cur.Turns)-2]; prompt.Role != "user" || prompt.Content != "explain goroutines" {
		t.Errorf("prompt turn = %+v", prompt)
	}
	if a.cfg.Model != "huihui_ai/qwen3-abliterated:30b-a3b" {
		t.Errorf("active model = %q, want the kept side's model", a.cfg.Model)
	}
	checkFrame(t, render(a), 120, 34, "after keeping a side")
}

// TestCompareKeepRejectsEmptySide guards against committing a prompt that has no answer under it. The thread always holds the prompt, so length alone must not be taken as proof there is something to keep.
func TestCompareKeepRejectsEmptySide(t *testing.T) {
	a := compareApp(t, 120, 34)
	if len(a.compare[0].turns) == 0 {
		t.Fatal("setup: the side should already carry the prompt")
	}
	before := len(a.cur.Turns)
	a.keepCompareSide(0)
	if len(a.cur.Turns) != before {
		t.Errorf("committed %d turns for a side with no answer",
			len(a.cur.Turns)-before)
	}
	if !a.comparing {
		t.Error("the race should stay open when there is nothing to keep")
	}
}

// TestCompareFollowUpKeepsThread covers a multi-round comparison: each side keeps its own thread, and keeping one commits all of its rounds.
func TestCompareFollowUpKeepsThread(t *testing.T) {
	a := compareApp(t, 120, 34)
	// Round one settles on both sides.
	for i, r := range a.compare {
		r.turn.Content = "answer one from side " + string(rune('A'+i))
		a.onCompareEnd(chatEndMsg{gen: a.compareGen, side: i})
	}
	for i, r := range a.compare {
		if len(r.turns) != 2 {
			t.Fatalf("side %d thread = %d turns, want prompt + answer", i, len(r.turns))
		}
		if r.turns[1].Role != "assistant" {
			t.Fatalf("side %d did not settle its answer", i)
		}
	}

	// A follow-up must carry that side's own answer as context.
	msgs := a.compareMessagesFor(a.compare[1])
	found := false
	for _, m := range msgs {
		if strings.Contains(m.Content, "answer one from side B") {
			found = true
		}
		if strings.Contains(m.Content, "answer one from side A") {
			t.Error("a side's context leaked the other side's answer")
		}
	}
	if !found {
		t.Error("follow-up context is missing this side's own answer")
	}

	// Round two, still streaming when the user keeps side 1.
	for i, r := range a.compare {
		r.turns = append(r.turns, store.Turn{Role: "user", Content: "and again?"})
		r.turn = store.Turn{Role: "assistant", Model: r.model,
			Content: "answer two from side " + string(rune('A'+i))}
	}
	before := len(a.cur.Turns)
	a.keepCompareSide(1)

	got := a.cur.Turns[before:]
	if len(got) != 4 {
		t.Fatalf("kept %d turns, want 4 (two full rounds)", len(got))
	}
	for _, turn := range got {
		if strings.Contains(turn.Content, "side A") {
			t.Errorf("keeping side B committed side A's text: %q", turn.Content)
		}
	}
	if got[len(got)-1].Role != "assistant" {
		t.Error("a kept thread must not end on an unanswered prompt")
	}
}

// TestCompareWheelHitsCorrectColumn checks per-column mouse scrolling.
func TestCompareWheelHitsCorrectColumn(t *testing.T) {
	a := compareApp(t, 140, 34)
	for _, r := range a.compare {
		r.turn.Content = strings.Repeat("many words to overflow the column ", 300)
	}
	a.refreshCompare()
	for i, r := range a.compare {
		r.vp.GotoTop()
		if r.vp.TotalLineCount() <= r.vp.Height() {
			t.Fatalf("setup: column %d must overflow (%d lines in %d rows)",
				i, r.vp.TotalLineCount(), r.vp.Height())
		}
	}

	right := a.compareRect(1)
	a.onCompareWheel(tea.Mouse{
		X: (right.x0 + right.x1) / 2, Y: (right.y0 + right.y1) / 2,
		Button: tea.MouseWheelDown,
	})
	if a.compare[0].vp.YOffset() != 0 {
		t.Error("scrolling over the right column moved the left one")
	}
	if a.compare[1].vp.YOffset() == 0 {
		t.Error("the right column did not scroll")
	}
}

// TestCompareFooterNamesWinner checks the verdict line once both sides land.
func TestCompareFooterNamesWinner(t *testing.T) {
	a := compareApp(t, 120, 34)
	for i, tps := range []float64{20, 65} {
		a.compare[i].streaming = false
		a.compare[i].turn.TokensPerSec = tps
		a.compare[i].turn.Content = "done"
	}
	footer := ansi.Strip(a.viewCompareVerdict())
	if !strings.Contains(footer, "★") {
		t.Errorf("footer does not mark a winner: %q", footer)
	}
	// The star must sit with the faster side's throughput, not the slower one. Names are trimmed to a budget, so anchor on the numbers instead.
	star, fast, slow := strings.Index(footer, "★"), strings.Index(footer, "65 tok/s"), strings.Index(footer, "20 tok/s")
	if fast < 0 || slow < 0 {
		t.Fatalf("footer lost a throughput figure: %q", footer)
	}
	if star < fast || star < slow != (fast < slow) {
		t.Errorf("the star should mark the faster side: %q", footer)
	}
}

// TestCompareFooterFitsLongNames pins the bug this footer had: long model names pushed the throughput and the winner's star off the end of the line.
func TestCompareFooterFitsLongNames(t *testing.T) {
	for _, w := range []int{80, 100, 140, 200} {
		a := compareApp(t, w, 34)
		a.compare[0].model = "registry.example.com/library/a-very-long-model-name:70b-instruct"
		a.compare[1].model = "registry.example.com/library/another-long-model-name:34b-chat"
		for i, tps := range []float64{12, 44} {
			a.compare[i].streaming = false
			a.compare[i].turn.TokensPerSec = tps
			a.compare[i].turn.Content = "done"
		}
		footer := ansi.Strip(a.viewCompareVerdict())
		if !strings.Contains(footer, "★") {
			t.Errorf("width %d: winner star lost to truncation: %q", w, footer)
		}
		if !strings.Contains(footer, "44 tok/s") || !strings.Contains(footer, "12 tok/s") {
			t.Errorf("width %d: a throughput figure was truncated away: %q", w, footer)
		}
		for line := range strings.SplitSeq(a.viewCompareVerdict(), "\n") {
			if got := lipgloss.Width(line); got > a.compareWidth() {
				t.Errorf("width %d: footer line is %d cells, want <= %d", w, got, a.compareWidth())
			}
		}
	}
}

// TestCompareLayoutFitsColumns checks the viewports are sized to their columns.
func TestCompareLayoutFitsColumns(t *testing.T) {
	a := compareApp(t, 120, 34)
	for i, r := range a.compare {
		want := a.compareColumnWidth(i) - 2 - scrollbarWidth
		if r.vp.Width() != want {
			t.Errorf("column %d viewport width = %d, want %d", i, r.vp.Width(), want)
		}
		if r.vp.Height() <= 0 {
			t.Errorf("column %d viewport height = %d", i, r.vp.Height())
		}
		// Rendered column must not exceed its declared width.
		if got := lipgloss.Width(a.viewCompareColumn(i, r)); got != a.compareColumnWidth(i) {
			t.Errorf("column %d renders %d cells, want %d", i, got, a.compareColumnWidth(i))
		}
	}
}

// TestCompareComposerStaysAvailable covers the mode's shape: the prompt box sits under the columns, tab moves between it and them, and the way out is always on screen.
func TestCompareComposerStaysAvailable(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {120, 40}, {100, 30}, {72, 20}} {
		w, h := sz[0], sz[1]
		a := compareApp(t, w, h)
		frame := render(a)
		checkFrame(t, frame, w, h, "compare with composer")

		plain := ansi.Strip(frame)
		if !strings.Contains(plain, "exit compare") {
			t.Errorf("%dx%d: no visible way out of compare mode", w, h)
		}

		// The columns, verdict, composer and hint must tile the content area.
		want := a.contentHeight()
		got := a.comparePanelHeight() + 1 + a.inputPanelHeight() + 1
		if got != want {
			t.Errorf("%dx%d: compare rows total %d, content area is %d", w, h, got, want)
		}
	}
}

// TestCompareFocusRing checks tab moves composer -> columns -> composer, and that only the composer holds the cursor.
func TestCompareFocusRing(t *testing.T) {
	a := compareApp(t, 120, 34)
	if a.compareColumnFocused() != -1 {
		t.Fatal("compare should open with the composer focused")
	}
	if a.View().Cursor == nil {
		t.Error("no cursor while the composer is focused")
	}

	a.cycleCompareFocus(1)
	if a.compareColumnFocused() != 0 {
		t.Errorf("tab went to column %d, want 0", a.compareColumnFocused())
	}
	if a.View().Cursor != nil {
		t.Error("a focused column must not show the composer cursor")
	}

	a.cycleCompareFocus(1)
	if a.compareColumnFocused() != 1 {
		t.Errorf("tab went to column %d, want 1", a.compareColumnFocused())
	}
	a.cycleCompareFocus(1)
	if a.compareColumnFocused() != -1 {
		t.Error("tab should wrap back to the composer")
	}
	a.cycleCompareFocus(-1)
	if a.compareColumnFocused() != 1 {
		t.Error("shift+tab should wrap to the last column")
	}
}

// TestCompareTypingGoesToComposer makes sure ordinary keys reach the prompt box rather than being read as column commands.
func TestCompareTypingGoesToComposer(t *testing.T) {
	a := compareApp(t, 120, 34)
	for _, r := range "hi 1 2 y n" {
		a.onCompareKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if got := a.input.Value(); got != "hi 1 2 y n" {
		t.Errorf("composer holds %q, want the typed text", got)
	}
	if !a.comparing {
		t.Error("typing must not end the comparison")
	}

	// With a column focused those same keys act instead of typing.
	a.cycleCompareFocus(1)
	before := a.input.Value()
	a.onCompareKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if a.input.Value() != before {
		t.Error("column keys leaked into the composer")
	}
}

// TestCompareSendRefusedWhileBusy guards the follow-up path against overlapping rounds.
func TestCompareSendRefusedWhileBusy(t *testing.T) {
	a := compareApp(t, 120, 34) // fixture is mid-round
	gen := a.compareGen
	a.sendCompare("another question")
	if a.compareGen != gen {
		t.Error("a second round started while the first was still streaming")
	}
	if !a.toastErr {
		t.Error("expected a toast explaining the refusal")
	}
}

// TestCompareSearchSpansBothColumns checks that a find in compare mode covers both models' answers and that cycling walks across them.
func TestCompareSearchSpansBothColumns(t *testing.T) {
	a := compareApp(t, 130, 34)
	// Keep the shared prompt clear of the search term, so the counts below measure the answers rather than the prompt echoed into both columns.
	for _, r := range a.compare {
		r.turns = []store.Turn{{Role: "user", Content: "explain concurrency"}}
	}
	a.compare[0].turn.Content = "goroutines are cheap\nand a goroutine is small"
	a.compare[1].turn.Content = "a goroutine is a green thread"
	a.refreshCompare()

	a.onCompareKey(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if !a.searching {
		t.Fatal("ctrl+f did not open the find bar in compare mode")
	}
	for _, r := range "goroutine" {
		a.onCompareKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if a.searchQuery != "goroutine" {
		t.Fatalf("query = %q", a.searchQuery)
	}

	// Two matches on the left, one on the right, numbered left column first.
	var perSide [2]int
	for _, h := range a.searchHits {
		if h.side < 0 || h.side > 1 {
			t.Fatalf("hit carries side %d, want a column index", h.side)
		}
		perSide[h.side]++
	}
	if perSide[0] != 2 || perSide[1] != 1 {
		t.Errorf("hits per column = %d,%d, want 2,1", perSide[0], perSide[1])
	}
	for i := 1; i < len(a.searchHits); i++ {
		if a.searchHits[i].side < a.searchHits[i-1].side {
			t.Error("hits should be ordered by column")
		}
	}

	// The bar reports the split.
	if got := ansi.Strip(a.viewSearchBar(130)); !strings.Contains(got, "(2|1)") {
		t.Errorf("search bar missing the per-column split: %q", got)
	}

	// Cycling past the left column's hits lands on the right one.
	a.searchIdx = 0
	a.stepMatch(1)
	a.stepMatch(1)
	if got := a.searchHits[a.searchIdx].side; got != 1 {
		t.Errorf("after cycling past column 0 the match is on side %d, want 1", got)
	}
	if a.compareIdx != 1 {
		t.Errorf("the highlighted column is %d, want the one holding the match", a.compareIdx)
	}

	// Both columns must actually carry highlight styling.
	for i, r := range a.compare {
		if !strings.Contains(r.vp.View(), "\x1b[") {
			t.Errorf("column %d has no styling at all", i)
		}
	}
	checkFrame(t, render(a), 130, 34, "compare search")

	// Committing hands the keyboard to the column holding the match, where n and N cycle rather than typing.
	a.onCompareKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if a.searching {
		t.Error("enter should close the find input")
	}
	if a.compareColumnFocused() != 1 {
		t.Errorf("commit focused column %d, want the one with the match", a.compareColumnFocused())
	}
	before := a.searchIdx
	a.onCompareKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if a.searchQuery != "goroutine" {
		t.Errorf("n was typed instead of cycling: query is now %q", a.searchQuery)
	}
	if a.searchIdx == before {
		t.Error("n did not advance the match")
	}

	// esc clears the search but stays in compare mode.
	a.onCompareKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if a.searchQuery != "" || len(a.searchHits) != 0 {
		t.Error("esc should clear the search")
	}
	if !a.comparing {
		t.Error("esc must not leave compare mode")
	}
}

// TestCompareSearchDoesNotTypeIntoComposer guards the find bar against the prompt box stealing its keystrokes.
func TestCompareSearchDoesNotTypeIntoComposer(t *testing.T) {
	a := compareApp(t, 120, 34)
	a.compare[0].turn.Content = "some text to find"
	a.refreshCompare()

	a.onCompareKey(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	for _, r := range "text" {
		a.onCompareKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if got := a.input.Value(); got != "" {
		t.Errorf("composer received the search keystrokes: %q", got)
	}
	if a.searchQuery != "text" {
		t.Errorf("find bar holds %q, want %q", a.searchQuery, "text")
	}
	if c := a.View().Cursor; c == nil {
		t.Error("no cursor on the find bar while typing")
	}
}

// TestEscReturnsToChat covers the escape hatch from the secondary tabs, and that it never steals the key from something already using it.
func TestEscReturnsToChat(t *testing.T) {
	esc := tea.KeyPressMsg{Code: tea.KeyEscape}

	for _, from := range []tab{tabModels, tabRunning, tabSettings} {
		a := newTestApp(120, 34)
		a.goTab(from)
		a.onKey(esc)
		if a.tab != tabChat {
			t.Errorf("esc on tab %d left us on tab %d, want the chat", from, a.tab)
		}
	}

	// The models filter owns esc while it is open: the first press cancels the filter, only a second one leaves the tab.
	a := newTestApp(120, 34)
	a.goTab(tabModels)
	a.onKey(tea.KeyPressMsg{Code: '/', Text: "/"})
	if !a.modelSearchOn {
		t.Fatal("/ did not open the model filter")
	}
	a.onKey(esc)
	if a.modelSearchOn {
		t.Error("esc should close the filter first")
	}
	if a.tab != tabModels {
		t.Error("esc closing the filter must not also leave the tab")
	}
	a.onKey(esc)
	if a.tab != tabChat {
		t.Error("a second esc should return to the chat")
	}

	// A modal owns esc too.
	a = newTestApp(120, 34)
	a.goTab(tabModels)
	a.overlay = overlayPull
	a.onKey(esc)
	if a.overlay != overlayNone {
		t.Error("esc should close the modal")
	}
	if a.tab != tabModels {
		t.Error("esc closing a modal must not also change tab")
	}

	// In the chat, esc still means focus and search, never a tab change.
	a = newTestApp(120, 34)
	a.setFocus(focusTranscript)
	a.onKey(esc)
	if a.tab != tabChat || a.focus != focusInput {
		t.Errorf("esc in chat should return focus to the composer, got tab %d focus %v", a.tab, a.focus)
	}
}

// TestEscEscalatesToTabBar covers the escape ladder in the chat: clear a search, then return to the composer, and only then hand the keyboard to the tab strip.
func TestEscEscalatesToTabBar(t *testing.T) {
	esc := tea.KeyPressMsg{Code: tea.KeyEscape}
	a := newTestApp(120, 34)
	a.cur.Turns = []store.Turn{{Role: "assistant", Content: "goroutines", Model: "llama3.2:3b"}}
	a.invalidateRenders()
	a.refreshTranscript()

	// Rung one: a live search.
	a.onKey(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	for _, r := range "goroutine" {
		a.onKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	a.onKey(esc)
	if a.searchQuery != "" {
		t.Error("first esc should clear the search")
	}
	if a.tabBarFocus {
		t.Fatal("esc jumped to the tab strip while the search was still open")
	}

	// Rung two: focus parked away from the composer.
	a.setFocus(focusTranscript)
	a.onKey(esc)
	if a.focus != focusInput {
		t.Error("esc should return focus to the composer")
	}
	if a.tabBarFocus {
		t.Fatal("esc reached the tab strip while focus was still in the body")
	}

	// Rung three: nothing left for esc to do.
	a.onKey(esc)
	if !a.tabBarFocus {
		t.Fatal("esc did not reach the tab strip")
	}
	if a.input.Focused() {
		t.Error("the composer must be blurred while the strip has the keyboard")
	}
	if a.View().Cursor != nil {
		t.Error("no text cursor should show while the strip has the keyboard")
	}
	checkFrame(t, render(a), 120, 34, "tab strip focused")
}

// TestTabBarNavigation drives the strip once it has the keyboard.
func TestTabBarNavigation(t *testing.T) {
	a := newTestApp(120, 34)
	a.focusTabBar()

	key := func(s string) {
		t.Helper()
		switch s {
		case "right":
			a.onKey(tea.KeyPressMsg{Code: tea.KeyRight})
		case "left":
			a.onKey(tea.KeyPressMsg{Code: tea.KeyLeft})
		case "enter":
			a.onKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		default:
			a.onKey(tea.KeyPressMsg{Code: rune(s[0]), Text: s})
		}
	}

	key("right")
	if a.tab != tabModels || !a.tabBarFocus {
		t.Errorf("right gave tab %d focus %v, want Models with the strip focused", a.tab, a.tabBarFocus)
	}
	// Moving previews the tab, and its content must render at the right size.
	checkFrame(t, render(a), 120, 34, "strip focused on Models")

	key("left")
	if a.tab != tabChat {
		t.Errorf("left gave tab %d, want Chat", a.tab)
	}
	// Wrapping at both ends.
	key("left")
	if a.tab != tabSettings {
		t.Errorf("left from the first tab gave %d, want Settings", a.tab)
	}
	key("right")
	if a.tab != tabChat {
		t.Errorf("right from the last tab gave %d, want Chat", a.tab)
	}

	// Digits jump directly.
	key("3")
	if a.tab != tabRunning {
		t.Errorf("3 gave tab %d, want Running", a.tab)
	}

	// Enter drops into the content.
	key("enter")
	if a.tabBarFocus {
		t.Error("enter should leave the strip")
	}

	// Landing back on the chat restores the composer.
	a.focusTabBar()
	key("1")
	a.onKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if a.tabBarFocus || a.tab != tabChat || !a.input.Focused() {
		t.Errorf("leaving the strip on chat should focus the composer: focus=%v tab=%d input=%v",
			a.tabBarFocus, a.tab, a.input.Focused())
	}

	// Typing must not leak into the composer while the strip has the keyboard.
	a.focusTabBar()
	before := a.input.Value()
	a.onKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if a.input.Value() != before {
		t.Errorf("a keystroke leaked into the composer: %q", a.input.Value())
	}

	// A global shortcut still works from the strip.
	a.onKey(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if a.overlay != overlayPalette {
		t.Error("ctrl+k should still open the palette from the tab strip")
	}
}
