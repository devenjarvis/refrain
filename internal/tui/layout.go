package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/devenjarvis/refrain/internal/config"
)

// Layout primitives. Per CONVENTIONS.md §5, width/height arithmetic lives here
// so a border/sidebar/chrome change is a one-line edit, not a multi-file grep.
// All helpers are pure (inputs → ints) and read no model or global state.

const (
	// borderWidth is the column/row cost of a single lipgloss border edge.
	borderWidth = 1
	// separatorWidth is the single column between two side-by-side panes.
	separatorWidth = 1
	// statusBarHeight is the height of the bottom status bar.
	statusBarHeight = 1
	// titleHeight is the height of a screen's top title row.
	titleHeight = 1
	// modalChrome is the total horizontal cost of a bordered, 1×2-padded modal
	// box: 2 border columns + 2 padding columns.
	modalChrome = 2*borderWidth + 2
)

// defaultSidebarWidth is the single source for the dashboard/new-session
// sidebar width. It generalizes config.DefaultSidebarWidth so a width change
// lands in one place.
const defaultSidebarWidth = config.DefaultSidebarWidth

// resolveSidebarWidth returns the configured sidebar width, falling back to the
// default when it has not been plumbed in yet (configured <= 0).
func resolveSidebarWidth(configured int) int {
	if configured > 0 {
		return configured
	}
	return defaultSidebarWidth
}

// innerWidth is the usable width inside a region framed by a single-column
// border on each side (dividers, panel content). Covers the `width - 2` idiom.
func innerWidth(w int) int {
	return w - 2*borderWidth
}

// modalContentWidth is the usable width inside a bordered+padded modal box.
// Covers the `w - 4` idiom.
func modalContentWidth(w int) int {
	return w - modalChrome
}

// previewTermWidth is the terminal column count for the agent preview pane:
// total minus the sidebar, the separator border, and the preview's own two
// border columns.
func previewTermWidth(total, sidebar int) int {
	return total - sidebar - separatorWidth - 2*borderWidth
}

// fillHeight pads content with blank rows at the bottom so the result is exactly
// height rows of width cells, used by views whose footer must pin to the bottom
// even when body is short.
func fillHeight(content string, width, height int) string {
	return lipgloss.Place(width, height, lipgloss.Left, lipgloss.Top, content)
}

// columnStrategy parameterizes the left pane of a two-column split.
type columnStrategy struct {
	num, den int // left target width = total*num/den
	min      int // minimum left width
	reserve  int // if >0, cap left so the right pane keeps at least `reserve` cols
}

// splitColumns sizes the left pane of a two-column layout per strategy s and
// returns (left, right), where `gap` columns sit between the panes. It is the
// single source for the repo-picker / file-browser / branch-picker / shipping
// feedback splits, each of which differs only in strategy and gap.
func splitColumns(total int, s columnStrategy, gap int) (left, right int) {
	left = total * s.num / s.den
	if left < s.min {
		left = s.min
	}
	if s.reserve > 0 && total > s.reserve && left > total-s.reserve {
		left = total - s.reserve
	}
	right = total - left - gap
	if right < 0 {
		right = 0
	}
	return left, right
}
