package tui

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Minimal helpers shared across per-key tests. The bubbletea v2 key-matching
// rule is documented at github.com/charmbracelet/ultraviolet/key.go:391 —
// Key.String() returns Text when non-empty (and not a literal space),
// otherwise the modifier-prefixed keystroke (e.g. "ctrl+d", "pgdown"). The
// helpers below set Code/Mod/Text so msg.String() matches what each panel's
// switch expects.

// keyRune constructs a printable-character key press. Use for letters and
// digits when no modifier is involved. msg.String() returns the rune.
func keyRune(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// keyShiftRune constructs a shifted printable key press where the visible
// character differs from the unshifted code. Text is the upper form so
// msg.String() returns it directly (e.g. keyShiftRune('g', "G") → "G").
func keyShiftRune(lower rune, upper string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: lower, Mod: tea.ModShift, Text: upper}
}

// keyCtrlRune constructs a ctrl+<letter> key press. msg.String() returns
// "ctrl+<letter>" because Text is empty.
func keyCtrlRune(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl}
}

// keyNamed constructs a named-key press (e.g. tea.KeyEscape, tea.KeyPgDown).
// msg.String() returns the keystroke form ("esc", "pgdown", etc.).
func keyNamed(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code}
}

// keyShiftNamed constructs shift+<named-key>.
func keyShiftNamed(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Mod: tea.ModShift}
}

// runCmdAll recursively flattens tea.BatchMsg into a slice of concrete
// messages so a test can find a specific msg type emitted alongside others.
func runCmdAll(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	out := []tea.Msg{}
	flatten(cmd(), &out)
	return out
}

func flatten(msg tea.Msg, out *[]tea.Msg) {
	if msg == nil {
		return
	}
	// tea.BatchMsg is a slice of tea.Cmd; sequence msgs are similar but
	// internal. Reflect into a slice if the msg is a slice of Cmd.
	if cmds, ok := msg.(tea.BatchMsg); ok {
		for _, c := range cmds {
			if c == nil {
				continue
			}
			flatten(c(), out)
		}
		return
	}
	*out = append(*out, msg)
}

// findMsg searches msgs for the first value that matches target's type and,
// when target is non-nil, returns true if equal. It returns the matching msg
// and true on success.
func findMsg[T tea.Msg](msgs []tea.Msg) (T, bool) {
	var zero T
	wantT := reflect.TypeOf(zero)
	for _, m := range msgs {
		if reflect.TypeOf(m) == wantT {
			return m.(T), true
		}
	}
	return zero, false
}
