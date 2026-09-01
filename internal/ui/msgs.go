package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/balintb/llamago/internal/ollama"
	"github.com/balintb/llamago/internal/tools"
)

// --- messages ---------------------------------------------------------------

type (
	// connectedMsg reports the result of the periodic health check.
	connectedMsg struct {
		version string
		err     error
	}

	// modelsMsg carries the result of GET /api/tags.
	modelsMsg struct {
		models []ollama.Model
		err    error
	}

	// runningMsg carries the result of GET /api/ps.
	runningMsg struct {
		models []ollama.RunningModel
		err    error
	}

	// showMsg carries model details for the inspector pane.
	showMsg struct {
		name string
		info *ollama.ShowResponse
		err  error
	}

	// actionMsg reports a one-shot mutation (delete, unload, copy) for toasting.
	actionMsg struct {
		text string
		err  error
	}

	// sessionDeletedMsg reports that a saved session's file is gone, so the sidebar can drop it.
	sessionDeletedMsg struct {
		id  string
		err error
	}

	// titledMsg carries a model-written session title back to the update loop. A zero value means the attempt failed and is ignored.
	titledMsg struct {
		id    string
		title string
	}

	// dropTurnsMsg carries a confirmed deletion back to the update loop, as a half-open range of turns.
	dropTurnsMsg struct {
		from, to int
	}

	// toolResultMsg carries what a tool produced back to the update loop.
	toolResultMsg struct {
		result tools.Result
	}

	// toolAllowedMsg says the user permitted a call, which runs it and remembers the answer for the rest of the conversation.
	toolAllowedMsg struct {
		call ollama.ToolCall
	}

	// rewindMsg carries a confirmed rewind back to the update loop, where the conversation can actually be truncated. idx is the prompt to keep.
	rewindMsg struct {
		idx int
	}

	// chatChunkMsg is one streamed token batch. gen guards against chunks arriving after the user stopped or restarted generation, and side names the comparison column, or sideChat for the ordinary transcript.
	chatChunkMsg struct {
		gen   int
		side  int
		chunk ollama.ChatResponse
	}

	// chatEndMsg closes out a generation, successfully or not.
	chatEndMsg struct {
		gen  int
		side int
		err  error
	}

	// pullChunkMsg is one progress update from an in-flight model download.
	pullChunkMsg struct {
		name     string
		progress ollama.PullProgress
	}

	// pullEndMsg closes out a download.
	pullEndMsg struct {
		name string
		err  error
	}

	// tickMsg drives the clock, countdowns and the pulsing status dot.
	tickMsg time.Time

	// toastExpireMsg dismisses the toast with the given id, if still current.
	toastExpireMsg int
)

// sideChat marks a stream belonging to the main transcript rather than to one of the comparison columns.
const sideChat = -1

// --- streaming plumbing -----------------------------------------------------

// feed carries streamed chunks from a producer goroutine into the Bubble Tea event loop. The producer sets err (if any) before closing ch, so a consumer that observes the close also observes the final error.
type feed[T any] struct {
	ch     chan T
	cancel context.CancelFunc
	err    error
}

func newFeed[T any](cancel context.CancelFunc) *feed[T] {
	return &feed[T]{ch: make(chan T, 64), cancel: cancel}
}

// stop cancels the producer. Chunks already queued are drained and discarded by the generation check in the update loop.
func (f *feed[T]) stop() {
	if f != nil && f.cancel != nil {
		f.cancel()
	}
}

// --- commands ---------------------------------------------------------------

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (a *App) connectCmd() tea.Cmd {
	return func() tea.Msg {
		v, err := a.client.Version(context.Background())
		return connectedMsg{version: v, err: err}
	}
}

func (a *App) listModelsCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		m, err := a.client.List(ctx)
		return modelsMsg{models: m, err: err}
	}
}

func (a *App) psCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		m, err := a.client.PS(ctx)
		return runningMsg{models: m, err: err}
	}
}

func (a *App) showCmd(name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		info, err := a.client.Show(ctx, name)
		return showMsg{name: name, info: info, err: err}
	}
}

func (a *App) deleteModelCmd(name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := a.client.Delete(ctx, name); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{text: "deleted " + name}
	}
}

func (a *App) unloadCmd(name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := a.client.Unload(ctx, name); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{text: "unloaded " + name}
	}
}

// streamChat starts a streaming completion and returns its feed together with the command that pumps the first chunk into the event loop. Comparison runs need the feed back so they can hold one per column.
func (a *App) streamChat(req ollama.ChatRequest, gen, side int) (*feed[ollama.ChatResponse], tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	f := newFeed[ollama.ChatResponse](cancel)

	go func() {
		defer close(f.ch)
		f.err = a.client.Chat(ctx, req, func(c ollama.ChatResponse) error {
			select {
			case f.ch <- c:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	return f, waitChat(f, gen, side)
}

// startChat kicks off the main transcript's completion.
func (a *App) startChat(req ollama.ChatRequest, gen int) tea.Cmd {
	f, cmd := a.streamChat(req, gen, sideChat)
	a.chatFeed = f
	return cmd
}

// waitChat blocks on the next chat chunk. It is re-issued after each chunk so the event loop stays responsive between tokens.
func waitChat(f *feed[ollama.ChatResponse], gen, side int) tea.Cmd {
	return func() tea.Msg {
		chunk, ok := <-f.ch
		if !ok {
			return chatEndMsg{gen: gen, side: side, err: f.err}
		}
		return chatChunkMsg{gen: gen, side: side, chunk: chunk}
	}
}

// startPull begins a model download and returns the command pumping its progress into the event loop.
func (a *App) startPull(name string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	f := newFeed[ollama.PullProgress](cancel)
	a.pullFeed = f

	go func() {
		defer close(f.ch)
		f.err = a.client.Pull(ctx, name, func(p ollama.PullProgress) error {
			select {
			case f.ch <- p:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	return waitPull(f, name)
}

func waitPull(f *feed[ollama.PullProgress], name string) tea.Cmd {
	return func() tea.Msg {
		p, ok := <-f.ch
		if !ok {
			return pullEndMsg{name: name, err: f.err}
		}
		return pullChunkMsg{name: name, progress: p}
	}
}
