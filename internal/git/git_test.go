package git_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devenjarvis/baton/internal/git"
)

// initTestRepo creates a temporary git repo with an initial commit and returns its path.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "commit.gpgsign", "false"},
		{"git", "checkout", "-b", "main"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup %v failed: %v\n%s", args, err, out)
		}
	}

	// Create an initial file and commit so the repo is not empty.
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "initial commit"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup %v failed: %v\n%s", args, err, out)
		}
	}

	return dir
}

func TestIsRepo(t *testing.T) {
	repo := initTestRepo(t)

	// A real git repo should return true.
	if !git.IsRepo(repo) {
		t.Errorf("IsRepo(%q) = false, want true", repo)
	}

	// A plain temp dir (not a git repo) should return false.
	plain := t.TempDir()
	if git.IsRepo(plain) {
		t.Errorf("IsRepo(%q) = true, want false", plain)
	}
}

func TestBaseBranch(t *testing.T) {
	repo := initTestRepo(t)

	branch, err := git.BaseBranch(repo)
	if err != nil {
		t.Fatalf("BaseBranch: %v", err)
	}
	if branch != "main" {
		t.Errorf("expected 'main', got %q", branch)
	}
}

func TestCreateWorktree(t *testing.T) {
	repo := initTestRepo(t)

	wt, err := git.CreateWorktree(repo, "agent1", "", "", "")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	// Worktree directory should exist.
	if _, err := os.Stat(wt.Path); os.IsNotExist(err) {
		t.Errorf("worktree path %s does not exist", wt.Path)
	}

	// Branch should be baton/agent1.
	if wt.Branch != "baton/agent1" {
		t.Errorf("expected branch 'baton/agent1', got %q", wt.Branch)
	}

	// BaseBranch should be main.
	if wt.BaseBranch != "main" {
		t.Errorf("expected base branch 'main', got %q", wt.BaseBranch)
	}

	// The branch should exist in git.
	cmd := exec.Command("git", "branch", "--list", "baton/agent1")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git branch --list: %v", err)
	}
	if !strings.Contains(string(out), "baton/agent1") {
		t.Errorf("branch baton/agent1 not found in git branch output: %s", out)
	}
}

func TestListWorktrees(t *testing.T) {
	repo := initTestRepo(t)

	_, err := git.CreateWorktree(repo, "agent1", "", "", "")
	if err != nil {
		t.Fatalf("CreateWorktree agent1: %v", err)
	}
	_, err = git.CreateWorktree(repo, "agent2", "", "", "")
	if err != nil {
		t.Fatalf("CreateWorktree agent2: %v", err)
	}

	list, err := git.ListWorktrees(repo, "")
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}

	names := make(map[string]bool)
	for _, w := range list {
		names[w.Name] = true
	}

	if !names["agent1"] || !names["agent2"] {
		t.Errorf("expected agent1 and agent2 in list, got %v", names)
	}
}

func TestDiff(t *testing.T) {
	repo := initTestRepo(t)

	wt, err := git.CreateWorktree(repo, "agent1", "", "", "")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	// Make a change in the worktree and commit it.
	newFile := filepath.Join(wt.Path, "feature.txt")
	if err := os.WriteFile(newFile, []byte("new feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "feature.txt"},
		{"git", "commit", "-m", "add feature"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = wt.Path
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, out)
		}
	}

	// Diff should show the change.
	diff, err := git.Diff(repo, wt)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(diff, "feature.txt") {
		t.Errorf("diff should mention feature.txt, got:\n%s", diff)
	}
	if !strings.Contains(diff, "new feature") {
		t.Errorf("diff should contain 'new feature', got:\n%s", diff)
	}

	// DiffStats should report files/insertions.
	stats, err := git.GetDiffStats(repo, wt)
	if err != nil {
		t.Fatalf("GetDiffStats: %v", err)
	}
	if stats.Files < 1 {
		t.Errorf("expected at least 1 file changed, got %d", stats.Files)
	}
	if stats.Insertions < 1 {
		t.Errorf("expected at least 1 insertion, got %d", stats.Insertions)
	}
}

func TestRemoveWorktree(t *testing.T) {
	repo := initTestRepo(t)

	wt, err := git.CreateWorktree(repo, "agent1", "", "", "")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	if err := git.RemoveWorktree(repo, wt, true); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}

	// Directory should be gone.
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Errorf("worktree path %s still exists", wt.Path)
	}

	// Branch should be deleted.
	cmd := exec.Command("git", "branch", "--list", "baton/agent1")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git branch --list: %v", err)
	}
	if strings.Contains(string(out), "baton/agent1") {
		t.Errorf("branch baton/agent1 should be deleted but still exists")
	}
}

// initTestRepoWithRemote creates a bare "origin" repo and a clone of it,
// both with an initial commit on main. Returns (clone path, bare path).
func initTestRepoWithRemote(t *testing.T) (string, string) {
	t.Helper()

	// Create bare repo to act as origin.
	bare := t.TempDir()
	for _, args := range [][]string{
		{"git", "init", "--bare"},
		{"git", "symbolic-ref", "HEAD", "refs/heads/main"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = bare
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup bare %v failed: %v\n%s", args, err, out)
		}
	}

	// Create a working repo, add a commit, push to bare.
	work := t.TempDir()
	for _, args := range [][]string{
		{"git", "clone", bare, "."},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "commit.gpgsign", "false"},
		{"git", "checkout", "-b", "main"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = work
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup work %v failed: %v\n%s", args, err, out)
		}
	}

	if err := os.WriteFile(filepath.Join(work, "README"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "initial commit"},
		{"git", "push", "-u", "origin", "main"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = work
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup work %v failed: %v\n%s", args, err, out)
		}
	}

	return work, bare
}

func TestFindPRTemplate_GithubDir(t *testing.T) {
	dir := t.TempDir()
	ghDir := filepath.Join(dir, ".github")
	if err := os.MkdirAll(ghDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ghDir, "PULL_REQUEST_TEMPLATE.md"), []byte("## Summary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := git.FindPRTemplate(dir)
	if got != "## Summary\n" {
		t.Errorf("FindPRTemplate = %q, want %q", got, "## Summary\n")
	}
}

func TestFindPRTemplate_DocsDir(t *testing.T) {
	dir := t.TempDir()
	docsDir := filepath.Join(dir, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "PULL_REQUEST_TEMPLATE.md"), []byte("## Changes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := git.FindPRTemplate(dir)
	if got != "## Changes\n" {
		t.Errorf("FindPRTemplate = %q, want %q", got, "## Changes\n")
	}
}

func TestFindPRTemplate_RootLevel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "PULL_REQUEST_TEMPLATE.md"), []byte("## Root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := git.FindPRTemplate(dir)
	if got != "## Root\n" {
		t.Errorf("FindPRTemplate = %q, want %q", got, "## Root\n")
	}
}

func TestFindPRTemplate_NotFound(t *testing.T) {
	dir := t.TempDir()
	got := git.FindPRTemplate(dir)
	if got != "" {
		t.Errorf("FindPRTemplate with no template = %q, want empty string", got)
	}
}

func TestFindPRTemplate_PrefersGithubDir(t *testing.T) {
	dir := t.TempDir()
	ghDir := filepath.Join(dir, ".github")
	if err := os.MkdirAll(ghDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ghDir, "PULL_REQUEST_TEMPLATE.md"), []byte("github\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PULL_REQUEST_TEMPLATE.md"), []byte("root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := git.FindPRTemplate(dir)
	if got != "github\n" {
		t.Errorf("FindPRTemplate should prefer .github/ dir, got %q", got)
	}
}

func TestUpdateBaseBranch(t *testing.T) {
	work, bare := initTestRepoWithRemote(t)

	// Push a new commit to origin via a second clone, so work's main is behind.
	tmp := t.TempDir()
	for _, args := range [][]string{
		{"git", "clone", bare, "."},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = tmp
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup tmp %v failed: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "new.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "remote commit"},
		{"git", "push", "origin", "main"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = tmp
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("push tmp %v failed: %v\n%s", args, err, out)
		}
	}

	// Verify work is behind: new.txt should not exist yet.
	if _, err := os.Stat(filepath.Join(work, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("expected new.txt to not exist before update")
	}

	// UpdateBaseBranch should fetch and fast-forward.
	if err := git.UpdateBaseBranch(work, "main"); err != nil {
		t.Fatalf("UpdateBaseBranch: %v", err)
	}

	// After update, new.txt should exist (local main was fast-forwarded).
	if _, err := os.Stat(filepath.Join(work, "new.txt")); os.IsNotExist(err) {
		t.Error("expected new.txt to exist after UpdateBaseBranch fast-forward")
	}
}

func TestUpdateBaseBranch_NoRemote(t *testing.T) {
	// A repo with no remote should return an error (fetch fails).
	repo := initTestRepo(t)

	err := git.UpdateBaseBranch(repo, "main")
	if err == nil {
		t.Error("expected error when no remote configured, got nil")
	}
}

func TestAttachWorktree(t *testing.T) {
	repo := initTestRepo(t)

	// Create a branch to attach to.
	cmd := exec.Command("git", "branch", "feature-x")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git branch feature-x: %v\n%s", err, out)
	}

	wt, err := git.AttachWorktree(repo, "my-agent", "", "feature-x")
	if err != nil {
		t.Fatalf("AttachWorktree: %v", err)
	}

	// Worktree directory should exist.
	if _, err := os.Stat(wt.Path); os.IsNotExist(err) {
		t.Errorf("worktree path %s does not exist", wt.Path)
	}

	// Name should be the agent name we passed.
	if wt.Name != "my-agent" {
		t.Errorf("expected Name 'my-agent', got %q", wt.Name)
	}

	// Branch should be the actual branch name, not prefixed.
	if wt.Branch != "feature-x" {
		t.Errorf("expected Branch 'feature-x', got %q", wt.Branch)
	}

	// BaseBranch should be main (current HEAD of repo).
	if wt.BaseBranch != "main" {
		t.Errorf("expected BaseBranch 'main', got %q", wt.BaseBranch)
	}

	// The worktree should be on the correct branch.
	cmd = exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = wt.Path
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse HEAD in worktree: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "feature-x" {
		t.Errorf("worktree HEAD should be feature-x, got %q", strings.TrimSpace(string(out)))
	}
}

func TestAttachWorktreeRemoteBranch(t *testing.T) {
	work, bare := initTestRepoWithRemote(t)

	// Create a branch on origin only via a second clone.
	tmp := t.TempDir()
	for _, args := range [][]string{
		{"git", "clone", bare, "."},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "commit.gpgsign", "false"},
		{"git", "checkout", "-b", "remote-feature"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = tmp
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup tmp %v failed: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "remote-feat.txt"), []byte("remote feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "add remote feature"},
		{"git", "push", "origin", "remote-feature"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = tmp
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("push tmp %v failed: %v\n%s", args, err, out)
		}
	}

	// Verify the local clone does NOT have this branch locally.
	cmd := exec.Command("git", "rev-parse", "--verify", "remote-feature")
	cmd.Dir = work
	if err := cmd.Run(); err == nil {
		t.Fatal("expected remote-feature to not exist locally before attach")
	}

	// AttachWorktree should auto-fetch and create the local tracking branch.
	wt, err := git.AttachWorktree(work, "remote-agent", "", "remote-feature")
	if err != nil {
		t.Fatalf("AttachWorktree remote branch: %v", err)
	}

	// The worktree should contain the remote-only file.
	if _, err := os.Stat(filepath.Join(wt.Path, "remote-feat.txt")); os.IsNotExist(err) {
		t.Error("expected remote-feat.txt in worktree attached to remote branch")
	}

	if wt.Branch != "remote-feature" {
		t.Errorf("expected Branch 'remote-feature', got %q", wt.Branch)
	}
}

func TestAttachWorktreeNonexistent(t *testing.T) {
	repo := initTestRepo(t)

	_, err := git.AttachWorktree(repo, "bad-agent", "", "no-such-branch")
	if err == nil {
		t.Fatal("expected error attaching to nonexistent branch, got nil")
	}
}

func TestListLocalBranches(t *testing.T) {
	repo := initTestRepo(t)

	// Create some extra branches.
	for _, branch := range []string{"feature-a", "feature-b", "bugfix-1"} {
		cmd := exec.Command("git", "branch", branch)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git branch %s: %v\n%s", branch, err, out)
		}
	}

	branches, err := git.ListLocalBranches(repo)
	if err != nil {
		t.Fatalf("ListLocalBranches: %v", err)
	}

	expected := map[string]bool{"main": true, "feature-a": true, "feature-b": true, "bugfix-1": true}
	got := make(map[string]bool)
	for _, b := range branches {
		got[b] = true
	}

	for name := range expected {
		if !got[name] {
			t.Errorf("expected branch %q in list, got %v", name, branches)
		}
	}

	// Should not contain HEAD.
	for _, b := range branches {
		if b == "HEAD" {
			t.Error("ListLocalBranches should not include HEAD")
		}
	}
}

func TestListRemoteBranches(t *testing.T) {
	work, bare := initTestRepoWithRemote(t)

	// Create and push additional branches via a second clone.
	tmp := t.TempDir()
	for _, args := range [][]string{
		{"git", "clone", bare, "."},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = tmp
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup tmp %v failed: %v\n%s", args, err, out)
		}
	}

	for _, branch := range []string{"remote-a", "remote-b"} {
		for _, args := range [][]string{
			{"git", "checkout", "-b", branch},
			{"git", "push", "origin", branch},
			{"git", "checkout", "main"},
		} {
			cmd := exec.Command(args[0], args[1:]...)
			cmd.Dir = tmp
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("push branch %s %v failed: %v\n%s", branch, args, err, out)
			}
		}
	}

	// Fetch in the working clone so it sees the remote branches.
	cmd := exec.Command("git", "fetch", "origin")
	cmd.Dir = work
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fetch origin: %v\n%s", err, out)
	}

	branches, err := git.ListRemoteBranches(work)
	if err != nil {
		t.Fatalf("ListRemoteBranches: %v", err)
	}

	got := make(map[string]bool)
	for _, b := range branches {
		got[b] = true
	}

	// Should have main, remote-a, remote-b (all without origin/ prefix).
	for _, name := range []string{"main", "remote-a", "remote-b"} {
		if !got[name] {
			t.Errorf("expected branch %q in remote list, got %v", name, branches)
		}
	}

	// Should NOT have HEAD or any origin/ prefix.
	for _, b := range branches {
		if b == "HEAD" {
			t.Error("ListRemoteBranches should not include HEAD")
		}
		if strings.HasPrefix(b, "origin/") {
			t.Errorf("ListRemoteBranches should strip origin/ prefix, got %q", b)
		}
	}
}

func TestRenameBranch(t *testing.T) {
	repo := initTestRepo(t)

	wt, err := git.CreateWorktree(repo, "agent1", "", "", "")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	got, err := git.RenameBranch(repo, wt.Branch, "baton/renamed")
	if err != nil {
		t.Fatalf("RenameBranch: %v", err)
	}
	if got != "baton/renamed" {
		t.Errorf("expected returned name %q, got %q", "baton/renamed", got)
	}

	// Old branch should no longer exist.
	cmd := exec.Command("git", "branch", "--list", wt.Branch)
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git branch --list: %v", err)
	}
	if strings.Contains(string(out), wt.Branch) {
		t.Errorf("old branch %q should be gone, branch list: %s", wt.Branch, out)
	}

	// New branch should exist.
	cmd = exec.Command("git", "branch", "--list", "baton/renamed")
	cmd.Dir = repo
	out, err = cmd.Output()
	if err != nil {
		t.Fatalf("git branch --list renamed: %v", err)
	}
	if !strings.Contains(string(out), "baton/renamed") {
		t.Errorf("new branch baton/renamed not found: %s", out)
	}

	// Worktree metadata should reflect the new branch.
	cmd = exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repo
	out, err = cmd.Output()
	if err != nil {
		t.Fatalf("git worktree list: %v", err)
	}
	if !strings.Contains(string(out), "refs/heads/baton/renamed") {
		t.Errorf("worktree list should reference refs/heads/baton/renamed, got:\n%s", out)
	}
}

func TestRenameBranch_CollisionFallback(t *testing.T) {
	repo := initTestRepo(t)

	// Pre-create the target branch so the primary rename collides.
	cmd := exec.Command("git", "branch", "baton/taken")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v\n%s", err, out)
	}

	wt, err := git.CreateWorktree(repo, "agent1", "", "", "")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	got, err := git.RenameBranch(repo, wt.Branch, "baton/taken")
	if err != nil {
		t.Fatalf("RenameBranch should fall back on collision: %v", err)
	}
	if got != "baton/taken-2" {
		t.Errorf("expected fallback to %q, got %q", "baton/taken-2", got)
	}
}

func TestRenameBranch_NonCollisionErrorDoesNotRetry(t *testing.T) {
	repo := initTestRepo(t)

	// Source branch does not exist — git will fail with "no branch named",
	// which is NOT a collision. The function should return the original error
	// immediately without cycling through -2..-9 suffixes.
	_, err := git.RenameBranch(repo, "baton/does-not-exist", "baton/target")
	if err == nil {
		t.Fatalf("RenameBranch with missing source should error, got nil")
	}
	// Error message should mention the original target, not a -N suffix.
	if !strings.Contains(err.Error(), `"baton/target"`) {
		t.Errorf("expected error to reference baton/target, got: %v", err)
	}
	if strings.Contains(err.Error(), "baton/target-") {
		t.Errorf("non-collision error should not have cycled through suffixes, got: %v", err)
	}
}

func TestRenameBranch_Idempotent(t *testing.T) {
	repo := initTestRepo(t)

	wt, err := git.CreateWorktree(repo, "agent1", "", "", "")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	got, err := git.RenameBranch(repo, wt.Branch, wt.Branch)
	if err != nil {
		t.Fatalf("RenameBranch same-name: %v", err)
	}
	if got != wt.Branch {
		t.Errorf("expected %q unchanged, got %q", wt.Branch, got)
	}
}

func TestPush(t *testing.T) {
	work, bare := initTestRepoWithRemote(t)

	// Create a worktree on a new branch.
	wt, err := git.CreateWorktree(work, "push-agent", "", "", "")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	// Commit something in the worktree.
	if err := os.WriteFile(filepath.Join(wt.Path, "pushed.txt"), []byte("pushed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "pushed.txt"},
		{"git", "commit", "-m", "add pushed.txt"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = wt.Path
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, out)
		}
	}

	// Push the branch to origin.
	if err := git.Push(wt.Path, wt.Branch); err != nil {
		t.Fatalf("Push: %v", err)
	}

	// Verify the branch now exists on the bare remote.
	cmd := exec.Command("git", "branch", "--list", wt.Branch)
	cmd.Dir = bare
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git branch list on bare: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), wt.Branch) {
		t.Errorf("expected branch %q on origin, branch list: %s", wt.Branch, out)
	}

	// Verify the upstream tracking was set (--set-upstream-to).
	cmd = exec.Command("git", "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	cmd.Dir = wt.Path
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("upstream check failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "origin/"+wt.Branch {
		t.Errorf("expected upstream origin/%s, got %q", wt.Branch, got)
	}
}

func TestCreateWorktreeWithStartPoint(t *testing.T) {
	work, bare := initTestRepoWithRemote(t)

	// Push a new commit to origin via a second clone.
	tmp := t.TempDir()
	for _, args := range [][]string{
		{"git", "clone", bare, "."},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = tmp
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup tmp %v failed: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "remote-only.txt"), []byte("from remote\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "remote-only commit"},
		{"git", "push", "origin", "main"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = tmp
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("push tmp %v failed: %v\n%s", args, err, out)
		}
	}

	// Fetch but don't merge — local main stays behind.
	cmd := exec.Command("git", "fetch", "origin", "main")
	cmd.Dir = work
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fetch failed: %v\n%s", err, out)
	}

	// Create worktree from origin/main (not local main).
	wt, err := git.CreateWorktree(work, "start-point-agent", "", "", "", "origin/main")
	if err != nil {
		t.Fatalf("CreateWorktree with startPoint: %v", err)
	}

	// The worktree should have remote-only.txt (from origin/main).
	if _, err := os.Stat(filepath.Join(wt.Path, "remote-only.txt")); os.IsNotExist(err) {
		t.Error("expected remote-only.txt in worktree created from origin/main")
	}

	// Local main should NOT have remote-only.txt (wasn't fast-forwarded).
	if _, err := os.Stat(filepath.Join(work, "remote-only.txt")); !os.IsNotExist(err) {
		t.Error("expected remote-only.txt to NOT exist in main worktree")
	}
}
