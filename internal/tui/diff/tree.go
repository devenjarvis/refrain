package diff

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"

	"github.com/devenjarvis/refrain/internal/diffmodel"
)

// FileSelectedMsg is emitted when the user selects a file leaf in the tree.
// It carries the path of the selected file so the parent model can swap in
// the corresponding Renderer.
type FileSelectedMsg struct {
	Path string
}

// Tree is the left-pane file picker. Build one with NewTree, feed it
// tea.KeyPressMsg via Update, and render with View(width, height).
type Tree struct {
	root *diffmodel.FileNode

	// flat is a flattened, visible-only view rebuilt whenever expand/collapse
	// changes. Index into flat is the cursor position.
	flat []*diffmodel.FileNode

	// expanded tracks which folder Paths are expanded. Leaves aren't in here.
	expanded map[string]bool

	cursor int
	scroll int
}

// NewTree builds a Tree for the given parsed model. All folders start
// expanded; the cursor lands on the first leaf.
func NewTree(m *diffmodel.Model) *Tree {
	t := &Tree{
		expanded: make(map[string]bool),
	}
	if m != nil {
		t.root = m.Tree()
	}
	// Expand every folder by default.
	var walk func(*diffmodel.FileNode)
	walk = func(n *diffmodel.FileNode) {
		if n == nil {
			return
		}
		if !n.IsLeaf {
			t.expanded[n.Path] = true
			for _, c := range n.Children {
				walk(c)
			}
		}
	}
	walk(t.root)

	t.rebuild()
	// Land cursor on first leaf if any.
	for i, n := range t.flat {
		if n.IsLeaf {
			t.cursor = i
			break
		}
	}
	return t
}

// Len reports how many visible rows the tree currently has.
func (t *Tree) Len() int { return len(t.flat) }

// Selected returns the currently selected node (may be a folder). Returns nil
// if the tree is empty.
func (t *Tree) Selected() *diffmodel.FileNode {
	if t.cursor < 0 || t.cursor >= len(t.flat) {
		return nil
	}
	return t.flat[t.cursor]
}

// SelectedFile returns the currently selected File, or nil if the cursor is
// on a folder or the tree is empty.
func (t *Tree) SelectedFile() *diffmodel.File {
	n := t.Selected()
	if n == nil || !n.IsLeaf {
		return nil
	}
	return n.File
}

// SelectPath moves the cursor to the leaf with the given path if found.
func (t *Tree) SelectPath(path string) {
	for i, n := range t.flat {
		if n.IsLeaf && n.Path == path {
			t.cursor = i
			return
		}
	}
}

// Update processes a key event and returns (tree, cmd). A non-nil cmd may
// carry a FileSelectedMsg when the user selects a leaf.
func (t *Tree) Update(msg tea.KeyPressMsg) (*Tree, tea.Cmd) {
	if len(t.flat) == 0 {
		return t, nil
	}
	switch msg.String() {
	case "j", "down":
		t.moveCursor(1)
	case "k", "up":
		t.moveCursor(-1)
	case "g":
		t.cursor = 0
	case "G":
		t.cursor = len(t.flat) - 1
	case "h", "left":
		t.collapseOrStepUp()
	case "l", "right":
		t.expandOrOpen()
		if sel := t.SelectedFile(); sel != nil {
			path := sel.Path
			return t, func() tea.Msg { return FileSelectedMsg{Path: path} }
		}
	case "enter":
		if sel := t.SelectedFile(); sel != nil {
			path := sel.Path
			return t, func() tea.Msg { return FileSelectedMsg{Path: path} }
		}
		// On a folder, enter toggles.
		t.toggleCurrent()
	case " ", "space":
		t.toggleCurrent()
	}
	return t, nil
}

func (t *Tree) moveCursor(delta int) {
	t.cursor += delta
	if t.cursor < 0 {
		t.cursor = 0
	}
	if t.cursor >= len(t.flat) {
		t.cursor = len(t.flat) - 1
	}
}

func (t *Tree) collapseOrStepUp() {
	n := t.Selected()
	if n == nil {
		return
	}
	// Folder & expanded: collapse it.
	if !n.IsLeaf && t.expanded[n.Path] {
		t.expanded[n.Path] = false
		t.rebuild()
		t.clampCursor()
		return
	}
	// Otherwise step the cursor to the parent folder.
	parentPath := parentPath(n.Path)
	for i, node := range t.flat {
		if !node.IsLeaf && node.Path == parentPath {
			t.cursor = i
			return
		}
	}
}

func (t *Tree) expandOrOpen() {
	n := t.Selected()
	if n == nil {
		return
	}
	if !n.IsLeaf {
		// Expand if not already.
		if !t.expanded[n.Path] {
			t.expanded[n.Path] = true
			t.rebuild()
			t.clampCursor()
		}
	}
}

func (t *Tree) toggleCurrent() {
	n := t.Selected()
	if n == nil || n.IsLeaf {
		return
	}
	t.expanded[n.Path] = !t.expanded[n.Path]
	t.rebuild()
	t.clampCursor()
}

func (t *Tree) clampCursor() {
	if t.cursor >= len(t.flat) {
		t.cursor = len(t.flat) - 1
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
}

// rebuild regenerates the flat visible list from root + expanded state.
func (t *Tree) rebuild() {
	t.flat = t.flat[:0]
	if t.root == nil {
		return
	}
	var walk func(*diffmodel.FileNode, int)
	walk = func(n *diffmodel.FileNode, depth int) {
		if n == nil {
			return
		}
		// Skip the synthetic root itself; descend directly.
		if n != t.root {
			t.flat = append(t.flat, n)
		}
		if n.IsLeaf {
			return
		}
		// Expanded by default; the synthetic root is always "expanded".
		if n == t.root || t.expanded[n.Path] {
			for _, c := range n.Children {
				next := depth
				if n != t.root {
					next = depth + 1
				}
				walk(c, next)
			}
		}
	}
	walk(t.root, 0)
}

// View renders the tree into a width×height string. If height is smaller
// than the flat list, the window slides so the cursor stays visible.
func (t *Tree) View(width, height int) string {
	if height < 1 {
		return ""
	}
	if width < 1 {
		width = 1
	}
	if len(t.flat) == 0 {
		return padBlock("(no files)", width, height)
	}

	// Window so cursor is visible.
	if t.cursor < t.scroll {
		t.scroll = t.cursor
	}
	if t.cursor >= t.scroll+height {
		t.scroll = t.cursor - height + 1
	}
	if t.scroll < 0 {
		t.scroll = 0
	}
	if t.scroll > len(t.flat)-height {
		t.scroll = len(t.flat) - height
		if t.scroll < 0 {
			t.scroll = 0
		}
	}

	end := t.scroll + height
	if end > len(t.flat) {
		end = len(t.flat)
	}

	lines := make([]string, 0, height)
	for i := t.scroll; i < end; i++ {
		n := t.flat[i]
		line := t.renderNode(n, i == t.cursor, width)
		lines = append(lines, line)
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

var (
	treeStyleCursor    = lipgloss.NewStyle().Bold(true).Foreground(colSecondary)
	treeStyleNormal    = lipgloss.NewStyle().Foreground(colMuted)
	treeStyleFile      = lipgloss.NewStyle().Foreground(lipgloss.Color("#F9FAFB"))
	treeStyleFileDim   = lipgloss.NewStyle().Foreground(colMuted)
	treeStyleAddCount  = lipgloss.NewStyle().Foreground(colAdd)
	treeStyleDelCount  = lipgloss.NewStyle().Foreground(colDel)
	treeStyleFolderTag = lipgloss.NewStyle().Foreground(colSecondary)
)

// depthOf counts the number of slashes in a node's Path to derive indent.
// The synthetic root has Path "" (depth 0). Top-level entries have depth 0.
func depthOf(path string) int {
	if path == "" {
		return 0
	}
	return strings.Count(path, "/")
}

func (t *Tree) renderNode(n *diffmodel.FileNode, isCursor bool, width int) string {
	indent := strings.Repeat("  ", depthOf(n.Path))
	var body string
	if n.IsLeaf {
		label := n.Name
		if n.File != nil {
			counts := treeStyleAddCount.Render(fmt.Sprintf("+%d", n.File.Insertions)) + " " +
				treeStyleDelCount.Render(fmt.Sprintf("-%d", n.File.Deletions))
			body = indent + "  " + treeStyleFile.Render(label) + " " + counts
		} else {
			body = indent + "  " + treeStyleFileDim.Render(label)
		}
	} else {
		var glyph string
		if t.expanded[n.Path] {
			glyph = "▾ "
		} else {
			glyph = "▸ "
		}
		body = indent + treeStyleFolderTag.Render(glyph) + n.Name
	}
	if isCursor {
		body = treeStyleCursor.Render("▍ ") + body
	} else {
		body = treeStyleNormal.Render("  ") + body
	}
	body = truncateVisible(body, width)
	return padTo(body, width)
}

func parentPath(p string) string {
	if p == "" {
		return ""
	}
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return ""
	}
	return p[:i]
}

func padBlock(msg string, width, height int) string {
	line := padTo(msg, width)
	lines := make([]string, height)
	lines[0] = line
	for i := 1; i < height; i++ {
		lines[i] = strings.Repeat(" ", width)
	}
	return strings.Join(lines, "\n")
}
