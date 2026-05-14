package diffmodel_test

import (
	"strings"
	"testing"

	"github.com/devenjarvis/refrain/internal/diffmodel"
)

func TestParse_EmptyInput(t *testing.T) {
	m, err := diffmodel.Parse("")
	if err != nil {
		t.Fatalf("Parse empty: %v", err)
	}
	if len(m.Files) != 0 {
		t.Errorf("expected 0 files, got %d", len(m.Files))
	}
}

func TestParse_Modified(t *testing.T) {
	raw := "diff --git a/foo.go b/foo.go\n" +
		"index abc..def 100644\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -1,3 +1,4 @@\n" +
		" package main\n" +
		" \n" +
		"+// added\n" +
		" func main() {}\n"

	m, err := diffmodel.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(m.Files))
	}
	f := m.Files[0]
	if f.Path != "foo.go" {
		t.Errorf("Path: expected foo.go, got %q", f.Path)
	}
	if f.Status != diffmodel.StatusModified {
		t.Errorf("Status: expected Modified, got %v", f.Status)
	}
	if f.Insertions != 1 || f.Deletions != 0 {
		t.Errorf("counts: expected 1+/0-, got %d+/%d-", f.Insertions, f.Deletions)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(f.Hunks))
	}
	h := f.Hunks[0]
	if len(h.Lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(h.Lines))
	}
	if h.Lines[0].Kind != diffmodel.LineContext || h.Lines[0].Text != "package main" {
		t.Errorf("line 0: %+v", h.Lines[0])
	}
	if h.Lines[0].OldNum != 1 || h.Lines[0].NewNum != 1 {
		t.Errorf("line 0 numbers: %d/%d", h.Lines[0].OldNum, h.Lines[0].NewNum)
	}
	if h.Lines[2].Kind != diffmodel.LineAdd || h.Lines[2].Text != "// added" {
		t.Errorf("line 2: %+v", h.Lines[2])
	}
	// Added line has no old-side number.
	if h.Lines[2].OldNum != 0 {
		t.Errorf("line 2 OldNum: expected 0, got %d", h.Lines[2].OldNum)
	}
}

func TestParse_Added(t *testing.T) {
	raw := "diff --git a/new.txt b/new.txt\n" +
		"new file mode 100644\n" +
		"index 0000000..1234567\n" +
		"--- /dev/null\n" +
		"+++ b/new.txt\n" +
		"@@ -0,0 +1,2 @@\n" +
		"+hello\n" +
		"+world\n"
	m, err := diffmodel.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(m.Files))
	}
	f := m.Files[0]
	if f.Status != diffmodel.StatusAdded {
		t.Errorf("expected Status Added, got %v", f.Status)
	}
	if f.Path != "new.txt" {
		t.Errorf("Path: %q", f.Path)
	}
	if f.Insertions != 2 || f.Deletions != 0 {
		t.Errorf("counts: %d+/%d-", f.Insertions, f.Deletions)
	}
	for i, l := range f.Hunks[0].Lines {
		if l.Kind != diffmodel.LineAdd {
			t.Errorf("line %d not an add: %v", i, l.Kind)
		}
	}
}

func TestParse_Deleted(t *testing.T) {
	raw := "diff --git a/old.txt b/old.txt\n" +
		"deleted file mode 100644\n" +
		"index 1234567..0000000\n" +
		"--- a/old.txt\n" +
		"+++ /dev/null\n" +
		"@@ -1,2 +0,0 @@\n" +
		"-gone1\n" +
		"-gone2\n"
	m, err := diffmodel.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(m.Files))
	}
	f := m.Files[0]
	if f.Status != diffmodel.StatusDeleted {
		t.Errorf("expected Status Deleted, got %v", f.Status)
	}
	if f.Path != "old.txt" {
		t.Errorf("Path: %q", f.Path)
	}
	if f.Insertions != 0 || f.Deletions != 2 {
		t.Errorf("counts: %d+/%d-", f.Insertions, f.Deletions)
	}
}

func TestParse_Renamed(t *testing.T) {
	raw := "diff --git a/old.txt b/new.txt\n" +
		"similarity index 100%\n" +
		"rename from old.txt\n" +
		"rename to new.txt\n"
	m, err := diffmodel.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(m.Files))
	}
	f := m.Files[0]
	if f.Status != diffmodel.StatusRenamed {
		t.Errorf("expected Status Renamed, got %v", f.Status)
	}
	if f.Path != "new.txt" {
		t.Errorf("Path: %q", f.Path)
	}
	if f.OldPath != "old.txt" {
		t.Errorf("OldPath: %q", f.OldPath)
	}
}

func TestParse_Binary(t *testing.T) {
	raw := "diff --git a/image.png b/image.png\n" +
		"index abc1234..def5678 100644\n" +
		"Binary files a/image.png and b/image.png differ\n"
	m, err := diffmodel.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(m.Files))
	}
	f := m.Files[0]
	if !f.IsBinary {
		t.Error("expected IsBinary = true")
	}
	if len(f.Hunks) != 0 {
		t.Errorf("binary file should have no hunks, got %d", len(f.Hunks))
	}
}

func TestParse_MultiHunk(t *testing.T) {
	raw := "diff --git a/foo.go b/foo.go\n" +
		"index abc..def 100644\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -1,3 +1,3 @@\n" +
		" a\n" +
		"-b\n" +
		"+B\n" +
		" c\n" +
		"@@ -10,2 +10,3 @@\n" +
		" x\n" +
		"+Y\n" +
		" z\n"
	m, err := diffmodel.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(m.Files))
	}
	f := m.Files[0]
	if len(f.Hunks) != 2 {
		t.Fatalf("expected 2 hunks, got %d", len(f.Hunks))
	}
	if f.Hunks[0].OldStart != 1 {
		t.Errorf("hunk 0 OldStart: %d", f.Hunks[0].OldStart)
	}
	if f.Hunks[1].OldStart != 10 {
		t.Errorf("hunk 1 OldStart: %d", f.Hunks[1].OldStart)
	}
	// Second hunk first line should be 'x' at old 10, new 10.
	l := f.Hunks[1].Lines[0]
	if l.OldNum != 10 || l.NewNum != 10 {
		t.Errorf("hunk 1 first line numbers: %d/%d", l.OldNum, l.NewNum)
	}
}

func TestTree_BuildsFromPaths(t *testing.T) {
	// Build a diff with 30 files across nested directories.
	var b strings.Builder
	paths := []string{
		"cmd/root.go",
		"cmd/hook.go",
		"cmd/doctor.go",
		"internal/tui/app.go",
		"internal/tui/view.go",
		"internal/tui/diff/render.go",
		"internal/tui/diff/tree.go",
		"internal/git/diff.go",
		"internal/git/worktree.go",
		"internal/git/git_test.go",
		"internal/agent/manager.go",
		"internal/agent/events.go",
		"internal/agent/status.go",
		"internal/hook/server.go",
		"internal/hook/client.go",
		"internal/config/global.go",
		"internal/config/repo.go",
		"internal/state/state.go",
		"docs/CHANGELOG.md",
		"docs/plans/a.md",
		"docs/plans/b.md",
		"docs/plans/c.md",
		"scripts/release.sh",
		"scripts/lint.sh",
		"README.md",
		"LICENSE",
		"go.mod",
		"go.sum",
		"Makefile",
		"main.go",
	}
	if len(paths) != 30 {
		t.Fatalf("test setup: want 30 paths, got %d", len(paths))
	}
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
	if len(m.Files) != 30 {
		t.Fatalf("expected 30 files, got %d", len(m.Files))
	}

	root := m.Tree()
	if root.IsLeaf {
		t.Fatal("root should not be a leaf")
	}

	// Count leaves; must be 30.
	var count int
	var walk func(*diffmodel.FileNode)
	walk = func(n *diffmodel.FileNode) {
		if n.IsLeaf {
			count++
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	if count != 30 {
		t.Errorf("expected 30 leaves, got %d", count)
	}

	// Root should have folders first, then files. Verify ordering.
	prevLeaf := false
	for _, c := range root.Children {
		if prevLeaf && !c.IsLeaf {
			t.Error("folders must come before files at each level")
			break
		}
		if c.IsLeaf {
			prevLeaf = true
		}
	}

	// Look up internal/tui/diff: should exist as a folder with 2 children.
	var findFolder func(*diffmodel.FileNode, string) *diffmodel.FileNode
	findFolder = func(n *diffmodel.FileNode, path string) *diffmodel.FileNode {
		if n.Path == path && !n.IsLeaf {
			return n
		}
		for _, c := range n.Children {
			if r := findFolder(c, path); r != nil {
				return r
			}
		}
		return nil
	}
	diffDir := findFolder(root, "internal/tui/diff")
	if diffDir == nil {
		t.Fatal("expected folder internal/tui/diff")
	}
	if len(diffDir.Children) != 2 {
		t.Errorf("internal/tui/diff: expected 2 children, got %d", len(diffDir.Children))
	}
}

func TestTree_EmptyModel(t *testing.T) {
	m := &diffmodel.Model{}
	root := m.Tree()
	if root == nil {
		t.Fatal("Tree should return non-nil root even for empty model")
	}
	if len(root.Children) != 0 {
		t.Errorf("empty model: expected no children, got %d", len(root.Children))
	}
}

func TestParse_OnlyAdditions(t *testing.T) {
	raw := "diff --git a/notes.md b/notes.md\n" +
		"index abc..def 100644\n" +
		"--- a/notes.md\n" +
		"+++ b/notes.md\n" +
		"@@ -1,2 +1,5 @@\n" +
		" head\n" +
		"+one\n" +
		"+two\n" +
		"+three\n" +
		" tail\n"
	m, err := diffmodel.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	f := m.Files[0]
	if f.Insertions != 3 || f.Deletions != 0 {
		t.Errorf("counts: %d+/%d-", f.Insertions, f.Deletions)
	}
}

func TestParse_OnlyDeletions(t *testing.T) {
	raw := "diff --git a/foo.go b/foo.go\n" +
		"index abc..def 100644\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -1,5 +1,2 @@\n" +
		" head\n" +
		"-one\n" +
		"-two\n" +
		"-three\n" +
		" tail\n"
	m, err := diffmodel.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	f := m.Files[0]
	if f.Insertions != 0 || f.Deletions != 3 {
		t.Errorf("counts: %d+/%d-", f.Insertions, f.Deletions)
	}
}

func TestParse_TrailingEmptyContextLine(t *testing.T) {
	// Regression: git.Diff previously used strings.TrimSpace which stripped
	// trailing " \n" context lines (empty source lines). go-gitdiff then saw
	// one fewer line than the header declared and returned
	// "fragment header miscounts lines: -1 old, -1 new".
	raw := "diff --git a/foo.go b/foo.go\n" +
		"index abc..def 100644\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -1,4 +1,5 @@\n" +
		" package main\n" +
		" \n" +
		"+// added\n" +
		" func main() {}\n" +
		" \n" // trailing empty context line — stripped by TrimSpace before the fix
	m, err := diffmodel.Parse(raw)
	if err != nil {
		t.Fatalf("Parse rejected diff ending with empty context line: %v", err)
	}
	if len(m.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(m.Files))
	}
	f := m.Files[0]
	if f.Insertions != 1 || f.Deletions != 0 {
		t.Errorf("counts: %d+/%d-", f.Insertions, f.Deletions)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(f.Hunks))
	}
	if len(f.Hunks[0].Lines) != 5 {
		t.Errorf("expected 5 hunk lines (4 original + 1 added), got %d", len(f.Hunks[0].Lines))
	}
}
