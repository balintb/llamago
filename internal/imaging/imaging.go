// Package imaging loads images and renders them for a terminal, either as half-block text that works anywhere or as a native graphics escape sequence where the terminal supports one.
package imaging

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"

	// Registered for their decoders.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/charmbracelet/x/ansi/sixel"
)

// Extensions are the image files llamago will offer to attach.
var Extensions = []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp"}

// PickerExtensions returns the extensions in both cases, for widgets that match them case-sensitively. It is only a display hint: IsImage remains the authority, since this cannot enumerate mixed spellings like ".Jpg".
func PickerExtensions() []string {
	out := make([]string, 0, len(Extensions)*2)
	for _, e := range Extensions {
		out = append(out, e, strings.ToUpper(e))
	}
	return out
}

// IsImage reports whether a path looks like an image llamago can read.
func IsImage(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return slices.Contains(Extensions, ext)
}

// Load decodes an image file.
func Load(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	return img, nil
}

// --- scaling ----------------------------------------------------------------

// Resize scales img to exactly w by h pixels by averaging over source areas.
//
// Averaging rather than nearest-neighbour because thumbnails here are a large downscale, where dropping pixels turns fine detail into noise.
func Resize(img image.Image, w, h int) image.Image {
	if w <= 0 || h <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	src := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	if src.Empty() {
		return out
	}

	for y := range h {
		y0 := src.Min.Y + y*src.Dy()/h
		y1 := max(src.Min.Y+(y+1)*src.Dy()/h, y0+1)
		for x := range w {
			x0 := src.Min.X + x*src.Dx()/w
			x1 := max(src.Min.X+(x+1)*src.Dx()/w, x0+1)

			var r, g, b, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, ca := img.At(sx, sy).RGBA()
					r += uint64(cr)
					g += uint64(cg)
					b += uint64(cb)
					a += uint64(ca)
					n++
				}
			}
			if n == 0 {
				continue
			}
			out.SetRGBA(x, y, color.RGBA{
				R: uint8(r / n >> 8), G: uint8(g / n >> 8),
				B: uint8(b / n >> 8), A: uint8(a / n >> 8),
			})
		}
	}
	return out
}

// FitCells returns the cell size a thumbnail should occupy to keep its aspect ratio within the given bounds.
//
// A half-block cell holds one pixel across and two down, and a terminal cell is about twice as tall as it is wide, so those two pixels come out roughly square. That is why the height halves here.
func FitCells(img image.Image, maxCols, maxRows int) (cols, rows int) {
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 || maxCols <= 0 || maxRows <= 0 {
		return 0, 0
	}
	cols = maxCols
	rows = max(1, (cols*b.Dy()+b.Dx())/(2*b.Dx()))
	if rows > maxRows {
		rows = maxRows
		cols = min(max(1, 2*rows*b.Dx()/b.Dy()), maxCols)
	}
	return cols, rows
}

// --- half-block rendering ---------------------------------------------------

// alphaFloor is the alpha below which a pixel counts as see-through. Antialiased edges land above it; a cleared background lands well below.
const alphaFloor = 32

// HalfBlocks renders img as styled text of exactly cols by rows cells.
//
// Each cell is a half block carrying two pixels: normally the foreground paints the top and the background the bottom. Transparent pixels are left to the terminal's own background instead of being painted, which is what makes a screenshot with rounded corners or a logo on no background look right. Colour values from RGBA() are alpha-premultiplied, so they are divided back out; skipping that is what turned every transparent pixel black.
func HalfBlocks(img image.Image, cols, rows int) string {
	if cols <= 0 || rows <= 0 {
		return ""
	}
	px := Resize(img, cols, rows*2)

	var b strings.Builder
	b.Grow(cols * rows * 40)
	for y := range rows {
		if y > 0 {
			b.WriteByte('\n')
		}
		var last string
		for x := range cols {
			top, topOK := straight(px.At(x, y*2))
			bottom, bottomOK := straight(px.At(x, y*2+1))

			var style, glyph string
			switch {
			case !topOK && !bottomOK:
				// Both see-through: draw nothing at all.
				style, glyph = "\x1b[m", " "
			case topOK && !bottomOK:
				style = fmt.Sprintf("\x1b[m\x1b[38;2;%d;%d;%dm", top.R, top.G, top.B)
				glyph = "▀"
			case !topOK && bottomOK:
				// Paint the lower half instead, so the clear half stays clear.
				style = fmt.Sprintf("\x1b[m\x1b[38;2;%d;%d;%dm", bottom.R, bottom.G, bottom.B)
				glyph = "▄"
			default:
				style = fmt.Sprintf("\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dm",
					top.R, top.G, top.B, bottom.R, bottom.G, bottom.B)
				glyph = "▀"
			}
			// Photos have long runs of near-identical cells; only re-emit the escape when it actually changes.
			if style != last {
				b.WriteString(style)
				last = style
			}
			b.WriteString(glyph)
		}
		b.WriteString("\x1b[m")
	}
	return b.String()
}

// straight converts a premultiplied colour back to its true one, reporting false when the pixel is too transparent to draw.
func straight(c color.Color) (color.RGBA, bool) {
	r, g, bl, a := c.RGBA()
	if a>>8 < alphaFloor {
		return color.RGBA{}, false
	}
	if a == 0 {
		return color.RGBA{}, false
	}
	// Undo the premultiplication, then drop to 8 bits.
	return color.RGBA{
		R: uint8(min(r*0xffff/a, 0xffff) >> 8),
		G: uint8(min(g*0xffff/a, 0xffff) >> 8),
		B: uint8(min(bl*0xffff/a, 0xffff) >> 8),
		A: uint8(a >> 8),
	}, true
}

// --- native graphics --------------------------------------------------------

// Protocol is a terminal's native image protocol, if it has one.
type Protocol int

const (
	// None means the terminal has no known graphics protocol; half blocks are the only option.
	None Protocol = iota
	Kitty
	ITerm
	Sixel
)

func (p Protocol) String() string {
	switch p {
	case Kitty:
		return "kitty"
	case ITerm:
		return "iterm2"
	case Sixel:
		return "sixel"
	default:
		return "none"
	}
}

// Detect works out which graphics protocol the terminal speaks, from the environment. env takes the form returned by os.Environ.
//
// Sniffing rather than querying the terminal: a Device Attributes query costs a round trip at startup and misbehaves under some multiplexers. Users can override the result, which is the escape valve for a wrong guess.
func Detect(env []string) Protocol {
	get := func(k string) string {
		prefix := k + "="
		for _, e := range env {
			if after, ok := strings.CutPrefix(e, prefix); ok {
				return after
			}
		}
		return ""
	}

	// Multiplexers need passthrough wrapping that is easy to get wrong, and a mangled escape sequence corrupts the screen. Stay text-only there.
	if get("TMUX") != "" || strings.HasPrefix(get("TERM"), "screen") {
		return None
	}

	term, prog := get("TERM"), get("TERM_PROGRAM")
	switch {
	case get("KITTY_WINDOW_ID") != "", term == "xterm-kitty",
		strings.EqualFold(prog, "ghostty"), strings.EqualFold(prog, "WezTerm"):
		return Kitty
	case strings.EqualFold(prog, "iTerm.app"):
		return ITerm
	case strings.Contains(term, "sixel"), term == "mlterm", term == "yaft-256color":
		return Sixel
	}
	return None
}

// ParseProtocol reads a protocol name, for the config override.
func ParseProtocol(s string) (Protocol, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return None, false
	case "kitty":
		return Kitty, true
	case "iterm", "iterm2":
		return ITerm, true
	case "sixel":
		return Sixel, true
	case "none", "off", "blocks":
		return None, true
	}
	return None, false
}

// Encode renders img as a native graphics escape sequence for the protocol.
// It returns an empty result for None, where half blocks are the only option.
func Encode(img image.Image, p Protocol) (string, error) {
	switch p {
	case Kitty:
		var payload bytes.Buffer
		if err := (&kitty.Encoder{}).Encode(&payload, img); err != nil {
			return "", err
		}
		b := img.Bounds()
		return ansi.KittyGraphics(payload.Bytes(),
			"f=32", "a=T",
			fmt.Sprintf("s=%d", b.Dx()), fmt.Sprintf("v=%d", b.Dy()),
		), nil

	case Sixel:
		var payload bytes.Buffer
		if err := (&sixel.Encoder{}).Encode(&payload, img); err != nil {
			return "", err
		}
		return ansi.SixelGraphics(0, 1, 0, payload.Bytes()), nil

	case ITerm:
		var png bytes.Buffer
		if err := encodePNG(&png, img); err != nil {
			return "", err
		}
		// OSC 1337 File: iTerm2's inline image protocol.
		return fmt.Sprintf("\x1b]1337;File=inline=1;preserveAspectRatio=1;size=%d:%s\a",
			png.Len(), base64Of(png.Bytes())), nil
	}
	return "", nil
}

func encodePNG(w *bytes.Buffer, img image.Image) error { return png.Encode(w, img) }

func base64Of(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
