package diff_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/refrain/internal/diffmodel"
	"github.com/devenjarvis/refrain/internal/tui/diff"
)

func buildTree(t *testing.T, paths ...string) *diff.Tree {
	t.Helper()
	var b strings.Builder
	for _, p := range paths {
		b.WriteString("diff --git a/" + p + " b/" + p + "\n")
		b.WriteString("index abc..def 100644\n")
		b.WriteString("--- a/" + p + "\n")
		b.WriteString("+++ b/" + p + "\n")
		b.WriteString("@@ -1 +1 @@\n")
		b.WriteString("-old\n")
		b.WriteString("+new\n")
	}
	m, err := diffmodel.Parse(b.String())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return diff.NewTree(m)
}

func keyPress(s string) tea.KeyPressMsg {
	switch s {
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "space":
		return tea.KeyPressMsg{Code: ' ', Text: " "}
	}
	if len(s) == 1 {
		r := rune(s[0])
		return tea.KeyPressMsg{Code: r, Text: s}
	}
	return tea.KeyPressMsg{}
}

func TestTree_InitialState(t *testing.T) {
	tr := buildTree(
		t,
		"cmd/root.go",
		"cmd/hook.go",
		"internal/tui/app.go",
	)
	if tr.Len() == 0 {
		t.Fatal("expected non-empty tree")
	}
	// Every folder starts expanded, so flat list = folders + leaves.
	sel := tr.SelectedFile()
	if sel == nil {
		t.Fatal("expected cursor on a leaf by default")
	}
}

func TestTree_CollapseFolder(t *testing.T) {
	tr := buildTree(
		t,
		"cmd/a.go",
		"cmd/b.go",
		"internal/x.go",
	)
	before := tr.Len()
	// Move cursor to the root folder "cmd" (first item).
	out := tr.View(40, 20)
	if !strings.Contains(ansi.Strip(out), "cmd") {
		t.Fatalf("expected 'cmd' visible: %q", ansi.Strip(out))
	}
	// Press g to land cursor on first row, then space to collapse.
	tr, _ = tr.Update(keyPress("g"))
	tr, _ = tr.Update(keyPress("space"))
	after := tr.Len()
	if after >= before {
		t.Errorf("collapse should reduce visible rows: before=%d after=%d", before, after)
	}
	// Re-expand.
	tr, _ = tr.Update(keyPress("space"))
	if tr.Len() != before {
		t.Errorf("re-expand should restore rows: want %d, got %d", before, tr.Len())
	}
}

func TestTree_CursorNavigation(t *testing.T) {
	tr := buildTree(t, "a.go", "b.go", "c.go")

	// Move down past bounds.
	for i := 0; i < 10; i++ {
		tr, _ = tr.Update(keyPress("j"))
	}
	if tr.Selected() == nil {
		t.Fatal("cursor should still be valid")
	}
	// Move up past bounds.
	for i := 0; i < 20; i++ {
		tr, _ = tr.Update(keyPress("k"))
	}
	if tr.Selected() == nil {
		t.Fatal("cursor should still be valid after upwards clamp")
	}
}

func TestTree_EnterEmitsSelection(t *testing.T) {
	tr := buildTree(t, "a.go", "b.go")
	// Cursor is already on a leaf.
	_, cmd := tr.Update(keyPress("enter"))
	if cmd == nil {
		t.Fatal("expected command from enter on leaf")
	}
	msg := cmd()
	sel, ok := msg.(diff.FileSelectedMsg)
	if !ok {
		t.Fatalf("expected FileSelectedMsg, got %T", msg)
	}
	if sel.Path == "" {
		t.Error("FileSelectedMsg should carry a path")
	}
}

func TestTree_EmptyModel(t *testing.T) {
	tr := diff.NewTree(&diffmodel.Model{})
	if tr.Len() != 0 {
		t.Errorf("expected empty tree, got %d rows", tr.Len())
	}
	out := tr.View(30, 5)
	lines := strings.Split(out, "\n")
	if len(lines) != 5 {
		t.Errorf("View should pad to height: got %d lines", len(lines))
	}
}

func TestTree_NarrowWidth(t *testing.T) {
	tr := buildTree(t, "internal/tui/diff/render.go")
	out := tr.View(20, 10)
	// No line may exceed 20 cells.
	for i, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w > 20 {
			t.Errorf("line %d width %d > 20: %q", i, w, line)
		}
	}
}

func TestTree_HCollapsesOrStepsUp(t *testing.T) {
	tr := buildTree(t, "cmd/a.go", "cmd/b.go")
	// Start cursor at a leaf.
	// Press h: since we're on a leaf, step up to parent folder.
	tr, _ = tr.Update(keyPress("h"))
	sel := tr.Selected()
	if sel == nil || sel.IsLeaf {
		t.Fatalf("after h on leaf, cursor should be on a folder; got %+v", sel)
	}
}
