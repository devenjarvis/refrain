package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/devenjarvis/refrain/internal/diffmodel"
	"github.com/devenjarvis/refrain/internal/tui/diff"
)

// Left-pane sizing.
const (
	diffTreeMinPaneWidth = 24
	diffTreePreferWidth  = 30
	diffTreeHideBelow    = 80 // total terminal width below which tree is hidden
)

type diffCloseMsg struct{}

// diffModel drives the new diff browser: collapsible file tree on the left,
// syntax-highlighted diff rendered into a viewport on the right.
type diffModel struct {
	agentName string
	model     *diffmodel.Model

	tree      *diff.Tree
	vp        viewport.Model
	renderers map[string]*diff.Renderer

	// selected is the path of the file currently painted into the viewport.
	selected   string
	sideBySide bool

	width, height int
}

// newDiffModel constructs a diffModel from a parsed diff. `height` should be
// the height available for the body only — the caller reserves the status bar.
func newDiffModel(agentName string, m *diffmodel.Model, width, height int) diffModel {
	d := diffModel{
		agentName:  agentName,
		model:      m,
		tree:       diff.NewTree(m),
		vp:         viewport.New(),
		renderers:  make(map[string]*diff.Renderer),
		sideBySide: true,
		width:      width,
		height:     height,
	}
	d.applySize()
	// Auto-open whatever the tree cursor landed on.
	if sel := d.tree.SelectedFile(); sel != nil {
		d.openFile(sel.Path)
	}
	return d
}

// Update processes a message and returns the updated model plus any command.
func (d diffModel) Update(msg tea.Msg) (diffModel, tea.Cmd) {
	switch msg := msg.(type) {
	case diff.FileSelectedMsg:
		d.openFile(msg.Path)
		return d, nil
	case tea.WindowSizeMsg:
		d.width = msg.Width
		d.height = msg.Height
		d.applySize()
		// Re-render current file with the new width.
		if d.selected != "" {
			d.refreshViewport()
		}
		return d, nil
	case tea.KeyPressMsg:
		return d.updateKey(msg)
	}
	return d, nil
}

func (d diffModel) updateKey(msg tea.KeyPressMsg) (diffModel, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		return d, func() tea.Msg { return diffCloseMsg{} }
	case "s":
		d.sideBySide = !d.sideBySide
		if d.selected != "" {
			d.refreshViewport()
		}
		return d, nil
	// Viewport-owned scroll keys.
	case "d", "ctrl+d":
		d.vp.HalfPageDown()
		return d, nil
	case "u", "ctrl+u":
		d.vp.HalfPageUp()
		return d, nil
	case "pgdown":
		d.vp.PageDown()
		return d, nil
	case "pgup":
		d.vp.PageUp()
		return d, nil
	}

	// If no tree visible, j/k/g/G scroll the viewport; otherwise they navigate
	// the tree (which may emit a FileSelectedMsg for us).
	if !d.treeVisible() {
		switch msg.String() {
		case "j", "down":
			d.vp.ScrollDown(1)
			return d, nil
		case "k", "up":
			d.vp.ScrollUp(1)
			return d, nil
		case "g":
			d.vp.GotoTop()
			return d, nil
		case "G":
			d.vp.GotoBottom()
			return d, nil
		}
		return d, nil
	}

	// Default: forward to the tree.
	var cmd tea.Cmd
	d.tree, cmd = d.tree.Update(msg)
	return d, cmd
}

// openFile swaps the viewport content to show `path`. No-ops if path is empty.
func (d *diffModel) openFile(path string) {
	if path == "" || path == d.selected {
		if path == d.selected && d.selected != "" {
			// Same file: rewind to top for consistency.
			d.vp.GotoTop()
		}
		return
	}
	d.selected = path
	d.refreshViewport()
	d.vp.GotoTop()
}

// refreshViewport re-renders the current file into the viewport using current
// dimensions and SxS toggle.
func (d *diffModel) refreshViewport() {
	if d.selected == "" || d.model == nil {
		d.vp.SetContent("")
		return
	}
	f := d.findFile(d.selected)
	if f == nil {
		d.vp.SetContent("")
		return
	}
	r, ok := d.renderers[d.selected]
	if !ok {
		r = diff.NewRenderer(f)
		d.renderers[d.selected] = r
	}
	bodyW := d.rightPaneWidth()
	content := r.Render(bodyW, d.sideBySide)
	d.vp.SetContent(content)
}

// findFile returns a pointer to the File in the model with the matching path.
func (d *diffModel) findFile(path string) *diffmodel.File {
	if d.model == nil {
		return nil
	}
	for i := range d.model.Files {
		if d.model.Files[i].Path == path {
			return &d.model.Files[i]
		}
	}
	return nil
}

// applySize recomputes pane widths and pushes them into child components.
func (d *diffModel) applySize() {
	rightW := d.rightPaneWidth()
	d.vp.SetWidth(rightW)
	bodyH := d.bodyHeight()
	d.vp.SetHeight(bodyH)
}

// treeVisible reports whether the left tree pane fits.
func (d *diffModel) treeVisible() bool {
	return d.width >= diffTreeHideBelow
}

// treePaneWidth returns the left pane width (or 0 if hidden).
func (d *diffModel) treePaneWidth() int {
	if !d.treeVisible() {
		return 0
	}
	w := diffTreePreferWidth
	if w > d.width/3 {
		w = d.width / 3
	}
	if w < diffTreeMinPaneWidth {
		w = diffTreeMinPaneWidth
	}
	return w
}

// rightPaneWidth returns the viewport's content width.
func (d *diffModel) rightPaneWidth() int {
	w := d.width - d.treePaneWidth()
	// Reserve 1 col for the separator between tree and diff.
	if d.treeVisible() {
		w--
	}
	if w < 1 {
		w = 1
	}
	return w
}

// bodyHeight returns the usable height (header eats 1 line).
func (d *diffModel) bodyHeight() int {
	h := d.height - 1
	if h < 1 {
		return 1
	}
	return h
}

// View renders the tree + viewport side-by-side, preceded by a one-line header.
func (d diffModel) View() string {
	if d.width <= 0 || d.height <= 0 {
		return ""
	}
	header := d.renderHeader()
	if d.model == nil || len(d.model.Files) == 0 {
		empty := lipgloss.NewStyle().Foreground(ColorMuted).Render(
			fmt.Sprintf("No changes for %s", d.agentName),
		)
		body := padViewBody(empty, d.width, d.bodyHeight())
		return lipgloss.JoinVertical(lipgloss.Left, header, body)
	}

	bodyH := d.bodyHeight()
	right := d.vp.View()
	var body string
	if d.treeVisible() {
		treeView := d.tree.View(d.treePaneWidth(), bodyH)
		sep := renderVerticalSeparator(bodyH)
		body = lipgloss.JoinHorizontal(lipgloss.Top, treeView, sep, right)
	} else {
		body = right
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body)
}

// renderHeader shows the agent name and the selected path (if any).
func (d diffModel) renderHeader() string {
	title := StyleTitle.Render(fmt.Sprintf("Diff · %s", d.agentName))
	var sub string
	if d.selected != "" {
		mode := "unified"
		if d.sideBySide && d.width >= diff.SideBySideMinWidth {
			mode = "side-by-side"
		}
		sub = lipgloss.NewStyle().Foreground(ColorMuted).Render(
			fmt.Sprintf("  %s  [%s]", d.selected, mode),
		)
	}
	line := title + sub
	if lipgloss.Width(line) > d.width {
		line = ansi.Truncate(line, d.width, "…")
	}
	return line + strings.Repeat(" ", max(0, d.width-lipgloss.Width(line)))
}

// renderVerticalSeparator returns a 1-col vertical rule of `h` rows.
func renderVerticalSeparator(h int) string {
	style := lipgloss.NewStyle().Foreground(ColorMuted)
	lines := make([]string, h)
	for i := range lines {
		lines[i] = style.Render("│")
	}
	return strings.Join(lines, "\n")
}

// padViewBody pads `s` to (width × height) cells with blank lines so the
// layout doesn't collapse when content is short.
func padViewBody(s string, width, height int) string {
	if height < 1 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) >= height {
		lines = lines[:height]
	} else {
		for len(lines) < height {
			lines = append(lines, strings.Repeat(" ", width))
		}
	}
	return strings.Join(lines, "\n")
}
