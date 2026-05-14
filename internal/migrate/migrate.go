// Package migrate handles one-time on-disk renames from the pre-rename "baton"
// layout to the current "refrain" layout. Both entry points are best-effort
// and idempotent: if the new path already exists they do nothing, so a second
// run is a no-op.
package migrate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GlobalHome moves ~/.baton/ to ~/.refrain/ if the latter does not yet exist.
// Returns nil and logs nothing if the new dir already exists or if neither
// exists (fresh install).
func GlobalHome() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("migrate: finding home dir: %w", err)
	}
	newDir := filepath.Join(home, ".refrain")
	oldDir := filepath.Join(home, ".baton")

	if _, err := os.Stat(newDir); err == nil {
		return nil
	}
	if _, err := os.Stat(oldDir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("migrate: stat %s: %w", oldDir, err)
	}

	if err := os.Rename(oldDir, newDir); err != nil {
		return fmt.Errorf("migrate: renaming %s -> %s: %w", oldDir, newDir, err)
	}
	return nil
}

// RepoState moves <repoPath>/.baton/ to <repoPath>/.refrain/ if the latter does
// not yet exist, runs `git worktree repair` so existing worktrees keep working
// after the directory rename, and updates <repoPath>/.gitignore.
//
// Best-effort: errors from `git worktree repair` and gitignore rewriting are
// logged via the returned error but the on-disk rename has already succeeded
// and is not rolled back. Callers should typically log and continue.
func RepoState(repoPath string) error {
	newDir := filepath.Join(repoPath, ".refrain")
	oldDir := filepath.Join(repoPath, ".baton")

	if _, err := os.Stat(newDir); err == nil {
		return nil
	}
	if _, err := os.Stat(oldDir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("migrate: stat %s: %w", oldDir, err)
	}

	if err := os.Rename(oldDir, newDir); err != nil {
		return fmt.Errorf("migrate: renaming %s -> %s: %w", oldDir, newDir, err)
	}

	// Repair git's per-worktree gitdir pointers. They store an absolute path
	// back into the (now-moved) .baton/worktrees/<name>/.git, so without this
	// step `git status` from inside a worktree would fail.
	cmd := exec.Command("git", "worktree", "repair")
	cmd.Dir = repoPath
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("migrate: git worktree repair in %s: %w\n%s", repoPath, err, strings.TrimSpace(string(out)))
	}

	if err := updateGitignore(repoPath); err != nil {
		return fmt.Errorf("migrate: updating .gitignore in %s: %w", repoPath, err)
	}
	return nil
}

// updateGitignore rewrites <repoPath>/.gitignore so any line ignoring `.baton`
// or `.baton/` becomes `.refrain` / `.refrain/`. If neither line is present
// `.refrain/` is appended on a new line. Missing .gitignore is created.
func updateGitignore(repoPath string) error {
	path := filepath.Join(repoPath, ".gitignore")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return os.WriteFile(path, []byte(".refrain/\n"), 0o644)
	}
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	rewrote := false
	hasRefrain := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case ".baton":
			lines[i] = ".refrain"
			rewrote = true
		case ".baton/":
			lines[i] = ".refrain/"
			rewrote = true
		case ".refrain", ".refrain/":
			hasRefrain = true
		}
	}

	if !rewrote && !hasRefrain {
		// Preserve trailing newline if present.
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines[len(lines)-1] = ".refrain/"
			lines = append(lines, "")
		} else {
			lines = append(lines, ".refrain/")
		}
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}
