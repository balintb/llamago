// Package ui implements llamago's terminal interface.
package ui

import (
	"os"
	"time"

	"charm.land/bubbles/v2/filepicker"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/balintb/llamago/internal/config"
	"github.com/balintb/llamago/internal/imaging"
	"github.com/balintb/llamago/internal/ollama"
	"github.com/balintb/llamago/internal/store"
	"github.com/balintb/llamago/internal/theme"
	"github.com/balintb/llamago/internal/tools"
)

type tab int

const (
	tabChat tab = iota
	tabModels
	tabRunning
	tabSettings
)

var tabNames = []string{"Chat", "Models", "Running", "Settings"}

// focus tracks which pane in the chat view receives navigation keys.
type focus int

const (
	focusInput focus = iota
	focusTranscript
	focusSessions
)

// overlay is the modal currently covering the app, if any.
type overlay int

const (
	overlayNone overlay = iota
	overlayHelp
	overlayPalette
	overlayConfirm
	overlayPull
	overlaySystem
	overlayRename
	overlayNudge
	overlayFind
	// overlayPicker is the file and directory browser, used both to attach an image and to choose where to save one.
	overlayPicker
)

// editorTarget names what the multi-line text editor overlay is editing. The overlay, its textarea and its key handling are identical either way; only the title and where the text lands differ.
type editorTarget int

const (
	editSystem editorTarget = iota
	editStop
)

// pickerMode is what the file browser is being used for.
type pickerMode int

const (
	pickImage pickerMode = iota
	pickSaveDir
	pickText
)

// pullLayer tracks the download progress of a single blob within a pull.
type pullLayer struct {
	digest    string
	total     int64
	completed int64
}

// App is the root Bubble Tea model. Sub-views are methods on App rather than nested models: every view reads the same model list and config, and flat state keeps that sharing explicit instead of plumbing it through messages.
type App struct {
	client *ollama.Client
	cfg    config.Config

	width, height int
	ready         bool
	tab           tab
	// tabBarFocus puts the keyboard on the top tab strip rather than in the tab's content.
	tabBarFocus bool
	frame       int

	// Connection health, refreshed on every tick.
	version string
	connErr error

	// Server data.
	// autoModel records that the active model was adopted for the user rather than chosen, so it can be swapped once capabilities arrive.
	autoModel bool
	models    []ollama.Model
	running   []ollama.RunningModel
	details   map[string]*ollama.ShowResponse
	loading   bool

	// Results of a search across every conversation. findTotal counts what was found, findHits what is small enough to list.
	findHits  []globalHit
	findIdx   int
	findQuery string
	findTotal int

	// Tools. toolAllowed remembers what was permitted for this conversation only; pendingCalls is what is left of the current round, and toolStep counts rounds so a model cannot call forever.
	tools        *tools.Registry
	toolErrs     []error
	toolAllowed  map[string]bool
	pendingCalls []ollama.ToolCall
	deniedCall   *ollama.ToolCall
	toolStep     int
	// titleTried marks sessions already offered to the model for naming, so an attempt that came back empty does not retry on every later turn.
	titleTried map[string]bool

	// expanded is which turns are unfolded - tool results, long call arguments, reasoning - by turn index. It is a view of the conversation rather than part of it, so it is not saved.
	expanded map[int]bool

	// library is the saved prompt library, loaded at startup and written through on change.
	library []store.Prompt

	// Chat. renameID is the session the rename overlay is editing, kept by id rather than by pointer so a reload underneath cannot rename the wrong one.
	renameIn textinput.Model
	renameID string
	// nudgeIn collects a one-off instruction for the next generation, and nudge carries it into exactly one request before being cleared.
	nudgeIn    textinput.Model
	nudge      string
	sessions   []*store.Session
	cur        *store.Session
	sessionIdx int
	focus      focus
	input      textarea.Model
	transcript viewport.Model
	sidebar    bool
	streaming  bool
	gen        int
	chatFeed   *feed[ollama.ChatResponse]
	startedAt  time.Time
	ttft       time.Duration
	tps        []float64
	tokens     int
	sampleAt   time.Time
	sampleTok  int
	showThink  bool
	pinBottom  bool

	// Prompt recall in the composer. histIdx counts back from the newest prompt and is -1 when not recalling; histDraft holds whatever was being typed when recall started, so stepping back down restores it.
	histIdx   int
	histDraft string

	// Transcript selection. selTurn indexes a.cur.Turns, or -1 for no selection; turnLines maps each turn to its first rendered line, which is what scrolls a selection into view.
	selTurn   int
	turnLines []int

	// Transcript search. searching means the find bar has the keyboard; a non-empty query keeps the highlights alive after it is committed.
	searching   bool
	searchIn    textinput.Model
	searchQuery string
	searchHits  []searchHit
	searchIdx   int

	lastRender  time.Time
	renderCache map[string]string

	// Models tab. detailScroll scrolls the card beside the list, which has no selection of its own.
	detailScroll  int
	modelIdx      int
	modelSearch   textinput.Model
	modelSearchOn bool

	// Pull.
	pullInput  textinput.Model
	pullIdx    int
	pulling    bool
	pullName   string
	pullStatus string
	pullLayers []pullLayer
	pullFeed   *feed[ollama.PullProgress]

	// Images.
	pending          []attachment // images queued for the next prompt
	detectedGraphics imaging.Protocol
	thumbCache       map[string]thumb
	placements       []imagePlacement
	picker           filepicker.Model
	pickerMode       pickerMode
	pickerTarget     imageRef

	// Model comparison.
	comparing     bool
	compare       []*compareRun
	compareIdx    int
	compareFocus  int
	compareGen    int
	comparePrompt string

	// Running tab. Every card is selectable, so the view follows the selection and needs no offset of its own.
	runIdx int

	// Settings tab. setScroll is how far the pane is scrolled; the view clamps it and pulls it back whenever the selected field would fall off screen.
	setIdx     int
	setScroll  int
	sysInput   textarea.Model
	editTarget editorTarget

	// Overlays.
	overlay     overlay
	helpScroll  int
	paletteIn   textinput.Model
	paletteIdx  int
	paletteCmds []command
	paletteMode paletteMode
	confirm     confirmState

	// Transient status line message.
	toast    string
	toastErr bool
	toastID  int

	spinner  spinner.Model
	progress progress.Model
	md       *glamour.TermRenderer
	mdWidth  int
}

// thumb is a cached half-block rendering of an attachment.
type thumb struct {
	body       string
	cols, rows int
}

// confirmState describes a pending destructive action awaiting a y/n answer.
type confirmState struct {
	prompt string
	action tea.Cmd
}

// Version is llamago's own version, shown on the opening screen. main sets it from its ldflags-injected value; it is deliberately not the Ollama server version, which the header reports separately.
var Version = "dev"

// New builds the root model. Nothing touches the network until Init runs.
func New(cfg config.Config) *App {
	// Before any style is built from the palette: everything below reads the theme package's colours at construction time.
	if cfg.Theme != "" {
		theme.Use(cfg.Theme)
	}

	in := textarea.New()
	in.Placeholder = "Ask anything…  (enter to send, alt+enter for a newline)"
	in.ShowLineNumbers = false
	in.CharLimit = 0
	in.EndOfBufferCharacter = ' '
	// Grow with the content up to maxComposerLines, then scroll internally. MaxContentHeight has to be set to something large: left at zero the widget falls back to refusing input past MaxHeight logical lines, which would stop typing at the cap instead of scrolling.
	in.DynamicHeight = true
	in.MinHeight = 1
	in.MaxHeight = maxComposerLines
	in.MaxContentHeight = composerContentCap
	in.SetVirtualCursor(false)
	in.Focus()
	styleTextarea(&in)

	sys := textarea.New()
	sys.Placeholder = "You are a helpful assistant…"
	sys.ShowLineNumbers = false
	sys.CharLimit = 0
	sys.SetVirtualCursor(false)
	styleTextarea(&sys)

	search := textinput.New()
	search.Placeholder = "filter models"
	search.Prompt = ""
	search.SetVirtualCursor(false)

	pull := textinput.New()
	pull.Placeholder = "llama3.2:3b"
	pull.Prompt = promptMark
	pull.SetVirtualCursor(false)

	pal := textinput.New()
	pal.Placeholder = "type a command…"
	pal.Prompt = promptMark
	pal.SetVirtualCursor(false)

	rename := textinput.New()
	rename.Placeholder = "session title"
	rename.Prompt = promptMark
	rename.SetVirtualCursor(false)

	nudge := textinput.New()
	nudge.Placeholder = "shorter · in Go · with an example"
	nudge.Prompt = promptMark
	nudge.SetVirtualCursor(false)

	find := textinput.New()
	find.Placeholder = "find in conversation"
	find.Prompt = ""
	find.SetVirtualCursor(false)

	fp := filepicker.New()
	fp.AllowedTypes = imaging.Extensions
	fp.ShowPermissions = false
	fp.ShowSize = true
	fp.AutoHeight = false
	if home, err := os.UserHomeDir(); err == nil {
		fp.CurrentDirectory = home
	}

	a := &App{
		client:           ollama.New(cfg.Host),
		cfg:              cfg,
		details:          map[string]*ollama.ShowResponse{},
		renderCache:      map[string]string{},
		thumbCache:       map[string]thumb{},
		picker:           fp,
		detectedGraphics: imaging.Detect(os.Environ()),
		input:            in,
		sysInput:         sys,
		modelSearch:      search,
		pullInput:        pull,
		paletteIn:        pal,
		searchIn:         find,
		renameIn:         rename,
		nudgeIn:          nudge,
		sidebar:          true,
		showThink:        true,
		pinBottom:        true,
		histIdx:          -1,
		selTurn:          -1,
		toolAllowed:      map[string]bool{},
		spinner:          spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(lipgloss.NewStyle().Foreground(theme.Violet))),
		progress:         progress.New(progress.WithColors(theme.Accent...), progress.WithoutPercentage()),
		transcript:       viewport.New(),
	}
	// Loading the registry is reading a directory, so it happens here rather than in Init: the Settings tab lists the tools and should not depend on whether the event loop has started.
	a.loadTools()
	a.sysInput.SetValue(cfg.System)
	a.cur = store.NewSession(cfg.Model, time.Now())
	return a
}

// Init loads saved sessions and fires the first round of server queries.
func (a *App) Init() tea.Cmd {
	if sessions, err := store.Load(); err == nil {
		a.sessions = sessions
		a.resumeLast()
	}
	if library, err := store.LoadPrompts(); err == nil {
		a.library = library
	}
	return tea.Batch(
		a.spinner.Tick,
		a.connectCmd(),
		a.listModelsCmd(),
		a.psCmd(),
		tick(),
	)
}

// resumeLast reopens the conversation last worked in, when the setting asks for it. The empty session New built is discarded rather than kept: it was never saved, so nothing is lost.
//
// "Last" is the most recently updated session, not the first in the list, which pinning reorders.
func (a *App) resumeLast() {
	if !a.cfg.Resume || len(a.sessions) == 0 {
		return
	}
	// Anything already typed or said outranks a saved session.
	if a.cur != nil && len(a.cur.Turns) > 0 {
		return
	}
	newest := a.sessions[0]
	for _, s := range a.sessions[1:] {
		if s.Updated.After(newest.Updated) {
			newest = s
		}
	}
	if len(newest.Turns) == 0 {
		return
	}
	a.cur = newest
	a.pinBottom = true
	if newest.Model != "" {
		a.cfg.Model = newest.Model
	}
}

// Update routes messages: overlays get first refusal, then global keys, then the active tab.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.ready = true
		a.layout()
		a.invalidateRenders()
		a.refreshTranscript()
		return a, nil

	case tickMsg:
		a.frame++
		var cmds []tea.Cmd
		cmds = append(cmds, tick())
		// Keep the ps view and the header's model count honest without hammering the server: refresh on a slow cadence.
		if a.frame%3 == 0 {
			cmds = append(cmds, a.psCmd(), a.connectCmd())
		}
		return a, tea.Batch(cmds...)

	case spinner.TickMsg:
		var cmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(msg)
		return a, cmd

	case progress.FrameMsg:
		m, cmd := a.progress.Update(msg)
		a.progress = m
		return a, cmd

	case connectedMsg:
		a.version, a.connErr = msg.version, msg.err
		return a, nil

	case modelsMsg:
		a.loading = false
		if msg.err != nil {
			return a, a.errToast(msg.err)
		}
		a.models = msg.models
		a.clampModelIdx()
		// Adopt a sensible default model on first run. Capabilities are not known yet, so this may land on an image generation model; the details handler below moves off it once that becomes clear.
		if a.cfg.Model == "" && len(a.models) > 0 {
			a.autoModel = true
			return a, tea.Batch(a.setModel(a.models[0].Name), a.fetchMissingDetails())
		}
		// Learn the active model's capabilities even if the user never opens the Models tab.
		if _, ok := a.details[a.cfg.Model]; !ok && a.cfg.Model != "" {
			return a, a.showCmd(a.cfg.Model)
		}
		return a, nil

	case runningMsg:
		if msg.err == nil {
			a.running = msg.models
			if a.runIdx >= len(a.running) {
				a.runIdx = max(0, len(a.running)-1)
			}
		}
		return a, nil

	case showMsg:
		if msg.err != nil {
			return a, nil
		}
		a.details[msg.name] = msg.info
		// An auto-adopted model that turns out to generate images cannot chat, so move to one that can rather than stranding the user.
		if a.autoModel && msg.name == a.cfg.Model && msg.info.CanImage() {
			if name := a.firstChatModel(); name != "" {
				return a, a.setModel(name)
			}
		}
		return a, nil

	case actionMsg:
		if msg.err != nil {
			return a, a.errToast(msg.err)
		}
		return a, tea.Batch(a.okToast(msg.text), a.listModelsCmd(), a.psCmd())

	case sessionDeletedMsg:
		if msg.err != nil {
			return a, a.errToast(msg.err)
		}
		a.forgetSession(msg.id)
		return a, a.okToast("session deleted")

	case titledMsg:
		if msg.id == "" || msg.title == "" {
			return a, nil
		}
		for _, s := range append([]*store.Session{a.cur}, a.sessions...) {
			if s != nil && s.ID == msg.id {
				s.Title = msg.title
				_ = s.Save()
				break
			}
		}
		return a, nil

	case dropTurnsMsg:
		return a, a.dropTurns(msg.from, msg.to)

	case toolResultMsg:
		return a, a.recordToolResult(msg.result)

	case toolAllowedMsg:
		// Allowing a call allows that tool for the rest of the conversation: being asked about the same tool on every call teaches people to approve without reading.
		a.toolAllowed[msg.call.Function.Name] = true
		a.deniedCall = nil
		return a, a.runToolCmd(msg.call)

	case rewindMsg:
		return a, a.rewind(msg.idx)

	case chatChunkMsg:
		if msg.side != sideChat {
			return a, a.onCompareChunk(msg)
		}
		return a, a.onChatChunk(msg)

	case chatEndMsg:
		if msg.side != sideChat {
			return a, a.onCompareEnd(msg)
		}
		return a, a.onChatEnd(msg)

	case pullChunkMsg:
		return a, a.onPullChunk(msg)

	case pullEndMsg:
		return a, a.onPullEnd(msg)

	case toastExpireMsg:
		if int(msg) == a.toastID {
			a.toast, a.toastErr = "", false
		}
		return a, nil

	case tea.KeyPressMsg:
		return a, a.onKey(msg)

	case tea.MouseWheelMsg:
		return a, a.onWheel(msg)

	case tea.MouseClickMsg:
		return a, a.onClick(msg)
	}

	// Anything unclaimed still needs to reach the focused text widget (paste, mouse, cursor blink).
	return a, a.forward(msg)
}

// onWheel scrolls whichever pane the pointer is over. Modals swallow the wheel so it can't move something hidden behind them.
func (a *App) onWheel(msg tea.MouseWheelMsg) tea.Cmd {
	if a.overlay != overlayNone {
		return nil
	}
	m := msg.Mouse()
	if a.tab != tabChat {
		return nil
	}
	if a.comparing {
		return a.onCompareWheel(m)
	}
	if !a.transcriptRect().contains(m.X, m.Y) {
		return nil
	}
	switch m.Button {
	case tea.MouseWheelUp:
		a.transcript.ScrollUp(wheelStep)
	case tea.MouseWheelDown:
		a.transcript.ScrollDown(wheelStep)
	default:
		return nil
	}
	// Scrolling away from the bottom detaches the view from a live stream; scrolling back re-attaches it.
	a.pinBottom = a.transcript.AtBottom()
	return nil
}

// onClick resolves a click against the thumbnails in the transcript. Clicking an image offers to save it, which is the one action worth a pointer.
func (a *App) onClick(msg tea.MouseClickMsg) tea.Cmd {
	m := msg.Mouse()
	if a.overlay != overlayNone || m.Button != tea.MouseLeft {
		return nil
	}

	// The tab strip is clickable from every tab, so this comes before anything that only applies to the chat.
	for i, span := range a.tabSpans() {
		if span.contains(m.X, m.Y) {
			if tab(i) == a.tab {
				// Already here: put the keyboard back in the tab's content rather than doing nothing.
				a.tabBarFocus = false
				return nil
			}
			return a.goTab(tab(i))
		}
	}

	if a.tab != tabChat || a.comparing {
		return nil
	}

	// Clicking a pane gives it the keyboard. Which pane has it is otherwise only reachable by tab, and a click that changes nothing reads as an unresponsive window.
	switch {
	case a.sidebarRect().contains(m.X, m.Y):
		a.setFocus(focusSessions)
		// A click on a list is a click on a row, so the highlight follows it.
		if row := a.sessionRowAt(m.Y); row >= 0 {
			a.sessionIdx = row
		}
		return nil
	case a.composerRect().contains(m.X, m.Y):
		a.setFocus(focusInput)
		return nil
	}

	r := a.transcriptRect()
	if !r.contains(m.X, m.Y) {
		return nil
	}
	a.setFocus(focusTranscript)
	// Map the screen cell back to a line of transcript content: inside the border, offset by however far the viewport has scrolled.
	line := m.Y - r.y0 - 1 + a.transcript.YOffset()
	col := m.X - r.x0 - 1

	for _, p := range a.placements {
		if line >= p.line0 && line < p.line1 && col >= p.col0 && col < p.col1 {
			return a.openSaveDirPicker(p.ref)
		}
	}
	return nil
}

// forward passes an unhandled message to whichever text widget is focused.
func (a *App) forward(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch {
	case a.overlay == overlayPicker:
		// The browser loads its listing asynchronously, so it must receive its own messages here or it never shows a single file.
		a.picker, cmd = a.picker.Update(msg)
	case a.overlay == overlayPalette:
		a.paletteIn, cmd = a.paletteIn.Update(msg)
	case a.overlay == overlayPull:
		a.pullInput, cmd = a.pullInput.Update(msg)
	case a.overlay == overlaySystem:
		a.sysInput, cmd = a.sysInput.Update(msg)
	case a.tab == tabChat && a.focus == focusInput:
		a.input, cmd = a.input.Update(msg)
	case a.tab == tabModels && a.modelSearchOn:
		a.modelSearch, cmd = a.modelSearch.Update(msg)
	}
	return cmd
}

// onKey dispatches a key press through overlays, global bindings, then the active tab.
func (a *App) onKey(msg tea.KeyPressMsg) tea.Cmd {
	if a.overlay != overlayNone {
		return a.onOverlayKey(msg)
	}

	switch msg.String() {
	case "ctrl+c":
		// While generating, ctrl+c means "stop", matching every other REPL.
		if a.streaming {
			return a.stopGeneration()
		}
		return a.quit()
	case "ctrl+q":
		return a.quit()
	case "ctrl+k":
		return a.openPalette()
	case "f1":
		a.overlay, a.helpScroll = overlayHelp, 0
		return nil
	case "alt+1":
		return a.goTab(tabChat)
	case "alt+2":
		return a.goTab(tabModels)
	case "alt+3":
		return a.goTab(tabRunning)
	case "alt+4":
		return a.goTab(tabSettings)
	case "ctrl+o":
		return a.goTab((a.tab + 1) % 4)
	case "ctrl+r":
		a.loading = true
		return tea.Batch(a.listModelsCmd(), a.psCmd(), a.connectCmd())
	}

	if a.tabBarFocus {
		return a.onTabBarKey(msg)
	}

	switch a.tab {
	case tabChat:
		if a.comparing {
			return a.onCompareKey(msg)
		}
		return a.onChatKey(msg)
	case tabModels:
		return a.onModelsKey(msg)
	case tabRunning:
		return a.onRunningKey(msg)
	case tabSettings:
		return a.onSettingsKey(msg)
	}
	return nil
}

// onTabBarKey drives the top strip. Moving switches tab immediately, so the body under it previews as you go.
func (a *App) onTabBarKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "left", "h", "shift+tab":
		return a.selectTab(a.tab - 1)
	case "right", "l", "tab":
		return a.selectTab(a.tab + 1)
	case "enter", "down", "j", "esc":
		return a.leaveTabBar()
	}
	if r := msg.Code; r >= '1' && r <= '0'+rune(len(tabNames)) && msg.Mod == 0 {
		return a.selectTab(tab(r - '1'))
	}
	return nil
}

// selectTab moves along the strip, wrapping at both ends, and keeps the keyboard on it.
func (a *App) selectTab(t tab) tea.Cmd {
	n := tab(len(tabNames))
	cmd := a.goTab((t%n + n) % n)
	// goTab hands focus to the content; stay on the strip instead.
	a.tabBarFocus = true
	a.input.Blur()
	return cmd
}

// focusTabBar moves the keyboard from the content to the tab strip.
func (a *App) focusTabBar() {
	a.tabBarFocus = true
	a.input.Blur()
}

// leaveTabBar drops back into the current tab's content.
func (a *App) leaveTabBar() tea.Cmd {
	a.tabBarFocus = false
	if a.tab == tabChat && !a.comparing {
		a.setFocus(focusInput)
	}
	return nil
}

func (a *App) goTab(t tab) tea.Cmd {
	a.tabBarFocus = false
	a.tab = t
	a.layout()
	var cmds []tea.Cmd
	switch t {
	case tabChat:
		// Re-apply the recorded focus. Handing the keyboard to the tab strip blurs the composer, and nothing put it back: the pane still drew as focused while the widget was not, so it was highlighted with no cursor in it.
		a.setFocus(a.focus)
		a.refreshTranscript()
	case tabModels:
		cmds = append(cmds, a.showSelectedModel())
	case tabRunning:
		cmds = append(cmds, a.psCmd())
	}
	return tea.Batch(cmds...)
}

// quit saves in-flight state before tearing down the program.
func (a *App) quit() tea.Cmd {
	a.stopCompare()
	a.chatFeed.stop()
	a.pullFeed.stop()
	if a.cur != nil {
		_ = a.cur.Save()
	}
	_ = a.cfg.Save()
	return tea.Quit
}

// isImageModel reports whether the named model generates images rather than chatting. Ollama exposes no generation endpoint, so such a model can only fail here; the guards using this keep it from breaking anything. Details are fetched lazily, so an uninspected model is assumed to chat.
func (a *App) isImageModel(name string) bool {
	d, ok := a.details[name]
	return ok && d.CanImage()
}

// firstChatModel is the first installed model other than the active one that is not known to generate images. Details arrive lazily, so an uninspected model is taken to chat.
func (a *App) firstChatModel() string {
	for _, m := range a.models {
		if d, ok := a.details[m.Name]; ok && d.CanImage() {
			continue
		}
		if m.Name != a.cfg.Model {
			return m.Name
		}
	}
	return ""
}

// setModel switches the active model and persists the choice. It returns a command fetching the model's details when they are not cached yet: the thinking capability is read from them, so a request built before they arrive would silently ignore the user's thinking setting.
func (a *App) setModel(name string) tea.Cmd {
	a.cfg.Model = name
	if a.cur != nil && len(a.cur.Turns) == 0 {
		a.cur.Model = name
	}
	_ = a.cfg.Save()
	if _, ok := a.details[name]; ok || name == "" {
		return nil
	}
	return a.showCmd(name)
}

// switchHost points the client at another server and reloads everything that came from the old one. Models, memory and the version are all per-server, so keeping any of it would describe a machine that is no longer being talked to.
func (a *App) switchHost(url string) tea.Cmd {
	if url == "" {
		return a.showToast("that host has no address", true)
	}
	a.cfg.Host = url
	_ = a.cfg.Save()
	a.client = ollama.New(url)

	a.models, a.running = nil, nil
	clear(a.details)
	a.version, a.connErr = "", nil
	a.loading = true

	name := a.cfg.HostName(a.client.Host())
	if name == "" {
		name = a.client.Host()
	}
	return tea.Batch(a.connectCmd(), a.listModelsCmd(), a.psCmd(), a.okToast("talking to "+name))
}

// --- layout -----------------------------------------------------------------

// contentHeight is the space between the header and the status bar.
func (a *App) contentHeight() int {
	return max(0, a.height-headerHeight-statusHeight)
}

const (
	headerHeight   = 2
	statusHeight   = 1
	scrollbarWidth = 1
	// wheelStep is how many lines one notch of the wheel moves.
	wheelStep     = 3
	sidebarWidth  = 26
	minSidebarFit = 76

	// maxComposerLines caps the composer; past it the text scrolls in place so the transcript keeps most of the window.
	maxComposerLines = 4
	// composerContentCap bounds total composer content in visual rows. It only exists to keep the widget's own guard well defined; it sits far past anything anyone types into a chat box.
	composerContentCap = 10000
)

// sidebarVisible reports whether there is room for the session list.
func (a *App) sidebarVisible() bool {
	return a.sidebar && a.width >= minSidebarFit
}

// layout resizes every child widget to the current terminal size.
func (a *App) layout() {
	if !a.ready {
		return
	}
	// panel() sizes are totals, so a pane's usable interior is 2 cells narrower and 2 rows shorter than the space it occupies.
	inner := max(10, a.bodyWidth()-2)
	if a.comparing {
		inner = max(10, a.compareWidth()-2)
	}

	// Never let the composer take so much of a short window that the transcript is squeezed out; the cap only bites well below a normal terminal.
	a.input.MaxHeight = min(maxComposerLines, max(1, a.contentHeight()/3))
	// Both panes give up their last column to a scrollbar gutter. Setting the width re-wraps the composer and recomputes its dynamic height, so this must happen before anything reads inputHeight().
	a.input.SetWidth(max(1, inner-scrollbarWidth))

	a.transcript.SetWidth(max(1, inner-scrollbarWidth))
	a.transcript.SetHeight(max(1, a.transcriptPanelHeight()-2))

	a.modelSearch.SetWidth(max(10, a.width/3))
	// Inputs sit inside their modal's interior, less the caret prompt they draw themselves and one cell for the cursor to sit on past the last character.
	const caret = 2
	a.pullInput.SetWidth(max(10, modalInner(a.pullWidth())-caret-1))
	a.paletteIn.SetWidth(max(10, modalInner(a.paletteWidth())-caret-1))
	a.sysInput.SetWidth(max(10, modalInner(a.systemWidth())-1))
	a.sysInput.SetHeight(min(6, max(2, a.height-14)))
	a.progress.SetWidth(max(10, min(48, a.width-30)))

	// A resize can hide the sidebar out from under the focus ring.
	if a.focus == focusSessions && !a.sidebarVisible() {
		a.setFocus(focusInput)
	}

	a.layoutCompare()

	if w := a.mdTargetWidth(); w != a.mdWidth {
		a.mdWidth = w
		a.md = newRenderer(w)
		a.invalidateRenders()
	}
}

// bodyWidth is the width of the main column, excluding the session sidebar.
func (a *App) bodyWidth() int {
	if a.sidebarVisible() {
		return a.width - sidebarWidth
	}
	return a.width
}

func (a *App) mdTargetWidth() int {
	// Leave room for the pane border, the scrollbar gutter and the two-cell speaker prefix.
	return max(20, a.bodyWidth()-6-scrollbarWidth)
}

// rect is a screen rectangle in cells, with half-open x1/y1 bounds.
type rect struct{ x0, y0, x1, y1 int }

// contains reports whether the point falls inside the rectangle.
func (r rect) contains(x, y int) bool {
	return x >= r.x0 && x < r.x1 && y >= r.y0 && y < r.y1
}

// sidebarRect is the session list's screen rectangle, empty when it is hidden.
func (a *App) sidebarRect() rect {
	if !a.sidebarVisible() {
		return rect{}
	}
	return rect{x0: 0, y0: headerHeight, x1: sidebarWidth, y1: headerHeight + a.contentHeight()}
}

// composerRect is the input panel's screen rectangle, below the transcript.
func (a *App) composerRect() rect {
	x0 := 0
	if a.sidebarVisible() {
		x0 = sidebarWidth
	}
	y0 := headerHeight + a.transcriptPanelHeight()
	return rect{x0: x0, y0: y0, x1: x0 + a.bodyWidth(), y1: y0 + a.inputPanelHeight()}
}

// sessionRowAt maps a click in the sidebar onto a row of the list, or -1. The list starts below its heading and a blank line, inside the panel border.
func (a *App) sessionRowAt(y int) int {
	const headingRows = 2
	row := y - headerHeight - 1 - headingRows
	if row < 0 || row > len(a.sessions) {
		return -1
	}
	return row
}

// transcriptRect is the transcript pane's screen rectangle, borders included. Mouse hit-testing uses it, so it must track the chat layout exactly.
func (a *App) transcriptRect() rect {
	x0 := 0
	if a.sidebarVisible() {
		x0 = sidebarWidth
	}
	return rect{
		x0: x0, y0: headerHeight,
		x1: x0 + a.bodyWidth(), y1: headerHeight + a.transcriptPanelHeight(),
	}
}

// inputHeight is the composer's current interior height. The widget owns this: it grows with wrapped content and stops at MaxHeight, so counting newlines here would miss soft wraps and clip the text.
func (a *App) inputHeight() int {
	return max(1, a.input.Height())
}

// inputPanelHeight is the composer's footprint including its border.
func (a *App) inputPanelHeight() int { return a.inputHeight() + 2 }

// transcriptPanelHeight is what remains of the content area once the composer and its one-line hint are placed.
func (a *App) transcriptPanelHeight() int {
	return max(3, a.contentHeight()-a.inputPanelHeight()-1)
}

// searchBarOrigin is the screen cell where the find bar's text begins. The bar replaces the composer hint, the last row of the chat body.
func (a *App) searchBarOrigin() (x, y int) {
	if a.sidebarVisible() {
		x = sidebarWidth
	}
	// " search " is the label rendered ahead of the input.
	return x + len(" search "), headerHeight + a.transcriptPanelHeight() + a.inputPanelHeight()
}

// inputOrigin is the screen cell of the composer's first editable character. The cursor is positioned from this, so it must track the chat layout exactly.
func (a *App) inputOrigin() (x, y int) {
	if a.comparing {
		return a.compareInputOrigin()
	}
	if a.sidebarVisible() {
		x = sidebarWidth
	}
	return x + 1, headerHeight + a.transcriptPanelHeight() + 1
}

// promptMark is the caret drawn by the palette and pull inputs. It is the input's own prompt so the widget accounts for its width when sizing itself.
var promptMark = lipgloss.NewStyle().Foreground(theme.Violet).Bold(true).Render("❯ ")

func newRenderer(width int) *glamour.TermRenderer {
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
		glamour.WithEmoji(),
	)
	if err != nil {
		return nil
	}
	return r
}

// styleComposer switches the composer between prose and command styling. A command reads as one: violet, the colour keys are drawn in everywhere else.
func styleComposer(t *textarea.Model, command bool) {
	s := textarea.DefaultDarkStyles()
	s.Focused.Base = lipgloss.NewStyle()
	s.Blurred.Base = lipgloss.NewStyle()
	s.Focused.Prompt = lipgloss.NewStyle().Foreground(theme.Violet)
	s.Blurred.Prompt = lipgloss.NewStyle().Foreground(theme.Faint)
	s.Focused.Placeholder = lipgloss.NewStyle().Foreground(theme.Faint)
	s.Focused.Text = lipgloss.NewStyle().Foreground(theme.Text)
	if command {
		// Indigo, unbolded: a command should read as a shift in register rather than as something shouting. Violet is the key colour and bold on top of it made the composer the loudest thing on screen.
		//
		// CursorLine, not Text: the textarea styles the line the cursor is on with CursorLine, and the composer is one line with the cursor always in it, so Text alone is never consulted.
		lit := lipgloss.NewStyle().Foreground(theme.Indigo)
		s.Focused.Text, s.Focused.CursorLine = lit, lit
		s.Blurred.Text, s.Blurred.CursorLine = lit, lit
	}
	s.Cursor.Color = theme.Magenta
	t.SetStyles(s)
	t.Prompt = ""
}

func styleTextarea(t *textarea.Model) {
	s := textarea.DefaultDarkStyles()
	s.Focused.Base = lipgloss.NewStyle()
	s.Blurred.Base = lipgloss.NewStyle()
	s.Focused.Prompt = lipgloss.NewStyle().Foreground(theme.Violet)
	s.Blurred.Prompt = lipgloss.NewStyle().Foreground(theme.Faint)
	s.Focused.Placeholder = lipgloss.NewStyle().Foreground(theme.Faint)
	s.Focused.Text = lipgloss.NewStyle().Foreground(theme.Text)
	s.Cursor.Color = theme.Magenta
	t.SetStyles(s)
	t.Prompt = ""
}

// --- toasts -----------------------------------------------------------------

func (a *App) errToast(err error) tea.Cmd {
	if err == nil {
		return nil
	}
	return a.showToast(err.Error(), true)
}

func (a *App) okToast(text string) tea.Cmd { return a.showToast(text, false) }

func (a *App) showToast(text string, isErr bool) tea.Cmd {
	a.toastID++
	a.toast, a.toastErr = text, isErr
	id := a.toastID
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return toastExpireMsg(id) })
}

func (a *App) invalidateRenders() {
	clear(a.renderCache)
}

func (a *App) clampModelIdx() {
	if n := len(a.visibleModels()); a.modelIdx >= n {
		a.modelIdx = max(0, n-1)
	}
}
