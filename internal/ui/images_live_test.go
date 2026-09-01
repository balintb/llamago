package ui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/balintb/llamago/internal/store"
)

// redCirclePNG writes an unmistakable test image: a red circle on white.
func redCirclePNG(t *testing.T) string {
	t.Helper()
	const size = 320
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	cx, cy, r := size/2, size/2, size/3
	for y := range size {
		for x := range size {
			c := color.RGBA{255, 255, 255, 255}
			if dx, dy := x-cx, y-cy; dx*dx+dy*dy <= r*r {
				c = color.RGBA{220, 20, 20, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	path := filepath.Join(t.TempDir(), "circle.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}

// visionModel finds an installed model that reports the vision capability.
func visionModel(t *testing.T, a *App, deadline time.Time) string {
	t.Helper()
	pump(t, a, a.fetchMissingDetails(), deadline, nil)
	for _, m := range a.models {
		if d, ok := a.details[m.Name]; ok && d.CanVision() && !d.CanImage() {
			return m.Name
		}
	}
	return ""
}

// TestLiveVisionRoundTrip attaches an image and checks the model actually saw it: the whole path from attachment store to base64 on the wire.
func TestLiveVisionRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a model into memory")
	}
	a := liveApp(t, 120, 40)
	deadline := time.Now().Add(6 * time.Minute)
	pump(t, a, tea.Batch(a.connectCmd(), a.listModelsCmd()), deadline, nil)

	model := visionModel(t, a, deadline)
	if model == "" {
		t.Skip("no installed model reports the vision capability")
	}
	a.setModel(model)
	a.cfg.Think = false
	a.cfg.NumPredict = 120
	t.Logf("vision model: %s", model)

	// Attach through the same path the picker uses.
	if cmd := a.attachCmd(redCirclePNG(t)); cmd != nil {
		cmd()
	}
	if len(a.pending) != 1 {
		t.Fatalf("attachment not queued: %v", a.pending)
	}

	a.input.SetValue("What shape and colour is in this image? Answer in one short sentence.")
	pump(t, a, a.send(), deadline, func(m tea.Msg) bool {
		_, done := m.(chatEndMsg)
		return done
	})

	if len(a.pending) != 0 {
		t.Error("pending attachments should clear once sent")
	}
	if len(a.cur.Turns) != 2 {
		t.Fatalf("got %d turns, want a prompt and a reply", len(a.cur.Turns))
	}
	prompt, reply := a.cur.Turns[0], a.cur.Turns[1]
	if len(prompt.Images) != 1 {
		t.Fatalf("the prompt turn carries %d images, want 1", len(prompt.Images))
	}
	if reply.Err != "" {
		t.Fatalf("generation failed: %s", reply.Err)
	}

	answer := strings.ToLower(reply.Content)
	t.Logf("reply: %q", strings.Join(strings.Fields(reply.Content), " "))
	// "circle", "circular", "round", "dot", "sphere" all count: the point is that the model described the shape at all, which it cannot do unless the image reached it.
	shaped := false
	for _, word := range []string{"circ", "round", "dot", "sphere", "ball", "disc"} {
		if strings.Contains(answer, word) {
			shaped = true
			break
		}
	}
	if !shaped {
		t.Errorf("the model did not describe the shape, so it likely never saw the image: %q", answer)
	}
	if !strings.Contains(answer, "red") {
		t.Errorf("the model did not mention the colour: %q", answer)
	}

	// The thumbnail must render and be clickable at the right spot.
	a.invalidateRenders()
	a.refreshTranscript()
	if len(a.placements) != 1 {
		t.Fatalf("got %d image placements, want 1", len(a.placements))
	}
	checkFrame(t, render(a), 120, 40, "transcript with an image")
}

// TestLiveVisionSurvivesReload checks an attachment outlives the session file, which is the point of storing it outside the JSON.
func TestLiveVisionSurvivesReload(t *testing.T) {
	name, err := store.SaveAttachment(redCirclePNG(t))
	if err != nil {
		t.Fatal(err)
	}
	data, err := store.AttachmentData(name)
	if err != nil {
		t.Fatalf("attachment unreadable after saving: %v", err)
	}
	if len(data) == 0 {
		t.Error("attachment is empty")
	}

	// The same bytes must not be stored twice.
	again, err := store.SaveAttachment(redCirclePNG(t))
	if err != nil {
		t.Fatal(err)
	}
	if again != name {
		t.Errorf("identical images stored under two names: %q and %q", name, again)
	}

	// A session round-trips its attachment names, and Messages turns them into base64 for the wire.
	s := &store.Session{Turns: []store.Turn{{Role: "user", Content: "look", Images: []string{name}}}}
	msgs := s.Messages("")
	if len(msgs) != 1 || len(msgs[0].Images) != 1 {
		t.Fatalf("messages carried %d images", len(msgs[0].Images))
	}
	if len(msgs[0].Images[0]) < 100 {
		t.Error("image did not encode to base64 for the request")
	}

	// A name that tries to climb out of the store must be refused.
	if _, err := store.AttachmentPath("../../etc/passwd"); err == nil {
		t.Error("a traversing attachment name was accepted")
	}
}
