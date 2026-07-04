package tui

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestDefaultKeyMap_NoDuplicateBindings verifies that no key string is bound
// to more than one action in the default map. A duplicate would mean two
// actions race for the same press — the dispatch order would silently win and
// the loser would be unreachable.
func TestDefaultKeyMap_NoDuplicateBindings(t *testing.T) {
	km := DefaultKeyMap()

	seen := map[string]string{} // key -> action name
	check := func(action string, keys []string) {
		t.Helper()
		for _, k := range keys {
			if prior, ok := seen[k]; ok {
				t.Errorf("key %q bound to both %s and %s", k, prior, action)
			}
			seen[k] = action
		}
	}

	check("Quit", km.Quit)
	check("Up", km.Up)
	check("Down", km.Down)
	check("Activate", km.Activate)
	check("NextRepo", km.NextRepo)
	check("NewSession", km.NewSession)
	check("AddAgent", km.AddAgent)
	check("OpenReview", km.OpenReview)
	check("OpenTerminal", km.OpenTerminal)
	check("OpenIDE", km.OpenIDE)
	check("OpenPR", km.OpenPR)
	check("OpenBranch", km.OpenBranch)
	check("ManageRepos", km.ManageRepos)
	check("AddRepo", km.AddRepo)
	check("Settings", km.Settings)
	check("OpenDiff", km.OpenDiff)
	check("KillAgent", km.KillAgent)
	check("KillSession", km.KillSession)
}

// TestDefaultKeyMap_EveryActionBound guards against a regression where a new
// action is added to KeyMap but DefaultKeyMap forgets to populate it. Uses
// reflection so adding a field automatically extends coverage.
func TestDefaultKeyMap_EveryActionBound(t *testing.T) {
	km := DefaultKeyMap()
	v := reflect.ValueOf(km)
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		name := v.Type().Field(i).Name
		if f.Kind() != reflect.Slice {
			continue
		}
		if f.Len() == 0 {
			t.Errorf("action %s has no default key binding", name)
		}
	}
}

// TestKeyMapMatch covers the happy path and the negative case for Match —
// confirming that an unbound key returns false and that any of the bound
// strings returns true.
func TestKeyMapMatch(t *testing.T) {
	km := DefaultKeyMap()
	cases := []struct {
		key    string
		action []string
		want   bool
	}{
		{"q", km.Quit, true},
		{"ctrl+c", km.Quit, true},
		{"k", km.Up, true},
		{"up", km.Up, true},
		{"j", km.Up, false}, // j is Down, not Up
		{"a", km.AddRepo, true},
		{"zz", km.NewSession, false},
		{"n", km.NewSession, true},
	}
	for _, tc := range cases {
		// tea.KeyPressMsg.String() reads from the underlying Key; build via
		// the documented public Key fields. For single-rune keys String()
		// returns the rune; for named keys ("ctrl+c", "up") it returns the
		// label. The cleanest portable path is to construct the Key manually.
		msg := synthesizeKeyPress(tc.key)
		got := km.Match(msg, tc.action)
		if got != tc.want {
			t.Errorf("Match(%q, %v) = %v; want %v", tc.key, tc.action, got, tc.want)
		}
	}
}

// synthesizeKeyPress returns a tea.KeyPressMsg whose String() equals s. The
// helper exists because tea.KeyPressMsg is built from an ultraviolet Key
// struct whose constructor differs slightly across Charm versions; routing
// through a known field assignment keeps the test resilient to that drift.
func synthesizeKeyPress(s string) tea.KeyPressMsg {
	// Use Text to ensure single-rune lowercase keys round-trip through String().
	// Multi-key labels like "ctrl+c" and "up" require setting Mod/Code; we
	// approximate by setting Text and relying on the String() fallback for
	// the bound-string match the production code actually performs.
	return tea.KeyPressMsg{Text: s}
}
