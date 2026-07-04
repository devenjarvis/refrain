package tui

import (
	"strings"
)

type keyHint struct {
	key  string
	desc string
}

var (
	// sessionListHints is the hint set shown on the root session list.
	// Workflow keys (c/x/X/t/e/p/d/o/a/s) act on the session under the list
	// cursor. Kept tight so a 120-col terminal never truncates "quit" off the
	// right edge — `t` (shell), `x`/`X` (kill), and `a` (add repo) are omitted
	// to stay within the column budget; the bindings remain active.
	sessionListHints = []keyHint{
		{"j/k", "navigate"},
		{"⏎", "open"},
		{"n", "session"},
		{"P", "plan"},
		{"r", "review"},
		{"p", "PR"},
		{"c", "agent"},
		{"d", "diff"},
		{"R", "repos"},
		{"o", "branch"},
		{"s", "settings"},
		{"q", "quit"},
	}

	diffHints = []keyHint{
		{"j/k", "tree"},
		{"h/l", "fold/open"},
		{"enter", "view"},
		{"d/u", "scroll"},
		{"s", "side-by-side"},
		{"q", "back"},
	}

	repoBrowsingHints = []keyHint{
		{"j/k", "navigate"},
		{"type", "filter"},
		{"enter", "open/select"},
		{"backspace", "up"},
		{".", "hidden"},
		{"esc", "cancel"},
	}

	repoConfigHints = []keyHint{
		{"j/k", "navigate"},
		{"←/→", "select"},
		{"enter", "edit/toggle"},
		{"ctrl+s", "save"},
		{"esc", "back"},
	}

	repoChecksHints = []keyHint{
		{"j/k", "navigate"},
		{"a", "add"},
		{"e", "edit"},
		{"d", "delete"},
		{"ctrl+s", "save"},
		{"esc", "back"},
	}

	branchPickerHints = []keyHint{
		{"j/k", "navigate"},
		{"enter", "select"},
		{"type", "filter"},
		{"backspace", "clear filter"},
		{"esc", "cancel"},
	}

	focusLaunchHints = []keyHint{
		{"esc", "back"},
		{"⇧esc", "interrupt"},
		{"alt+[/]", "tab"},
		{"ctrl+t", "shell"},
		{"ctrl+n", "agent"},
		{"ctrl+w", "close"},
		{"pgup/pgdn", "scroll"},
	}

	repoPickerHints = []keyHint{
		{"j/k", "navigate"},
		{"type", "filter"},
		{"enter", "select"},
		{"a", "add repo"},
		{"esc", "cancel"},
	}

	repoPickerManageHints = []keyHint{
		{"j/k", "navigate"},
		{"⏎", "switch"},
		{"s", "settings"},
		{"d", "remove"},
		{"a", "add"},
		{"esc", "cancel"},
	}

	newSessionHints = []keyHint{
		{"⏎", "start"},
		{"ctrl+p", "plan first"},
		{"ctrl+j", "newline"},
		{"tab", "context/overrides"},
		{"esc", "cancel"},
	}
)

func renderStatusBar(hints []keyHint, width int) string {
	keyStyle := StyleBold.Foreground(ColorText)
	descStyle := StyleSubtle

	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		parts = append(parts, keyStyle.Render(h.key)+" "+descStyle.Render(h.desc))
	}

	content := strings.Join(parts, "  ")
	return StyleStatusBar.Width(width).Render(content)
}
