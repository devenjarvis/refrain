// Package testutil provides shared helpers for mdrender tests and other
// packages that need to compare ANSI-styled output to plain expectations.
package testutil

import "regexp"

// ansiRE matches CSI SGR (and other CSI-final-0x40..0x7E) escape sequences.
// We strip these to compare visible content while ignoring color/bold etc.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// StripANSI removes ANSI CSI escape sequences from s. It is the canonical
// helper for assertions that compare visible content across the test suite.
func StripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}
