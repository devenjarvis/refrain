package git_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devenjarvis/refrain/internal/git"
)

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
