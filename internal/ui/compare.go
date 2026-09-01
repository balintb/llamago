package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/balintb/llamago/internal/ollama"
	"github.com/balintb/llamago/internal/store"
	"github.com/balintb/llamago/internal/theme"
)

// compareSides is how many models a race starts with. More can be added while it runs, up to what the terminal is wide enough to show. Two to begin with shrink each column past the point of being readable.
const compareSides = 2

// minCompareColumn is the narrowest a column can get and still hold a readable answer. It is what caps how many models can race at once.
const minCompareColumn = 34

// compareFocusComposer is the focus value for the prompt box; columns take the values above it, so focus 1 is the first column.
const compareFocusComposer = 0

// compareRun is one side of a race: its model, the response accumulating from it, and the timings needed to rank the sides when both finish.
type compareRun struct {
	model string
	// turns is this side's own thread: the shared prompts plus the answers this model gave, so a follow-up continues from what this side actually said.
	turns []store.Turn
	turn  store.Turn // the answer currently streaming
	feed  *feed[ollama.ChatResponse]
	vp    viewport.Model

	started   time.Time
	ttft      time.Duration
	elapsed   time.Duration
	tokens    int
	tps       []float64
	sampleAt  time.Time
	sampleTok int

	// streaming is true only between starting a round and its stream ending.
	// A freshly created side is idle, not finished: conflating the two made a new race look busy to itself and refuse to start.
	streaming bool
	err       error
}

// running reports whether this side is mid-generation.
func (r *compareRun) running() bool { return r != nil && r.streaming }

// hasAnswer reports whether a turn carries anything the user can see, counting reasoning-only output that a tight token budget cut short.
func hasAnswer(t store.Turn) bool {
	return strings.TrimSpace(t.Content)+strings.TrimSpace(t.Thinking) != ""
}

// --- lifecycle --------------------------------------------------------------

// startCompare opens the race. The active model takes the left column and other takes the right; the prompt is sent to both at once.
func (a *App) startCompare(other string, prompt string) tea.Cmd {
	if a.cfg.Model == "" {
		return a.showToast("no model selected", true)
	}
	if other == "" || other == a.cfg.Model {
		return a.showToast("pick a different model to compare against", true)
	}
	// A generation model cannot hold a conversation, so it can never race one.
	for _, m := range []string{a.cfg.Model, other} {
		if a.isImageModel(m) {
			return a.showToast(shortModel(m)+" generates images and cannot chat", true)
		}
	}

	a.comparing = true
	a.compareIdx = 0
	a.compareFocus = compareFocusComposer
	a.compare = make([]*compareRun, compareSides)
	for i, model := range []string{a.cfg.Model, other} {
		a.compare[i] = &compareRun{model: model, vp: viewport.New()}
	}
	a.layout()
	// Details drive the thinking flag; fetch any that are missing so later rounds are correct even if the first was built without them.
	return tea.Batch(a.sendCompare(prompt), a.fetchMissingDetails())
}

// fetchMissingDetails requests capabilities for every installed model that has not been inspected yet.
func (a *App) fetchMissingDetails() tea.Cmd {
	var cmds []tea.Cmd
	for _, m := range a.models {
		if _, ok := a.details[m.Name]; !ok {
			cmds = append(cmds, a.showCmd(m.Name))
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// sendCompare puts a prompt to every side at once, continuing each side's own thread. This is what makes a comparison a conversation rather than one shot.
func (a *App) sendCompare(prompt string) tea.Cmd {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil
	}
	if a.compareBusy() {
		return a.showToast("still generating - ctrl+c to stop", true)
	}

	a.compareGen++
	a.comparePrompt = prompt
	now := time.Now()

	cmds := make([]tea.Cmd, 0, compareSides+1)
	for i, r := range a.compare {
		r.turns = append(r.turns, store.Turn{Role: "user", Content: prompt, At: now})
		r.turn = store.Turn{Role: "assistant", Model: r.model, At: now}
		r.started, r.sampleAt = now, now
		r.ttft, r.elapsed = 0, 0
		r.tokens, r.sampleTok = 0, 0
		r.tps = r.tps[:0]
		r.streaming, r.err = true, nil

		req := ollama.ChatRequest{
			Model:     r.model,
			Messages:  a.compareMessagesFor(r),
			KeepAlive: a.cfg.KeepAlive,
			Options:   a.cfg.Options(),
		}
		if a.modelCanThink(r.model) {
			think := a.cfg.Think
			req.Think = &think
		}
		cmds = append(cmds, a.startCompareSide(i, req))
	}
	a.compareFocus = compareFocusComposer
	a.layout()
	a.refreshCompare()
	return tea.Batch(append(cmds, a.spinner.Tick)...)
}

// compareMessagesFor builds one side's prompt: the conversation that preceded the race, then that side's own exchanges within it.
func (a *App) compareMessagesFor(r *compareRun) []ollama.Message {
	msgs := a.cur.Messages(a.systemPrompt())
	for _, t := range r.turns {
		if t.Err != "" || strings.TrimSpace(t.Content) == "" {
			continue
		}
		msgs = append(msgs, ollama.Message{Role: t.Role, Content: t.Content})
	}
	return msgs
}

// startCompareSide launches one side's stream.
func (a *App) startCompareSide(side int, req ollama.ChatRequest) tea.Cmd {
	f, cmd := a.streamChat(req, a.compareGen, side)
	a.compare[side].feed = f
	return cmd
}

// stopCompare cancels any side still running.
func (a *App) stopCompare() {
	for _, r := range a.compare {
		if r.running() {
			r.feed.stop()
			r.streaming = false
		}
	}
}

// exitCompare tears the race down and returns to the normal transcript.
func (a *App) exitCompare() tea.Cmd {
	a.stopCompare()
	a.comparing = false
	a.compare = nil
	a.comparePrompt = ""
	a.setFocus(focusInput)
	a.layout()
	a.refreshTranscript()
	return nil
}

// keepCompareSide commits one side's answer to the conversation and closes the race, so a comparison ends by choosing a winner rather than discarding both.
func (a *App) keepCompareSide(side int) tea.Cmd {
	if side < 0 || side >= len(a.compare) {
		return nil
	}
	r := a.compare[side]
	// Take the whole thread, not just the last answer: a comparison can run for several rounds and keeping it should preserve all of them.
	kept := append([]store.Turn(nil), r.turns...)
	if hasAnswer(r.turn) {
		kept = append(kept, r.turn)
	}
	// A trailing prompt with nothing under it would be re-sent on the next generation, so drop it.
	if n := len(kept); n > 0 && kept[n-1].Role == "user" {
		kept = kept[:n-1]
	}
	// The thread always starts with a prompt, so length alone proves nothing: require an actual answer before committing anything.
	answered := false
	for _, t := range kept {
		if t.Role == "assistant" && hasAnswer(t) {
			answered = true
			break
		}
	}
	if !answered {
		return a.showToast("that side has no answer to keep", true)
	}

	a.cur.Turns = append(a.cur.Turns, kept...)
	a.cur.Model = r.model
	a.cur.Touch()
	setCmd := a.setModel(r.model)
	a.rememberSession(a.cur)
	_ = a.cur.Save()

	name := strings.TrimSuffix(r.model, ":latest")
	cmd := a.exitCompare()
	a.invalidateRenders()
	a.refreshTranscript()
	return tea.Batch(cmd, setCmd, a.okToast("kept "+name+"'s answer"))
}

// --- streaming --------------------------------------------------------------

func (a *App) onCompareChunk(msg chatChunkMsg) tea.Cmd {
	if msg.gen != a.compareGen || msg.side < 0 || msg.side >= len(a.compare) {
		return nil
	}
	r := a.compare[msg.side]
	if r == nil || !r.streaming {
		return nil
	}

	if r.ttft == 0 && (msg.chunk.Message.Content != "" || msg.chunk.Message.Thinking != "") {
		r.ttft = time.Since(r.started)
	}
	r.turn.Content += msg.chunk.Message.Content
	r.turn.Thinking += msg.chunk.Message.Thinking

	if msg.chunk.Message.Content != "" || msg.chunk.Message.Thinking != "" {
		r.tokens++
	}
	if d := time.Since(r.sampleAt); d >= tpsSample {
		r.tps = append(r.tps, float64(r.tokens-r.sampleTok)/d.Seconds())
		if len(r.tps) > 32 {
			r.tps = r.tps[1:]
		}
		r.sampleAt, r.sampleTok = time.Now(), r.tokens
	}

	if msg.chunk.Done {
		r.turn.TokensPerSec = msg.chunk.TokensPerSecond()
		r.turn.EvalCount = msg.chunk.EvalCount
		r.turn.PromptCount = msg.chunk.PromptEvalCount
		r.turn.TTFT = r.ttft
		r.turn.Total = time.Duration(msg.chunk.TotalDuration)
	}
	a.refreshCompare()
	return waitChat(r.feed, msg.gen, msg.side)
}

func (a *App) onCompareEnd(msg chatEndMsg) tea.Cmd {
	if msg.gen != a.compareGen || msg.side < 0 || msg.side >= len(a.compare) {
		return nil
	}
	r := a.compare[msg.side]
	if r == nil {
		return nil
	}
	r.streaming = false
	r.elapsed = time.Since(r.started)
	if msg.err != nil && msg.err.Error() != "context canceled" {
		r.err = msg.err
	}
	// Settle the finished answer into this side's thread so a follow-up carries it as context and the column keeps showing it.
	if hasAnswer(r.turn) {
		r.turns = append(r.turns, r.turn)
		r.turn = store.Turn{}
	}
	a.refreshCompare()
	return nil
}

// compareBusy reports whether any side is still generating.
func (a *App) compareBusy() bool {
	for _, r := range a.compare {
		if r.running() {
			return true
		}
	}
	return false
}

// --- keys -------------------------------------------------------------------

// compareColumnFocused reports the focused column, or -1 when the composer has the keyboard.
func (a *App) compareColumnFocused() int {
	if a.compareFocus == compareFocusComposer {
		return -1
	}
	return min(a.compareFocus-1, len(a.compare)-1)
}

// cycleCompareFocus moves between the composer and the columns.
func (a *App) cycleCompareFocus(dir int) {
	n := len(a.compare) + 1
	a.compareFocus = ((a.compareFocus+dir)%n + n) % n
	if a.compareFocus == compareFocusComposer {
		a.input.Focus()
		return
	}
	a.compareIdx = a.compareFocus - 1
	a.input.Blur()
}

func (a *App) onCompareKey(msg tea.KeyPressMsg) tea.Cmd {
	if a.searching {
		return a.onSearchKey(msg)
	}
	// Keys that work wherever the focus is.
	switch msg.String() {
	case "ctrl+f":
		return a.openSearch()
	case "ctrl+\\":
		return a.exitCompare()
	case "alt+a":
		// Another model against the same prompts. alt rather than a bare key: the composer has the keyboard here, and "+" is something people type.
		if len(a.models) < 2 {
			return a.showToast("no other model installed", true)
		}
		return tea.Batch(a.openPaletteMode(paletteCompare), a.fetchMissingDetails())
	case "ctrl+c":
		if a.compareBusy() {
			a.stopCompare()
			return a.okToast("stopped both sides")
		}
		return a.exitCompare()
	case "tab":
		a.cycleCompareFocus(1)
		return nil
	case "shift+tab":
		a.cycleCompareFocus(-1)
		return nil
	case "esc":
		// Clear a committed search first, then fall back to typing. Leaving the mode entirely is ctrl+\, which the hint line spells out.
		if a.searchQuery != "" {
			a.closeSearch()
			return nil
		}
		a.compareFocus = compareFocusComposer
		a.input.Focus()
		return nil
	}

	if a.compareColumnFocused() < 0 {
		return a.onCompareComposerKey(msg)
	}
	return a.onCompareColumnKey(msg)
}

// onCompareComposerKey types the next prompt for both models.
func (a *App) onCompareComposerKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		prompt := a.input.Value()
		a.input.Reset()
		a.layout()
		return a.sendCompare(prompt)
	case "alt+enter", "shift+enter", "ctrl+j":
		a.input.InsertRune('\n')
		a.layout()
		return nil
	}
	before := a.input.Height()
	var cmd tea.Cmd
	a.input, cmd = a.input.Update(msg)
	if a.input.Height() != before {
		a.layout()
		a.refreshCompare()
	}
	return cmd
}

// onCompareColumnKey navigates the focused column.
func (a *App) onCompareColumnKey(msg tea.KeyPressMsg) tea.Cmd {
	side := a.compareColumnFocused()
	r := a.compare[side]

	switch msg.String() {
	case "/":
		return a.openSearch()
	case "n":
		a.stepMatch(1)
		return nil
	case "N":
		a.stepMatch(-1)
		return nil
	case "enter":
		return a.keepCompareSide(side)
	case "1":
		return a.keepCompareSide(0)
	case "2":
		return a.keepCompareSide(1)
	case "y":
		if body := strings.TrimSpace(a.compareTranscript(r)); body != "" {
			return tea.Batch(tea.SetClipboard(body),
				a.okToast("copied "+strings.TrimSuffix(r.model, ":latest")))
		}
		return nil
	case "left", "h":
		a.cycleCompareFocus(-1)
		return nil
	case "right", "l":
		a.cycleCompareFocus(1)
		return nil
	case "j", "down":
		r.vp.ScrollDown(1)
	case "k", "up":
		r.vp.ScrollUp(1)
	case "d", "pgdown":
		r.vp.HalfPageDown()
	case "u", "pgup":
		r.vp.HalfPageUp()
	case "g", "home":
		r.vp.GotoTop()
	case "G", "end":
		r.vp.GotoBottom()
	}
	return nil
}

// compareTranscript is one side's answers as plain text, for copying.
func (a *App) compareTranscript(r *compareRun) string {
	var b strings.Builder
	for _, t := range r.turns {
		if t.Role != "assistant" {
			continue
		}
		b.WriteString(strings.TrimSpace(t.Content) + "\n\n")
	}
	b.WriteString(strings.TrimSpace(r.turn.Content))
	return strings.TrimSpace(b.String())
}

// onCompareWheel scrolls whichever column the pointer is over.
func (a *App) onCompareWheel(m tea.Mouse) tea.Cmd {
	for i := range a.compare {
		if !a.compareRect(i).contains(m.X, m.Y) {
			continue
		}
		switch m.Button {
		case tea.MouseWheelUp:
			a.compare[i].vp.ScrollUp(wheelStep)
		case tea.MouseWheelDown:
			a.compare[i].vp.ScrollDown(wheelStep)
		}
		return nil
	}
	return nil
}

// --- layout and rendering ---------------------------------------------------

// Comparing is a focused mode: it hides the session sidebar and gives the whole width to the columns.
func (a *App) compareWidth() int { return a.width }

// compareColumnWidth is the total width of one column, borders included. The last column absorbs the remainder so the pair always spans the full width.
func (a *App) compareColumnWidth(side int) int {
	n := max(1, len(a.compare))
	w := a.compareWidth() / n
	if side == n-1 {
		return a.compareWidth() - w*(n-1)
	}
	return w
}

// compareColumnX is a column's left edge.
func (a *App) compareColumnX(side int) int {
	return side * (a.compareWidth() / max(1, len(a.compare)))
}

// maxCompareSides is how many columns this terminal can hold and still be worth reading. Past it, adding a model is refused rather than shrinking every column into uselessness.
func (a *App) maxCompareSides() int {
	return max(compareSides, min(4, a.compareWidth()/minCompareColumn))
}

// racing reports whether a model already holds a column.
func (a *App) racing(model string) bool {
	for _, r := range a.compare {
		if r != nil && r.model == model {
			return true
		}
	}
	return false
}

// addCompareSide brings another model into a race already under way. It joins with an empty thread and answers from the next prompt on: replaying the conversation so far would be a different race from the one being run.
func (a *App) addCompareSide(model string) tea.Cmd {
	if !a.comparing {
		return a.showToast("not comparing - ctrl+\\ starts a race", true)
	}
	if len(a.compare) >= a.maxCompareSides() {
		return a.showToast(fmt.Sprintf("no room for a %dth column in this window",
			len(a.compare)+1), true)
	}
	for _, r := range a.compare {
		if r.model == model {
			return a.showToast(shortModel(model)+" is already racing", true)
		}
	}
	if a.isImageModel(model) {
		return a.showToast(shortModel(model)+" generates images and cannot chat", true)
	}
	a.compare = append(a.compare, &compareRun{model: model, vp: viewport.New()})
	a.layout()
	return a.okToast(shortModel(model) + " joins from the next prompt")
}

// compareRect is a column's screen rectangle, for mouse hit-testing.
func (a *App) compareRect(side int) rect {
	x0 := a.compareColumnX(side)
	return rect{
		x0: x0, y0: headerHeight,
		x1: x0 + a.compareColumnWidth(side), y1: headerHeight + a.comparePanelHeight(),
	}
}

// comparePanelHeight leaves room for the verdict line, the composer and the hint line beneath the columns.
func (a *App) comparePanelHeight() int {
	return max(3, a.contentHeight()-1-a.inputPanelHeight()-1)
}

// layoutCompare sizes every column.
func (a *App) layoutCompare() {
	if !a.comparing {
		return
	}
	for i, r := range a.compare {
		if r == nil {
			continue
		}
		inner := max(10, a.compareColumnWidth(i)-2)
		// Two header rows inside the pane: model name and its live stats.
		r.vp.SetWidth(max(1, inner-scrollbarWidth))
		r.vp.SetHeight(max(1, a.comparePanelHeight()-2-2))
	}
}

// compareInputOrigin is the screen cell of the composer's first character while comparing. The cursor is placed from it, so it must track this layout.
func (a *App) compareInputOrigin() (x, y int) {
	return 1, headerHeight + a.comparePanelHeight() + 1 + 1
}

// compareSearchBarOrigin is where the find bar's text begins while comparing. The bar replaces the hint line under the composer.
func (a *App) compareSearchBarOrigin() (x, y int) {
	return len(" search "), headerHeight + a.comparePanelHeight() + 1 + a.inputPanelHeight()
}

// refreshCompare re-renders every column, following the tail as tokens arrive.
func (a *App) refreshCompare() {
	if !a.comparing {
		return
	}
	var hits []searchHit
	for i, r := range a.compare {
		if r == nil {
			continue
		}
		atBottom := r.vp.AtBottom()
		content := a.renderCompareBody(r)

		if a.searchQuery != "" {
			// Hits are numbered across both columns, so this column's current index is the global one less what the columns before it produced.
			var found []searchHit
			content, found = highlightMatches(content, a.searchQuery, a.searchIdx-len(hits), i)
			hits = append(hits, found...)
		}

		r.vp.SetContent(content)
		// Following the tail would fight the jump to a match.
		if (atBottom || r.streaming) && a.searchQuery == "" {
			r.vp.GotoBottom()
		}
	}
	a.searchHits = hits
}

// renderCompareBody renders one side's whole thread at the column's width.
func (a *App) renderCompareBody(r *compareRun) string {
	width := max(10, r.vp.Width())
	var parts []string

	for _, t := range r.turns {
		parts = append(parts, a.renderCompareTurn(t, width))
	}
	if hasAnswer(r.turn) {
		parts = append(parts, a.renderCompareTurn(r.turn, width))
	}
	if r.err != nil {
		parts = append(parts, theme.Err.Render("✗ "+r.err.Error()))
	}
	if len(parts) == 0 {
		parts = append(parts, theme.Dim.Render("waiting…"))
	}
	return strings.Join(parts, "\n\n")
}

// renderCompareTurn draws one message inside a column. Columns are narrow, so the body is plain text: glamour's indents and rules eat too much of it.
func (a *App) renderCompareTurn(t store.Turn, width int) string {
	if t.Role == "user" {
		bar := lipgloss.NewStyle().Foreground(theme.Violet).Render("▌")
		body := lipgloss.NewStyle().Foreground(theme.Subtle).Width(max(1, width-2)).
			Render(strings.TrimSpace(t.Content))
		return indent(body, bar+" ")
	}

	var parts []string
	if t.Thinking != "" && a.showThink {
		style := lipgloss.NewStyle().Foreground(theme.Faint).Italic(true).Width(max(1, width-2))
		parts = append(parts, theme.Dim.Render("⋮ reasoning")+"\n"+
			indent(style.Render(strings.TrimSpace(t.Thinking)), theme.Dim.Render("⋮ ")))
	}
	if body := strings.TrimSpace(t.Content); body != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(theme.Text).Width(width).Render(body))
	}
	return strings.Join(parts, "\n")
}

// viewCompare draws the columns plus the verdict footer.
func (a *App) viewCompare() string {
	cols := make([]string, 0, len(a.compare))
	for i, r := range a.compare {
		cols = append(cols, a.viewCompareColumn(i, r))
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Top, cols...),
		a.viewCompareVerdict(),
		panel(a.composerView(), a.compareWidth(), a.inputPanelHeight(),
			a.compareFocus == compareFocusComposer),
		a.viewCompareHint(),
	)
}

func (a *App) viewCompareColumn(side int, r *compareRun) string {
	width := a.compareColumnWidth(side)
	focused := side == a.compareColumnFocused()

	name := strings.TrimSuffix(r.model, ":latest")
	marker := theme.Dim.Render(fmt.Sprintf(" %d ", side+1))
	if focused {
		marker = lipgloss.NewStyle().Foreground(theme.Violet).Bold(true).
			Render(fmt.Sprintf("▌%d ", side+1))
	}
	head := marker + lipgloss.NewStyle().Bold(true).
		Foreground(theme.FamilyColor(a.familyOf(r.model))).Render(name)

	var stats string
	switch {
	case r.err != nil:
		stats = theme.Err.Render("failed")
	case r.streaming:
		stats = a.spinner.View() + " " + theme.Dim.Render(compareLiveStats(r))
	default:
		// Stats live on the last settled answer once a round finishes.
		last := lastAnswer(r)
		if last.TokensPerSec == 0 {
			stats = theme.Dim.Render("ready")
			break
		}
		stats = theme.Dim.Render(fmt.Sprintf("%.0f tok/s · %d tokens", last.TokensPerSec, last.EvalCount))
		if r.elapsed > 0 {
			stats += theme.Dim.Render(" · " + shortDuration(r.elapsed))
		}
	}

	inner := max(1, width-2)
	body := attachScrollbar(r.vp.View(), r.vp.Width(), r.vp.Height(),
		scrollbarColumn(r.vp.Height(), r.vp.TotalLineCount(), r.vp.YOffset(), focused))

	content := strings.Join([]string{
		theme.Truncate(head, inner),
		theme.Truncate(stats, inner),
		body,
	}, "\n")
	return panel(content, width, a.comparePanelHeight(), focused)
}

func compareLiveStats(r *compareRun) string {
	s := shortDuration(time.Since(r.started))
	if n := len(r.tps); n > 0 {
		s += fmt.Sprintf(" · %.0f tok/s %s", r.tps[n-1], sparkline(r.tps))
	}
	return s
}

// familyOf looks up a model's family so a column is tinted like the rest of the app tints that model.
func (a *App) familyOf(name string) string {
	for _, m := range a.models {
		if m.Name == name {
			return m.Details.Family
		}
	}
	return ""
}

// viewCompareVerdict is the single line above the composer: who is ahead, or who won once both sides land.
func (a *App) viewCompareVerdict() string {
	width := a.compareWidth()
	if a.compareBusy() {
		names := make([]string, 0, len(a.compare))
		for _, r := range a.compare {
			names = append(names, strings.TrimSuffix(r.model, ":latest"))
		}
		busy := " " + a.spinner.View() + " " + theme.Dim.Render("racing "+strings.Join(names, " · "))
		return theme.Pad(theme.Truncate(busy, width), width)
	}

	// Rank by throughput, which is the number people actually compare.
	best, bestTPS := -1, 0.0
	for i, r := range a.compare {
		if tps := a.compareTPS(r); tps > bestTPS {
			best, bestTPS = i, tps
		}
	}

	const sep = "   ·   "
	// Give each side an equal budget and trim the model name to fit, so the throughput and the winner's star always survive.
	perSide := (width - len(sep) - 2) / max(1, len(a.compare))
	nameBudget := perSide - len(" 999 tok/s ★")

	build := func(nameWidth int) string {
		parts := make([]string, 0, len(a.compare))
		for i, r := range a.compare {
			label := fmt.Sprintf("%d", i+1)
			if nameWidth > 0 {
				label += " " + theme.Truncate(strings.TrimSuffix(r.model, ":latest"), nameWidth)
			}
			tps := a.compareTPS(r)
			switch {
			case r.err != nil:
				parts = append(parts, theme.Err.Render(label+" failed"))
			case tps == 0:
				parts = append(parts, theme.Dim.Render(label))
			case i == best:
				parts = append(parts, lipgloss.NewStyle().Foreground(theme.Green).Render(
					fmt.Sprintf("%s  %.0f tok/s ★", label, tps)))
			default:
				parts = append(parts, theme.Dim.Render(fmt.Sprintf("%s  %.0f tok/s", label, tps)))
			}
		}
		return " " + strings.Join(parts, theme.Dim.Render(sep))
	}

	summary := build(max(0, nameBudget))
	// Last resort on a very narrow terminal: drop the names entirely rather than let the numbers get cut off.
	if lipgloss.Width(summary) > width {
		summary = build(0)
	}
	return theme.Pad(theme.Truncate(summary, width), width)
}

// lastAnswer is a side's most recent assistant turn, preferring one still streaming. Finished answers are moved off r.turn onto the thread, so reading r.turn alone would report zeroes between rounds.
func lastAnswer(r *compareRun) store.Turn {
	if hasAnswer(r.turn) {
		return r.turn
	}
	for i := len(r.turns) - 1; i >= 0; i-- {
		if r.turns[i].Role == "assistant" {
			return r.turns[i]
		}
	}
	return store.Turn{}
}

// compareTPS is a side's throughput on its most recent completed answer.
func (a *App) compareTPS(r *compareRun) float64 { return lastAnswer(r).TokensPerSec }

// viewCompareHint is the line under the composer. It always spells out how to leave the mode, which is otherwise the one thing with no obvious way back.
func (a *App) viewCompareHint() string {
	width := a.compareWidth()
	if a.searching || a.searchQuery != "" {
		return a.viewSearchBar(width)
	}
	exit := theme.Key.Render("ctrl+\\") + theme.Dim.Render(" exit compare")

	var left string
	if a.compareColumnFocused() < 0 {
		left = theme.Dim.Render("↵ ask all · ") + theme.Key.Render("tab") +
			theme.Dim.Render(" pick a column to keep or scroll")
	} else {
		left = theme.Key.Render("↵") + theme.Dim.Render(" keep · ") +
			theme.Key.Render("y") + theme.Dim.Render(" copy · ") +
			theme.Key.Render("jk") + theme.Dim.Render(" scroll · ") +
			theme.Key.Render("/") + theme.Dim.Render(" find · ") +
			theme.Key.Render("tab") + theme.Dim.Render(" next")
	}
	return spread(" "+left, exit+" ", width)
}
