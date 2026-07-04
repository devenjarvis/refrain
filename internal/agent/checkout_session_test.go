package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devenjarvis/refrain/internal/git"
)

// currentBranch returns the branch checked out in the repo's main working tree.
func currentBranch(t *testing.T, repo string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// createCheckoutSession creates a checkout session backed by a bash sleep stub.
func createCheckoutSession(t *testing.T, mgr *Manager) (*Session, *Agent) {
	t.Helper()
	cfg := Config{Task: "checkout-test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionInDirWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 60")
	})
	if err != nil {
		t.Fatalf("CreateSessionInDirWithCommand: %v", err)
	}
	return sess, ag
}

func TestCreateSessionInDir_RunsInMainCheckout(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	branch := currentBranch(t, repo)
	sess, ag := createCheckoutSession(t, mgr)

	if sess.Worktree.Path != repo {
		t.Errorf("Worktree.Path = %q, want repo path %q", sess.Worktree.Path, repo)
	}
	if ag.WorktreePath != repo {
		t.Errorf("agent WorktreePath = %q, want repo path %q", ag.WorktreePath, repo)
	}
	if sess.Kind() != KindCheckout {
		t.Errorf("Kind = %q, want %q", sess.Kind(), KindCheckout)
	}
	if sess.Branch() != branch {
		t.Errorf("Branch = %q, want current branch %q", sess.Branch(), branch)
	}
	if !sess.HasClaudeName() {
		t.Error("checkout session must have hasClaudeName=true so the first prompt never renames the user's branch")
	}
	if sess.ownsBranch {
		t.Error("checkout session must not own the user's branch")
	}
	// Display name reads as "the checkout": the slugified current branch.
	if got, want := sess.CurrentName(), slugify(branch); got != want {
		t.Errorf("Name = %q, want slugified branch %q", got, want)
	}

	// No worktree was created under .refrain/worktrees.
	if entries, err := os.ReadDir(filepath.Join(repo, ".refrain", "worktrees")); err == nil && len(entries) > 0 {
		t.Errorf("expected no worktree dirs, found %d", len(entries))
	}
}

func TestCreateSessionInDir_OnePerRepo(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	sess, _ := createCheckoutSession(t, mgr)

	cfg := Config{Task: "second", Rows: 24, Cols: 80}
	_, _, err := mgr.CreateSessionInDirWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 60")
	})
	if !errors.Is(err, ErrCheckoutSessionExists) {
		t.Fatalf("second checkout session: err = %v, want ErrCheckoutSessionExists", err)
	}

	// Worktree sessions are unaffected by the checkout slot.
	if _, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 60")
	}); err != nil {
		t.Fatalf("worktree session alongside checkout session: %v", err)
	}

	// Killing the checkout session frees the slot.
	if err := mgr.KillSession(sess.ID); err != nil {
		t.Fatalf("KillSession: %v", err)
	}
	if _, _, err := mgr.CreateSessionInDirWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 60")
	}); err != nil {
		t.Fatalf("checkout session after kill: %v", err)
	}
}

// TestCheckoutSessionCleanup_NeverRemovesCheckout is the safety-critical test
// for the rollback design's Phase 1: Session.Cleanup must be a guaranteed
// no-op on the tree for KindCheckout, on every teardown path.
func TestCheckoutSessionCleanup_NeverRemovesCheckout(t *testing.T) {
	teardowns := []struct {
		name string
		run  func(t *testing.T, mgr *Manager, sess *Session)
	}{
		{"KillSession", func(t *testing.T, mgr *Manager, sess *Session) {
			if err := mgr.KillSession(sess.ID); err != nil {
				t.Fatalf("KillSession: %v", err)
			}
		}},
		{"Shutdown", func(t *testing.T, mgr *Manager, sess *Session) {
			mgr.Shutdown()
		}},
		{"DirectCleanup", func(t *testing.T, mgr *Manager, sess *Session) {
			if err := sess.Cleanup(mgr.RepoPath()); err != nil {
				t.Fatalf("Cleanup: %v", err)
			}
		}},
		{"KillLastAgentAutoClose", func(t *testing.T, mgr *Manager, sess *Session) {
			agents := sess.Agents()
			if len(agents) != 1 {
				t.Fatalf("expected 1 agent, got %d", len(agents))
			}
			if err := mgr.KillAgent(sess.ID, agents[0].ID); err != nil {
				t.Fatalf("KillAgent: %v", err)
			}
		}},
	}

	for _, tc := range teardowns {
		t.Run(tc.name, func(t *testing.T) {
			repo := setupTestRepo(t)
			mgr := NewManager(repo, defaultTestSettings())
			defer mgr.Shutdown()

			branch := currentBranch(t, repo)
			sess, _ := createCheckoutSession(t, mgr)

			tc.run(t, mgr, sess)

			// The user's checkout must be fully intact.
			if _, err := os.Stat(filepath.Join(repo, "README.md")); err != nil {
				t.Errorf("README.md gone after %s: %v", tc.name, err)
			}
			if !git.IsRepo(repo) {
				t.Errorf("repo is no longer a git repository after %s", tc.name)
			}
			if got := currentBranch(t, repo); got != branch {
				t.Errorf("branch after %s = %q, want %q", tc.name, got, branch)
			}
		})
	}
}

func TestCheckoutSession_SuppressesBranchRename(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	var namerCalls atomic.Int32
	mgr.SetBranchNamer(func(ctx context.Context, instruction string) (string, error) {
		namerCalls.Add(1)
		return "renamed-by-haiku", nil
	})

	branch := currentBranch(t, repo)
	sess, ag := createCheckoutSession(t, mgr)

	// Simulate the first actionable prompt reaching the dispatcher. The
	// hasClaudeName gate returns synchronously before any namer goroutine is
	// spawned; the short sleep gives a wrongly-spawned goroutine a chance to
	// hit the namer so the counter assertion below can catch a regression.
	mgr.maybeRenameFromPrompt(sess, ag, "add dark mode to the settings page")
	time.Sleep(100 * time.Millisecond)

	if got := namerCalls.Load(); got != 0 {
		t.Errorf("branch namer invoked %d times for a checkout session, want 0", got)
	}
	if got := currentBranch(t, repo); got != branch {
		t.Errorf("user's branch renamed to %q — checkout sessions must never rename", got)
	}
	if sess.Branch() != branch {
		t.Errorf("session branch = %q, want %q", sess.Branch(), branch)
	}
}

// TestCheckoutSession_HooksFileLandsInRepoRefrainDir verifies the design-doc
// claim that hooks need no relocation for checkout sessions: buildHookArgs
// writes <worktreePath>/.refrain/hooks.json, and for a checkout session
// worktreePath == repoPath, whose .refrain/ is already gitignored.
func TestCheckoutSession_HooksFileLandsInRepoRefrainDir(t *testing.T) {
	repo := setupTestRepo(t)
	socket := filepath.Join(repo, ".refrain", "hook.sock")

	args, err := buildHookArgs(Config{}, repo, socket)
	if err != nil {
		t.Fatalf("buildHookArgs: %v", err)
	}

	wantPath := filepath.Join(repo, ".refrain", "hooks.json")
	if len(args) != 2 || args[0] != "--settings" || args[1] != wantPath {
		t.Fatalf("args = %v, want [--settings %s]", args, wantPath)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("hooks.json not written inside repo .refrain/: %v", err)
	}
}

func TestCheckoutSession_DetachResume(t *testing.T) {
	repo := setupTestRepo(t)

	mgr1 := NewManager(repo, defaultTestSettings())
	sess, _ := createCheckoutSession(t, mgr1)
	sessID := sess.ID

	bs := mgr1.Detach()
	if bs == nil {
		t.Fatal("expected non-nil RefrainState from Detach")
	}
	if len(bs.Sessions) != 1 {
		t.Fatalf("expected 1 session in snapshot, got %d", len(bs.Sessions))
	}
	ss := bs.Sessions[0]
	if ss.Kind != string(KindCheckout) {
		t.Errorf("persisted Kind = %q, want %q", ss.Kind, KindCheckout)
	}
	if ss.WorktreePath != repo {
		t.Errorf("persisted WorktreePath = %q, want repo %q", ss.WorktreePath, repo)
	}
	if ss.OwnsBranch {
		t.Error("persisted OwnsBranch = true, want false for checkout session")
	}
	if !ss.HasClaudeName {
		t.Error("persisted HasClaudeName = false, want true for checkout session")
	}

	// The repo must be untouched by detach.
	if _, err := os.Stat(filepath.Join(repo, "README.md")); err != nil {
		t.Fatalf("README.md gone after detach: %v", err)
	}

	// The user switches branches while refrain is detached.
	cmd := exec.Command("git", "checkout", "-b", "feature/while-detached")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %v\n%s", err, out)
	}

	// Resume with bash standing in for claude so the test is hermetic.
	settings := defaultTestSettings()
	settings.AgentProgram = "bash"
	mgr2 := NewManager(repo, settings)
	defer mgr2.Shutdown()

	if err := mgr2.ResumeSession(ss, Config{Rows: 24, Cols: 80}); err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}

	resumed := mgr2.GetSession(sessID)
	if resumed == nil {
		t.Fatal("resumed session not found")
	}
	if resumed.Kind() != KindCheckout {
		t.Errorf("resumed Kind = %q, want %q", resumed.Kind(), KindCheckout)
	}
	if resumed.Worktree.Path != repo {
		t.Errorf("resumed Worktree.Path = %q, want repo %q", resumed.Worktree.Path, repo)
	}
	// The session reflects the branch the checkout actually has now, not the
	// snapshot taken before the user switched.
	if got := resumed.Branch(); got != "feature/while-detached" {
		t.Errorf("resumed Branch = %q, want %q (HEAD moved while detached)", got, "feature/while-detached")
	}

	// The cleanup guard must survive the resume round-trip.
	if err := mgr2.KillSession(sessID); err != nil {
		t.Fatalf("KillSession after resume: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "README.md")); err != nil {
		t.Errorf("README.md gone after killing resumed checkout session: %v", err)
	}
	if !git.IsRepo(repo) {
		t.Error("repo is no longer a git repository after killing resumed checkout session")
	}
}

func TestWorktreeSession_PersistsWorktreeKind(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 60")
	})
	if err != nil {
		t.Fatal(err)
	}
	if sess.Kind() != KindWorktree {
		t.Errorf("Kind = %q, want %q", sess.Kind(), KindWorktree)
	}

	bs := mgr.Detach()
	if bs == nil {
		t.Fatal("expected non-nil RefrainState")
	}
	if got := bs.Sessions[0].Kind; got != string(KindWorktree) {
		t.Errorf("persisted Kind = %q, want %q", got, KindWorktree)
	}
}

func TestSessionKindFromString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  SessionKind
	}{
		{"empty means legacy worktree", "", KindWorktree},
		{"worktree", "worktree", KindWorktree},
		{"checkout", "checkout", KindCheckout},
		{"unknown falls back to worktree", "banana", KindWorktree},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SessionKindFromString(tt.input); got != tt.want {
				t.Errorf("SessionKindFromString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
