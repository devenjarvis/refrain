package theme

// Spacing tokens. These codify the magic padding/indent numbers that were
// previously inlined as literals, so a spacing change is a one-line edit. Width
// *arithmetic* (border/sidebar/chrome math) stays in internal/tui/layout.go;
// these are the fixed design-system constants those helpers and styles consume.

// Padding pairs are {vertical, horizontal}, matching lipgloss.Padding(v, h).
var (
	// PadModal is the inner padding of a modal box.
	PadModal = [2]int{1, 2}
	// PadStatusBar is the status bar's inner padding.
	PadStatusBar = [2]int{0, 1}
	// PadCell is the dashboard checks-cell inner padding.
	PadCell = [2]int{0, 1}
)

const (
	// IndentTree is the per-depth indent (in cells) of the diff file tree.
	IndentTree = 2
	// LabelWidth is the fixed column width of a form field label.
	LabelWidth = 22
)
