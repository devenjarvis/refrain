package diffmodel

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// WrapMarker is the glyph appended to continuation lines so a wrapped cell
// visually disambiguates from a naturally short line.
const WrapMarker = "↵"

// WrapCell returns the physical lines a cell expands into when its visible
// width (via ansi.StringWidth) exceeds width. Every line except the last is
// truncated to width-1 cells and suffixed with WrapMarker so the total
// visible width per wrapped line is exactly width. Widths <= 0 are treated as
// 1 to avoid infinite wrapping.
//
// Width measurement honors ANSI escape sequences and wide (East Asian)
// characters. Never use len() or utf8.RuneCountInString to measure the
// result — it may contain ANSI.
func WrapCell(text string, width int) []string {
	if width <= 0 {
		width = 1
	}
	if ansi.StringWidth(text) <= width {
		return []string{text}
	}

	// Reserve one cell for the wrap marker on every non-final line.
	budget := width - 1
	if budget < 1 {
		budget = 1
	}

	segments := strings.Split(ansi.Hardwrap(text, budget, false), "\n")
	// Append marker to all lines except the last. The last line is whatever
	// falls off the end and is left alone (may be short).
	for i := 0; i < len(segments)-1; i++ {
		segments[i] = segments[i] + WrapMarker
	}
	return segments
}
