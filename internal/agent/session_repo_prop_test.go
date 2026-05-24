package agent

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/refrain/internal/state"
	"pgregory.net/rapid"
)

// Every session's worktree path is a subdirectory of its manager's repoPath.
func TestSessionRepoProp_WorktreeAlwaysUnderRepo(t *testing.T) {
	repo := setupTestRepo(t)

	rapid.Check(t, func(t *rapid.T) {
		mgr := NewManager(repo, defaultTestSettings())
		defer mgr.Shutdown()

		n := rapid.IntRange(1, 3).Draw(t, "session_count")
		for i := 0; i < n; i++ {
			sess, _ := createTestSession(t, mgr)
			assertWorktreeUnderRepo(t, repo, sess)
		}
	})
}

// Every agent within a session has WorktreePath equal to the session's Worktree.Path.
func TestSessionRepoProp_AgentWorktreePathMatchesSession(t *testing.T) {
	repo := setupTestRepo(t)

	rapid.Check(t, func(t *rapid.T) {
		mgr := NewManager(repo, defaultTestSettings())
		defer mgr.Shutdown()

		sess, _ := createTestSession(t, mgr)
		extra := rapid.IntRange(0, 2).Draw(t, "extra_agents")
		for i := 0; i < extra; i++ {
			cfg := Config{Task: "extra", Rows: 24, Cols: 80}
			_, err := mgr.AddAgentWithCommand(sess.ID, cfg, func(name string) *exec.Cmd {
				return exec.Command("bash", "-c", "sleep 5")
			})
			if err != nil {
				t.Fatalf("AddAgentWithCommand: %v", err)
			}
		}

		assertAgentPathsMatch(t, sess)
		if got := len(sess.Agents()); got != 1+extra {
			t.Fatalf("expected %d agents, got %d", 1+extra, got)
		}
	})
}

// PlanPath and PrevPlanPath are always under the session's worktree.
func TestSessionRepoProp_PlanPathsUnderWorktree(t *testing.T) {
	repo := setupTestRepo(t)

	rapid.Check(t, func(t *rapid.T) {
		mgr := NewManager(repo, defaultTestSettings())
		defer mgr.Shutdown()

		sess := createTestSessionPlanOnly(t, mgr)
		wtPath := sess.Worktree.Path

		planPath := sess.PlanPath()
		prevPath := sess.PrevPlanPath()

		if !isSubdir(wtPath, planPath) {
			t.Fatalf("PlanPath %q not under worktree %q", planPath, wtPath)
		}
		if !isSubdir(wtPath, prevPath) {
			t.Fatalf("PrevPlanPath %q not under worktree %q", prevPath, wtPath)
		}

		// WritePlan + ReadPlan round-trip stays in the correct location.
		content := "# Goal\ntest-" + rapid.String().Draw(t, "suffix") + "\n"
		if err := sess.WritePlan(content); err != nil {
			t.Fatalf("WritePlan: %v", err)
		}
		got, err := sess.ReadPlan()
		if err != nil {
			t.Fatalf("ReadPlan: %v", err)
		}
		if got != content {
			t.Fatalf("ReadPlan round-trip mismatch")
		}
		if !isSubdir(wtPath, sess.PlanPath()) {
			t.Fatalf("plan file path drifted from worktree")
		}
	})
}

// Walking through any sequence of lifecycle phase transitions never changes
// the session's worktree path.
func TestSessionRepoProp_LifecycleTransitionsPreserveWorktreePath(t *testing.T) {
	repo := setupTestRepo(t)

	rapid.Check(t, func(t *rapid.T) {
		mgr := NewManager(repo, defaultTestSettings())
		defer mgr.Shutdown()

		sess, _ := createTestSession(t, mgr)
		originalPath := sess.Worktree.Path
		originalBranch := sess.Worktree.BaseBranch

		phases := genForwardPhaseSequence(t)
		for _, p := range phases {
			sess.SetLifecyclePhase(p)
			if sess.Worktree.Path != originalPath {
				t.Fatalf("Worktree.Path changed from %q to %q after phase %v",
					originalPath, sess.Worktree.Path, p)
			}
			if sess.Worktree.BaseBranch != originalBranch {
				t.Fatalf("Worktree.BaseBranch changed after phase %v", p)
			}
			assertWorktreeUnderRepo(t, repo, sess)
		}
	})
}

// DraftRequest.Cwd always matches the session's worktree path.
func TestSessionRepoProp_DraftCwdMatchesWorktreePath(t *testing.T) {
	repo := setupTestRepo(t)

	rapid.Check(t, func(t *rapid.T) {
		mgr := NewManager(repo, defaultTestSettings())
		defer mgr.Shutdown()

		drafter := newCapturingDrafter("# Goal\ntest\n\n## Tasks\n- [ ] one\n")
		mgr.SetPlanDrafter(drafter)

		sess, _ := createTestSession(t, mgr)

		if err := mgr.StartDraft(sess.ID, "add a feature"); err != nil {
			t.Fatalf("StartDraft: %v", err)
		}

		waitForConditionProp(t, 2*time.Second, func() bool { return !sess.IsDrafting() })

		cwds := drafter.DraftCwds()
		if len(cwds) == 0 {
			t.Fatal("drafter was never called")
		}
		for _, cwd := range cwds {
			if cwd != sess.Worktree.Path {
				t.Fatalf("DraftRequest.Cwd = %q, want %q", cwd, sess.Worktree.Path)
			}
		}
	})
}

// ReviseRequest.Cwd always matches the session's worktree path.
func TestSessionRepoProp_ReviseCwdMatchesWorktreePath(t *testing.T) {
	repo := setupTestRepo(t)

	rapid.Check(t, func(t *rapid.T) {
		mgr := NewManager(repo, defaultTestSettings())
		defer mgr.Shutdown()

		drafter := newCapturingDrafter("# Goal\nrevised\n\n## Tasks\n- [ ] one\n")
		mgr.SetPlanDrafter(drafter)

		sess, _ := createTestSession(t, mgr)
		if err := sess.WritePlan("# Goal\noriginal\n\n## Tasks\n- [ ] x\n"); err != nil {
			t.Fatalf("WritePlan: %v", err)
		}

		if err := mgr.RevisePlan(sess.ID, "improve the plan"); err != nil {
			t.Fatalf("RevisePlan: %v", err)
		}

		waitForConditionProp(t, 2*time.Second, func() bool { return !sess.IsRevising() })

		cwds := drafter.ReviseCwds()
		if len(cwds) == 0 {
			t.Fatal("drafter.Revise was never called")
		}
		for _, cwd := range cwds {
			if cwd != sess.Worktree.Path {
				t.Fatalf("ReviseRequest.Cwd = %q, want %q", cwd, sess.Worktree.Path)
			}
		}
	})
}

// Multiple sessions on the same manager all have worktrees under the same repo
// and all worktree paths are mutually distinct.
func TestSessionRepoProp_MultipleSessionsDistinctWorktreesSameRepo(t *testing.T) {
	repo := setupTestRepo(t)

	rapid.Check(t, func(t *rapid.T) {
		mgr := NewManager(repo, defaultTestSettings())
		defer mgr.Shutdown()

		n := rapid.IntRange(2, 4).Draw(t, "session_count")
		sessions := make([]*Session, n)
		for i := 0; i < n; i++ {
			sessions[i], _ = createTestSession(t, mgr)
		}

		paths := make(map[string]bool)
		for _, sess := range sessions {
			assertWorktreeUnderRepo(t, repo, sess)
			if paths[sess.Worktree.Path] {
				t.Fatalf("duplicate worktree path %q across sessions", sess.Worktree.Path)
			}
			paths[sess.Worktree.Path] = true
		}
	})
}

// After detach and resume, the session's worktree path and repo affinity are
// preserved.
func TestSessionRepoProp_DetachResumePreservesRepoPaths(t *testing.T) {
	repo := setupTestRepo(t)

	rapid.Check(t, func(t *rapid.T) {
		mgr := NewManager(repo, defaultTestSettings())

		sess, _ := createTestSession(t, mgr)
		origWorktreePath := sess.Worktree.Path
		origBranch := sess.Branch()
		origBaseBranch := sess.Worktree.BaseBranch
		sessID := sess.ID

		st := mgr.Detach()
		if st == nil {
			t.Fatal("Detach returned nil state")
		}
		if len(st.Sessions) == 0 {
			t.Fatal("no sessions in detached state")
		}

		var saved state.SessionState
		for _, ss := range st.Sessions {
			if ss.ID == sessID {
				saved = ss
				break
			}
		}
		if saved.ID == "" {
			t.Fatal("session not found in detached state")
		}
		if saved.WorktreePath != origWorktreePath {
			t.Fatalf("saved WorktreePath = %q, want %q", saved.WorktreePath, origWorktreePath)
		}

		mgr2 := NewManager(repo, defaultTestSettings())
		defer mgr2.Shutdown()
		mgr2.SetBranchNamer(nil)
		mgr2.SetTaskSummarizer(nil)

		cfg := Config{Task: "resume", Rows: 24, Cols: 80}
		if err := mgr2.ResumeSession(saved, cfg); err != nil {
			t.Fatalf("ResumeSession: %v", err)
		}

		resumed := mgr2.GetSession(sessID)
		if resumed == nil {
			t.Fatal("resumed session not found")
		}
		if resumed.Worktree.Path != origWorktreePath {
			t.Fatalf("resumed Worktree.Path = %q, want %q", resumed.Worktree.Path, origWorktreePath)
		}
		if resumed.Branch() != origBranch {
			t.Fatalf("resumed Branch = %q, want %q", resumed.Branch(), origBranch)
		}
		if resumed.Worktree.BaseBranch != origBaseBranch {
			t.Fatalf("resumed BaseBranch = %q, want %q", resumed.Worktree.BaseBranch, origBaseBranch)
		}
		assertWorktreeUnderRepo(t, repo, resumed)
	})
}

// Sessions on two different repos never reference each other's paths.
func TestSessionRepoProp_CrossRepoIsolation(t *testing.T) {
	repoA := setupTestRepo(t)
	repoB := setupTestRepo(t)

	rapid.Check(t, func(t *rapid.T) {
		mgrA := NewManager(repoA, defaultTestSettings())
		defer mgrA.Shutdown()
		mgrB := NewManager(repoB, defaultTestSettings())
		defer mgrB.Shutdown()

		sessA, _ := createTestSession(t, mgrA)
		sessB, _ := createTestSession(t, mgrB)

		assertWorktreeUnderRepo(t, repoA, sessA)
		if isSubdir(repoB, sessA.Worktree.Path) {
			t.Fatalf("session A worktree %q is under repo B %q", sessA.Worktree.Path, repoB)
		}

		assertWorktreeUnderRepo(t, repoB, sessB)
		if isSubdir(repoA, sessB.Worktree.Path) {
			t.Fatalf("session B worktree %q is under repo A %q", sessB.Worktree.Path, repoA)
		}

		if sessA.Worktree.Path == sessB.Worktree.Path {
			t.Fatal("sessions on different repos share the same worktree path")
		}
	})
}

// Config.RepoPath is set to m.repoPath by the Manager before creating agents.
func TestSessionRepoProp_ConfigRepoPathSetByManager(t *testing.T) {
	repo := setupTestRepo(t)

	rapid.Check(t, func(t *rapid.T) {
		mgr := NewManager(repo, defaultTestSettings())
		defer mgr.Shutdown()

		sess, ag := createTestSession(t, mgr)

		if mgr.RepoPath() != repo {
			t.Fatalf("Manager.RepoPath() = %q, want %q", mgr.RepoPath(), repo)
		}
		assertWorktreeUnderRepo(t, mgr.RepoPath(), sess)
		if !isSubdir(mgr.RepoPath(), ag.WorktreePath) {
			t.Fatalf("agent WorktreePath %q not under manager repo %q", ag.WorktreePath, mgr.RepoPath())
		}
	})
}

// A shell agent added to a session has WorktreePath matching the session.
func TestSessionRepoProp_ShellAgentWorktreePathMatchesSession(t *testing.T) {
	repo := setupTestRepo(t)

	rapid.Check(t, func(t *rapid.T) {
		mgr := NewManager(repo, defaultTestSettings())
		defer mgr.Shutdown()

		sess, _ := createTestSession(t, mgr)

		shellCfg := Config{Name: "shell", Rows: 24, Cols: 80}
		shell, err := mgr.AddShell(sess.ID, shellCfg)
		if err != nil {
			t.Fatalf("AddShell: %v", err)
		}

		if shell.WorktreePath != sess.Worktree.Path {
			t.Fatalf("shell WorktreePath = %q, want %q", shell.WorktreePath, sess.Worktree.Path)
		}
		if !shell.IsShell {
			t.Fatal("expected shell agent to have IsShell=true")
		}
		assertAgentPathsMatch(t, sess)
	})
}

// CreateSessionOnBranch produces a worktree under the manager's repo.
func TestSessionRepoProp_CreateSessionOnBranch_WorktreeUnderRepo(t *testing.T) {
	repo := setupTestRepo(t)

	cmd := exec.Command("git", "branch", "test-attach-branch")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("creating branch: %v\n%s", err, out)
	}

	rapid.Check(t, func(t *rapid.T) {
		mgr := NewManager(repo, defaultTestSettings())
		defer mgr.Shutdown()

		cfg := Config{Task: "branch-attach", Rows: 24, Cols: 80}
		sess, ag, err := mgr.CreateSessionOnBranchWithCommand(
			"test-attach-branch", "", cfg,
			func(name string) *exec.Cmd {
				return exec.Command("bash", "-c", "sleep 5")
			},
		)
		if err != nil {
			t.Fatalf("CreateSessionOnBranchWithCommand: %v", err)
		}

		assertWorktreeUnderRepo(t, repo, sess)
		if ag.WorktreePath != sess.Worktree.Path {
			t.Fatalf("agent WorktreePath = %q, want %q", ag.WorktreePath, sess.Worktree.Path)
		}
		if !strings.Contains(sess.Branch(), "test-attach-branch") {
			t.Fatalf("Branch() = %q, want to contain %q", sess.Branch(), "test-attach-branch")
		}
	})
}

// Planning-only sessions (no agents) also have worktrees under the repo.
func TestSessionRepoProp_PlanningSessionWorktreeUnderRepo(t *testing.T) {
	repo := setupTestRepo(t)

	rapid.Check(t, func(t *rapid.T) {
		mgr := NewManager(repo, defaultTestSettings())
		defer mgr.Shutdown()

		sess := createTestSessionPlanOnly(t, mgr)
		assertWorktreeUnderRepo(t, repo, sess)

		if sess.AgentCount() != 0 {
			t.Fatalf("planning session should have 0 agents, got %d", sess.AgentCount())
		}
		if !isSubdir(sess.Worktree.Path, sess.PlanPath()) {
			t.Fatalf("PlanPath %q not under worktree %q", sess.PlanPath(), sess.Worktree.Path)
		}
	})
}

// The worktree path never shares a prefix with any other session's worktree
// path (they are peers under .refrain/worktrees/, not nested).
func TestSessionRepoProp_WorktreePathsNeverNested(t *testing.T) {
	repo := setupTestRepo(t)

	rapid.Check(t, func(t *rapid.T) {
		mgr := NewManager(repo, defaultTestSettings())
		defer mgr.Shutdown()

		n := rapid.IntRange(2, 4).Draw(t, "count")
		sessions := make([]*Session, n)
		for i := 0; i < n; i++ {
			sessions[i], _ = createTestSession(t, mgr)
		}

		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				if i == j {
					continue
				}
				pi := sessions[i].Worktree.Path
				pj := sessions[j].Worktree.Path
				if isSubdir(pi, pj) {
					t.Fatalf("session %d worktree %q is nested under session %d worktree %q",
						j, pj, i, pi)
				}
			}
		}
	})
}

// All session worktrees live directly under the worktree directory.
func TestSessionRepoProp_WorktreePathStructure(t *testing.T) {
	repo := setupTestRepo(t)

	rapid.Check(t, func(t *rapid.T) {
		mgr := NewManager(repo, defaultTestSettings())
		defer mgr.Shutdown()

		sess, _ := createTestSession(t, mgr)
		wtPath := sess.Worktree.Path

		wtDir := filepath.Dir(wtPath)
		expectedDir := filepath.Join(repo, ".refrain", "worktrees")
		if wtDir != expectedDir {
			t.Fatalf("worktree parent = %q, want %q", wtDir, expectedDir)
		}

		if filepath.Base(wtPath) != sess.CurrentName() {
			t.Fatalf("worktree basename = %q, session name = %q", filepath.Base(wtPath), sess.CurrentName())
		}
	})
}
