package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/config"
	"github.com/balintb/llamago/internal/ollama"
	"github.com/balintb/llamago/internal/store"
)

// newTestApp builds an app with representative data and no network access.
func newTestApp(w, h int) *App {
	cfg := config.Default()
	cfg.Model = "llama3.2:3b"
	cfg.System = "You are terse."

	a := New(cfg)
	a.models = []ollama.Model{
		{
			Name: "llama3.2:3b", Size: 2019393189, Digest: "sha256:abcdef0123456789",
			ModifiedAt: time.Now().Add(-72 * time.Hour),
			Details:    ollama.Details{Family: "llama", ParameterSize: "3.2B", QuantizationLevel: "Q4_K_M", Format: "gguf"},
		},
		{
			Name: "huihui_ai/qwen3-abliterated:30b-a3b", Size: 18556699290, Digest: "sha256:45838c4ad568",
			ModifiedAt: time.Now().Add(-time.Hour),
			Details:    ollama.Details{Family: "qwen3moe", ParameterSize: "30.5B", QuantizationLevel: "Q4_K_M", Format: "gguf"},
		},
	}
	a.running = []ollama.RunningModel{{
		Name: "llama3.2:3b", Size: 3000000000, SizeVRAM: 2400000000,
		ExpiresAt: time.Now().Add(4*time.Minute + 30*time.Second),
		Details:   ollama.Details{Family: "llama", ParameterSize: "3.2B", QuantizationLevel: "Q4_K_M"},
	}}
	a.details["llama3.2:3b"] = &ollama.ShowResponse{
		Details:      a.models[0].Details,
		Capabilities: []string{"completion", "tools", "thinking"},
		System:       "You are a helpful assistant with a long system prompt that should be clipped rather than allowed to blow out the detail pane layout.",
		ModelInfo:    map[string]any{"llama.context_length": float64(131072), "general.parameter_count": float64(3212749888)},
	}
	a.sessions = []*store.Session{
		{ID: "1", Title: "Explain the borrow checker", Model: "llama3.2:3b", Updated: time.Now()},
		{ID: "2", Title: "A session title that is quite long and must be truncated", Model: "llama3.2:3b", Updated: time.Now()},
	}
	a.cur.Turns = []store.Turn{
		{Role: "user", Content: "Write a haiku about Go and explain it.", At: time.Now()},
		{
			Role: "assistant", Model: "llama3.2:3b", At: time.Now(),
			Thinking:     "The user wants a haiku. Let me count syllables carefully.",
			Content:      "# Haiku\n\nGoroutines flow free\n`select` waits on many streams\nDeadlock sleeps alone\n\n- 5/7/5 syllables\n- Uses **Go** idioms\n\n```go\nfunc main() { go work() }\n```",
			TokensPerSec: 42.7, EvalCount: 128, PromptCount: 19, TTFT: 320 * time.Millisecond,
		},
	}

	a.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return a
}

// render drives View() and returns the frame's content.
func render(a *App) string { return a.View().Content }

// checkFrame asserts the two invariants every frame must satisfy: it fills the terminal exactly, and no line overflows the width (which would wrap and shove the whole layout down a row).
func checkFrame(t *testing.T, frame string, w, h int, label string) {
	t.Helper()
	lines := strings.Split(frame, "\n")
	if len(lines) != h {
		t.Errorf("%s: got %d lines, want %d", label, len(lines), h)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got > w {
			t.Errorf("%s: line %d is %d cells wide, want <= %d\n  %q",
				label, i, got, w, ansi.Strip(line))
		}
	}
}

// TestFrameGeometry renders every tab at a spread of terminal sizes.
func TestFrameGeometry(t *testing.T) {
	sizes := [][2]int{
		{80, 24},  // classic
		{120, 40}, // roomy
		{100, 30}, // typical
		{72, 20},  // narrow: sidebar should collapse
		{200, 60}, // very wide
		{51, 15},  // just above the minimum
	}
	tabs := []struct {
		tab  tab
		name string
	}{
		{tabChat, "chat"},
		{tabModels, "models"},
		{tabRunning, "running"},
		{tabSettings, "settings"},
	}

	for _, sz := range sizes {
		w, h := sz[0], sz[1]
		for _, tc := range tabs {
			a := newTestApp(w, h)
			a.goTab(tc.tab)
			checkFrame(t, render(a), w, h, fmt.Sprintf("%s @ %dx%d", tc.name, w, h))
		}
	}
}

// TestOverlayGeometry checks that every modal composites without disturbing the frame's size.
func TestOverlayGeometry(t *testing.T) {
	overlays := []struct {
		name  string
		setup func(a *App)
	}{
		{"help", func(a *App) { a.overlay = overlayHelp }},
		{"palette", func(a *App) { a.openPalette() }},
		{"pull", func(a *App) { a.overlay = overlayPull }},
		{"system", func(a *App) { a.overlay = overlaySystem }},
		{"confirm", func(a *App) {
			a.overlay = overlayConfirm
			a.confirm = confirmState{prompt: "Delete llama3.2:3b (2.0 GB)?"}
		}},
	}
	for _, sz := range [][2]int{{80, 24}, {120, 40}, {60, 18}, {118, 34}} {
		w, h := sz[0], sz[1]
		for _, o := range overlays {
			a := newTestApp(w, h)
			o.setup(a)
			label := fmt.Sprintf("overlay %s @ %dx%d", o.name, w, h)
			frame := render(a)
			checkFrame(t, frame, w, h, label)

			// A modal must sit on top of the app, not replace it. The header row and the status row lie outside every modal, so both must survive.
			lines := strings.Split(ansi.Strip(frame), "\n")
			if !strings.Contains(lines[0], "llamago") {
				t.Errorf("%s: header row lost - overlay replaced the frame instead of compositing\n  row 0: %q",
					label, lines[0])
			}
			if last := lines[len(lines)-1]; !strings.Contains(last, "ctrl+") {
				t.Errorf("%s: status row covered by the overlay\n  last row: %q", label, last)
			}
		}
	}
}

// TestPaletteHeightIsStable pins the command palette to one size. Entries near the bottom carry long hints (a session's model name), and if a row overflows the modal it wraps and the box grows as you scroll, which makes the palette jump under the cursor.
func TestPaletteHeightIsStable(t *testing.T) {
	a := newTestApp(118, 34)
	// Long, realistic names in both the title and the hint columns.
	a.models = append(a.models, ollama.Model{
		Name: "registry.example.com/library/some-very-long-model-name:70b-instruct-q5_K_M",
		Size: 40 << 30, Details: ollama.Details{Family: "llama"},
	})
	a.sessions = []*store.Session{
		{ID: "1", Title: "A session with a genuinely long title that keeps going", Model: "huihui_ai/qwen3-abliterated:30b-a3b"},
		{ID: "2", Title: "Another", Model: "registry.example.com/library/some-very-long-model-name:70b-instruct-q5_K_M"},
	}
	a.openPalette()

	n := len(a.filteredCommands())
	if n < 6 {
		t.Fatalf("need a scrollable palette, got %d commands", n)
	}

	_, want := lipgloss.Size(a.viewPalette())
	for i := range n {
		a.paletteIdx = i
		box := a.viewPalette()
		w, h := lipgloss.Size(box)
		if h != want {
			t.Errorf("palette height changed at index %d (%q): %d, want %d",
				i, a.filteredCommands()[i].title, h, want)
		}
		if w > modalWidth(a) {
			t.Errorf("palette width at index %d is %d, want <= %d", i, w, modalWidth(a))
		}
	}

	// Filtering down to a handful of matches must not resize it either.
	for _, q := range []string{"chat", "use", "open", "zzz"} {
		a.paletteIn.SetValue(q)
		a.paletteIdx = 0
		if _, h := lipgloss.Size(a.viewPalette()); h != want {
			t.Errorf("palette height changed for query %q: %d, want %d", q, h, want)
		}
	}
}

// modalWidth is the palette's declared total width, for the overflow check.
func modalWidth(a *App) int { return max(40, min(72, a.width-8)) }

// longChat fills the current session with enough turns to overflow the pane.
func longChat(a *App, turns int) {
	a.cur.Turns = nil
	for i := range turns {
		a.cur.Turns = append(a.cur.Turns,
			store.Turn{Role: "user", Content: fmt.Sprintf("question number %d", i)},
			store.Turn{Role: "assistant", Model: "llama3.2:3b",
				Content: fmt.Sprintf("answer number %d, which runs on for a while", i)},
		)
	}
	a.invalidateRenders()
	a.refreshTranscript()
}

// TestScrollbarAppearsOnlyWhenScrollable checks the gutter: blank when everything fits, a track plus thumb when it doesn't.
func TestScrollbarAppearsOnlyWhenScrollable(t *testing.T) {
	a := newTestApp(118, 34)

	// A single short turn fits, so the gutter stays empty.
	a.cur.Turns = []store.Turn{{Role: "user", Content: "hi"}}
	a.invalidateRenders()
	a.refreshTranscript()
	if got := ansi.Strip(a.viewChat()); strings.Contains(got, "█") {
		t.Error("scrollbar thumb drawn while the content fits")
	}

	longChat(a, 40)
	if a.transcript.TotalLineCount() <= a.transcript.Height() {
		t.Fatalf("setup: content should overflow (%d lines in %d rows)",
			a.transcript.TotalLineCount(), a.transcript.Height())
	}
	if got := ansi.Strip(a.viewChat()); !strings.Contains(got, "█") {
		t.Error("no scrollbar thumb despite overflowing content")
	}
	checkFrame(t, render(a), 118, 34, "chat with scrollbar")
}

// TestScrollbarTracksOffset checks that the thumb moves from top to bottom as the viewport scrolls, and is sized by the visible share of the content.
func TestScrollbarTracksOffset(t *testing.T) {
	const height, total = 10, 50
	top := scrollbarColumn(height, total, 0, true)
	bottom := scrollbarColumn(height, total, total-height, true)

	thumbRows := func(col []string) (first, last, n int) {
		first = -1
		for i, c := range col {
			if strings.Contains(c, "█") {
				if first < 0 {
					first = i
				}
				last = i
				n++
			}
		}
		return
	}

	tf, _, tn := thumbRows(top)
	bf, bl, bn := thumbRows(bottom)
	if tf != 0 {
		t.Errorf("at offset 0 the thumb starts at row %d, want 0", tf)
	}
	if bl != height-1 {
		t.Errorf("at max offset the thumb ends at row %d, want %d", bl, height-1)
	}
	if bf <= tf {
		t.Errorf("thumb did not move down: top starts %d, bottom starts %d", tf, bf)
	}
	// 10 of 50 lines visible: roughly a fifth of the track.
	if tn != bn || tn != 2 {
		t.Errorf("thumb size = %d/%d rows, want a stable 2", tn, bn)
	}

	// Every row is exactly one cell, or the gutter would break the layout.
	for i, c := range top {
		if lipgloss.Width(c) != 1 {
			t.Errorf("scrollbar row %d is %d cells wide, want 1", i, lipgloss.Width(c))
		}
	}
}

// TestMouseWheelScrollsTranscript checks wheel handling and its hit-testing: only the transcript pane responds, and only on the chat tab.
func TestMouseWheelScrollsTranscript(t *testing.T) {
	a := newTestApp(118, 34)
	longChat(a, 40)
	a.transcript.GotoBottom()

	wheel := func(x, y int, btn tea.MouseButton) {
		t.Helper()
		a.Update(tea.MouseWheelMsg{X: x, Y: y, Button: btn})
	}

	r := a.transcriptRect()
	midX, midY := (r.x0+r.x1)/2, (r.y0+r.y1)/2

	atBottom := a.transcript.YOffset()
	wheel(midX, midY, tea.MouseWheelUp)
	up := a.transcript.YOffset()
	if up >= atBottom {
		t.Fatalf("wheel up did not scroll: offset %d -> %d", atBottom, up)
	}
	if up != atBottom-wheelStep {
		t.Errorf("wheel up moved %d lines, want %d", atBottom-up, wheelStep)
	}
	if a.pinBottom {
		t.Error("scrolling up should detach the view from the bottom")
	}

	wheel(midX, midY, tea.MouseWheelDown)
	if got := a.transcript.YOffset(); got != up+wheelStep {
		t.Errorf("wheel down moved to %d, want %d", got, up+wheelStep)
	}
	if !a.pinBottom {
		t.Error("scrolling back to the bottom should re-attach the view")
	}

	// Outside the pane: the composer below, and the header above.
	before := a.transcript.YOffset()
	wheel(midX, r.y1+1, tea.MouseWheelUp)
	wheel(midX, r.y0-1, tea.MouseWheelUp)
	wheel(r.x0-1, midY, tea.MouseWheelUp)
	if got := a.transcript.YOffset(); got != before {
		t.Errorf("wheel outside the pane scrolled it: %d -> %d", before, got)
	}

	// Another tab, and behind a modal, must both ignore the wheel.
	a.goTab(tabModels)
	wheel(midX, midY, tea.MouseWheelUp)
	if got := a.transcript.YOffset(); got != before {
		t.Errorf("wheel scrolled the transcript from another tab: %d -> %d", before, got)
	}
	a.goTab(tabChat)
	a.overlay = overlayHelp
	wheel(midX, midY, tea.MouseWheelUp)
	if got := a.transcript.YOffset(); got != before {
		t.Errorf("wheel scrolled the transcript behind a modal: %d -> %d", before, got)
	}
}

// TestWheelHitTestFollowsSidebar makes sure the pane rectangle tracks the sidebar, so the wheel doesn't act on the session list's columns.
func TestWheelHitTestFollowsSidebar(t *testing.T) {
	wide := newTestApp(120, 30)
	if r := wide.transcriptRect(); r.x0 != sidebarWidth {
		t.Errorf("with a sidebar the pane starts at x=%d, want %d", r.x0, sidebarWidth)
	}
	narrow := newTestApp(70, 30)
	if r := narrow.transcriptRect(); r.x0 != 0 {
		t.Errorf("without a sidebar the pane starts at x=%d, want 0", r.x0)
	}

	// The rectangle must stop where the composer begins.
	a := newTestApp(120, 30)
	inputTop := headerHeight + a.transcriptPanelHeight()
	if r := a.transcriptRect(); r.y1 != inputTop {
		t.Errorf("pane bottom = %d, want %d (the composer's top border)", r.y1, inputTop)
	}
}

// TestTabCyclesFocus covers the focus ring: tab forward, shift+tab back, esc straight to the composer.
func TestTabCyclesFocus(t *testing.T) {
	key := func(a *App, s string) {
		t.Helper()
		var k tea.KeyPressMsg
		switch s {
		case "tab":
			k = tea.KeyPressMsg{Code: tea.KeyTab}
		case "shift+tab":
			k = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
		case "esc":
			k = tea.KeyPressMsg{Code: tea.KeyEscape}
		}
		a.onKey(k)
	}

	a := newTestApp(120, 30) // wide: sidebar visible, so all three panes
	if a.focus != focusInput {
		t.Fatalf("initial focus = %v, want composer", a.focus)
	}

	want := []focus{focusTranscript, focusSessions, focusInput}
	for i, w := range want {
		key(a, "tab")
		if a.focus != w {
			t.Fatalf("tab #%d: focus = %v, want %v", i+1, a.focus, w)
		}
	}
	// Only the composer may hold the text cursor.
	if !a.input.Focused() {
		t.Error("composer should be focused after wrapping around")
	}

	key(a, "shift+tab")
	if a.focus != focusSessions {
		t.Errorf("shift+tab: focus = %v, want sessions", a.focus)
	}
	if a.input.Focused() {
		t.Error("composer must be blurred while the sidebar has focus")
	}

	key(a, "esc")
	if a.focus != focusInput || !a.input.Focused() {
		t.Errorf("esc: focus = %v (input focused=%v), want composer", a.focus, a.input.Focused())
	}

	// Tab must no longer reach the composer as literal input.
	before := a.input.Value()
	key(a, "tab")
	if a.input.Value() != before {
		t.Errorf("tab inserted text into the composer: %q", a.input.Value())
	}

	// With the sidebar collapsed the ring is two panes and never lands on it.
	narrow := newTestApp(70, 30)
	for range 6 {
		key(narrow, "tab")
		if narrow.focus == focusSessions {
			t.Fatal("focus landed on the hidden session list")
		}
	}
}

// TestSidebarHidingReleasesFocus covers a resize that removes the pane holding focus.
func TestSidebarHidingReleasesFocus(t *testing.T) {
	a := newTestApp(120, 30)
	a.setFocus(focusSessions)
	if a.focus != focusSessions {
		t.Fatal("setup: sessions should have focus")
	}

	a.Update(tea.WindowSizeMsg{Width: 70, Height: 30})
	if a.focus == focusSessions {
		t.Error("focus stayed on the session list after it was hidden")
	}
	if !a.input.Focused() {
		t.Error("composer should regain focus when the sidebar disappears")
	}
	checkFrame(t, render(a), 70, 30, "after sidebar collapse")
}

// TestSidebarCollapses verifies the session list yields when space is tight, and that the composer cursor tracks it.
func TestSidebarCollapses(t *testing.T) {
	wide := newTestApp(120, 30)
	if !wide.sidebarVisible() {
		t.Error("sidebar should show at 120 columns")
	}
	if x, _ := wide.inputOrigin(); x != sidebarWidth+1 {
		t.Errorf("input origin x = %d, want %d", x, sidebarWidth+1)
	}

	narrow := newTestApp(70, 30)
	if narrow.sidebarVisible() {
		t.Error("sidebar should collapse at 70 columns")
	}
	if x, _ := narrow.inputOrigin(); x != 1 {
		t.Errorf("collapsed input origin x = %d, want 1", x)
	}
}

// TestCursorInsideFrame guards against placing the terminal cursor outside the visible area, which some terminals render at a wildly wrong spot.
func TestCursorInsideFrame(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {120, 40}, {60, 16}} {
		w, h := sz[0], sz[1]
		a := newTestApp(w, h)
		v := a.View()
		if v.Cursor == nil {
			t.Fatalf("%dx%d: expected a cursor while the composer is focused", w, h)
		}
		if v.Cursor.X < 0 || v.Cursor.X >= w || v.Cursor.Y < 0 || v.Cursor.Y >= h {
			t.Errorf("%dx%d: cursor at (%d,%d) is outside the frame", w, h, v.Cursor.X, v.Cursor.Y)
		}
	}
}

// TestOverlayHidesCursor checks that a modal takes the cursor away from the composer underneath it.
func TestOverlayHidesCursor(t *testing.T) {
	a := newTestApp(100, 30)
	a.overlay = overlayHelp
	if v := a.View(); v.Cursor != nil {
		t.Error("help overlay should not expose the composer cursor")
	}
}

// TestComposerGrows verifies that a multi-line prompt steals height from the transcript rather than overflowing the frame.
func TestComposerGrows(t *testing.T) {
	a := newTestApp(100, 30)
	before := a.transcriptPanelHeight()

	a.input.SetValue("one\ntwo\nthree\nfour")
	a.layout()

	if a.inputHeight() != 4 {
		t.Errorf("composer height = %d, want 4", a.inputHeight())
	}
	if after := a.transcriptPanelHeight(); after >= before {
		t.Errorf("transcript should shrink: %d -> %d", before, after)
	}
	checkFrame(t, render(a), 100, 30, "grown composer")
}

// TestComposerWrapsLongLine covers the core of soft wrapping: one long line with no newline in it must still grow the composer, and the wrap has to happen inside the text column, not across the scrollbar gutter.
func TestComposerWrapsLongLine(t *testing.T) {
	a := newTestApp(100, 30)
	textWidth := a.input.Width()
	if want := a.bodyWidth() - 2 - scrollbarWidth; textWidth != want {
		t.Fatalf("composer text width = %d, want %d (interior less the gutter)", textWidth, want)
	}

	beforeH := a.inputHeight()
	beforeTranscript := a.transcriptPanelHeight()

	// Two and a bit rows of text, as a single logical line.
	a.input.SetValue(strings.Repeat("wrap ", textWidth/2))
	a.layout()

	if a.input.LineCount() != 1 {
		t.Fatalf("setup: should still be one logical line, got %d", a.input.LineCount())
	}
	if a.inputHeight() <= beforeH {
		t.Errorf("composer did not grow for wrapped text: %d -> %d", beforeH, a.inputHeight())
	}
	if a.transcriptPanelHeight() >= beforeTranscript {
		t.Errorf("transcript did not yield rows to the wrapped composer: %d -> %d",
			beforeTranscript, a.transcriptPanelHeight())
	}

	// No rendered composer row may exceed the text column: anything wider means the wrap width ignored the gutter and the text runs under the scrollbar.
	for i, line := range strings.Split(ansi.Strip(a.input.View()), "\n") {
		if w := lipgloss.Width(line); w > textWidth {
			t.Errorf("composer row %d is %d cells, want <= %d: %q", i, w, textWidth, line)
		}
	}
	checkFrame(t, render(a), 100, 30, "wrapped composer")
}

// TestComposerCapsAtFourLinesAndScrolls checks the cap: the box stops growing, keeps accepting input, and scrolls instead.
func TestComposerCapsAtFourLinesAndScrolls(t *testing.T) {
	a := newTestApp(100, 30)

	for n := 1; n <= 10; n++ {
		a.input.SetValue(strings.TrimSuffix(strings.Repeat("line\n", n), "\n"))
		a.layout()
		if got, want := a.inputHeight(), min(n, maxComposerLines); got != want {
			t.Errorf("%d lines: composer height = %d, want %d", n, got, want)
		}
		checkFrame(t, render(a), 100, 30, fmt.Sprintf("composer with %d lines", n))
	}

	// Text past the cap must be kept, not refused.
	a.input.SetValue(strings.TrimSuffix(strings.Repeat("line\n", 10), "\n"))
	a.layout()
	if a.input.LineCount() != 10 {
		t.Errorf("composer dropped input past the cap: %d logical lines, want 10", a.input.LineCount())
	}

	// And it must scroll: move the cursor to the end and check the offset moved.
	a.input.MoveToEnd()
	a.layout()
	if a.input.ScrollYOffset() == 0 {
		t.Error("composer did not scroll to follow the cursor past the cap")
	}
	if !strings.Contains(ansi.Strip(a.composerView()), "█") {
		t.Error("no composer scrollbar thumb despite scrolled content")
	}
}

// TestComposerScrollbarOnlyWhenScrolling keeps the gutter blank while the text fits, including wrapped text that grew the box but still fits inside the cap.
func TestComposerScrollbarOnlyWhenScrolling(t *testing.T) {
	a := newTestApp(100, 30)
	hasBar := func() bool {
		got := ansi.Strip(a.composerView())
		return strings.Contains(got, "█") || strings.Contains(got, "┊")
	}

	for _, tc := range []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"one short line", "short"},
		{"two logical lines", "one\ntwo"},
		{"wrapped under the cap", strings.Repeat("wrap ", a.input.Width()/4)},
	} {
		a.input.SetValue(tc.value)
		a.layout()
		if a.inputHeight() >= maxComposerLines {
			t.Fatalf("%s: setup should stay under the cap, got height %d", tc.name, a.inputHeight())
		}
		if hasBar() {
			t.Errorf("%s: scrollbar drawn for content that fits (height %d)", tc.name, a.inputHeight())
		}
	}

	// Past the cap it must appear.
	a.input.SetValue(strings.TrimSuffix(strings.Repeat("line\n", 12), "\n"))
	a.layout()
	if !hasBar() {
		t.Error("no scrollbar once the composer is capped and holding more text")
	}
}

// TestComposerCursorStaysInsidePane guards the cursor against the composer's internal scrolling: it must stay within the pane no matter how much is typed.
func TestComposerCursorStaysInsidePane(t *testing.T) {
	a := newTestApp(100, 30)
	for _, v := range []string{
		"one",
		strings.Repeat("wrap ", 60),
		strings.TrimSuffix(strings.Repeat("line\n", 12), "\n"),
	} {
		a.input.SetValue(v)
		a.input.MoveToEnd()
		a.layout()

		c := a.View().Cursor
		if c == nil {
			t.Fatalf("no cursor for %.20q", v)
		}
		x0, y0 := a.inputOrigin()
		x1 := x0 + a.input.Width()
		y1 := y0 + a.inputHeight()
		if c.X < x0 || c.X >= x1 || c.Y < y0 || c.Y >= y1 {
			t.Errorf("cursor (%d,%d) outside the composer [%d,%d)x[%d,%d) for %.20q",
				c.X, c.Y, x0, x1, y0, y1, v)
		}
	}
}

// TestComposerResetReclaimsRows checks that sending a long prompt hands the rows back to the transcript.
func TestComposerResetReclaimsRows(t *testing.T) {
	a := newTestApp(100, 30)
	tall := a.transcriptPanelHeight()

	a.input.SetValue(strings.TrimSuffix(strings.Repeat("line\n", 6), "\n"))
	a.layout()
	if a.transcriptPanelHeight() >= tall {
		t.Fatal("setup: transcript should have shrunk")
	}

	a.cfg.Model = "llama3.2:3b"
	a.send()
	if got := a.inputHeight(); got != 1 {
		t.Errorf("composer height after send = %d, want 1", got)
	}
	if got := a.transcriptPanelHeight(); got != tall {
		t.Errorf("transcript height after send = %d, want %d", got, tall)
	}
	checkFrame(t, render(a), 100, 30, "after send")
}

// TestStaleChunksIgnored ensures a chunk from a superseded generation cannot write into the current turn.
func TestStaleChunksIgnored(t *testing.T) {
	a := newTestApp(100, 30)
	a.streaming = true
	a.gen = 5
	a.cur.Turns = append(a.cur.Turns, store.Turn{Role: "assistant"})

	a.onChatChunk(chatChunkMsg{gen: 4, chunk: ollama.ChatResponse{
		Message: ollama.Message{Content: "stale"},
	}})
	if got := a.lastTurn().Content; got != "" {
		t.Errorf("stale chunk was applied: %q", got)
	}

	a.onChatChunk(chatChunkMsg{gen: 5, chunk: ollama.ChatResponse{
		Message: ollama.Message{Content: "fresh"},
	}})
	if got := a.lastTurn().Content; got != "fresh" {
		t.Errorf("current chunk not applied: %q", got)
	}
}

// TestStopDropsEmptyTurn checks that stopping before any token arrives doesn't leave a blank assistant bubble behind.
func TestStopDropsEmptyTurn(t *testing.T) {
	a := newTestApp(100, 30)
	n := len(a.cur.Turns)
	a.streaming = true
	a.chatFeed = newFeed[ollama.ChatResponse](func() {})
	a.cur.Turns = append(a.cur.Turns, store.Turn{Role: "assistant"})

	a.stopGeneration()
	if len(a.cur.Turns) != n {
		t.Errorf("empty assistant turn kept: %d turns, want %d", len(a.cur.Turns), n)
	}
}

// TestFailedTurnsExcludedFromContext keeps errored turns out of the next prompt.
func TestFailedTurnsExcludedFromContext(t *testing.T) {
	s := &store.Session{Turns: []store.Turn{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "", Err: "connection reset"},
		{Role: "user", Content: "still there?"},
	}}
	msgs := s.Messages("sys")
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3 (system + 2 good turns)", len(msgs))
	}
	for _, m := range msgs {
		if m.Content == "" {
			t.Error("an empty/errored turn leaked into the prompt")
		}
	}
}

// TestPaletteFilter exercises the fuzzy matcher used by the command palette.
func TestPaletteFilter(t *testing.T) {
	a := newTestApp(100, 30)
	a.openPalette()
	all := len(a.filteredCommands())

	a.paletteIn.SetValue("newchat")
	got := a.filteredCommands()
	if len(got) == 0 || len(got) >= all {
		t.Fatalf("filter matched %d of %d commands", len(got), all)
	}
	if got[0].title != "New chat" {
		t.Errorf("top match = %q, want %q", got[0].title, "New chat")
	}

	a.paletteIn.SetValue("zzzqqq")
	if n := len(a.filteredCommands()); n != 0 {
		t.Errorf("nonsense query matched %d commands", n)
	}
}

// TestTooSmallTerminal checks the graceful degradation path.
func TestTooSmallTerminal(t *testing.T) {
	a := newTestApp(40, 10)
	frame := render(a)
	if !strings.Contains(ansi.Strip(frame), "terminal too small") {
		t.Error("expected the too-small notice")
	}
	checkFrame(t, frame, 40, 10, "too small")
}
