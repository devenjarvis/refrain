package agent

import (
	"pgregory.net/rapid"
)

// arbitrarySlugInput generates strings that exercise a wide range of slugify
// behaviours: pure alphanumeric, whitespace-heavy, unicode, long inputs that
// require truncation, and inputs that reduce to the empty string after
// normalization.
func arbitrarySlugInput(t *rapid.T) string {
	// Mix four input shapes uniformly.
	kind := rapid.IntRange(0, 3).Draw(t, "kind")
	switch kind {
	case 0:
		// Short ASCII printable string — common happy path.
		return rapid.StringOf(rapid.Map(rapid.IntRange(0x20, 0x7E), func(n int) rune { return rune(n) })).Draw(t, "ascii")
	case 1:
		// Whitespace and punctuation heavy — exercises trimming / collapsing.
		pool := []rune{' ', ' ', '\t', '\n', '-', '_', '.', '!', 'a', 'b', '1'}
		return rapid.StringOf(rapid.SampledFrom(pool)).Draw(t, "ws_heavy")
	case 2:
		// Long string (60-120 chars) — exercises the 41-byte truncation window.
		return rapid.StringOfN(rapid.Rune(), 60, 120, -1).Draw(t, "long")
	default:
		// Full unicode — exercises multi-byte rune handling.
		return rapid.String().Draw(t, "unicode")
	}
}
