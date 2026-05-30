package tui

import (
	"fmt"
	"os/exec"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/devenjarvis/refrain/internal/editor"
)

// ideOpenedMsg reports the outcome of launching the configured IDE. A non-nil
// err surfaces as a transient error in the UI (§8).
type ideOpenedMsg struct{ err error }

// openIDECmd launches the IDE executable detached from refrain and reports the
// result as an ideOpenedMsg. Running the exec inside the tea.Cmd — rather than
// a fire-and-forget goroutine — keeps the side effect on the Bubble Tea command
// path so a launch failure can surface to the user (§4: goroutines produce
// messages, they don't silently mutate or drop work the UI depends on).
func openIDECmd(exe string, args []string, dir string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command(exe, args...)
		cmd.Dir = dir
		if err := cmd.Start(); err != nil {
			return ideOpenedMsg{err: err}
		}
		return ideOpenedMsg{}
	}
}

const (
	editorFieldLabel        = "Editor"
	editorCustomFieldLabel  = "Custom Command"
	editorCustomOption      = "Custom"
	editorCustomPlaceholder = "e.g. code -n"
)

// addEditorFields appends an editor select plus custom-command text input to
// a form. stored is the raw IDE command currently saved (may be empty). The
// select is pre-selected to match stored if it corresponds to a detected
// editor, otherwise "Custom" with stored placed in the text field.
func addEditorFields(fields []formField, stored string) []formField {
	const inputWidth = 30
	detected := editor.Detect()
	options := make([]string, 0, len(detected)+1)
	for _, e := range detected {
		options = append(options, e.Name)
	}
	options = append(options, editorCustomOption)

	selected := len(options) - 1 // "Custom" by default
	customText := ""
	trimmed := strings.TrimSpace(stored)
	if trimmed == "" {
		if len(detected) > 0 {
			selected = 0
		}
	} else if m := editor.MatchCommand(trimmed); m != nil {
		for i, name := range options {
			if name == m.Name {
				selected = i
				break
			}
		}
	} else {
		customText = stored
	}

	fields = addSelect(fields, editorFieldLabel, options, selected)
	fields = addTextInput(fields, editorCustomFieldLabel, customText, editorCustomPlaceholder, inputWidth)
	return fields
}

// extractIDECommand reads the Editor select + Custom Command text and returns
// the IDE command to store. Empty string means "leave IDECommand unset".
func extractIDECommand(f configForm) string {
	selected := f.selectValue(editorFieldLabel)
	if selected == "" || selected == editorCustomOption {
		return strings.TrimSpace(f.textValue(editorCustomFieldLabel))
	}
	// Select options only ever contain detected editor names (plus "Custom").
	// The command format is deterministic — don't re-run Detect() here, since
	// a mid-session uninstall or filesystem hiccup would silently erase the
	// user's previously saved command.
	return fmt.Sprintf(`open -a %q`, selected)
}

// splitIDECommand tokenizes a shell-like command string, honoring double and
// single quotes so app names with spaces (e.g. open -a "Visual Studio Code")
// survive as a single argument. Backslash escapes the next rune inside or
// outside quotes (diverges from POSIX, which only treats backslash as escape
// in a narrow set of double-quoted contexts). Unterminated quotes run to EOL.
func splitIDECommand(s string) []string {
	var (
		tokens []string
		cur    strings.Builder
		quote  rune // 0 when not quoted; '"' or '\'' otherwise
		inTok  bool
		escape bool
	)

	flush := func() {
		if inTok {
			tokens = append(tokens, cur.String())
			cur.Reset()
			inTok = false
		}
	}

	for _, r := range s {
		if escape {
			cur.WriteRune(r)
			inTok = true
			escape = false
			continue
		}
		if r == '\\' {
			escape = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			cur.WriteRune(r)
			inTok = true
			continue
		}
		if r == '"' || r == '\'' {
			quote = r
			inTok = true
			continue
		}
		if unicode.IsSpace(r) {
			flush()
			continue
		}
		cur.WriteRune(r)
		inTok = true
	}
	if escape {
		cur.WriteRune('\\')
		inTok = true
	}
	flush()
	return tokens
}
