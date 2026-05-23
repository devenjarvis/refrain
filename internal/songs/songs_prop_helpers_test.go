package songs

import (
	"pgregory.net/rapid"
)

// genTrackName generates arbitrary strings suitable for use as track names,
// mixing common music title patterns, whitespace-heavy inputs, long strings
// that exercise truncation, and full unicode.
func genTrackName(t *rapid.T) string {
	kind := rapid.IntRange(0, 3).Draw(t, "kind")
	switch kind {
	case 0:
		// Short ASCII printable — common happy path for real track names.
		return rapid.StringOf(rapid.Map(rapid.IntRange(0x20, 0x7E), func(n int) rune { return rune(n) })).Draw(t, "ascii")
	case 1:
		// Whitespace and punctuation heavy — exercises Trim and collapse logic.
		pool := []rune{' ', ' ', '\t', '\n', '-', '_', '.', '!', '(', ')', '&', 'a', 'b', '1'}
		return rapid.StringOf(rapid.SampledFrom(pool)).Draw(t, "ws")
	case 2:
		// Long string (60-120 chars) — exercises the 41-byte truncation window.
		return rapid.StringOfN(rapid.Rune(), 60, 120, -1).Draw(t, "long")
	default:
		// Full unicode — exercises multi-byte rune normalisation.
		return rapid.String().Draw(t, "unicode")
	}
}
