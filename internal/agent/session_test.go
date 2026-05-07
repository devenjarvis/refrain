package agent

import (
	"fmt"
	"os"
	"os/exec"
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

func TestNaturalExitAutoClosesSession(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "echo done")
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = mgr.AddAgentWithCommand(sess.ID, cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "echo done")
	})
	if err != nil {
		t.Fatal(err)
	}

	wtPath := sess.Worktree.Path
	sessID := sess.ID

	// Wait for both agents to exit naturally and session to auto-close.
	deadline := time.After(5 * time.Second)
	gotSessionClosed := false
	for !gotSessionClosed {
		select {
		case e := <-mgr.Events():
			if e.Type == EventSessionClosed && e.SessionID == sessID {
				gotSessionClosed = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for EventSessionClosed")
		}
	}

	if mgr.GetSession(sessID) != nil {
		t.Error("session should be removed after all agents exit naturally")
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Error("worktree should be removed after session auto-close")
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

func TestSession_LifecyclePhase_DefaultIsInProgress(t *testing.T) {
	s := newSession("id", "name", &git.WorktreeInfo{})
	if got := s.LifecyclePhase(); got != LifecycleInProgress {
		t.Errorf("default LifecyclePhase = %v, want LifecycleInProgress", got)
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
