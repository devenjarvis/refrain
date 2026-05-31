package theme

import "github.com/charmbracelet/lipgloss"

// Border-style constructors. Each returns a fresh lipgloss.Style so callers can
// chain Width/Height/Padding without mutating shared state. Color and border
// shape are the design-system decision; sizing is the caller's.

// BorderModal is the canonical centered-overlay box: a rounded primary border.
// Callers add Width/Padding (see PadModal). Shared by the repo-config,
// repo-checks, global-settings, and note modals so overlays render identically.
func BorderModal() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary)
}

// BorderPaneLeft is the left pane of a two-column split: a muted normal border
// drawn only on the right edge, separating it from the right pane. Shared by the
// file-browser, branch-picker, and repo-picker.
func BorderPaneLeft() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, true, false, false).
		BorderForeground(ColorMuted)
}
