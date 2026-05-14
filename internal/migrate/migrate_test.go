package migrate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobalHome_Idempotent_WhenNewExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Both dirs exist — GlobalHome must not touch either.
	if err := os.Mkdir(filepath.Join(home, ".refrain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, ".baton"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".baton", "marker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := GlobalHome(); err != nil {
		t.Fatalf("GlobalHome: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".baton", "marker")); err != nil {
		t.Errorf("~/.baton/marker should still exist when ~/.refrain pre-exists, got %v", err)
	}
}

func TestGlobalHome_Renames(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := os.Mkdir(filepath.Join(home, ".baton"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".baton", "repos.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := GlobalHome(); err != nil {
		t.Fatalf("GlobalHome: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".baton")); !os.IsNotExist(err) {
		t.Errorf("~/.baton should be gone after migration")
	}
	if _, err := os.Stat(filepath.Join(home, ".refrain", "repos.json")); err != nil {
		t.Errorf("~/.refrain/repos.json should exist after migration: %v", err)
	}
}

func TestGlobalHome_NoOp_FreshInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := GlobalHome(); err != nil {
		t.Fatalf("GlobalHome: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".refrain")); !os.IsNotExist(err) {
		t.Errorf("~/.refrain should not be created when no legacy dir exists")
	}
}

func TestRepoState_RenamesAndRepairsWorktree(t *testing.T) {
	repo := initRepo(t)
	if out, err := exec.Command("git", "-C", repo, "commit", "--allow-empty", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}

	// Pre-rename layout: a worktree under .baton/worktrees/foo.
	if err := os.MkdirAll(filepath.Join(repo, ".baton"), 0o755); err != nil {
		t.Fatal(err)
	}
	wtPath := filepath.Join(repo, ".baton", "worktrees", "foo")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-b", "baton/foo", wtPath).CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}

	// .gitignore mentions the old name.
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".baton/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RepoState(repo); err != nil {
		t.Fatalf("RepoState: %v", err)
	}

	// Old dir gone, new dir present.
	if _, err := os.Stat(filepath.Join(repo, ".baton")); !os.IsNotExist(err) {
		t.Errorf(".baton should be gone")
	}
	if _, err := os.Stat(filepath.Join(repo, ".refrain", "worktrees", "foo")); err != nil {
		t.Errorf(".refrain/worktrees/foo missing: %v", err)
	}

	// Worktree's git status should work — the gitdir pointer was repaired.
	if out, err := exec.Command("git", "-C", filepath.Join(repo, ".refrain", "worktrees", "foo"), "status").CombinedOutput(); err != nil {
		t.Errorf("git status in moved worktree: %v\n%s", err, out)
	}

	// .gitignore was rewritten.
	data, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), ".refrain/") {
		t.Errorf(".gitignore should contain .refrain/, got: %s", data)
	}
	if strings.Contains(string(data), ".baton/") {
		t.Errorf(".gitignore should not contain .baton/, got: %s", data)
	}
}

func TestRepoState_NoOp_WhenNewDirExists(t *testing.T) {
	repo := initRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".refrain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".baton"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := RepoState(repo); err != nil {
		t.Fatalf("RepoState: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".baton")); err != nil {
		t.Errorf(".baton should be untouched when .refrain already exists")
	}
}

func TestUpdateGitignore_AppendsWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("foo\nbar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := updateGitignore(dir); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(data), ".refrain/") {
		t.Errorf(".gitignore should contain .refrain/, got: %s", data)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
		{"config", "tag.gpgsign", "false"},
		{"checkout", "-b", "main"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}
