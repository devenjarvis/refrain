package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type keyHint struct {
	key  string
	desc string
}

var (
	// dashboardHints is the unified hint set shown on the pipeline view (the
	// only dashboard mode). Workflow keys (c/x/X/t/e/p/d/o/a/s) act on the
	// session under the pipeline cursor. Kept tight so a 120-col terminal
	// never truncates "quit" off the right edge — the `b` hint's desc is
	// rewritten to "break" by App.View() outside the Planning section.
	// `t` (shell) and `x` (kill) are omitted to stay within the column budget
	// after adding `R repos` and `o branch`; both bindings remain active.
	dashboardHints = []keyHint{
		{"j/k", "navigate"},
		{"⏎", "open"},
		{"n", "session"},
		{"b", "build"},
		{"m", "ready"},
		{"r", "review"},
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
)

func renderStatusBar(hints []keyHint, width int) string {
	keyStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorText)

	descStyle := lipgloss.NewStyle().
		Foreground(ColorMuted)

	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		parts = append(parts, keyStyle.Render(h.key)+" "+descStyle.Render(h.desc))
	}

	content := strings.Join(parts, "  ")
	return StyleStatusBar.Width(width).Render(content)
}
