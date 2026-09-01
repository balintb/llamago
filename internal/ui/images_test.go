package ui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/balintb/llamago/internal/config"
	"github.com/balintb/llamago/internal/imaging"
	"github.com/balintb/llamago/internal/ollama"
	"github.com/balintb/llamago/internal/store"
)

// testPNG writes a solid-colour image and returns its path.
func testPNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 4), uint8(y * 4), 200, 255})
		}
	}
	p := filepath.Join(t.TempDir(), "t.png")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return p
}

// appWithImage attaches an image and commits it as a user turn.
func appWithImage(t *testing.T, w, h int) *App {
	t.Helper()
	a := newTestApp(w, h)
	name, err := store.SaveAttachment(testPNG(t, 200, 100))
	if err != nil {
		t.Fatal(err)
	}
	a.cur.Turns = []store.Turn{
		{Role: "user", Content: "what is this?", Images: []string{name}},
		{Role: "assistant", Model: "qwen2.5vl:3b", Content: "a gradient"},
	}
	a.invalidateRenders()
	a.refreshTranscript()
	return a
}

// TestThumbnailRendersInTranscript checks an image becomes styled text that respects the pane, so it scrolls and lays out like everything else.
func TestThumbnailRendersInTranscript(t *testing.T) {
	a := appWithImage(t, 110, 34)

	frame := render(a)
	checkFrame(t, frame, 110, 34, "transcript with an image")
	if !strings.Contains(ansi.Strip(frame), "▀") {
		t.Error("no half-block glyphs, so the thumbnail did not render")
	}
	if !strings.Contains(ansi.Strip(frame), "🖼1") {
		t.Error("thumbnail caption missing its number")
	}
	// Caption must advertise the click action.
	if !strings.Contains(ansi.Strip(frame), "click to save") {
		t.Error("caption does not mention that clicking saves")
	}
}

// TestImagePlacementMatchesRender is the property clicks depend on: the recorded rectangle must line up with where the art actually is.
func TestImagePlacementMatchesRender(t *testing.T) {
	a := appWithImage(t, 110, 34)
	if len(a.placements) != 1 {
		t.Fatalf("got %d placements, want 1", len(a.placements))
	}
	p := a.placements[0]

	lines := strings.Split(a.transcript.GetContent(), "\n")
	if p.line0 < 0 || p.line1 > len(lines) || p.line0 >= p.line1 {
		t.Fatalf("placement lines %d-%d out of range for %d lines", p.line0, p.line1, len(lines))
	}
	// Every line the placement claims must actually hold half blocks.
	for i := p.line0; i < p.line1; i++ {
		if !strings.Contains(lines[i], "▀") {
			t.Errorf("line %d is claimed by the placement but has no image: %q",
				i, ansi.Strip(lines[i]))
		}
	}
	// The line just past it must not.
	if p.line1 < len(lines) && strings.Contains(lines[p.line1], "▀") {
		t.Error("the placement stops short of the image")
	}
}

// TestClickOnImageOpensSavePicker covers the pointer path end to end.
func TestClickOnImageOpensSavePicker(t *testing.T) {
	a := appWithImage(t, 110, 34)
	p := a.placements[0]
	r := a.transcriptRect()

	// Convert a content line back to a screen row the way onClick does.
	screenY := p.line0 - a.transcript.YOffset() + r.y0 + 1
	screenX := p.col0 + r.x0 + 1
	if screenY < r.y0 || screenY >= r.y1 {
		t.Skipf("image is scrolled out of view at this size (row %d)", screenY)
	}

	a.Update(tea.MouseClickMsg{X: screenX, Y: screenY, Button: tea.MouseLeft})
	if a.overlay != overlayPicker {
		t.Fatalf("clicking the image gave overlay %v, want the picker", a.overlay)
	}
	if a.pickerMode != pickSaveDir {
		t.Error("clicking an image should open the directory picker, not the file picker")
	}
	if a.pickerTarget.name != p.ref.name {
		t.Error("the picker targeted a different image")
	}
	if !a.picker.DirAllowed || a.picker.FileAllowed {
		t.Error("a save-destination picker must select directories, not files")
	}
	checkFrame(t, render(a), 110, 34, "save picker open")

	// A click on empty transcript must do nothing.
	a.overlay = overlayNone
	a.Update(tea.MouseClickMsg{X: r.x1 - 2, Y: r.y1 - 2, Button: tea.MouseLeft})
	if a.overlay != overlayNone {
		t.Error("a click away from the image opened something")
	}
}

// TestImagePickerFiltersToImages checks the attach picker only offers images.
func TestImagePickerFiltersToImages(t *testing.T) {
	a := newTestApp(110, 34)
	a.openImagePicker()
	if a.overlay != overlayPicker || a.pickerMode != pickImage {
		t.Fatal("ctrl+i did not open the image picker")
	}
	if !a.picker.FileAllowed || a.picker.DirAllowed {
		t.Error("the attach picker should select files, not directories")
	}
	if len(a.picker.AllowedTypes) == 0 {
		t.Error("the attach picker should filter to image types")
	}
	checkFrame(t, render(a), 110, 34, "attach picker open")
}

// TestAttachRejectsNonImages keeps bad input out of the attachment store.
func TestAttachRejectsNonImages(t *testing.T) {
	a := newTestApp(110, 34)

	notImage := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(notImage, []byte("hello"), 0o644)
	a.attachCmd(notImage)
	if len(a.pending) != 0 {
		t.Error("a text file was accepted as an image")
	}
	if !a.toastErr {
		t.Error("attaching a non-image should report an error")
	}

	// An image extension over corrupt bytes must fail at decode.
	corrupt := filepath.Join(t.TempDir(), "broken.png")
	os.WriteFile(corrupt, []byte("not really a png"), 0o644)
	a.attachCmd(corrupt)
	if len(a.pending) != 0 {
		t.Error("a corrupt image was accepted")
	}
}

// TestPendingAttachmentsClearAndSend covers the queue either side of sending.
func TestPendingAttachmentsClearAndSend(t *testing.T) {
	a := newTestApp(110, 34)
	a.cfg.Model = "qwen2.5vl:3b"
	a.attachCmd(testPNG(t, 40, 40))
	if len(a.pending) != 1 {
		t.Fatalf("attach queued %d images", len(a.pending))
	}
	if !strings.Contains(ansi.Strip(a.viewPendingChips()), "1 image") {
		t.Error("the composer line does not show the queued attachment")
	}

	// Clearing empties the queue.
	a.clearPending()
	if len(a.pending) != 0 {
		t.Error("clear left attachments queued")
	}

	// An image with no text is still a valid prompt.
	a.attachCmd(testPNG(t, 40, 40))
	a.input.SetValue("")
	before := len(a.cur.Turns)
	a.send()
	if len(a.cur.Turns) <= before {
		t.Fatal("sending an image with no text produced no turn")
	}
	// send appends the prompt, then generate opens the reply, so the prompt is the second from the end.
	turn := a.cur.Turns[len(a.cur.Turns)-2]
	if turn.Role != "user" || len(turn.Images) != 1 {
		t.Errorf("sent turn = %+v, want a user turn carrying one image", turn)
	}
	if len(a.pending) != 0 {
		t.Error("the queue should empty once sent")
	}
}

// TestCopyIntoDoesNotClobber checks saving twice keeps both files.
func TestCopyIntoDoesNotClobber(t *testing.T) {
	src := testPNG(t, 20, 20)
	dir := t.TempDir()

	first, err := copyInto(src, dir, "img")
	if err != nil {
		t.Fatal(err)
	}
	second, err := copyInto(src, dir, "img")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("the second save overwrote the first")
	}
	for _, p := range []string{first, second} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s missing after save: %v", p, err)
		}
	}
	if filepath.Ext(first) != ".png" {
		t.Errorf("saved file lost its extension: %s", first)
	}
}

// imageModelApp makes an image generation model the active one.
func imageModelApp(t *testing.T, w, h int) *App {
	t.Helper()
	a := newTestApp(w, h)
	a.models = append(a.models, ollama.Model{Name: "x/flux2-klein:4b", Size: 5_700_000_000})
	a.details["x/flux2-klein:4b"] = &ollama.ShowResponse{Capabilities: []string{"image"}}
	a.setModel("x/flux2-klein:4b")
	a.cur.Turns = nil
	a.invalidateRenders()
	a.refreshTranscript()
	return a
}

// TestImageModelCannotChat checks a generation model is refused before a request is sent, rather than surfacing a bare server error.
func TestImageModelCannotChat(t *testing.T) {
	a := imageModelApp(t, 110, 34)
	if !a.isImageModel(a.cfg.Model) {
		t.Fatal("setup: the model should be detected as image generation")
	}

	a.input.SetValue("draw me a cat")
	before := len(a.cur.Turns)
	a.send()
	if len(a.cur.Turns) != before {
		t.Error("a prompt was queued for a model that cannot chat")
	}
	if !a.toastErr {
		t.Error("sending to a generation model should report why")
	}

	checkFrame(t, render(a), 110, 34, "image model active")
}

// TestImageModelExcludedFromCompare keeps generation models out of a race, which would fail at the server with "does not support chat".
func TestImageModelExcludedFromCompare(t *testing.T) {
	a := imageModelApp(t, 120, 34)
	a.startCompare("llama3.2:3b", "hello")
	if a.comparing {
		t.Error("a race started with a generation model")
	}
	if !a.toastErr {
		t.Error("starting such a race should explain the refusal")
	}

	// It must not be offered as an opponent either.
	a.setModel("llama3.2:3b")
	a.comparePrompt = "hello"
	a.openPaletteMode(paletteCompare)
	for _, c := range a.filteredCommands() {
		if strings.Contains(c.title, "flux2") {
			t.Errorf("the opponent list offers a generation model: %q", c.title)
		}
	}
}

// TestAutoModelSkipsImageModel covers first run on a machine whose first model happens to be a generation one.
func TestAutoModelSkipsImageModel(t *testing.T) {
	a := New(config.Default())
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 34})

	// The server lists the generation model first.
	a.Update(modelsMsg{models: []ollama.Model{
		{Name: "x/flux2-klein:4b"},
		{Name: "phi4-mini:latest"},
	}})
	if a.cfg.Model != "x/flux2-klein:4b" {
		t.Fatalf("setup: expected the first model to be adopted, got %q", a.cfg.Model)
	}

	// Capabilities arrive and the app should move off it on its own.
	a.Update(showMsg{name: "x/flux2-klein:4b",
		info: &ollama.ShowResponse{Capabilities: []string{"image"}}})
	if a.cfg.Model == "x/flux2-klein:4b" {
		t.Error("the app stayed on a model that cannot chat")
	}
	if a.cfg.Model != "phi4-mini:latest" {
		t.Errorf("moved to %q, want the chat-capable model", a.cfg.Model)
	}
}

// TestExplicitImageModelChoiceIsKept checks the auto-switch does not override a deliberate choice.
func TestExplicitImageModelChoiceIsKept(t *testing.T) {
	a := imageModelApp(t, 110, 34) // setModel called directly, so not auto
	if a.autoModel {
		t.Fatal("setup: the model should not be marked as auto-adopted")
	}
	a.Update(showMsg{name: "x/flux2-klein:4b",
		info: &ollama.ShowResponse{Capabilities: []string{"image"}}})
	if a.cfg.Model != "x/flux2-klein:4b" {
		t.Error("an explicitly chosen model was swapped out from under the user")
	}
}

// TestPickerListsFiles is the regression test for the browser showing nothing: its listing arrives as an async message, which has to be routed to it.
func TestPickerListsFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one.png", "two.jpg", "notes.txt"} {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
	}
	os.Mkdir(filepath.Join(dir, "subdir"), 0o755)

	a := newTestApp(110, 34)
	a.picker.CurrentDirectory = dir
	cmd := a.openImagePicker()
	if cmd == nil {
		t.Fatal("opening the picker returned no command to load the directory")
	}

	// Feed the listing back the way the event loop does.
	msg := cmd()
	if msg == nil {
		t.Fatal("the picker's load command produced no message")
	}
	a.Update(msg)

	view := ansi.Strip(a.picker.View())
	if strings.Contains(view, "No Files Found") {
		t.Fatalf("the browser is still empty after its listing arrived:\n%s", view)
	}
	for _, want := range []string{"one.png", "two.jpg", "subdir"} {
		if !strings.Contains(view, want) {
			t.Errorf("browser does not list %q:\n%s", want, view)
		}
	}
	// The whole frame must still hold together with the browser open.
	checkFrame(t, render(a), 110, 34, "picker listing files")
}

// TestPickerNavigatesIntoDirectories checks the browser can actually move around the filesystem rather than being stuck in one folder.
func TestPickerNavigatesIntoDirectories(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "pictures")
	os.Mkdir(sub, 0o755)
	os.WriteFile(filepath.Join(sub, "inside.png"), []byte("x"), 0o644)

	a := newTestApp(110, 34)
	a.picker.CurrentDirectory = root
	if cmd := a.openImagePicker(); cmd != nil {
		if msg := cmd(); msg != nil {
			a.Update(msg)
		}
	}
	if !strings.Contains(ansi.Strip(a.picker.View()), "pictures") {
		t.Fatalf("setup: the subdirectory is not listed:\n%s", ansi.Strip(a.picker.View()))
	}

	// Entering it should load the new directory and eventually show its file.
	for range 8 {
		_, cmd := a.picker.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		a.picker, _ = a.picker.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd != nil {
			if msg := cmd(); msg != nil {
				a.picker, _ = a.picker.Update(msg)
			}
		}
		if strings.Contains(ansi.Strip(a.picker.View()), "inside.png") {
			return
		}
	}
	t.Errorf("could not navigate into a subdirectory:\n%s", ansi.Strip(a.picker.View()))
}

// TestPickerHomeShortcut checks the escape hatch out of a deep directory.
func TestPickerHomeShortcut(t *testing.T) {
	deep := filepath.Join(t.TempDir(), "a", "b", "c")
	os.MkdirAll(deep, 0o755)

	a := newTestApp(110, 34)
	a.picker.CurrentDirectory = deep
	a.openImagePicker()

	a.onPickerKey(tea.KeyPressMsg{Code: '~', Text: "~"})
	home, _ := os.UserHomeDir()
	if a.picker.CurrentDirectory != home {
		t.Errorf("~ went to %q, want %q", a.picker.CurrentDirectory, home)
	}

	// The header must show where you are, shortened at home.
	if got := ansi.Strip(a.viewPicker()); !strings.Contains(got, "~") {
		t.Errorf("picker header does not show the home-relative path:\n%s", got)
	}
	// And it must advertise how to move around.
	for _, key := range []string{"up", "home", "cancel"} {
		if !strings.Contains(ansi.Strip(a.viewPicker()), key) {
			t.Errorf("picker hint does not mention %q", key)
		}
	}
}

// TestPrettyPath covers the home-relative shortening.
func TestPrettyPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got := prettyPath(home); got != "~" {
		t.Errorf("prettyPath(home) = %q, want ~", got)
	}
	if got := prettyPath(filepath.Join(home, "Pictures")); got != "~/Pictures" {
		t.Errorf("prettyPath = %q, want ~/Pictures", got)
	}
	if got := prettyPath("/etc"); got != "/etc" {
		t.Errorf("prettyPath(/etc) = %q, want it unchanged", got)
	}
}

// TestPickerAcceptsUppercaseExtensions is the regression test for "PHOTO.JPG is not an image": the browser matches extensions case-sensitively and greys such files out, so its filter must not be treated as the authority.
func TestPickerAcceptsUppercaseExtensions(t *testing.T) {
	dir := t.TempDir()
	// A real PNG under a variety of spellings, plus a genuine non-image.
	src := testPNG(t, 24, 24)
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"lower.png", "UPPER.PNG", "Mixed.PnG", "shot.JPEG"} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o644)

	// attachCmd updates state directly and returns a toast command; running that command would just block on its dismissal timer.
	visionApp := func() *App {
		a := newTestApp(110, 34)
		a.cfg.Model = "qwen2.5vl:3b"
		a.details["qwen2.5vl:3b"] = &ollama.ShowResponse{
			Capabilities: []string{"completion", "vision"},
		}
		return a
	}

	for _, name := range []string{"lower.png", "UPPER.PNG", "Mixed.PnG", "shot.JPEG"} {
		a := visionApp()
		a.attachCmd(filepath.Join(dir, name))
		if len(a.pending) != 1 {
			t.Errorf("%s was refused: pending=%d", name, len(a.pending))
		}
		if a.toastErr {
			t.Errorf("%s produced an error toast: %q", name, a.toast)
		}
	}

	// A real non-image must still be refused.
	a := visionApp()
	a.attachCmd(filepath.Join(dir, "notes.txt"))
	if len(a.pending) != 0 || !a.toastErr {
		t.Errorf("a text file should still be refused: pending=%d err=%v", len(a.pending), a.toastErr)
	}
}

// TestPickerExtensionsCoverBothCases pins the hint list handed to the browser.
func TestPickerExtensionsCoverBothCases(t *testing.T) {
	got := imaging.PickerExtensions()
	for _, want := range []string{".png", ".PNG", ".jpg", ".JPG", ".jpeg", ".JPEG"} {
		if !slices.Contains(got, want) {
			t.Errorf("PickerExtensions missing %q: %v", want, got)
		}
	}
	// And IsImage stays the authority for spellings the list cannot cover.
	for _, name := range []string{"a.PnG", "b.jPeG", "c.Gif"} {
		if !imaging.IsImage(name) {
			t.Errorf("IsImage(%q) = false", name)
		}
	}
}

// TestImageKeysNeedTranscriptFocus pins the reported behaviour: v and o are transcript keys, so from the composer they are text. The caption must say so rather than promising they work anywhere.
func TestImageKeysNeedTranscriptFocus(t *testing.T) {
	a := appWithImage(t, 110, 34)

	// From the composer they are ordinary characters.
	a.setFocus(focusInput)
	a.onKey(tea.KeyPressMsg{Code: 'v', Text: "v"})
	a.onKey(tea.KeyPressMsg{Code: 'o', Text: "o"})
	if got := a.input.Value(); got != "vo" {
		t.Errorf("composer holds %q, want the typed characters", got)
	}

	// So the caption has to tell the user how to reach them.
	caption := ansi.Strip(render(a))
	if !strings.Contains(caption, "tab") {
		t.Errorf("caption does not say focus must move first:\n%s", caption)
	}

	// With the transcript focused they act instead of typing.
	a.input.Reset()
	a.setFocus(focusTranscript)
	before := a.input.Value()
	a.onKey(tea.KeyPressMsg{Code: 'v', Text: "v"})
	if a.input.Value() != before {
		t.Error("v was typed into the composer while the transcript had focus")
	}
}

// TestViewImageTargetsVisibleOne checks v acts on the image you are looking at rather than always the newest.
func TestViewImageTargetsVisibleOne(t *testing.T) {
	a := newTestApp(110, 24)
	first, err := store.SaveAttachment(testPNG(t, 120, 120))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.SaveAttachment(testPNG(t, 90, 160))
	if err != nil {
		t.Fatal(err)
	}
	a.cur.Turns = []store.Turn{
		{Role: "user", Content: "one", Images: []string{first}},
		{Role: "assistant", Model: "m", Content: strings.Repeat("filler line\n", 20)},
		{Role: "user", Content: "two", Images: []string{second}},
	}
	a.invalidateRenders()
	a.refreshTranscript()
	if len(a.placements) != 2 {
		t.Fatalf("got %d placements, want 2", len(a.placements))
	}

	// Scrolled to the top, the first image is the visible one.
	a.transcript.GotoTop()
	if got := a.visibleImageRef(); got.name != first {
		t.Errorf("at the top, v would target %q, want the first image", got.name)
	}
	// Scrolled to the bottom, the second.
	a.transcript.GotoBottom()
	if got := a.visibleImageRef(); got.name != second {
		t.Errorf("at the bottom, v would target %q, want the second image", got.name)
	}
}

// TestPendingAttachmentShowsInTranscript is the regression test for attaching producing no visible feedback: a queued image must appear in the conversation rather than only on a status line.
func TestPendingAttachmentShowsInTranscript(t *testing.T) {
	dir := t.TempDir()
	data, _ := os.ReadFile(testPNG(t, 160, 90))
	shot := filepath.Join(dir, "Screenshot.png")
	os.WriteFile(shot, data, 0o644)

	a := newTestApp(110, 30)
	a.cur.Turns = nil
	a.invalidateRenders()
	a.refreshTranscript()
	before := ansi.Strip(render(a))
	if strings.Contains(before, "attaching") {
		t.Fatal("setup: nothing should be attached yet")
	}

	a.attachCmd(shot)

	got := ansi.Strip(render(a))
	if !strings.Contains(got, "attaching") {
		t.Errorf("the transcript does not announce the attachment:\n%s", got)
	}
	if !strings.Contains(got, "Screenshot.png") {
		t.Error("the transcript does not name the attached file")
	}
	if !strings.Contains(got, "▀") {
		t.Error("no thumbnail rendered for the queued attachment")
	}
	if !strings.Contains(got, "not sent yet") {
		t.Error("nothing marks the attachment as unsent")
	}
	checkFrame(t, render(a), 110, 30, "pending attachment")

	// It is clickable like any other image.
	if len(a.placements) != 1 {
		t.Fatalf("got %d placements for the queued image, want 1", len(a.placements))
	}
	if a.placements[0].turnRef() != -1 {
		t.Error("a queued image should not claim to belong to a turn")
	}

	// Clearing removes it from the conversation again.
	a.clearPending()
	if got := ansi.Strip(render(a)); strings.Contains(got, "attaching") {
		t.Errorf("clearing left the attachment on screen:\n%s", got)
	}
}

// TestPendingMovesIntoTheSentTurn checks the queued block becomes a real turn.
func TestPendingMovesIntoTheSentTurn(t *testing.T) {
	a := newTestApp(110, 30)
	a.cfg.Model = "qwen2.5vl:3b"
	a.details["qwen2.5vl:3b"] = &ollama.ShowResponse{Capabilities: []string{"completion", "vision"}}
	a.cur.Turns = nil
	a.attachCmd(testPNG(t, 80, 80))
	a.input.SetValue("what is this?")
	a.send()

	if len(a.pending) != 0 {
		t.Error("the queue should empty on send")
	}
	if got := ansi.Strip(render(a)); strings.Contains(got, "not sent yet") {
		t.Error("the transcript still shows the attachment as unsent")
	}
	turn := a.cur.Turns[0]
	if turn.Role != "user" || len(turn.Images) != 1 {
		t.Fatalf("sent turn = %+v, want a user turn with one image", turn)
	}
	// And it still renders, now as part of the conversation.
	if !strings.Contains(ansi.Strip(render(a)), "🖼1") {
		t.Error("the sent image lost its numbered caption")
	}
}
