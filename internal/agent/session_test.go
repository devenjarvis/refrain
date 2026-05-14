package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/baton/internal/git"
)

func TestSessionCreation(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 2")
	})
	if err != nil {
		t.Fatal(err)
	}

	if sess.Name == "" {
		t.Error("session name should not be empty")
	}
	if sess.Worktree == nil {
		t.Fatal("session worktree should not be nil")
	}
	if ag == nil {
		t.Fatal("first agent should not be nil")
	}
	if sess.AgentCount() != 1 {
		t.Errorf("expected 1 agent, got %d", sess.AgentCount())
	}
}

func TestMultipleAgentsShareWorktree(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, ag1, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}

	ag2, err := mgr.AddAgentWithCommand(sess.ID, cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Both agents should share the same worktree path.
	if ag1.WorktreePath != ag2.WorktreePath {
		t.Errorf("agents should share worktree path: %s != %s", ag1.WorktreePath, ag2.WorktreePath)
	}
	if ag1.WorktreePath != sess.Worktree.Path {
		t.Errorf("agent worktree path should match session: %s != %s", ag1.WorktreePath, sess.Worktree.Path)
	}
	if sess.AgentCount() != 2 {
		t.Errorf("expected 2 agents, got %d", sess.AgentCount())
	}
	if mgr.AgentCount() != 2 {
		t.Errorf("expected 2 total agents, got %d", mgr.AgentCount())
	}
}

func TestKillAgentSessionSurvives(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, ag1, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 10")
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = mgr.AddAgentWithCommand(sess.ID, cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 10")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Kill just the first agent.
	if err := mgr.KillAgent(sess.ID, ag1.ID); err != nil {
		t.Fatal(err)
	}

	// Session should still exist with one agent.
	if mgr.GetSession(sess.ID) == nil {
		t.Error("session should still exist after killing one agent")
	}
	if sess.AgentCount() != 1 {
		t.Errorf("expected 1 agent remaining, got %d", sess.AgentCount())
	}
	if mgr.AgentCount() != 1 {
		t.Errorf("expected 1 total agent, got %d", mgr.AgentCount())
	}

	// Worktree should still exist.
	if _, err := os.Stat(sess.Worktree.Path); os.IsNotExist(err) {
		t.Error("worktree should still exist after killing one agent")
	}
}

func TestKillSessionCleansAll(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 10")
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = mgr.AddAgentWithCommand(sess.ID, cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 10")
	})
	if err != nil {
		t.Fatal(err)
	}

	wtPath := sess.Worktree.Path
	sessID := sess.ID

	if err := mgr.KillSession(sessID); err != nil {
		t.Fatal(err)
	}

	// Session should be gone.
	if mgr.GetSession(sessID) != nil {
		t.Error("session should be removed after KillSession")
	}
	// Worktree should be gone.
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Error("worktree should be removed after KillSession")
	}
	if mgr.AgentCount() != 0 {
		t.Errorf("expected 0 agents, got %d", mgr.AgentCount())
	}
}

func TestSessionCompositeStatus(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	// Create a session with an agent that exits quickly.
	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "echo hi; sleep 0.3")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Initially should be starting or active.
	s := sess.Status()
	if s != StatusStarting && s != StatusActive {
		t.Errorf("expected Starting or Active initially, got %s", s)
	}

	// Add a second agent that runs longer.
	_, err = mgr.AddAgentWithCommand(sess.ID, cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "echo world; sleep 2")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait briefly for output.
	time.Sleep(200 * time.Millisecond)
	s = sess.Status()
	if s != StatusActive && s != StatusStarting {
		t.Errorf("expected Active or Starting with running agents, got %s", s)
	}

	// Wait for the first agent to finish but the second is still running.
	time.Sleep(500 * time.Millisecond)
	// Session should still be active since agent 2 is still running.
	s = sess.Status()
	if s != StatusActive && s != StatusIdle {
		t.Errorf("expected Active or Idle (agent2 still running), got %s", s)
	}
}

func TestSessionAgentsSorted(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, ag1, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(10 * time.Millisecond) // ensure different CreatedAt

	ag2, err := mgr.AddAgentWithCommand(sess.ID, cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}

	agents := sess.Agents()
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
	if agents[0].ID != ag1.ID {
		t.Errorf("expected first agent %s, got %s", ag1.ID, agents[0].ID)
	}
	if agents[1].ID != ag2.ID {
		t.Errorf("expected second agent %s, got %s", ag2.ID, agents[1].ID)
	}
}

func TestKillLastAgentAutoClosesSession(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, ag1, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 10")
	})
	if err != nil {
		t.Fatal(err)
	}

	ag2, err := mgr.AddAgentWithCommand(sess.ID, cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 10")
	})
	if err != nil {
		t.Fatal(err)
	}

	wtPath := sess.Worktree.Path
	sessID := sess.ID

	// Kill first agent — session should survive.
	if err := mgr.KillAgent(sessID, ag1.ID); err != nil {
		t.Fatal(err)
	}
	if mgr.GetSession(sessID) == nil {
		t.Fatal("session should still exist after killing first agent")
	}

	// Kill second agent — session should auto-close.
	if err := mgr.KillAgent(sessID, ag2.ID); err != nil {
		t.Fatal(err)
	}
	if mgr.GetSession(sessID) != nil {
		t.Error("session should be removed after killing last agent")
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Error("worktree should be removed after session auto-close")
	}

	// Verify EventSessionClosed was emitted.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-mgr.Events():
			if e.Type == EventSessionClosed && e.SessionID == sessID {
				return // success
			}
		case <-deadline:
			t.Error("expected EventSessionClosed event")
			return
		}
	}
}

// TestNaturalExitDoesNotCloseSession pins the lifecycle-pipeline contract:
// when agents exit naturally (clean exit) the session must stay in the
// manager so the user can advance it to REVIEWING → SHIPPING. Sessions are
// only removed by explicit actions (KillSession, merge, Detach cleanup).
// Worktree must also persist — removing it would break the review panel.
func TestNaturalExitDoesNotCloseSession(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, ag1, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "echo done")
	})
	if err != nil {
		t.Fatal(err)
	}

	ag2, err := mgr.AddAgentWithCommand(sess.ID, cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "echo done")
	})
	if err != nil {
		t.Fatal(err)
	}

	wtPath := sess.Worktree.Path
	sessID := sess.ID

	// Wait for both agents to exit naturally.
	deadline := time.After(5 * time.Second)
	doneCount := 0
	for doneCount < 2 {
		select {
		case e := <-mgr.Events():
			if e.Type == EventDone && e.SessionID == sessID {
				doneCount++
			}
		case <-deadline:
			t.Fatal("timed out waiting for both agents to exit")
		}
	}

	_ = ag1
	_ = ag2

	// Session must remain — the user needs to advance it via 'm' (review) or
	// explicit kill. Auto-closing on natural exit was removed because it races
	// with TestManager_AgentCount_ExcludesExitedAgents and breaks the pipeline.
	if mgr.GetSession(sessID) == nil {
		t.Error("session must remain in manager after agents exit naturally")
	}
	if sess.AgentCount() != 2 {
		t.Errorf("both agents must stay in session map after natural exit, got AgentCount=%d", sess.AgentCount())
	}
	if sess.LiveAgentCount() != 0 {
		t.Errorf("live count must be 0 after both agents exit, got LiveAgentCount=%d", sess.LiveAgentCount())
	}
	// Worktree must persist for review panel use.
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		t.Error("worktree must persist after agents exit naturally")
	}
}

func TestAddShellCreatesShellAgent(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}

	shellCfg := Config{Rows: 24, Cols: 80}
	shell, err := sess.AddShell(shellCfg)
	if err != nil {
		t.Fatal(err)
	}

	if !shell.IsShell {
		t.Error("shell agent should have IsShell == true")
	}
	if shell.Name != "shell" {
		t.Errorf("expected shell agent name 'shell', got %q", shell.Name)
	}
	if !sess.HasShell() {
		t.Error("HasShell() should return true after adding shell")
	}
}

func TestAddShellEnforcesOnePerSession(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}

	shellCfg := Config{Rows: 24, Cols: 80}
	_, err = sess.AddShell(shellCfg)
	if err != nil {
		t.Fatal(err)
	}

	// Second shell should fail.
	_, err = sess.AddShell(shellCfg)
	if err == nil {
		t.Error("expected error when adding second shell, got nil")
	}
}

func TestSessionStatusExcludesShell(t *testing.T) {
	repo := setupTestRepo(t)

	// Create session directly (no manager) to avoid auto-close interference.
	wt := &git.WorktreeInfo{Path: repo, Branch: "main", BaseBranch: "main"}
	sess := newSession("test-sess", "test", wt)

	// Add a Claude agent that exits quickly.
	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	ag, err := sess.AddAgent(cfg, func(_ string) *exec.Cmd { return exec.Command("bash", "-c", "echo done") })
	if err != nil {
		t.Fatal(err)
	}

	// Add a shell (which will stay active).
	shellCfg := Config{Rows: 24, Cols: 80}
	shell, err := sess.AddShell(shellCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer shell.Kill()

	// Wait for the Claude agent to exit naturally.
	select {
	case <-ag.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Claude agent to exit")
	}

	// Session status should be Done — shell is excluded from the composite.
	st := sess.Status()
	if st != StatusDone {
		t.Fatalf("expected session status Done (shell excluded), got %s", st)
	}
}

func TestCreateSessionOnBranch(t *testing.T) {
	repo := setupTestRepo(t)

	// Create a branch to attach to.
	cmd := exec.Command("git", "branch", "feature/add-auth")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("creating branch: %v\n%s", err, out)
	}

	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionOnBranchWithCommand("feature/add-auth", "main", cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Session name should be derived from branch.
	if sess.Name != "add-auth" {
		t.Errorf("expected session name 'add-auth', got %q", sess.Name)
	}

	// Agent should be created.
	if ag == nil {
		t.Fatal("agent should not be nil")
	}

	// Worktree should exist.
	if _, err := os.Stat(sess.Worktree.Path); os.IsNotExist(err) {
		t.Error("worktree should exist")
	}

	// Branch should be the one we specified.
	if sess.Worktree.Branch != "feature/add-auth" {
		t.Errorf("expected branch 'feature/add-auth', got %q", sess.Worktree.Branch)
	}

	// Base branch should be overridden to "main".
	if sess.Worktree.BaseBranch != "main" {
		t.Errorf("expected base branch 'main', got %q", sess.Worktree.BaseBranch)
	}
}

func TestCreateSessionOnBranchPreservesBranch(t *testing.T) {
	repo := setupTestRepo(t)

	// Create and switch to a branch.
	for _, args := range [][]string{
		{"git", "branch", "feature/keep-me"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, _, err := mgr.CreateSessionOnBranchWithCommand("feature/keep-me", "", cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 10")
	})
	if err != nil {
		t.Fatal(err)
	}

	sessID := sess.ID

	// Kill the session — should clean up worktree but NOT delete the branch.
	if err := mgr.KillSession(sessID); err != nil {
		t.Fatal(err)
	}

	// Branch should still exist.
	cmd := exec.Command("git", "branch", "--list", "feature/keep-me")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git branch --list: %v", err)
	}
	if !strings.Contains(string(out), "feature/keep-me") {
		t.Error("branch should be preserved after killing attached session")
	}
}

func TestCreateSessionOwnsBranchDeletesOnCleanup(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 10")
	})
	if err != nil {
		t.Fatal(err)
	}

	branch := sess.Worktree.Branch
	sessID := sess.ID

	if err := mgr.KillSession(sessID); err != nil {
		t.Fatal(err)
	}

	// Branch should be deleted (normal session owns its branch).
	cmd := exec.Command("git", "branch", "--list", branch)
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git branch --list: %v", err)
	}
	if strings.Contains(string(out), branch) {
		t.Errorf("branch %s should be deleted after killing session that owns it", branch)
	}
}

func TestSlugifyBranchName(t *testing.T) {
	tests := []struct {
		branch   string
		wantName string // expected name, or "" to indicate RandomName fallback
	}{
		{"feature/add-auth", "add-auth"},
		{"bugfix/fix-login-issue", "fix-login-issue"},
		{"main", "main"},
		{"some/nested/deep/branch", "branch"},
	}

	for _, tt := range tests {
		name := slugifyBranchName(tt.branch, nil)
		if tt.wantName != "" && name != tt.wantName {
			t.Errorf("slugifyBranchName(%q) = %q, want %q", tt.branch, name, tt.wantName)
		}
		if name == "" {
			t.Errorf("slugifyBranchName(%q) returned empty string", tt.branch)
		}
	}
}

func TestSlugifyBranchNameCollisionFallback(t *testing.T) {
	existing := []string{"add-auth"}
	name := slugifyBranchName("feature/add-auth", existing)

	// Should fall back to a random name due to collision.
	if name == "add-auth" {
		t.Error("expected fallback to random name on collision, got 'add-auth'")
	}
	if name == "" {
		t.Error("name should not be empty")
	}
}

func TestAddAgentDefaultAssignsUniqueName(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, ag1, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Agent should get a random name distinct from session name.
	if ag1.Name == "" {
		t.Fatal("agent 1 should have a non-empty name")
	}
	if ag1.Name == sess.Name {
		t.Errorf("agent name %q should differ from session name %q", ag1.Name, sess.Name)
	}

	// Add a second agent — should also get a unique name.
	ag2, err := mgr.AddAgentWithCommand(sess.ID, cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}

	if ag2.Name == "" {
		t.Fatal("agent 2 should have a non-empty name")
	}
	if ag2.Name == ag1.Name {
		t.Errorf("agent 2 name %q should differ from agent 1 name %q", ag2.Name, ag1.Name)
	}
	if ag2.Name == sess.Name {
		t.Errorf("agent 2 name %q should differ from session name %q", ag2.Name, sess.Name)
	}

	// Explicit names should be preserved.
	ag3, err := mgr.AddAgentWithCommand(sess.ID, Config{Name: "custom-name", Task: "test", Rows: 24, Cols: 80}, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	if ag3.Name != "custom-name" {
		t.Errorf("explicit name should be preserved, got %q", ag3.Name)
	}
}

func TestSession_LifecyclePhase_DefaultIsPlanning(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})
	if got := s.LifecyclePhase(); got != LifecyclePlanning {
		t.Errorf("default LifecyclePhase = %v, want LifecyclePlanning", got)
	}
}

func TestSession_SetLifecyclePhase(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})
	s.SetLifecyclePhase(LifecycleReadyForReview)
	if got := s.LifecyclePhase(); got != LifecycleReadyForReview {
		t.Errorf("LifecyclePhase() = %v, want LifecycleReadyForReview", got)
	}
}

func TestSession_OriginalPrompt_SetOnlyOnce(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})
	if s.OriginalPrompt() != "" {
		t.Fatal("expected empty initial prompt")
	}
	s.SetOriginalPrompt("first prompt")
	s.SetOriginalPrompt("second prompt") // must be ignored
	if got := s.OriginalPrompt(); got != "first prompt" {
		t.Errorf("OriginalPrompt() = %q, want %q", got, "first prompt")
	}
}

func TestSession_MarkDone(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})
	if !s.DoneAt().IsZero() {
		t.Fatal("expected zero DoneAt before MarkDone")
	}
	s.MarkDone()
	if s.DoneAt().IsZero() {
		t.Error("DoneAt should be set after MarkDone")
	}
	before := s.DoneAt()
	s.MarkDone() // second call must be no-op
	if s.DoneAt() != before {
		t.Error("MarkDone called twice should not update timestamp")
	}
}

func TestSession_RestoreDoneAt(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})
	ts := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	s.RestoreDoneAt(ts)
	if got := s.DoneAt(); !got.Equal(ts) {
		t.Errorf("DoneAt = %v, want %v", got, ts)
	}
	// RestoreDoneAt overwrites unconditionally, unlike MarkDone which is idempotent.
	ts2 := ts.Add(time.Hour)
	s.RestoreDoneAt(ts2)
	if got := s.DoneAt(); !got.Equal(ts2) {
		t.Errorf("second RestoreDoneAt: DoneAt = %v, want %v", got, ts2)
	}
}

// --- Task summary tests ---

func TestSession_TryStartTaskSummary_FirstCallReturnsTrue(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})
	if !s.TryStartTaskSummary() {
		t.Error("TryStartTaskSummary() should return true on first call")
	}
}

func TestSession_TryStartTaskSummary_ReturnsFalseIfSummarizing(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})
	if !s.TryStartTaskSummary() {
		t.Fatal("expected true on first call")
	}
	// In-flight summarize: second call should return false.
	if s.TryStartTaskSummary() {
		t.Error("TryStartTaskSummary() should return false while summarizing is in flight")
	}
}

func TestSession_TryStartTaskSummary_ReturnsFalseAfterHasSummary(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})
	s.SetTaskSummary("done")
	// hasTaskSummary is now true; should never start again.
	if s.TryStartTaskSummary() {
		t.Error("TryStartTaskSummary() should return false once hasTaskSummary is true")
	}
}

func TestSession_FinishTaskSummary_AllowsRetry(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})
	if !s.TryStartTaskSummary() {
		t.Fatal("expected true on first call")
	}
	s.finishTaskSummary()
	// After finish (no hasTaskSummary set), should be allowed to try again.
	if !s.TryStartTaskSummary() {
		t.Error("TryStartTaskSummary() should return true after finishTaskSummary clears the flag")
	}
}

func TestSession_SetTaskSummary_SetsFieldsAndHasTaskSummary(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})
	if s.HasTaskSummary() {
		t.Fatal("HasTaskSummary() should be false initially")
	}
	s.SetTaskSummary("implement auth")
	if !s.HasTaskSummary() {
		t.Error("HasTaskSummary() should be true after SetTaskSummary")
	}
	if got := s.TaskSummary(); got != "implement auth" {
		t.Errorf("TaskSummary() = %q, want %q", got, "implement auth")
	}
}

func TestSession_SetTaskSummary_EmptyStringStillSetsHasSummary(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})
	s.SetTaskSummary("")
	if !s.HasTaskSummary() {
		t.Error("HasTaskSummary() should be true even when summary is empty string")
	}
}

// --- Drafting state machine tests ---

func TestSession_TryStartDraft_FirstCallReturnsTrue(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})
	cancelled := false
	cancel := func() { cancelled = true }
	if !s.TryStartDraft(cancel) {
		t.Error("TryStartDraft() should return true on first call")
	}
	if !s.IsDrafting() {
		t.Error("IsDrafting() should be true after TryStartDraft returns true")
	}
	if cancelled {
		t.Error("cancel should not have fired yet — only on finishDraft / CancelDraft")
	}
}

func TestSession_TryStartDraft_ReturnsFalseIfDrafting(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})
	if !s.TryStartDraft(func() {}) {
		t.Fatal("expected true on first call")
	}
	if s.TryStartDraft(func() {}) {
		t.Error("TryStartDraft() should return false while drafting is in flight")
	}
}

func TestSession_FinishDraft_ClearsFlagAndCallsCancel(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})

	cancelCalls := 0
	if !s.TryStartDraft(func() { cancelCalls++ }) {
		t.Fatal("expected true on first call")
	}

	s.finishDraft()
	if s.IsDrafting() {
		t.Error("IsDrafting() should be false after finishDraft")
	}
	if cancelCalls != 1 {
		t.Errorf("cancel calls = %d, want 1 (finishDraft must release the context)", cancelCalls)
	}

	// After finish, a fresh draft is allowed.
	if !s.TryStartDraft(func() {}) {
		t.Error("TryStartDraft should be allowed after finishDraft")
	}
}

func TestSession_CancelDraft_NoOpWhenNotDrafting(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})
	// Should not panic; nothing to cancel.
	s.CancelDraft()
}

func TestSession_CancelDraft_FiresStoredCancel(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})

	cancelCalls := 0
	if !s.TryStartDraft(func() { cancelCalls++ }) {
		t.Fatal("expected true on first call")
	}

	s.CancelDraft()
	if cancelCalls != 1 {
		t.Errorf("CancelDraft cancel calls = %d, want 1", cancelCalls)
	}

	// finishDraft after CancelDraft should still fire cancel — the stored
	// cancel func has not been cleared yet because finishDraft owns the
	// clearing, not CancelDraft. The runtime guarantees double-cancel is
	// safe per the context package spec.
	s.finishDraft()
	if cancelCalls != 2 {
		t.Errorf("after finishDraft, cancel calls = %d, want 2", cancelCalls)
	}
}

func TestSession_DraftError_RoundTrip(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})
	if s.DraftError() != nil {
		t.Errorf("DraftError() = %v, want nil initially", s.DraftError())
	}
	want := fmt.Errorf("boom")
	s.SetDraftError(want)
	if got := s.DraftError(); got != want {
		t.Errorf("DraftError() = %v, want %v", got, want)
	}
	s.SetDraftError(nil)
	if s.DraftError() != nil {
		t.Error("DraftError() should be cleared after SetDraftError(nil)")
	}
}

func TestSession_FinishDraft_DoesNotClearDraftError(t *testing.T) {
	// finishDraft only clears the in-flight flag; the last error stays so
	// the Planning card can render the failure even after the goroutine exits.
	s := newSession("id", "name", &git.WorktreeInfo{})
	if !s.TryStartDraft(func() {}) {
		t.Fatal("expected true on first call")
	}
	s.SetDraftError(fmt.Errorf("subprocess died"))
	s.finishDraft()
	if s.DraftError() == nil {
		t.Error("finishDraft should not clear DraftError")
	}
}

// --- LastOutputTime tests ---

func TestSession_LastOutputTime_ZeroWithNoAgents(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})
	if got := s.LastOutputTime(); !got.IsZero() {
		t.Errorf("LastOutputTime() = %v, want zero", got)
	}
}

func TestSession_LastOutputTime_ReturnsMaxAcrossAgents(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})

	earlier := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	later := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	a1 := &Agent{ID: "a1", IsShell: false, CreatedAt: time.Now()}
	a1.lastOutput = earlier
	a2 := &Agent{ID: "a2", IsShell: false, CreatedAt: time.Now()}
	a2.lastOutput = later

	s.mu.Lock()
	s.agents["a1"] = a1
	s.agents["a2"] = a2
	s.mu.Unlock()

	if got := s.LastOutputTime(); !got.Equal(later) {
		t.Errorf("LastOutputTime() = %v, want %v", got, later)
	}
}

func TestSession_LastOutputTime_SkipsShellAgents(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})

	shellTime := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	claudeTime := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	shell := &Agent{ID: "shell", IsShell: true, CreatedAt: time.Now()}
	shell.lastOutput = shellTime
	claude := &Agent{ID: "claude", IsShell: false, CreatedAt: time.Now()}
	claude.lastOutput = claudeTime

	s.mu.Lock()
	s.agents["shell"] = shell
	s.agents["claude"] = claude
	s.mu.Unlock()

	// Should return claudeTime, not shellTime (shell is excluded).
	if got := s.LastOutputTime(); !got.Equal(claudeTime) {
		t.Errorf("LastOutputTime() = %v, want %v (shell excluded)", got, claudeTime)
	}
}

func TestSession_LastOutputTime_ZeroWhenOnlyShellAgents(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})

	shell := &Agent{ID: "shell", IsShell: true, CreatedAt: time.Now()}
	shell.lastOutput = time.Now()

	s.mu.Lock()
	s.agents["shell"] = shell
	s.mu.Unlock()

	if got := s.LastOutputTime(); !got.IsZero() {
		t.Errorf("LastOutputTime() = %v, want zero (only shell agents)", got)
	}
}

// --- IsReviewable tests ---

func makeReviewableSession(t *testing.T, statuses []Status, includeShell bool) *Session {
	t.Helper()
	s := newSession("id", "name", &git.WorktreeInfo{})
	s.mu.Lock()
	for i, st := range statuses {
		id := fmt.Sprintf("a%d", i)
		ag := &Agent{ID: id, IsShell: false, CreatedAt: time.Now()}
		ag.status = st
		s.agents[id] = ag
	}
	if includeShell {
		shell := &Agent{ID: "shell", IsShell: true, CreatedAt: time.Now()}
		shell.status = StatusActive
		s.agents["shell"] = shell
	}
	s.mu.Unlock()
	return s
}

func TestSession_IsReviewable_AllIdle(t *testing.T) {
	s := makeReviewableSession(t, []Status{StatusIdle, StatusIdle}, false)
	if !s.IsReviewable() {
		t.Error("IsReviewable() = false, want true for all-idle session")
	}
}

func TestSession_IsReviewable_MixedIdleAndActive(t *testing.T) {
	s := makeReviewableSession(t, []Status{StatusIdle, StatusActive}, false)
	if s.IsReviewable() {
		t.Error("IsReviewable() = true, want false when an agent is Active")
	}
}

func TestSession_IsReviewable_AllDone(t *testing.T) {
	s := makeReviewableSession(t, []Status{StatusDone, StatusDone}, false)
	if !s.IsReviewable() {
		t.Error("IsReviewable() = false, want true for all-done session")
	}
}

func TestSession_IsReviewable_Waiting(t *testing.T) {
	s := makeReviewableSession(t, []Status{StatusWaiting}, false)
	if s.IsReviewable() {
		t.Error("IsReviewable() = true, want false for waiting session")
	}
}

func TestSession_IsReviewable_Starting(t *testing.T) {
	s := makeReviewableSession(t, []Status{StatusStarting}, false)
	if s.IsReviewable() {
		t.Error("IsReviewable() = true, want false for starting session")
	}
}

func TestSession_IsReviewable_ShellOnly(t *testing.T) {
	s := makeReviewableSession(t, nil, true)
	if s.IsReviewable() {
		t.Error("IsReviewable() = true, want false for shell-only session")
	}
}

func TestSession_IsReviewable_NoAgents(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})
	if s.IsReviewable() {
		t.Error("IsReviewable() = true, want false for no-agents session")
	}
}

func TestSession_IsReviewable_SingleError(t *testing.T) {
	s := makeReviewableSession(t, []Status{StatusError}, false)
	if !s.IsReviewable() {
		t.Error("IsReviewable() = false, want true for single-error session")
	}
}

func TestSession_IsReviewable_IgnoresShellWhenOtherAgentsReviewable(t *testing.T) {
	// Shell agent in StatusActive shouldn't block reviewability.
	s := makeReviewableSession(t, []Status{StatusIdle}, true)
	if !s.IsReviewable() {
		t.Error("IsReviewable() = false, want true (shell agent should be ignored)")
	}
}

// --- Plan persistence tests ---

func TestSession_PlanPath(t *testing.T) {
	dir := t.TempDir()
	s := newSession("id", "name", &git.WorktreeInfo{Path: dir})
	want := filepath.Join(dir, ".claude", "plan.md")
	if got := s.PlanPath(); got != want {
		t.Errorf("PlanPath() = %q, want %q", got, want)
	}
}

func TestSession_HasPlan_FalseInitially(t *testing.T) {
	dir := t.TempDir()
	s := newSession("id", "name", &git.WorktreeInfo{Path: dir})
	if s.HasPlan() {
		t.Error("HasPlan() should be false before any WritePlan")
	}
}

func TestSession_ReadPlan_ReturnsEmptyWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	s := newSession("id", "name", &git.WorktreeInfo{Path: dir})
	got, err := s.ReadPlan()
	if err != nil {
		t.Fatalf("ReadPlan: %v", err)
	}
	if got != "" {
		t.Errorf("ReadPlan() = %q, want \"\" for missing plan file", got)
	}
}

func TestSession_WritePlan_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := newSession("id", "name", &git.WorktreeInfo{Path: dir})
	const body = "# Goal\nDo a thing\n\n## Tasks\n- [ ] one\n"
	if err := s.WritePlan(body); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	if !s.HasPlan() {
		t.Error("HasPlan() should report true after WritePlan")
	}
	got, err := s.ReadPlan()
	if err != nil {
		t.Fatalf("ReadPlan: %v", err)
	}
	if got != body {
		t.Errorf("plan = %q, want %q", got, body)
	}
}

func TestSession_WritePlan_CreatesClaudeDir(t *testing.T) {
	dir := t.TempDir()
	s := newSession("id", "name", &git.WorktreeInfo{Path: dir})
	if err := s.WritePlan("# Goal\nx"); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	claudeDir := filepath.Join(dir, ".claude")
	info, err := os.Stat(claudeDir)
	if err != nil {
		t.Fatalf("stat .claude: %v", err)
	}
	if !info.IsDir() {
		t.Error(".claude should be a directory")
	}
}

func TestSession_WritePlan_AtomicReplace(t *testing.T) {
	dir := t.TempDir()
	s := newSession("id", "name", &git.WorktreeInfo{Path: dir})

	if err := s.WritePlan("v1"); err != nil {
		t.Fatal(err)
	}
	if err := s.WritePlan("v2"); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadPlan()
	if err != nil {
		t.Fatal(err)
	}
	if got != "v2" {
		t.Errorf("after second write, plan = %q, want v2", got)
	}

	// No .tmp files should be left behind.
	entries, err := os.ReadDir(filepath.Join(dir, ".claude"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

func TestSession_WritePlan_AppendsToGitignoreWhenMissing(t *testing.T) {
	dir := t.TempDir()
	s := newSession("id", "name", &git.WorktreeInfo{Path: dir})

	gitignorePath := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("node_modules\n.baton/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := s.WritePlan("body"); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}

	got, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), ".claude/") {
		t.Errorf(".gitignore missing .claude/ after WritePlan; got=%q", string(got))
	}
}

func TestSession_WritePlan_GitignoreUntouchedWhenAlreadyCovered(t *testing.T) {
	dir := t.TempDir()
	s := newSession("id", "name", &git.WorktreeInfo{Path: dir})

	gitignorePath := filepath.Join(dir, ".gitignore")
	const original = ".claude/\nnode_modules\n"
	if err := os.WriteFile(gitignorePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := s.WritePlan("body"); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}

	got, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Errorf(".gitignore was modified despite already covering .claude/\nbefore=%q\nafter=%q", original, string(got))
	}
}

func TestSession_WritePlan_GitignoreCreatedWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	s := newSession("id", "name", &git.WorktreeInfo{Path: dir})

	if err := s.WritePlan("body"); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}

	gitignorePath := filepath.Join(dir, ".gitignore")
	got, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(got), ".claude/") {
		t.Errorf("freshly-created .gitignore missing .claude/; got=%q", string(got))
	}
}

func TestSession_WritePlan_NoTrailingNewlineInGitignore(t *testing.T) {
	// Pre-existing .gitignore without a trailing newline must get one inserted
	// before the ".claude/" line so it lands on its own line.
	dir := t.TempDir()
	s := newSession("id", "name", &git.WorktreeInfo{Path: dir})

	gitignorePath := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("node_modules"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := s.WritePlan("body"); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}

	got, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "node_modules\n.claude/") {
		t.Errorf(".gitignore should have .claude/ on its own line; got=%q", string(got))
	}
}

func TestClaudeIgnoreCovered(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"empty", "", false},
		{"unrelated", "node_modules\n.baton/\n", false},
		{"with slash", ".claude/\n", true},
		{"without slash", ".claude\n", true},
		{"surrounded", "node_modules\n.claude/\nfoo\n", true},
		{"comment matching", "# .claude/\n", false},
		{"prefix match should not trigger", ".claudefoo\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeIgnoreCovered(tc.body); got != tc.want {
				t.Errorf("claudeIgnoreCovered(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

func TestSession_HasPrevPlan_FalseInitially(t *testing.T) {
	dir := t.TempDir()
	s := newSession("id", "name", &git.WorktreeInfo{Path: dir})
	if s.HasPrevPlan() {
		t.Error("HasPrevPlan() should be false before any snapshot")
	}
}

func TestSession_RestorePrevPlan_NoSnapshotIsNoop(t *testing.T) {
	dir := t.TempDir()
	s := newSession("id", "name", &git.WorktreeInfo{Path: dir})
	prev, restored, err := s.RestorePrevPlan()
	if err != nil {
		t.Fatalf("RestorePrevPlan: %v", err)
	}
	if restored {
		t.Error("RestorePrevPlan should report restored=false when no snapshot exists")
	}
	if prev != "" {
		t.Errorf("RestorePrevPlan prev = %q, want empty", prev)
	}
}

func TestSession_SnapshotAndRestoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := newSession("id", "name", &git.WorktreeInfo{Path: dir})
	const v1 = "# Goal\nv1 plan\n\n## Tasks\n- [ ] step\n"
	if err := s.WritePlan(v1); err != nil {
		t.Fatalf("WritePlan v1: %v", err)
	}
	if err := s.snapshotPlanToPrev(); err != nil {
		t.Fatalf("snapshotPlanToPrev: %v", err)
	}
	if !s.HasPrevPlan() {
		t.Fatal("HasPrevPlan should be true after snapshot")
	}
	const v2 = "# Goal\nv2 plan\n"
	if err := s.WritePlan(v2); err != nil {
		t.Fatalf("WritePlan v2: %v", err)
	}
	prev, restored, err := s.RestorePrevPlan()
	if err != nil {
		t.Fatalf("RestorePrevPlan: %v", err)
	}
	if !restored {
		t.Error("RestorePrevPlan should report restored=true")
	}
	if prev != v1 {
		t.Errorf("RestorePrevPlan prev = %q, want %q", prev, v1)
	}
	got, err := s.ReadPlan()
	if err != nil {
		t.Fatal(err)
	}
	if got != v1 {
		t.Errorf("plan after restore = %q, want %q", got, v1)
	}
	if s.HasPrevPlan() {
		t.Error("HasPrevPlan should be false after restore (single-step undo)")
	}
}

func TestSession_SnapshotEmptyPlanIsNoop(t *testing.T) {
	dir := t.TempDir()
	s := newSession("id", "name", &git.WorktreeInfo{Path: dir})
	if err := s.snapshotPlanToPrev(); err != nil {
		t.Fatalf("snapshotPlanToPrev with no plan: %v", err)
	}
	if s.HasPrevPlan() {
		t.Error("HasPrevPlan should be false when no plan exists to snapshot")
	}
}

func TestSession_ReviseGate_RejectsWhileDrafting(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{Path: t.TempDir()})
	if !s.TryStartDraft(func() {}) {
		t.Fatal("TryStartDraft should succeed initially")
	}
	if s.TryStartRevise(func() {}) {
		t.Error("TryStartRevise should return false while drafting")
	}
}

func TestSession_ReviseGate_DoubleDispatchRejected(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{Path: t.TempDir()})
	if !s.TryStartRevise(func() {}) {
		t.Fatal("first TryStartRevise should succeed")
	}
	if s.TryStartRevise(func() {}) {
		t.Error("second TryStartRevise should return false while revising")
	}
	s.finishRevise()
	if s.IsRevising() {
		t.Error("IsRevising should be false after finishRevise")
	}
}

func TestSession_CachedPlan_EmptyForFreshSession(t *testing.T) {
	dir := t.TempDir()
	s := newSession("id", "name", &git.WorktreeInfo{Path: dir})
	plan, present := s.CachedPlan()
	if present {
		t.Errorf("CachedPlan() present=true for fresh session, want false")
	}
	if plan != "" {
		t.Errorf("CachedPlan() plan=%q for fresh session, want empty", plan)
	}
}

func TestSession_CachedPlan_PopulatedByWritePlan(t *testing.T) {
	dir := t.TempDir()
	s := newSession("id", "name", &git.WorktreeInfo{Path: dir})
	const body = "# Goal\nDo a thing\n\n## Tasks\n- [ ] one\n"
	if err := s.WritePlan(body); err != nil {
		t.Fatal(err)
	}
	plan, present := s.CachedPlan()
	if !present {
		t.Fatal("CachedPlan() present=false after WritePlan")
	}
	if plan != body {
		t.Errorf("CachedPlan() plan = %q, want %q", plan, body)
	}
}

func TestSession_CachedPlan_LazyLoadsFromDisk(t *testing.T) {
	// Resumed sessions don't go through WritePlan in the current process,
	// so the first CachedPlan() call must lazy-load from disk.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	const body = "# Goal\nresumed\n\n- [x] done\n- [ ] todo\n"
	planPath := filepath.Join(dir, ".claude", "plan.md")
	if err := os.WriteFile(planPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newSession("id", "name", &git.WorktreeInfo{Path: dir})
	plan, present := s.CachedPlan()
	if !present || plan != body {
		t.Errorf("CachedPlan() = (%q, %v), want (%q, true)", plan, present, body)
	}

	// Second call with unchanged mtime must return the cached content without
	// re-reading (cache hit). Overwrite the file content but keep the mtime
	// identical so the stat check sees no change.
	fi, err := os.Stat(planPath)
	if err != nil {
		t.Fatal(err)
	}
	origMtime := fi.ModTime()
	const bodyChanged = "# Goal\nresumed\n\n- [x] done\n- [x] todo\n"
	if err := os.WriteFile(planPath, []byte(bodyChanged), 0o644); err != nil {
		t.Fatal(err)
	}
	// Restore original mtime to simulate an unchanged file.
	if err := os.Chtimes(planPath, origMtime, origMtime); err != nil {
		t.Fatal(err)
	}
	plan2, _ := s.CachedPlan()
	if plan2 != body {
		t.Errorf("same mtime: CachedPlan() = %q, want cached %q", plan2, body)
	}

	// Now bump mtime to the future — the cache must re-read and return the new content.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(planPath, future, future); err != nil {
		t.Fatal(err)
	}
	plan3, _ := s.CachedPlan()
	if plan3 != bodyChanged {
		t.Errorf("bumped mtime: CachedPlan() = %q, want new content %q", plan3, bodyChanged)
	}
}

func TestSession_CachedPlan_RereadsOnExternalMtimeChange(t *testing.T) {
	// The build agent edits plan.md directly via Claude's Edit tool, bypassing
	// WritePlan. CachedPlan must detect the mtime change and re-read the file.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	const body1 = "# Goal\noriginal\n\n- [ ] task one\n"
	planPath := filepath.Join(dir, ".claude", "plan.md")
	if err := os.WriteFile(planPath, []byte(body1), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newSession("id", "name", &git.WorktreeInfo{Path: dir})
	plan, present := s.CachedPlan()
	if !present || plan != body1 {
		t.Fatalf("CachedPlan() = (%q, %v), want (%q, true)", plan, present, body1)
	}

	// Simulate build agent toggling a checkbox via external file write.
	const body2 = "# Goal\noriginal\n\n- [x] task one\n"
	if err := os.WriteFile(planPath, []byte(body2), 0o644); err != nil {
		t.Fatal(err)
	}
	// Bump mtime to the future so even coarse-grained filesystems detect the change.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(planPath, future, future); err != nil {
		t.Fatal(err)
	}

	plan2, _ := s.CachedPlan()
	if plan2 != body2 {
		t.Errorf("after external mtime bump, CachedPlan() = %q, want %q", plan2, body2)
	}
}

func TestSession_CachedPlan_RestorePrevPlanRefreshesCache(t *testing.T) {
	dir := t.TempDir()
	s := newSession("id", "name", &git.WorktreeInfo{Path: dir})
	const v1 = "# Goal\nv1\n"
	const v2 = "# Goal\nv2\n"
	if err := s.WritePlan(v1); err != nil {
		t.Fatal(err)
	}
	if err := s.snapshotPlanToPrev(); err != nil {
		t.Fatal(err)
	}
	if err := s.WritePlan(v2); err != nil {
		t.Fatal(err)
	}
	plan, _ := s.CachedPlan()
	if plan != v2 {
		t.Fatalf("after WritePlan(v2), cache = %q, want %q", plan, v2)
	}
	if _, _, err := s.RestorePrevPlan(); err != nil {
		t.Fatal(err)
	}
	plan, _ = s.CachedPlan()
	if plan != v1 {
		t.Errorf("after RestorePrevPlan, cache = %q, want %q", plan, v1)
	}
}

func TestParsePlanSections(t *testing.T) {
	fullPlan := `# Goal
Fix the auth redirect bug.

## Spec
1. Users are redirected correctly.
2. Tokens are validated.

## Context
internal/auth/handler.go:42

## Reuse
existing middleware helpers

## Risks
Token expiry edge case

## Tasks
- [ ] write handler test
- [ ] implement redirect

## Verification
go test -race ./internal/auth

## Not in scope
OAuth2 support`

	t.Run("full plan returns all four sections", func(t *testing.T) {
		s := ParsePlanSections(fullPlan)
		if s.Goal != "Fix the auth redirect bug." {
			t.Errorf("Goal = %q, want %q", s.Goal, "Fix the auth redirect bug.")
		}
		if !strings.Contains(s.Spec, "Users are redirected correctly.") {
			t.Errorf("Spec missing expected content, got %q", s.Spec)
		}
		if !strings.Contains(s.Verification, "go test -race ./internal/auth") {
			t.Errorf("Verification missing expected content, got %q", s.Verification)
		}
		if !strings.Contains(s.NotInScope, "OAuth2 support") {
			t.Errorf("NotInScope missing expected content, got %q", s.NotInScope)
		}
	})

	t.Run("missing Verification returns empty string", func(t *testing.T) {
		plan := "# Goal\nDo a thing.\n\n## Spec\nSome spec.\n\n## Tasks\n- [ ] task\n"
		s := ParsePlanSections(plan)
		if s.Verification != "" {
			t.Errorf("Verification = %q, want empty", s.Verification)
		}
		if s.Goal != "Do a thing." {
			t.Errorf("Goal = %q, want %q", s.Goal, "Do a thing.")
		}
	})

	t.Run("only Goal populates Goal, rest empty", func(t *testing.T) {
		plan := "# Goal\nOnly the goal here.\n"
		s := ParsePlanSections(plan)
		if s.Goal != "Only the goal here." {
			t.Errorf("Goal = %q, want %q", s.Goal, "Only the goal here.")
		}
		if s.Spec != "" {
			t.Errorf("Spec = %q, want empty", s.Spec)
		}
		if s.Verification != "" {
			t.Errorf("Verification = %q, want empty", s.Verification)
		}
		if s.NotInScope != "" {
			t.Errorf("NotInScope = %q, want empty", s.NotInScope)
		}
	})

	t.Run("heading lines excluded from body", func(t *testing.T) {
		plan := "# Goal\nGoal text.\n\n## Spec\nSpec text.\n"
		s := ParsePlanSections(plan)
		if strings.Contains(s.Goal, "# Goal") {
			t.Errorf("Goal body contains heading, got %q", s.Goal)
		}
		if strings.Contains(s.Spec, "## Spec") {
			t.Errorf("Spec body contains heading, got %q", s.Spec)
		}
	})
}

// TestSession_CleanupIsIdempotent pins M2: a second Cleanup call must not
// re-run git worktree remove (which would return a "not a worktree" error
// and surface a spurious shutdown failure). Both Shutdown's teardown loop
// and closeSession from a natural agent exit can call Cleanup on the same
// session.
func TestSession_CleanupIsIdempotent(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	sess, _, err := mgr.CreateSessionWithCommand(
		Config{Task: "test", Rows: 24, Cols: 80},
		func(name string) *exec.Cmd { return exec.Command("bash", "-c", "exit 0") },
	)
	if err != nil {
		t.Fatal(err)
	}

	// First call: real cleanup, should succeed.
	if err := sess.Cleanup(repo); err != nil {
		t.Fatalf("first Cleanup: %v", err)
	}
	worktreePath := sess.Worktree.Path
	if _, statErr := os.Stat(worktreePath); !os.IsNotExist(statErr) {
		t.Errorf("worktree %s should be removed after first Cleanup", worktreePath)
	}

	// Second call: must be a no-op returning the same (nil) error, not a
	// "not a worktree" error from a re-run git command.
	if err := sess.Cleanup(repo); err != nil {
		t.Errorf("second Cleanup must be idempotent, got: %v", err)
	}
}
