package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WorktreeInfo holds metadata about an agent's git worktree.
type WorktreeInfo struct {
	Name       string // agent name
	Path       string // absolute path to worktree dir
	Branch     string // branch name (refrain/<name>)
	BaseBranch string // branch worktree was created from
}

// runGit executes a git command in the given directory and returns its output.
// On error, the returned error includes stderr for debugging.
//
// LC_ALL=C pins git's messages to English so code that inspects stderr for
// well-known substrings (e.g. RenameBranch's collision detection) works on
// hosts with localized git. Porcelain output is already locale-independent,
// so this has no effect on other callers.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// runGitRaw is like runGit but returns the output without trimming whitespace.
// Use this for commands like git diff where trailing whitespace is meaningful
// (context lines representing empty source lines start with a space character
// and would be corrupted by TrimSpace).
func runGitRaw(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// IsRepo reports whether path is inside a git repository.
// It runs `git rev-parse --git-dir` in path and returns true on success.
func IsRepo(path string) bool {
	_, err := runGit(path, "rev-parse", "--git-dir")
	return err == nil
}

// BaseBranch returns the current branch name for the given repo.
func BaseBranch(repoPath string) (string, error) {
	return runGit(repoPath, "rev-parse", "--abbrev-ref", "HEAD")
}

// UpdateBaseBranch fetches the given branch from origin and attempts to
// fast-forward the local ref to match. This is best-effort: if the fetch
// fails (e.g. offline), an error is returned. If the fast-forward fails
// (e.g. local has diverged), the error is silently ignored since the fetch
// already updated origin/<branch> which can be used as a start point.
func UpdateBaseBranch(repoPath, branch string) error {
	if _, err := runGit(repoPath, "fetch", "origin", branch); err != nil {
		return fmt.Errorf("fetching origin/%s: %w", branch, err)
	}

	// Check if local is ancestor of remote (safe to fast-forward).
	if _, err := runGit(repoPath, "merge-base", "--is-ancestor", branch, "origin/"+branch); err != nil {
		// Local has diverged — skip fast-forward, but fetch succeeded.
		return nil //nolint:nilerr // intentional: fetch updated origin/<branch> for use as start point
	}

	// Fast-forward the local branch.
	current, _ := BaseBranch(repoPath)
	if current == branch {
		// Branch is checked out — use merge --ff-only.
		_, _ = runGit(repoPath, "merge", "--ff-only", "origin/"+branch)
	} else {
		// Branch is not checked out — update ref directly.
		_, _ = runGit(repoPath, "branch", "-f", branch, "origin/"+branch)
	}

	return nil
}

// CreateWorktree creates a new git worktree for the named agent.
// branchPrefix and worktreeDir control naming and placement; pass empty
// strings to use defaults ("refrain/" and ".refrain/worktrees").
// An optional startPoint specifies the commit to branch from; if omitted,
// the worktree branches from the current HEAD.
func CreateWorktree(repoPath, agentName, branchPrefix, worktreeDir, baseBranch string, startPoint ...string) (*WorktreeInfo, error) {
	if branchPrefix == "" {
		branchPrefix = "refrain/"
	}
	if worktreeDir == "" {
		worktreeDir = ".refrain/worktrees"
	}

	if baseBranch == "" {
		var err error
		baseBranch, err = BaseBranch(repoPath)
		if err != nil {
			return nil, fmt.Errorf("getting base branch: %w", err)
		}
	}

	branch := branchPrefix + agentName
	wtPath := filepath.Join(repoPath, worktreeDir, agentName)

	args := []string{"worktree", "add", "-b", branch, wtPath}
	if len(startPoint) > 0 && startPoint[0] != "" {
		args = append(args, startPoint[0])
	}

	if _, err := runGit(repoPath, args...); err != nil {
		return nil, fmt.Errorf("creating worktree: %w", err)
	}

	absPath, err := filepath.Abs(wtPath)
	if err != nil {
		return nil, err
	}

	return &WorktreeInfo{
		Name:       agentName,
		Path:       absPath,
		Branch:     branch,
		BaseBranch: baseBranch,
	}, nil
}

// AttachWorktree creates a new git worktree that checks out an existing branch.
// Unlike CreateWorktree, it does NOT create a new branch (no -b flag).
// For remote-only branches, it fetches from origin first so git can
// auto-create the local tracking branch.
// worktreeDir defaults to ".refrain/worktrees" if empty.
func AttachWorktree(repoPath, name, worktreeDir, branch string) (*WorktreeInfo, error) {
	if worktreeDir == "" {
		worktreeDir = ".refrain/worktrees"
	}

	// Check if the branch exists locally.
	_, localErr := runGit(repoPath, "rev-parse", "--verify", branch)

	if localErr != nil {
		// Local branch doesn't exist — try fetching from origin.
		// This handles the case where the remote knows about the branch
		// but we haven't fetched it yet.
		if _, err := runGit(repoPath, "fetch", "origin", branch); err != nil {
			// Fetch failed — branch doesn't exist on origin either.
			return nil, fmt.Errorf("branch %q not found locally or on origin", branch)
		}
	}

	base, err := BaseBranch(repoPath)
	if err != nil {
		return nil, fmt.Errorf("getting base branch: %w", err)
	}

	wtPath := filepath.Join(repoPath, worktreeDir, name)

	if _, err := runGit(repoPath, "worktree", "add", wtPath, branch); err != nil {
		return nil, fmt.Errorf("attaching worktree: %w", err)
	}

	absPath, err := filepath.Abs(wtPath)
	if err != nil {
		return nil, err
	}

	return &WorktreeInfo{
		Name:       name,
		Path:       absPath,
		Branch:     branch,
		BaseBranch: base,
	}, nil
}

// ListLocalBranches returns the names of all local branches in the repo.
func ListLocalBranches(repoPath string) ([]string, error) {
	out, err := runGit(repoPath, "branch", "--format", "%(refname:short)")
	if err != nil {
		return nil, fmt.Errorf("listing local branches: %w", err)
	}

	lines := strings.Split(out, "\n")
	branches := make([]string, 0, len(lines))
	for _, line := range lines {
		name := strings.TrimSpace(line)
		if name == "" || name == "HEAD" {
			continue
		}
		branches = append(branches, name)
	}
	return branches, nil
}

// ListRemoteBranches returns the names of all remote branches, with the
// "origin/" prefix stripped and HEAD entries filtered out.
func ListRemoteBranches(repoPath string) ([]string, error) {
	out, err := runGit(repoPath, "branch", "-r", "--format", "%(refname:short)")
	if err != nil {
		return nil, fmt.Errorf("listing remote branches: %w", err)
	}

	rlines := strings.Split(out, "\n")
	branches := make([]string, 0, len(rlines))
	for _, line := range rlines {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		// Strip origin/ prefix.
		short := strings.TrimPrefix(name, "origin/")
		if short == "HEAD" {
			continue
		}
		branches = append(branches, short)
	}
	return branches, nil
}

// RemoveWorktree removes a worktree and optionally deletes its branch.
func RemoveWorktree(repoPath string, wt *WorktreeInfo, deleteBranch bool) error {
	if _, err := runGit(repoPath, "worktree", "remove", "--force", wt.Path); err != nil {
		return fmt.Errorf("removing worktree: %w", err)
	}

	if deleteBranch {
		if _, err := runGit(repoPath, "branch", "-D", wt.Branch); err != nil {
			return fmt.Errorf("deleting branch: %w", err)
		}
	}

	return nil
}

// ListWorktrees returns all refrain-managed worktrees in the repo.
// branchPrefix controls which branches are considered refrain-managed;
// pass empty string to use the default ("refrain/").
func ListWorktrees(repoPath, branchPrefix string) ([]*WorktreeInfo, error) {
	if branchPrefix == "" {
		branchPrefix = "refrain/"
	}

	out, err := runGit(repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	base, _ := BaseBranch(repoPath)
	branchRef := "branch refs/heads/" + branchPrefix

	var worktrees []*WorktreeInfo
	var current *WorktreeInfo

	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path := strings.TrimPrefix(line, "worktree ")
			current = &WorktreeInfo{Path: path}
		case strings.HasPrefix(line, branchRef):
			if current != nil {
				branch := strings.TrimPrefix(line, "branch refs/heads/")
				name := strings.TrimPrefix(branch, branchPrefix)
				current.Branch = branch
				current.Name = name
				current.BaseBranch = base
				worktrees = append(worktrees, current)
			}
		case line == "":
			current = nil
		}
	}

	return worktrees, nil
}

// FindPRTemplate searches worktreePath for a GitHub PR template file and
// returns its contents verbatim, or "" if none is found. Search order:
//  1. .github/PULL_REQUEST_TEMPLATE.md
//  2. docs/PULL_REQUEST_TEMPLATE.md
//  3. PULL_REQUEST_TEMPLATE.md (repo root)
//
// The filename is matched case-insensitively within each directory by scanning
// directory entries, so both "PULL_REQUEST_TEMPLATE.md" and
// "pull_request_template.md" are found.
func FindPRTemplate(worktreePath string) string {
	candidates := []struct{ dir, name string }{
		{filepath.Join(worktreePath, ".github"), "pull_request_template.md"},
		{filepath.Join(worktreePath, "docs"), "pull_request_template.md"},
		{worktreePath, "pull_request_template.md"},
	}
	for _, c := range candidates {
		entries, err := os.ReadDir(c.dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.EqualFold(e.Name(), c.name) {
				data, err := os.ReadFile(filepath.Join(c.dir, e.Name()))
				if err == nil {
					return string(data)
				}
			}
		}
	}
	return ""
}

// Push pushes branch to origin from the given worktreePath, setting the remote
// tracking ref on the first push (--set-upstream). Force-with-lease is NOT
// used so the push is safe for first-time publishing of a feature branch.
// Callers must ensure any commits on the branch are ready to share before
// calling — there is no additional safety gate inside this function.
func Push(worktreePath, branch string) error {
	_, err := runGit(worktreePath, "push", "--set-upstream", "origin", branch)
	if err != nil {
		return fmt.Errorf("push %q: %w", branch, err)
	}
	return nil
}

// GetRemoteURL returns the URL for the "origin" remote of the repo at repoPath.
func GetRemoteURL(repoPath string) (string, error) {
	return runGit(repoPath, "remote", "get-url", "origin")
}

// RenameBranch renames oldBranch to newBranch via `git branch -m`. The rename
// is atomic and git automatically updates HEAD symrefs in any worktree that
// has the branch checked out, so callers do not need to pause a running
// worktree process.
//
// If newBranch already exists, the function retries with "-2", "-3", ...
// suffixes up to "-9" before giving up. The actual new branch name (which may
// include a collision suffix) is returned so callers can update their own
// metadata accurately. Non-collision errors (missing source branch, invalid
// refname, etc.) are returned immediately without retrying.
func RenameBranch(repoPath, oldBranch, newBranch string) (string, error) {
	if newBranch == "" {
		return "", fmt.Errorf("new branch name is empty")
	}
	if oldBranch == newBranch {
		return newBranch, nil
	}

	candidate := newBranch
	for i := 1; i <= 9; i++ {
		_, err := runGit(repoPath, "branch", "-m", oldBranch, candidate)
		if err == nil {
			return candidate, nil
		}
		if !isBranchCollisionErr(err) {
			return "", fmt.Errorf("rename branch %q -> %q: %w", oldBranch, candidate, err)
		}
		candidate = fmt.Sprintf("%s-%d", newBranch, i+1)
	}
	return "", fmt.Errorf("rename branch %q -> %q: all collision candidates up to %q are taken", oldBranch, newBranch, candidate)
}

// isBranchCollisionErr reports whether err from `git branch -m` indicates the
// destination branch already exists (as opposed to other failure modes).
func isBranchCollisionErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "already exists") || strings.Contains(msg, "already used")
}
