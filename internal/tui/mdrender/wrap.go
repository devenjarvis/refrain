package mdrender

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// wrapPlain breaks a single source line into width-respecting display
// segments. Input is plain text (no ANSI); output is plain text. Word
// boundaries are preferred but long words are hard-broken so no segment
// exceeds the requested width.
//
// Empty input yields a single empty segment so callers iterating segments
// always emit at least one row per source line — that matches the textarea's
// behavior and keeps source-line ↔ display-line scroll math honest.
func wrapPlain(line string, width int) []string {
	if width < 1 {
		return []string{line}
	}
	if line == "" {
		return []string{""}
	}
	// Fast path: line already fits.
	if ansi.StringWidth(line) <= width {
		return []string{line}
	}

	// Word-wrap at spaces with hard-break fallback for long words.
	var out []string
	var cur strings.Builder
	curWidth := 0

	// Walk graphemes via ansi.StringWidth on rune-by-rune slices is awkward;
	// since the input is plain text without combining marks, a rune-by-rune
	// pass with rune-width is good enough for our markdown corpus.
	runes := []rune(line)
	wordStart := 0
	wordWidth := 0
	for i := 0; i <= len(runes); i++ {
		atEnd := i == len(runes)
		var ch rune
		if !atEnd {
			ch = runes[i]
		}
		if atEnd || ch == ' ' || ch == '\t' {
			// Emit accumulated word.
			word := string(runes[wordStart:i])
			if word != "" {
				if curWidth+wordWidth > width && cur.Len() > 0 {
					// Strip trailing space from current segment before flushing.
					trimmed := strings.TrimRight(cur.String(), " \t")
					out = append(out, trimmed)
					cur.Reset()
					curWidth = 0
				}
				if wordWidth > width {
					// Hard-break the long word into width-sized chunks. If
					// there's anything in cur, flush it first.
					if cur.Len() > 0 {
						trimmed := strings.TrimRight(cur.String(), " \t")
						out = append(out, trimmed)
						cur.Reset()
						curWidth = 0
					}
					chunks := hardBreak(word, width)
					// All but the last chunk are full segments.
					for i, c := range chunks {
						if i == len(chunks)-1 {
							cur.WriteString(c)
							curWidth = ansi.StringWidth(c)
						} else {
							out = append(out, c)
						}
					}
				} else {
					cur.WriteString(word)
					curWidth += wordWidth
				}
			}
			if !atEnd {
				// Append the whitespace if it fits; otherwise wrap. Tabs are
				// treated as 1 column here — terminals expand them to the next
				// tab stop, but `ansi.StringWidth("\t")` reports 0, which would
				// make the guard a no-op for tabs. Plan markdown is overwhelmingly
				// space-indented; for tabbed prose the wrap will run wider than a
				// terminal's rendered width by up to 7 cells per tab. Acceptable
				// trade-off for the corpus we serve.
				wsWidth := 1
				if curWidth+wsWidth > width {
					trimmed := strings.TrimRight(cur.String(), " \t")
					out = append(out, trimmed)
					cur.Reset()
					curWidth = 0
				} else {
					cur.WriteRune(ch)
					curWidth += wsWidth
				}
				wordStart = i + 1
				wordWidth = 0
			}
			continue
		}
		// Non-space rune: extend current word.
		wordWidth += runeWidth(ch)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	if len(out) == 0 {
		out = append(out, "")
	}
	return out
}

// hardBreak chops a long word into width-sized rune chunks. ANSI is not
// expected here (input is plain).
func hardBreak(word string, width int) []string {
	if width < 1 {
		return []string{word}
	}
	var out []string
	var cur strings.Builder
	curWidth := 0
	for _, r := range word {
		w := runeWidth(r)
		if curWidth+w > width && cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
			curWidth = 0
		}
		cur.WriteRune(r)
		curWidth += w
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// runeWidth returns the visible width of a single rune. For the markdown
// inputs we expect (mostly ASCII, occasional emoji), ansi.StringWidth on a
// 1-rune string is overkill; but using it keeps width semantics consistent
// with the rest of the code.
func runeWidth(r rune) int {
	return ansi.StringWidth(string(r))
}
