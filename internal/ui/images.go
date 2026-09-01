package ui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/balintb/llamago/internal/imaging"
	"github.com/balintb/llamago/internal/store"
	"github.com/balintb/llamago/internal/theme"
)

// Thumbnails are capped so one screenshot cannot swallow the transcript.
const (
	thumbMaxCols = 48
	thumbMaxRows = 16
)

// attachment is an image queued for the next prompt. The stored name is a content digest, so the original file name is kept alongside it for display.
type attachment struct {
	name  string
	label string
}

// attachmentNames is the wire form: just the stored names.
func attachmentNames(as []attachment) []string {
	if len(as) == 0 {
		return nil
	}
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.name
	}
	return out
}

// imageRef points at one image in the conversation.
type imageRef struct {
	turn int
	name string // attachment name
}

// imagePlacement is where a thumbnail landed in the rendered transcript, in content lines and columns. Clicks are resolved against these.
type imagePlacement struct {
	ref          imageRef
	line0, line1 int // content line range, half open
	col0, col1   int
}

// turnRef is the turn a placement belongs to, or -1 for a queued attachment that has not been sent yet.
func (p imagePlacement) turnRef() int { return p.ref.turn }

// --- attachments ------------------------------------------------------------

// attachCmd copies the chosen file into the attachment store and queues it for the next prompt.
func (a *App) attachCmd(path string) tea.Cmd {
	if !imaging.IsImage(path) {
		return a.showToast("not an image: "+filepath.Base(path), true)
	}
	// Decode before accepting, so a broken file fails now rather than at request time.
	if _, err := imaging.Load(path); err != nil {
		return a.errToast(err)
	}
	name, err := store.SaveAttachment(path)
	if err != nil {
		return a.errToast(err)
	}
	a.pending = append(a.pending, attachment{name: name, label: filepath.Base(path)})
	// Show it in the transcript straight away: a queued attachment the user cannot see is indistinguishable from one that failed to attach.
	a.refreshTranscript()

	msg := fmt.Sprintf("attached %s", filepath.Base(path))
	if !a.modelCanVision(a.cfg.Model) {
		// Warn rather than refuse: capabilities are cached lazily and the user may be about to switch models.
		return a.showToast(msg+" - but "+shortModel(a.cfg.Model)+" has no vision support", true)
	}
	return a.okToast(msg)
}

// modelCanVision reports whether the server said the model accepts images.
func (a *App) modelCanVision(name string) bool {
	d, ok := a.details[name]
	return ok && d.CanVision()
}

// clearPending drops queued attachments.
func (a *App) clearPending() tea.Cmd {
	if len(a.pending) == 0 {
		return nil
	}
	n := len(a.pending)
	a.pending = nil
	a.refreshTranscript()
	return a.okToast(fmt.Sprintf("cleared %d attachment(s)", n))
}

// viewPendingChips is the composer-line summary of queued attachments.
func (a *App) viewPendingChips() string {
	if len(a.pending) == 0 {
		return ""
	}
	label := fmt.Sprintf("📎 %d image", len(a.pending))
	if len(a.pending) > 1 {
		label += "s"
	}
	return lipgloss.NewStyle().Foreground(theme.Cyan).Render(label) +
		theme.Dim.Render(" ready to send · ctrl+x clear")
}

// renderPending draws the queued attachments at the foot of the transcript, so what is about to be sent is visible in the conversation rather than only announced on a status line. Placements are recorded relative to the block.
func (a *App) renderPending(width int) (string, []imagePlacement) {
	if len(a.pending) == 0 {
		return "", nil
	}
	bar := lipgloss.NewStyle().Foreground(theme.Cyan).Render("▌")
	head := bar + " " + lipgloss.NewStyle().Foreground(theme.Cyan).Bold(true).Render("attaching") +
		theme.Dim.Render("  not sent yet · ") + theme.Key.Render("↵") + theme.Dim.Render(" to send · ") +
		theme.Key.Render("ctrl+x") + theme.Dim.Render(" to clear")

	parts := []string{head}
	var placed []imagePlacement
	for _, att := range a.pending {
		art, cols, rows := a.renderThumbnail(att.name, 0, max(8, width-2))

		line0 := 0
		for _, p := range parts {
			line0 += lipgloss.Height(p)
		}
		placed = append(placed, imagePlacement{
			ref:   imageRef{turn: -1, name: att.name},
			line0: line0, line1: line0 + rows,
			col0: 2, col1: 2 + cols,
		})
		parts = append(parts, indent(art, "  "))
		parts = append(parts, "  "+lipgloss.NewStyle().Foreground(theme.Cyan).
			Render(theme.Truncate("📎 "+att.label, max(1, width-2))))
	}
	return strings.Join(parts, "\n"), placed
}

// --- rendering --------------------------------------------------------------

// graphicsProtocol is the protocol to use for full resolution viewing, honoring the config override.
func (a *App) graphicsProtocol() imaging.Protocol {
	if p, forced := imaging.ParseProtocol(a.cfg.Graphics); forced {
		return p
	}
	return a.detectedGraphics
}

// renderThumbnail draws one attachment as half blocks, with a caption. The result is plain styled text, so it scrolls, searches and exports like the rest of the transcript.
func (a *App) renderThumbnail(name string, n, maxCols int) (body string, cols, rows int) {
	key := fmt.Sprintf("%s\x00%d\x00%d", name, maxCols, n)
	if hit, ok := a.thumbCache[key]; ok {
		return hit.body, hit.cols, hit.rows
	}

	path, err := store.AttachmentPath(name)
	if err != nil {
		return theme.Err.Render("✗ " + err.Error()), maxCols, 1
	}
	img, err := imaging.Load(path)
	if err != nil {
		return theme.Err.Render("✗ " + err.Error()), maxCols, 1
	}

	cols, rows = imaging.FitCells(img, min(maxCols, thumbMaxCols), thumbMaxRows)
	pixels := img.Bounds()
	art := imaging.HalfBlocks(img, cols, rows)

	// A queued attachment has no index yet; its file name is captioned beneath it by renderPending instead.
	if n == 0 {
		body = art
		a.thumbCache[key] = thumb{body: body, cols: cols, rows: rows}
		return body, cols, rows
	}
	// Say what actually works from where the user is: v and o are transcript keys, and the composer holds focus by default, so promising them bare meant they were typed into the prompt instead.
	caption := lipgloss.NewStyle().Foreground(theme.Teal).Render(fmt.Sprintf("🖼%d", n)) +
		theme.Dim.Render(fmt.Sprintf(" %d×%d · click to save · ", pixels.Dx(), pixels.Dy())) +
		theme.Key.Render("tab") + theme.Dim.Render(" then ") +
		theme.Key.Render("v") + theme.Dim.Render("/") + theme.Key.Render("o") +
		theme.Dim.Render(" to view/open")

	body = art + "\n" + theme.Truncate(caption, maxCols)
	a.thumbCache[key] = thumb{body: body, cols: cols, rows: rows}
	return body, cols, rows
}

// images lists every attachment in the conversation, in order, numbered the way the captions are.
func (a *App) images() []imageRef {
	if a.cur == nil {
		return nil
	}
	var out []imageRef
	for i, t := range a.cur.Turns {
		for _, name := range t.Images {
			out = append(out, imageRef{turn: i, name: name})
		}
	}
	return out
}

// --- actions ----------------------------------------------------------------

// viewImageCmd shows an image at full resolution.
//
// Bubble Tea renders through a cell buffer with no way to embed raw graphics bytes, so this borrows the terminal through tea.Exec: the program pauses, the escape sequence goes straight to the tty, and a keypress hands it back. Where there is no graphics protocol, fall back to the platform's image viewer.
func (a *App) viewImageCmd(ref imageRef) tea.Cmd {
	path, err := store.AttachmentPath(ref.name)
	if err != nil {
		return a.errToast(err)
	}
	proto := a.graphicsProtocol()
	if proto == imaging.None {
		return a.openImageCmd(ref)
	}
	img, err := imaging.Load(path)
	if err != nil {
		return a.errToast(err)
	}
	payload, err := imaging.Encode(img, proto)
	if err != nil || payload == "" {
		return a.openImageCmd(ref)
	}
	b := img.Bounds()
	label := fmt.Sprintf("%s  %d×%d  (%s)", ref.name[:min(12, len(ref.name))], b.Dx(), b.Dy(), proto)
	return tea.Exec(&fullscreenImage{payload: payload, label: label}, func(err error) tea.Msg {
		return actionMsg{text: "", err: err}
	})
}

// openImageCmd hands the file to the platform's image viewer.
func (a *App) openImageCmd(ref imageRef) tea.Cmd {
	path, err := store.AttachmentPath(ref.name)
	if err != nil {
		return a.errToast(err)
	}
	return func() tea.Msg {
		// Attachments are content-addressed and extensionless-looking to a viewer, so hand over a temp copy with a friendly name.
		tmp, err := tempCopy(path)
		if err != nil {
			return actionMsg{err: err}
		}
		if err := openExternally(tmp); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{text: "opened " + filepath.Base(tmp)}
	}
}

// saveImageCmd copies an image into dir under a readable name.
func (a *App) saveImageCmd(ref imageRef, dir string) tea.Cmd {
	src, err := store.AttachmentPath(ref.name)
	if err != nil {
		return a.errToast(err)
	}
	return func() tea.Msg {
		dest, err := copyInto(src, dir, "llamago-image")
		if err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{text: "saved to " + dest}
	}
}

// tempCopy writes a copy of path into the temp directory, keeping its extension.
func tempCopy(path string) (string, error) {
	return copyInto(path, os.TempDir(), "llamago")
}

// copyInto copies src into dir, adding a numeric suffix rather than clobbering a file that is already there.
func copyInto(src, dir, stem string) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ext := filepath.Ext(src)
	dest := filepath.Join(dir, stem+ext)
	for i := 1; ; i++ {
		if _, err := os.Stat(dest); os.IsNotExist(err) {
			break
		}
		if i > 999 {
			return "", fmt.Errorf("too many files named %s* in %s", stem, dir)
		}
		dest = filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, i, ext))
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return "", err
	}
	return dest, nil
}

// openExternally launches the platform's default handler for a file.
func openExternally(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}

// fullscreenImage paints an image over the whole terminal until a key is pressed. It satisfies tea.ExecCommand, which is the supported way to take the tty from the renderer for a moment.
type fullscreenImage struct {
	payload string
	label   string

	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func (v *fullscreenImage) SetStdin(r io.Reader)  { v.stdin = r }
func (v *fullscreenImage) SetStdout(w io.Writer) { v.stdout = w }
func (v *fullscreenImage) SetStderr(w io.Writer) { v.stderr = w }

func (v *fullscreenImage) Run() error {
	if v.stdout == nil {
		return nil
	}
	// Clear, draw, then wait. \r\n throughout because the terminal may still be in raw mode, where a bare \n does not return the carriage.
	fmt.Fprint(v.stdout, "\x1b[2J\x1b[H")
	fmt.Fprint(v.stdout, v.payload)
	fmt.Fprintf(v.stdout, "\r\n\r\n  %s\r\n  press enter to return\r\n", v.label)

	if v.stdin != nil {
		buf := make([]byte, 1)
		for {
			n, err := v.stdin.Read(buf)
			if err != nil || n == 0 {
				break
			}
			if buf[0] == '\n' || buf[0] == '\r' || buf[0] == 'q' || buf[0] == 27 {
				break
			}
		}
	}
	fmt.Fprint(v.stdout, "\x1b[2J\x1b[H")
	return nil
}

// --- picker -----------------------------------------------------------------

// openImagePicker browses for an image to attach.
func (a *App) openImagePicker() tea.Cmd {
	a.pickerMode = pickImage
	a.picker.AllowedTypes = imaging.PickerExtensions()
	a.picker.DirAllowed = false
	a.picker.FileAllowed = true
	return a.openPicker()
}

// openSaveDirPicker browses for a directory to save an image into.
func (a *App) openSaveDirPicker(ref imageRef) tea.Cmd {
	a.pickerMode = pickSaveDir
	a.pickerTarget = ref
	// No type filter: every directory must be reachable, and files are only shown so the user can see what is already there.
	a.picker.AllowedTypes = nil
	a.picker.DirAllowed = true
	a.picker.FileAllowed = false
	if a.cfg.SaveDir != "" {
		a.picker.CurrentDirectory = a.cfg.SaveDir
	}
	return a.openPicker()
}

func (a *App) openPicker() tea.Cmd {
	a.overlay = overlayPicker
	a.picker.SetHeight(max(3, min(14, a.height-10)))
	return a.picker.Init()
}

// onPickerKey drives the browser and acts on a selection.
func (a *App) onPickerKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+f":
		// The picker binds esc to "go up a level" as well; closing the modal is the more useful meaning here, and h/backspace still walk upwards.
		a.overlay = overlayNone
		return nil
	case "~":
		// Jumping home is the one shortcut worth having: a browser that starts deep in a temp directory is tedious to climb out of.
		if home, err := os.UserHomeDir(); err == nil {
			a.picker.CurrentDirectory = home
			return a.picker.Init()
		}
		return nil
	case "ctrl+s":
		// Saving needs a way to accept the directory being browsed, since the picker only reports a selection when entering one.
		if a.pickerMode == pickSaveDir {
			return a.acceptSaveDir(a.picker.CurrentDirectory)
		}
	}

	var cmd tea.Cmd
	a.picker, cmd = a.picker.Update(msg)

	if ok, path := a.picker.DidSelectFile(msg); ok {
		a.overlay = overlayNone
		switch a.pickerMode {
		case pickSaveDir:
			return a.acceptSaveDir(path)
		case pickText:
			return tea.Batch(cmd, a.attachText(path))
		}
		return tea.Batch(cmd, a.attachCmd(path))
	}
	if ok, path := a.picker.DidSelectDisabledFile(msg); ok {
		// The extension list is a hint: anything that turns out to be text is worth inlining, and source trees are full of unlisted extensions.
		if a.pickerMode == pickText {
			a.overlay = overlayNone
			return tea.Batch(cmd, a.attachText(path))
		}
		// The browser matches extensions case-sensitively, so it greys out perfectly good files like PHOTO.JPG. Its filter is a hint; trust the decoder instead and take anything that really is an image.
		if imaging.IsImage(path) {
			a.overlay = overlayNone
			return tea.Batch(cmd, a.attachCmd(path))
		}
		return tea.Batch(cmd, a.showToast("not an image: "+filepath.Base(path), true))
	}
	return cmd
}

// acceptSaveDir remembers the chosen directory and copies the image there.
func (a *App) acceptSaveDir(dir string) tea.Cmd {
	a.overlay = overlayNone
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		dir = filepath.Dir(dir)
	}
	a.cfg.SaveDir = dir
	_ = a.cfg.Save()
	return a.saveImageCmd(a.pickerTarget, dir)
}

// viewPicker renders the browser.
func (a *App) viewPicker() string {
	width := a.pickerWidth()
	inner := modalInner(width)

	title := "Attach an image"
	action := theme.Key.Render("↵") + theme.Dim.Render(" choose")
	if a.pickerMode == pickSaveDir {
		title = "Save image to which folder?"
		action = theme.Key.Render("↵") + theme.Dim.Render(" enter folder · ") +
			theme.Key.Render("ctrl+s") + theme.Dim.Render(" save here")
	}
	// Spell out how to move around: the browser's own bindings are not discoverable, and esc closes this rather than walking up a level.
	nav := theme.Key.Render("↑↓") + theme.Dim.Render(" move · ") +
		theme.Key.Render("h") + theme.Dim.Render("/") + theme.Key.Render("⌫") +
		theme.Dim.Render(" up · ") + theme.Key.Render("~") + theme.Dim.Render(" home · ") +
		theme.Key.Render("esc") + theme.Dim.Render(" cancel")

	rows := []string{
		theme.Dim.Render("in ") + lipgloss.NewStyle().Foreground(theme.Cyan).
			Render(theme.Truncate(prettyPath(a.picker.CurrentDirectory), inner-3)),
		"",
		a.picker.View(),
		"",
		theme.Truncate(action+theme.Dim.Render(" · ")+nav, inner),
	}
	return modal(title, strings.Join(rows, "\n"), width)
}

// prettyPath shortens a home-relative path with a tilde, so the header shows where you are rather than a wall of prefix.
func prettyPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
		return "~/" + rest
	}
	return p
}

func (a *App) pickerWidth() int { return max(44, min(78, a.width-8)) }

// viewImageAt acts on the image nearest the top of the visible transcript, falling back to the most recent one. Scrolling to an image and pressing v should act on that image, not on whichever happens to be last.
func (a *App) viewImageAt(external bool) tea.Cmd {
	if len(a.images()) == 0 {
		return a.showToast("no images in this conversation", true)
	}
	ref := a.visibleImageRef()
	if external {
		return a.openImageCmd(ref)
	}
	return a.viewImageCmd(ref)
}

// visibleImageRef is the image nearest the top of the visible transcript, falling back to the most recent one when none is on screen.
func (a *App) visibleImageRef() imageRef {
	imgs := a.images()
	if len(imgs) == 0 {
		return imageRef{}
	}
	ref := imgs[len(imgs)-1]

	top := a.transcript.YOffset()
	bottom := top + a.transcript.Height()
	best := -1
	for _, p := range a.placements {
		// Any part on screen counts; prefer the highest such image.
		if p.line1 > top && p.line0 < bottom && (best < 0 || p.line0 < best) {
			best, ref = p.line0, p.ref
		}
	}
	return ref
}
