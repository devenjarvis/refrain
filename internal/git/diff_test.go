package git_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devenjarvis/refrain/internal/git"
)

func TestParseDiffFiles_Empty(t *testing.T) {
	files := git.ParseDiffFiles("")
	if len(files) != 0 {
		t.Errorf("expected empty slice, got %d entries", len(files))
	}
}

func TestParseDiffFiles_SingleModified(t *testing.T) {
	rawDiff := `diff --git a/foo.go b/foo.go
index abc1234..def5678 100644
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 package main

+// added comment
 func main() {}
`
	files := git.ParseDiffFiles(rawDiff)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	f := files[0]
	if f.Status != "M" {
		t.Errorf("expected Status \"M\", got %q", f.Status)
	}
	if f.Path != "foo.go" {
		t.Errorf("expected Path \"foo.go\", got %q", f.Path)
	}
	if len(f.Lines) == 0 {
		t.Error("expected non-empty Lines")
	}
	if f.Lines[0] != "diff --git a/foo.go b/foo.go" {
		t.Errorf("first line should be diff header, got %q", f.Lines[0])
	}
	if f.Insertions != 1 {
		t.Errorf("expected 1 insertion, got %d", f.Insertions)
	}
	if f.Deletions != 0 {
		t.Errorf("expected 0 deletions, got %d", f.Deletions)
	}
}

func TestParseDiffFiles_AddedAndDeleted(t *testing.T) {
	rawDiff := `diff --git a/new.txt b/new.txt
new file mode 100644
index 0000000..1234567
--- /dev/null
+++ b/new.txt
@@ -0,0 +1,2 @@
+hello
+world
diff --git a/old.txt b/old.txt
deleted file mode 100644
index 1234567..0000000
--- a/old.txt
+++ /dev/null
@@ -1,2 +0,0 @@
-goodbye
-world
`
	files := git.ParseDiffFiles(rawDiff)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}

	// First file: added
	if files[0].Path != "new.txt" {
		t.Errorf("expected first path \"new.txt\", got %q", files[0].Path)
	}
	if files[0].Status != "A" {
		t.Errorf("expected first status \"A\", got %q", files[0].Status)
	}

	// Second file: deleted
	if files[1].Path != "old.txt" {
		t.Errorf("expected second path \"old.txt\", got %q", files[1].Path)
	}
	if files[1].Status != "D" {
		t.Errorf("expected second status \"D\", got %q", files[1].Status)
	}
}

func TestParseDiffFiles_BinaryFile(t *testing.T) {
	rawDiff := `diff --git a/image.png b/image.png
index abc1234..def5678 100644
Binary files a/image.png and b/image.png differ
`
	files := git.ParseDiffFiles(rawDiff)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	f := files[0]
	if f.Path != "image.png" {
		t.Errorf("expected Path \"image.png\", got %q", f.Path)
	}
	if f.Status != "M" {
		t.Errorf("expected Status \"M\", got %q", f.Status)
	}
	if len(f.Lines) == 0 {
		t.Error("expected non-empty Lines for binary file")
	}
	// Should not have any +/- content lines
	for _, line := range f.Lines {
		if len(line) > 0 && (line[0] == '+' || line[0] == '-') {
			t.Errorf("binary file should not have +/- lines, got %q", line)
		}
	}
}

func TestParseDiffFiles_InsertionDeletionCounts(t *testing.T) {
	rawDiff := `diff --git a/added.txt b/added.txt
new file mode 100644
index 0000000..1234567
--- /dev/null
+++ b/added.txt
@@ -0,0 +1,3 @@
+line1
+line2
+line3
diff --git a/deleted.txt b/deleted.txt
deleted file mode 100644
index 1234567..0000000
--- a/deleted.txt
+++ /dev/null
@@ -1,2 +0,0 @@
-gone1
-gone2
diff --git a/mod.txt b/mod.txt
index abc..def 100644
--- a/mod.txt
+++ b/mod.txt
@@ -1,3 +1,3 @@
 keep
-old
+new
 tail
`
	files := git.ParseDiffFiles(rawDiff)
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}
	checks := map[string]struct{ ins, del int }{
		"added.txt":   {3, 0},
		"deleted.txt": {0, 2},
		"mod.txt":     {1, 1},
	}
	for _, f := range files {
		want := checks[f.Path]
		if f.Insertions != want.ins {
			t.Errorf("%s: expected %d insertions, got %d", f.Path, want.ins, f.Insertions)
		}
		if f.Deletions != want.del {
			t.Errorf("%s: expected %d deletions, got %d", f.Path, want.del, f.Deletions)
		}
	}
}

func TestParseHunks_SingleHunk(t *testing.T) {
	// Note: git outputs " \n" (space+newline) for blank context lines, not
	// bare empty lines. Build the string explicitly so the blank context
	// line in the hunk body has the required leading space.
	rawDiff := "diff --git a/foo.go b/foo.go\n" +
		"index abc1234..def5678 100644\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -1,3 +1,4 @@\n" +
		" package main\n" +
		" \n" +
		"+// added comment\n" +
		" func main() {}\n"
	files := git.ParseDiffFiles(rawDiff)
	hunks := git.ParseHunks(files[0])
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}
	h := hunks[0]
	if h.OldStart != 1 || h.OldCount != 3 {
		t.Errorf("old range: expected 1,3 got %d,%d", h.OldStart, h.OldCount)
	}
	if h.NewStart != 1 || h.NewCount != 4 {
		t.Errorf("new range: expected 1,4 got %d,%d", h.NewStart, h.NewCount)
	}
	if len(h.Lines) != 4 {
		t.Fatalf("expected 4 hunk lines, got %d", len(h.Lines))
	}
	if h.Lines[0].Kind != git.HunkLineContext || h.Lines[0].Text != "package main" {
		t.Errorf("line 0 wrong: kind=%v text=%q", h.Lines[0].Kind, h.Lines[0].Text)
	}
	if h.Lines[1].Kind != git.HunkLineContext || h.Lines[1].Text != "" {
		t.Errorf("line 1 wrong: kind=%v text=%q", h.Lines[1].Kind, h.Lines[1].Text)
	}
	if h.Lines[2].Kind != git.HunkLineAddition || h.Lines[2].Text != "// added comment" {
		t.Errorf("line 2 wrong: kind=%v text=%q", h.Lines[2].Kind, h.Lines[2].Text)
	}
	if h.Lines[3].Kind != git.HunkLineContext || h.Lines[3].Text != "func main() {}" {
		t.Errorf("line 3 wrong: kind=%v text=%q", h.Lines[3].Kind, h.Lines[3].Text)
	}
}

func TestParseHunks_MultipleHunks(t *testing.T) {
	rawDiff := `diff --git a/foo.go b/foo.go
index abc..def 100644
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,3 @@
 a
-b
+B
 c
@@ -10,2 +10,3 @@
 x
+Y
 z
`
	files := git.ParseDiffFiles(rawDiff)
	hunks := git.ParseHunks(files[0])
	if len(hunks) != 2 {
		t.Fatalf("expected 2 hunks, got %d", len(hunks))
	}
	if hunks[0].OldStart != 1 || hunks[1].OldStart != 10 {
		t.Errorf("hunk starts wrong: %d, %d", hunks[0].OldStart, hunks[1].OldStart)
	}
	if len(hunks[0].Lines) != 4 {
		t.Errorf("hunk 0 lines: expected 4, got %d", len(hunks[0].Lines))
	}
	if len(hunks[1].Lines) != 3 {
		t.Errorf("hunk 1 lines: expected 3, got %d", len(hunks[1].Lines))
	}
}

func TestParseHunks_AddedFile(t *testing.T) {
	rawDiff := `diff --git a/new.txt b/new.txt
new file mode 100644
index 0000000..1234567
--- /dev/null
+++ b/new.txt
@@ -0,0 +1,2 @@
+hello
+world
`
	files := git.ParseDiffFiles(rawDiff)
	hunks := git.ParseHunks(files[0])
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}
	if len(hunks[0].Lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(hunks[0].Lines))
	}
	for i, l := range hunks[0].Lines {
		if l.Kind != git.HunkLineAddition {
			t.Errorf("line %d: expected addition, got %v", i, l.Kind)
		}
	}
}

func TestParseHunks_DeletedFile(t *testing.T) {
	rawDiff := `diff --git a/old.txt b/old.txt
deleted file mode 100644
index 1234567..0000000
--- a/old.txt
+++ /dev/null
@@ -1,2 +0,0 @@
-goodbye
-world
`
	files := git.ParseDiffFiles(rawDiff)
	hunks := git.ParseHunks(files[0])
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}
	for i, l := range hunks[0].Lines {
		if l.Kind != git.HunkLineDeletion {
			t.Errorf("line %d: expected deletion, got %v", i, l.Kind)
		}
	}
}

func TestParseHunks_BinaryFile(t *testing.T) {
	rawDiff := `diff --git a/image.png b/image.png
index abc1234..def5678 100644
Binary files a/image.png and b/image.png differ
`
	files := git.ParseDiffFiles(rawDiff)
	hunks := git.ParseHunks(files[0])
	if len(hunks) != 0 {
		t.Errorf("expected 0 hunks for binary file, got %d", len(hunks))
	}
}

func TestParseHunks_EmptyDiff(t *testing.T) {
	files := git.ParseDiffFiles("")
	if len(files) != 0 {
		t.Fatalf("expected 0 files, got %d", len(files))
	}
	// Ensure ParseHunks on a zero-value DiffFile also behaves.
	hunks := git.ParseHunks(git.DiffFile{})
	if len(hunks) != 0 {
		t.Errorf("expected 0 hunks for empty DiffFile, got %d", len(hunks))
	}
}

// gitInDir runs a git command in dir, failing the test on error.
func gitInDir(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(
		os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func TestGetPerFileDiffStats_AddedModifiedDeleted(t *testing.T) {
	repo := initTestRepo(t)

	// Create a file that will be deleted later.
	if err := os.WriteFile(filepath.Join(repo, "to-delete.txt"), []byte("delete me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInDir(t, repo, "add", "to-delete.txt")
	gitInDir(t, repo, "commit", "-m", "add file to delete")

	wt, err := git.CreateWorktree(repo, "stats-agent", "", "", "")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	// Add a new file.
	if err := os.WriteFile(filepath.Join(wt.Path, "new.txt"), []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInDir(t, wt.Path, "add", "new.txt")

	// Modify existing file.
	if err := os.WriteFile(filepath.Join(wt.Path, "README"), []byte("init\nupdated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInDir(t, wt.Path, "add", "README")

	// Delete a file.
	if err := os.Remove(filepath.Join(wt.Path, "to-delete.txt")); err != nil {
		t.Fatal(err)
	}
	gitInDir(t, wt.Path, "add", "to-delete.txt")

	gitInDir(t, wt.Path, "commit", "-m", "add, modify, delete")

	fileStats, aggStats, err := git.GetPerFileDiffStats(repo, wt)
	if err != nil {
		t.Fatalf("GetPerFileDiffStats: %v", err)
	}

	// Build a map for easy lookup.
	byPath := make(map[string]git.FileStat)
	for _, fs := range fileStats {
		byPath[fs.Path] = fs
	}

	// Check added file.
	if fs, ok := byPath["new.txt"]; !ok {
		t.Error("expected new.txt in results")
	} else {
		if fs.Status != "A" {
			t.Errorf("new.txt: expected status A, got %q", fs.Status)
		}
		if fs.Insertions != 2 {
			t.Errorf("new.txt: expected 2 insertions, got %d", fs.Insertions)
		}
		if fs.Deletions != 0 {
			t.Errorf("new.txt: expected 0 deletions, got %d", fs.Deletions)
		}
	}

	// Check modified file.
	if fs, ok := byPath["README"]; !ok {
		t.Error("expected README in results")
	} else {
		if fs.Status != "M" {
			t.Errorf("README: expected status M, got %q", fs.Status)
		}
		if fs.Insertions < 1 {
			t.Errorf("README: expected at least 1 insertion, got %d", fs.Insertions)
		}
	}

	// Check deleted file.
	if fs, ok := byPath["to-delete.txt"]; !ok {
		t.Error("expected to-delete.txt in results")
	} else {
		if fs.Status != "D" {
			t.Errorf("to-delete.txt: expected status D, got %q", fs.Status)
		}
		if fs.Deletions != 1 {
			t.Errorf("to-delete.txt: expected 1 deletion, got %d", fs.Deletions)
		}
	}

	// Check aggregate stats.
	if aggStats.Files != 3 {
		t.Errorf("expected 3 files, got %d", aggStats.Files)
	}
	if aggStats.Insertions < 3 {
		t.Errorf("expected at least 3 insertions, got %d", aggStats.Insertions)
	}
	if aggStats.Deletions < 1 {
		t.Errorf("expected at least 1 deletion, got %d", aggStats.Deletions)
	}
}

func TestGetPerFileDiffStats_BinaryFile(t *testing.T) {
	repo := initTestRepo(t)

	wt, err := git.CreateWorktree(repo, "bin-agent", "", "", "")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	// Write a binary file (contains null bytes).
	binaryContent := []byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0x00, 0x00}
	if err := os.WriteFile(filepath.Join(wt.Path, "image.png"), binaryContent, 0o644); err != nil {
		t.Fatal(err)
	}
	gitInDir(t, wt.Path, "add", "image.png")
	gitInDir(t, wt.Path, "commit", "-m", "add binary file")

	fileStats, aggStats, err := git.GetPerFileDiffStats(repo, wt)
	if err != nil {
		t.Fatalf("GetPerFileDiffStats: %v", err)
	}

	if len(fileStats) != 1 {
		t.Fatalf("expected 1 file, got %d", len(fileStats))
	}

	fs := fileStats[0]
	if fs.Path != "image.png" {
		t.Errorf("expected path image.png, got %q", fs.Path)
	}
	if fs.Insertions != 0 {
		t.Errorf("binary file: expected 0 insertions, got %d", fs.Insertions)
	}
	if fs.Deletions != 0 {
		t.Errorf("binary file: expected 0 deletions, got %d", fs.Deletions)
	}

	if aggStats.Files != 1 {
		t.Errorf("expected 1 file in aggregate, got %d", aggStats.Files)
	}
}

func TestGetPerFileDiffStats_UncommittedChanges(t *testing.T) {
	repo := initTestRepo(t)

	wt, err := git.CreateWorktree(repo, "wip-agent", "", "", "")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	// Make a committed change.
	if err := os.WriteFile(filepath.Join(wt.Path, "committed.txt"), []byte("line1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInDir(t, wt.Path, "add", "committed.txt")
	gitInDir(t, wt.Path, "commit", "-m", "add committed file")

	// Make an uncommitted change (new file, staged but not committed).
	if err := os.WriteFile(filepath.Join(wt.Path, "uncommitted.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInDir(t, wt.Path, "add", "uncommitted.txt")

	// Make an uncommitted modification to the committed file (unstaged).
	if err := os.WriteFile(filepath.Join(wt.Path, "committed.txt"), []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fileStats, _, err := git.GetPerFileDiffStats(repo, wt)
	if err != nil {
		t.Fatalf("GetPerFileDiffStats: %v", err)
	}

	byPath := make(map[string]git.FileStat)
	for _, fs := range fileStats {
		byPath[fs.Path] = fs
	}

	// committed.txt should show merged committed + uncommitted stats.
	if fs, ok := byPath["committed.txt"]; !ok {
		t.Error("expected committed.txt in results")
	} else {
		if fs.Insertions < 2 {
			t.Errorf("committed.txt: expected at least 2 insertions (committed + uncommitted), got %d", fs.Insertions)
		}
	}

	// uncommitted.txt should appear from working tree diff.
	if _, ok := byPath["uncommitted.txt"]; !ok {
		t.Error("expected uncommitted.txt in results from working tree changes")
	}
}

func TestDiff_IncludesCommittedAndUncommitted(t *testing.T) {
	repo := initTestRepo(t)

	wt, err := git.CreateWorktree(repo, "wip-agent", "", "", "")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	// Committed change.
	if err := os.WriteFile(filepath.Join(wt.Path, "committed.txt"), []byte("line1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInDir(t, wt.Path, "add", "committed.txt")
	gitInDir(t, wt.Path, "commit", "-m", "add committed file")

	// Uncommitted staged change.
	if err := os.WriteFile(filepath.Join(wt.Path, "staged.txt"), []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInDir(t, wt.Path, "add", "staged.txt")

	// Uncommitted unstaged change to the committed file.
	if err := os.WriteFile(filepath.Join(wt.Path, "committed.txt"), []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Uncommitted untracked file — git diff with --diff-filter=AMD should not
	// include this (it has no tracked side), so we don't assert on it.

	raw, err := git.Diff(repo, wt)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	if !strings.Contains(raw, "committed.txt") {
		t.Errorf("diff missing committed.txt:\n%s", raw)
	}
	if !strings.Contains(raw, "staged.txt") {
		t.Errorf("diff missing staged.txt:\n%s", raw)
	}
	if !strings.Contains(raw, "+line2") {
		t.Errorf("diff missing uncommitted +line2 addition:\n%s", raw)
	}
	if !strings.Contains(raw, "+staged") {
		t.Errorf("diff missing +staged addition:\n%s", raw)
	}

	// Parseable as a unified diff.
	files := git.ParseDiffFiles(raw)
	if len(files) < 2 {
		t.Errorf("expected at least 2 files in parsed diff, got %d", len(files))
	}
}

func TestGetPerFileDiffStats_NoChanges(t *testing.T) {
	repo := initTestRepo(t)

	wt, err := git.CreateWorktree(repo, "empty-agent", "", "", "")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	fileStats, aggStats, err := git.GetPerFileDiffStats(repo, wt)
	if err != nil {
		t.Fatalf("GetPerFileDiffStats: %v", err)
	}

	if len(fileStats) != 0 {
		t.Errorf("expected 0 file stats, got %d", len(fileStats))
	}
	if aggStats.Files != 0 {
		t.Errorf("expected 0 files, got %d", aggStats.Files)
	}
}
