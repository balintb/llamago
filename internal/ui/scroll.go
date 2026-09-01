package ui

import "strings"

// Panes whose content grows past the space they are given used to be hard clipped, which loses the tail without saying so. These two functions are what a growable pane uses instead: one decides where the window sits, the other draws it with a scrollbar.

// scrollOffset clamps a requested offset to the content and pulls it back far enough to keep line keep visible. A negative keep means nothing has to stay in view, which is the case for a pane with no selection of its own.
func scrollOffset(total, height, offset, keep int) int {
	if height <= 0 {
		return 0
	}
	off := min(max(offset, 0), max(0, total-height))
	if keep < 0 {
		return off
	}
	if keep < off {
		return keep
	}
	if keep >= off+height {
		return min(keep-height+1, max(0, total-height))
	}
	return off
}

// scrollPane windows pre-rendered lines and attaches the scrollbar the transcript uses, so "there is more below" is visible rather than implied. width is the room for the text; the bar takes one column beyond it.
func scrollPane(rows []string, width, height, offset int, focused bool) string {
	if height <= 0 {
		return ""
	}
	off := min(max(offset, 0), max(0, len(rows)-height))
	end := min(len(rows), off+height)

	var body string
	if off < end {
		body = strings.Join(rows[off:end], "\n")
	}
	bar := scrollbarColumn(height, len(rows), off, focused)
	return attachScrollbar(body, max(1, width), height, bar)
}

// flatten splits rendered blocks into the screen lines they actually occupy: a wrapped preview is one entry and several lines, and windowing counts lines.
func flatten(rows []string) []string {
	return strings.Split(strings.Join(rows, "\n"), "\n")
}
