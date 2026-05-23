// Package proptest provides shared rapid generators and oracle functions for
// property-based tests across the refrain codebase.
package proptest

import (
	"pgregory.net/rapid"
)

// ArbitraryString generates strings from the full Unicode range, including
// control characters, surrogates, and unusual whitespace sequences.
func ArbitraryString() *rapid.Generator[string] {
	return rapid.String()
}

// ASCIIPrintableString generates strings containing only printable ASCII
// characters (code points 0x20–0x7E).
func ASCIIPrintableString() *rapid.Generator[string] {
	return rapid.StringOf(rapid.Map(rapid.IntRange(0x20, 0x7E), func(n int) rune { return rune(n) }))
}

// UnicodeString generates strings from a broad BMP range exercising multi-byte
// UTF-8 sequences, excluding surrogates.
func UnicodeString() *rapid.Generator[string] {
	return rapid.StringOf(rapid.Rune())
}

// WhitespaceHeavyString generates strings that mix alphanumeric characters
// with a high proportion of whitespace (spaces, tabs, newlines) and common
// punctuation, stress-testing slug normalization and trimming logic.
func WhitespaceHeavyString() *rapid.Generator[string] {
	// Weighted pool: whitespace chars (repeated for higher probability) + alphanum.
	pool := []rune{
		' ', ' ', ' ', '\t', '\n', '\r',
		'-', '_', '.', ',', '!', '?', '(', ')',
		'a', 'b', 'c', '1', '2', '3', 'A', 'B',
	}
	return rapid.StringOf(rapid.SampledFrom(pool))
}
