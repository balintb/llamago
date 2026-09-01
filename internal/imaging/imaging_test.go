package imaging

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// fixture writes a test PNG and returns its path.
func fixture(t *testing.T, w, h int, fn func(x, y int) color.RGBA) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, fn(x, y))
		}
	}
	path := filepath.Join(t.TempDir(), "fixture.png")
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

func TestLoadAndIsImage(t *testing.T) {
	path := fixture(t, 8, 8, func(x, y int) color.RGBA { return color.RGBA{255, 0, 0, 255} })
	img, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := img.Bounds(); got.Dx() != 8 || got.Dy() != 8 {
		t.Errorf("bounds = %v, want 8x8", got)
	}

	for _, ok := range []string{"a.png", "B.JPG", "x.jpeg", "y.gif"} {
		if !IsImage(ok) {
			t.Errorf("IsImage(%q) = false", ok)
		}
	}
	for _, no := range []string{"a.txt", "b", "c.go", "d.pngx"} {
		if IsImage(no) {
			t.Errorf("IsImage(%q) = true", no)
		}
	}

	if _, err := Load(filepath.Join(t.TempDir(), "missing.png")); err == nil {
		t.Error("loading a missing file should fail")
	}
	// A file that is not an image must fail cleanly rather than panic.
	bad := filepath.Join(t.TempDir(), "bad.png")
	os.WriteFile(bad, []byte("not a png"), 0o644)
	if _, err := Load(bad); err == nil {
		t.Error("loading a corrupt image should fail")
	}
}

func TestResizeAveragesAndSizes(t *testing.T) {
	// Left half red, right half blue.
	src := image.NewRGBA(image.Rect(0, 0, 100, 50))
	for y := range 50 {
		for x := range 100 {
			c := color.RGBA{255, 0, 0, 255}
			if x >= 50 {
				c = color.RGBA{0, 0, 255, 255}
			}
			src.SetRGBA(x, y, c)
		}
	}

	out := Resize(src, 10, 4)
	if got := out.Bounds(); got.Dx() != 10 || got.Dy() != 4 {
		t.Fatalf("resized to %v, want 10x4", got)
	}
	if r, _, _, _ := out.At(0, 0).RGBA(); r>>8 < 200 {
		t.Error("left edge lost its red")
	}
	if _, _, b, _ := out.At(9, 0).RGBA(); b>>8 < 200 {
		t.Error("right edge lost its blue")
	}

	// Degenerate sizes must not panic.
	if got := Resize(src, 0, 5).Bounds(); !got.Empty() {
		t.Errorf("zero width gave %v", got)
	}
	Resize(image.NewRGBA(image.Rect(0, 0, 0, 0)), 4, 4)
}

func TestFitCellsPreservesAspect(t *testing.T) {
	cases := []struct {
		name         string
		w, h         int
		maxC, maxR   int
		wantC, wantR int
	}{
		// Square: half as many rows as columns, since a cell holds two pixels.
		{"square", 100, 100, 40, 100, 40, 20},
		// Wide: shorter still.
		{"wide", 200, 50, 40, 100, 40, 5},
		// Tall image clamped by the row budget, and narrowed to keep aspect.
		{"tall clamped", 50, 200, 40, 10, 5, 10},
	}
	for _, tc := range cases {
		img := image.NewRGBA(image.Rect(0, 0, tc.w, tc.h))
		c, r := FitCells(img, tc.maxC, tc.maxR)
		if c != tc.wantC || r != tc.wantR {
			t.Errorf("%s: FitCells = %dx%d, want %dx%d", tc.name, c, r, tc.wantC, tc.wantR)
		}
		if c > tc.maxC || r > tc.maxR {
			t.Errorf("%s: %dx%d exceeds the budget %dx%d", tc.name, c, r, tc.maxC, tc.maxR)
		}
	}

	if c, r := FitCells(image.NewRGBA(image.Rect(0, 0, 0, 0)), 10, 10); c != 0 || r != 0 {
		t.Errorf("empty image gave %dx%d", c, r)
	}
}

func TestHalfBlocksGeometry(t *testing.T) {
	// A vertical split so the two halves must differ.
	src := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for y := range 20 {
		for x := range 20 {
			c := color.RGBA{0, 200, 0, 255}
			if y >= 10 {
				c = color.RGBA{0, 0, 200, 255}
			}
			src.SetRGBA(x, y, c)
		}
	}

	const cols, rows = 12, 6
	out := HalfBlocks(src, cols, rows)
	lines := strings.Split(out, "\n")
	if len(lines) != rows {
		t.Fatalf("got %d lines, want %d", len(lines), rows)
	}
	for i, line := range lines {
		// Every line must be exactly cols cells wide, or it breaks the layout.
		if got := ansi.StringWidth(line); got != cols {
			t.Errorf("line %d is %d cells, want %d", i, got, cols)
		}
		if !strings.Contains(line, "▀") {
			t.Errorf("line %d has no half-block glyphs", i)
		}
		// Each line must reset, so styling cannot bleed into the next row.
		if !strings.HasSuffix(line, "\x1b[m") {
			t.Errorf("line %d does not reset its style", i)
		}
	}
	if HalfBlocks(src, 0, 5) != "" || HalfBlocks(src, 5, 0) != "" {
		t.Error("a zero dimension should render nothing")
	}
}

func TestDetectProtocol(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		want Protocol
	}{
		{"kitty by term", []string{"TERM=xterm-kitty"}, Kitty},
		{"kitty by window id", []string{"TERM=xterm-256color", "KITTY_WINDOW_ID=1"}, Kitty},
		{"ghostty", []string{"TERM_PROGRAM=ghostty"}, Kitty},
		{"wezterm", []string{"TERM_PROGRAM=WezTerm"}, Kitty},
		{"iterm", []string{"TERM_PROGRAM=iTerm.app"}, ITerm},
		{"sixel term", []string{"TERM=xterm-sixel"}, Sixel},
		{"plain xterm", []string{"TERM=xterm-256color"}, None},
		{"apple terminal", []string{"TERM_PROGRAM=Apple_Terminal", "TERM=xterm-256color"}, None},
		{"nothing", nil, None},
		// A multiplexer needs passthrough wrapping, so stay text-only.
		{"tmux hides kitty", []string{"TERM=xterm-kitty", "TMUX=/tmp/x"}, None},
		{"screen", []string{"TERM=screen-256color", "KITTY_WINDOW_ID=1"}, None},
	}
	for _, tc := range cases {
		if got := Detect(tc.env); got != tc.want {
			t.Errorf("%s: Detect = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestParseProtocol(t *testing.T) {
	for _, tc := range []struct {
		in    string
		want  Protocol
		force bool
	}{
		{"auto", None, false},
		{"", None, false},
		{"kitty", Kitty, true},
		{"iterm2", ITerm, true},
		{"sixel", Sixel, true},
		{"none", None, true},
		{"blocks", None, true},
		{"nonsense", None, false},
	} {
		got, forced := ParseProtocol(tc.in)
		if got != tc.want || forced != tc.force {
			t.Errorf("ParseProtocol(%q) = %v,%v, want %v,%v", tc.in, got, forced, tc.want, tc.force)
		}
	}
}

func TestEncodeProducesProtocolPayloads(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 16), uint8(y * 16), 128, 255})
		}
	}

	for _, tc := range []struct {
		p      Protocol
		prefix string
	}{
		{Kitty, "\x1b_G"},    // APC, kitty graphics
		{Sixel, "\x1bP"},     // DCS, sixel
		{ITerm, "\x1b]1337"}, // OSC 1337, iTerm2 inline image
	} {
		out, err := Encode(img, tc.p)
		if err != nil {
			t.Errorf("%v: %v", tc.p, err)
			continue
		}
		if out == "" {
			t.Errorf("%v: encoded to nothing", tc.p)
			continue
		}
		if !strings.HasPrefix(out, tc.prefix) {
			t.Errorf("%v: payload starts %q, want prefix %q", tc.p, out[:min(12, len(out))], tc.prefix)
		}
	}

	// None has no native form; half blocks are the only option there.
	if out, err := Encode(img, None); err != nil || out != "" {
		t.Errorf("Encode(None) = %q, %v; want empty", out, err)
	}
}

// sgrPattern matches a full foreground+background pair.
var sgrPattern = regexp.MustCompile(`\x1b\[38;2;(\d+);(\d+);(\d+)`)

// firstDrawnColor returns the first foreground colour in the rendering.
func firstDrawnColor(t *testing.T, out string) color.RGBA {
	t.Helper()
	m := sgrPattern.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no colour in output: %q", out)
	}
	var c color.RGBA
	for i, p := range []*uint8{&c.R, &c.G, &c.B} {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			t.Fatal(err)
		}
		*p = uint8(n)
	}
	return c
}

// TestHalfBlocksColorsMatchSource is the regression test for images rendering black. Geometry alone never caught it: every cell was the right shape and the wrong colour.
func TestHalfBlocksColorsMatchSource(t *testing.T) {
	want := color.RGBA{220, 30, 40, 255}

	// The decoders hand back different concrete types depending on the file, and each has its own path through RGBA(); all must survive the round trip.
	cases := map[string]image.Image{
		"RGBA":  image.NewRGBA(image.Rect(0, 0, 16, 16)),
		"NRGBA": image.NewNRGBA(image.Rect(0, 0, 16, 16)),
	}
	for name, img := range cases {
		switch v := img.(type) {
		case *image.RGBA:
			for y := range 16 {
				for x := range 16 {
					v.SetRGBA(x, y, want)
				}
			}
		case *image.NRGBA:
			for y := range 16 {
				for x := range 16 {
					v.SetNRGBA(x, y, color.NRGBA{want.R, want.G, want.B, 255})
				}
			}
		}
		got := firstDrawnColor(t, HalfBlocks(img, 8, 4))
		if got.R != want.R || got.G != want.G || got.B != want.B {
			t.Errorf("%s: rendered %v, want %v", name, got, want)
		}
		if got.R == 0 && got.G == 0 && got.B == 0 {
			t.Errorf("%s: rendered black", name)
		}
	}
}

// TestHalfBlocksTransparency covers the case that produced the all-black screenshot: a PNG with an alpha channel. Clear pixels must be left to the terminal background rather than painted black.
func TestHalfBlocksTransparency(t *testing.T) {
	// Opaque red on the left, fully clear on the right, as a window screenshot with rounded corners looks.
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := range 32 {
		for x := range 32 {
			c := color.NRGBA{220, 30, 40, 255}
			if x >= 16 {
				c = color.NRGBA{220, 30, 40, 0}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	out := HalfBlocks(img, 16, 4)

	// The opaque half keeps its colour.
	if got := firstDrawnColor(t, out); got.R < 200 || got.G > 60 {
		t.Errorf("opaque half rendered %v, want red", got)
	}
	// Nothing may be painted pure black, which is what clear pixels used to be.
	for _, m := range sgrPattern.FindAllStringSubmatch(out, -1) {
		if m[1] == "0" && m[2] == "0" && m[3] == "0" {
			t.Error("a transparent pixel was painted black")
		}
	}
	// The clear half must fall back to blank cells.
	if !strings.Contains(out, " ") {
		t.Error("no blank cells, so transparency was painted over")
	}

	// A fully transparent image draws nothing at all.
	clear := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	blank := HalfBlocks(clear, 8, 4)
	if sgrPattern.MatchString(blank) {
		t.Errorf("a fully transparent image painted colour: %q", blank)
	}
	// It still has to occupy the right space.
	for i, line := range strings.Split(blank, "\n") {
		if got := ansi.StringWidth(line); got != 8 {
			t.Errorf("blank line %d is %d cells, want 8", i, got)
		}
	}
}

// TestHalfBlocksPartialAlpha checks antialiased edges keep their colour rather than darkening toward black.
func TestHalfBlocksPartialAlpha(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := range 8 {
		for x := range 8 {
			img.SetNRGBA(x, y, color.NRGBA{200, 200, 200, 128}) // half-opaque grey
		}
	}
	got := firstDrawnColor(t, HalfBlocks(img, 4, 2))
	if got.R < 150 {
		t.Errorf("half-opaque grey rendered %v; premultiplication was not undone", got)
	}
}
