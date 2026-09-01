package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestScrollOffsetClampsAndFollows(t *testing.T) {
	cases := []struct {
		name                        string
		total, height, offset, keep int
		want                        int
	}{
		{"everything fits", 5, 10, 0, 0, 0},
		{"cannot scroll past the end", 20, 10, 99, -1, 10},
		{"cannot scroll before the start", 20, 10, -5, -1, 0},
		{"keeps a line above the window", 20, 10, 12, 3, 3},
		{"keeps a line below the window", 20, 10, 0, 15, 6},
		{"leaves a visible line alone", 20, 10, 4, 8, 4},
		{"no line to keep", 20, 10, 7, -1, 7},
		{"keep beyond the content still clamps", 20, 10, 0, 99, 10},
		{"zero height", 20, 0, 5, 3, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scrollOffset(tc.total, tc.height, tc.offset, tc.keep); got != tc.want {
				t.Fatalf("scrollOffset(%d,%d,%d,%d) = %d, want %d",
					tc.total, tc.height, tc.offset, tc.keep, got, tc.want)
			}
		})
	}
}

// Whatever the offset, the pane draws exactly the height it was given: a short window would leave the frame ragged and a tall one would push the layout.
func TestScrollPaneAlwaysFillsItsHeight(t *testing.T) {
	rows := make([]string, 30)
	for i := range rows {
		rows[i] = fmt.Sprintf("line %d", i)
	}
	for _, off := range []int{0, 5, 25, 99} {
		got := scrollPane(rows, 20, 12, off, true)
		if n := len(strings.Split(got, "\n")); n != 12 {
			t.Errorf("offset %d drew %d lines, want 12", off, n)
		}
	}
	// Shorter content still fills the pane rather than collapsing.
	if n := len(strings.Split(scrollPane(rows[:3], 20, 12, 0, true), "\n")); n != 12 {
		t.Errorf("short content drew %d lines, want 12", n)
	}
}

func TestScrollPaneShowsTheWindowAndABar(t *testing.T) {
	rows := make([]string, 30)
	for i := range rows {
		rows[i] = fmt.Sprintf("line %d", i)
	}
	top := ansi.Strip(scrollPane(rows, 20, 10, 0, true))
	if !strings.Contains(top, "line 0") || strings.Contains(top, "line 20") {
		t.Fatalf("the top of the pane shows the wrong window:\n%s", top)
	}
	bottom := ansi.Strip(scrollPane(rows, 20, 10, 20, true))
	if !strings.Contains(bottom, "line 29") || strings.Contains(bottom, "line 0") {
		t.Fatalf("the end of the pane shows the wrong window:\n%s", bottom)
	}
	// The bar occupies one column beyond the text.
	for line := range strings.SplitSeq(bottom, "\n") {
		if w := len([]rune(line)); w != 21 {
			t.Fatalf("line %q is %d cells, want 20 plus the bar", line, w)
		}
	}
}

// Blocks that wrap are several screen lines from one entry, and windowing counts lines.
func TestFlattenCountsScreenLines(t *testing.T) {
	if got := flatten([]string{"one", "two\nthree", ""}); len(got) != 4 {
		t.Fatalf("flatten produced %d lines, want 4: %q", len(got), got)
	}
}
