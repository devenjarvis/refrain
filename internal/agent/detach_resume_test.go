package agent

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devenjarvis/refrain/internal/config"
	"github.com/devenjarvis/refrain/internal/state"
)

func TestDetachSnapshotsState(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 60")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Set a session ID on the agent to simulate polling.
	ag.SetClaudeSessionID("test-session-uuid")
	ag.SetDisplayName("my-task")
	ag.SetClaudeName(true)

	wtPath := sess.Worktree.Path
	sessID := sess.ID

	bs := mgr.Detach()
	if bs == nil {
		t.Fatal("expected non-nil RefrainState from Detach")
	}

	// State should have the session.
	if len(bs.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(bs.Sessions))
	}

	ss := bs.Sessions[0]
	if ss.ID != sessID {
		t.Errorf("expected session ID %s, got %s", sessID, ss.ID)
	}
	if ss.WorktreePath != wtPath {
		t.Errorf("expected worktree path %s, got %s", wtPath, ss.WorktreePath)
	}

	// Agent state should be captured.
	if len(ss.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(ss.Agents))
	}
	as := ss.Agents[0]
	if as.ClaudeSessionID != "test-session-uuid" {
		t.Errorf("expected session ID 'test-session-uuid', got %q", as.ClaudeSessionID)
	}
	if as.DisplayName != "my-task" {
		t.Errorf("expected display name 'my-task', got %q", as.DisplayName)
	}

	// Worktree should still exist (not cleaned up).
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		t.Error("worktree should still exist after detach")
	}

	// Manager should have no sessions after detach.
	if mgr.AgentCount() != 0 {
		t.Errorf("expected 0 agents after detach, got %d", mgr.AgentCount())
	}
}

func TestDetachEmptyReturnsNil(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())

	bs := mgr.Detach()
	if bs != nil {
		t.Errorf("expected nil RefrainState for empty manager, got %+v", bs)
	}
}

func TestDetachSkipsCompleteSessionsAndCleansWorktree(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sessComplete, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 60")
	})
	if err != nil {
		t.Fatal(err)
	}
	sessKeep, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 60")
	})
	if err != nil {
		t.Fatal(err)
	}

	completePath := sessComplete.Worktree.Path
	keepPath := sessKeep.Worktree.Path
	keepID := sessKeep.ID

	sessComplete.SetLifecyclePhase(LifecycleComplete)

	bs := mgr.Detach()
	if bs == nil {
		t.Fatal("expected non-nil RefrainState when one non-complete session remains")
	}
	if len(bs.Sessions) != 1 {
		t.Fatalf("expected 1 session in snapshot, got %d", len(bs.Sessions))
	}
	if bs.Sessions[0].ID != keepID {
		t.Errorf("expected snapshotted session ID %s, got %s", keepID, bs.Sessions[0].ID)
	}

	// Complete session's worktree must be removed.
	if _, err := os.Stat(completePath); !os.IsNotExist(err) {
		t.Errorf("complete session worktree should have been removed, but Stat returned: %v", err)
	}

	// Non-complete session's worktree must be preserved.
	if _, err := os.Stat(keepPath); os.IsNotExist(err) {
		t.Error("non-complete session worktree should still exist after detach")
	}
}

func TestDetachAllCompleteReturnsNil(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 60")
	})
	if err != nil {
		t.Fatal(err)
	}
	sess.SetLifecyclePhase(LifecycleComplete)

	bs := mgr.Detach()
	if bs != nil {
		t.Errorf("expected nil RefrainState when all sessions are complete, got %+v", bs)
	}
}

func TestDetachSaveLoadRoundTrip(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 60")
	})
	if err != nil {
		t.Fatal(err)
	}

	ag.SetClaudeSessionID("uuid-abc")

	_ = sess // used implicitly

	bs := mgr.Detach()
	if bs == nil {
		t.Fatal("expected non-nil RefrainState")
	}

	// Save to disk.
	if err := state.Save(repo, bs); err != nil {
		t.Fatal(err)
	}

	// Load from disk.
	loaded, err := state.Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil {
		t.Fatal("expected loaded state, got nil")
	}
	if len(loaded.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(loaded.Sessions))
	}
	if loaded.Sessions[0].Agents[0].ClaudeSessionID != "uuid-abc" {
		t.Errorf("expected session ID 'uuid-abc', got %q", loaded.Sessions[0].Agents[0].ClaudeSessionID)
	}
}

func TestResumeSessionCreatesAgentWithResume(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not in PATH")
	}
	repo := setupTestRepo(t)

	// First manager: create a session and detach.
	mgr1 := NewManager(repo, defaultTestSettings())
	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr1.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 60")
	})
	if err != nil {
		t.Fatal(err)
	}

	ag.SetClaudeSessionID("resume-uuid-123")
	ag.SetDisplayName("my-agent")
	ag.SetClaudeName(true)

	sessID := sess.ID
	sessName := sess.Name
	wtPath := sess.Worktree.Path
	branch := sess.Worktree.Branch
	baseBranch := sess.Worktree.BaseBranch

	bs := mgr1.Detach()
	if bs == nil {
		t.Fatal("expected non-nil RefrainState")
	}

	// Second manager: resume from saved state.
	mgr2 := NewManager(repo, defaultTestSettings())
	defer mgr2.Shutdown()

	resumeCfg := Config{Rows: 24, Cols: 80}
	if err := mgr2.ResumeSession(bs.Sessions[0], resumeCfg); err != nil {
		t.Fatal(err)
	}

	// Verify session was recreated.
	resumedSess := mgr2.GetSession(sessID)
	if resumedSess == nil {
		t.Fatal("resumed session not found")
	}
	if resumedSess.Name != sessName {
		t.Errorf("expected session name %q, got %q", sessName, resumedSess.Name)
	}
	if resumedSess.Worktree.Path != wtPath {
		t.Errorf("expected worktree path %q, got %q", wtPath, resumedSess.Worktree.Path)
	}
	if resumedSess.Worktree.Branch != branch {
		t.Errorf("expected branch %q, got %q", branch, resumedSess.Worktree.Branch)
	}
	if resumedSess.Worktree.BaseBranch != baseBranch {
		t.Errorf("expected base branch %q, got %q", baseBranch, resumedSess.Worktree.BaseBranch)
	}

	// Verify agent was created.
	agents := resumedSess.Agents()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].GetDisplayName() != "my-agent" {
		t.Errorf("expected display name 'my-agent', got %q", agents[0].GetDisplayName())
	}
	if agents[0].ClaudeSessionID() != "resume-uuid-123" {
		t.Errorf("expected claude session ID 'resume-uuid-123', got %q", agents[0].ClaudeSessionID())
	}
}

func TestResumeSessionMissingWorktreeReturnsError(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	ss := state.SessionState{
		ID:           "session-99",
		Name:         "nonexistent",
		WorktreePath: "/tmp/does-not-exist-" + time.Now().Format("20060102150405"),
		Branch:       "refrain/nonexistent",
		BaseBranch:   "main",
		Agents: []state.AgentState{
			{ID: "session-99-agent-1", Name: "test"},
		},
	}

	err := mgr.ResumeSession(ss, Config{Rows: 24, Cols: 80})
	if err == nil {
		t.Fatal("expected error for missing worktree")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %s", err)
	}
}

func TestResumeNextIDNoCollision(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not in PATH")
	}
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	// Create a session and detach from a first manager to get the worktree.
	mgr1 := NewManager(repo, defaultTestSettings())
	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, _, err := mgr1.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 60")
	})
	if err != nil {
		t.Fatal(err)
	}
	wtPath := sess.Worktree.Path
	mgr1.Detach()

	// Resume with a high session ID.
	ss := state.SessionState{
		ID:           "session-50",
		Name:         sess.Name,
		WorktreePath: wtPath,
		Branch:       sess.Worktree.Branch,
		BaseBranch:   sess.Worktree.BaseBranch,
		Agents: []state.AgentState{
			{ID: "session-50-agent-1", Name: "test"},
		},
	}

	if err := mgr.ResumeSession(ss, Config{Rows: 24, Cols: 80}); err != nil {
		t.Fatal(err)
	}

	// Create a new session — its ID should be > 50.
	newSess, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 60")
	})
	if err != nil {
		t.Fatal(err)
	}

	if newSess.ID == "session-50" {
		t.Error("new session ID should not collide with resumed session")
	}
	// The nextID should have been bumped past 50, so the new session
	// should have an ID > 50.
	num := parseSessionNum(newSess.ID)
	if num <= 50 {
		t.Errorf("expected new session ID > 50, got %s (num=%d)", newSess.ID, num)
	}
}

func TestBuildResumeArgs(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		sessID   string
		wantArgs []string
	}{
		{
			name:     "with session ID and bypass",
			cfg:      Config{BypassPermissions: true, Task: "do stuff"},
			sessID:   "uuid-123",
			wantArgs: []string{"--dangerously-skip-permissions", "--resume", "uuid-123", "do stuff"},
		},
		{
			name:     "empty session ID falls back to continue",
			cfg:      Config{BypassPermissions: true},
			sessID:   "",
			wantArgs: []string{"--dangerously-skip-permissions", "--continue"},
		},
		{
			name:     "no bypass no task",
			cfg:      Config{},
			sessID:   "uuid-456",
			wantArgs: []string{"--resume", "uuid-456"},
		},
		{
			name:     "continue with task",
			cfg:      Config{Task: "hello"},
			sessID:   "",
			wantArgs: []string{"--continue", "hello"},
		},
		{
			name:     "agent model prepended for claude",
			cfg:      Config{AgentModel: "claude-opus-4-8", BypassPermissions: true, Task: "do stuff"},
			sessID:   "uuid-123",
			wantArgs: []string{"--model", "claude-opus-4-8", "--dangerously-skip-permissions", "--resume", "uuid-123", "do stuff"},
		},
		{
			name:     "agent model ignored for non-claude program",
			cfg:      Config{AgentProgram: "bash", AgentModel: "claude-opus-4-8", BypassPermissions: true},
			sessID:   "uuid-456",
			wantArgs: []string{"--dangerously-skip-permissions", "--resume", "uuid-456"},
		},
		{
			name:     "empty agent model passes nothing",
			cfg:      Config{AgentModel: "", Task: "hello"},
			sessID:   "",
			wantArgs: []string{"--continue", "hello"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildResumeArgs(tt.cfg, tt.sessID)
			if len(got) != len(tt.wantArgs) {
				t.Fatalf("expected %d args, got %d: %v", len(tt.wantArgs), len(got), got)
			}
			for i, want := range tt.wantArgs {
				if got[i] != want {
					t.Errorf("arg[%d]: expected %q, got %q", i, want, got[i])
				}
			}
		})
	}
}

func TestForceQuitCleansEverything(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 60")
	})
	if err != nil {
		t.Fatal(err)
	}

	ag.SetClaudeSessionID("uuid-force")
	wtPath := sess.Worktree.Path

	// Simulate force quit: Shutdown + Remove state.
	mgr.Shutdown()
	_ = state.Remove(repo)

	// Worktree should be gone (Shutdown calls Cleanup).
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Error("worktree should be removed after Shutdown")
	}

	// State file should not exist.
	loaded, err := state.Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != nil {
		t.Error("expected no state file after force quit")
	}
}

func TestDetachResumePreservesOwnsBranch(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not in PATH")
	}
	repo := setupTestRepo(t)

	// Create a branch to attach to.
	cmd := exec.Command("git", "branch", "feature/test-detach")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("creating branch: %v\n%s", err, out)
	}

	mgr1 := NewManager(repo, defaultTestSettings())
	cfg := Config{Task: "test", Rows: 24, Cols: 80}

	// Create an attached session (ownsBranch=false).
	sess, _, err := mgr1.CreateSessionOnBranchWithCommand("feature/test-detach", "main", cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 60")
	})
	if err != nil {
		t.Fatal(err)
	}
	wtPath := sess.Worktree.Path

	bs := mgr1.Detach()
	if bs == nil {
		t.Fatal("expected non-nil RefrainState")
	}

	// OwnsBranch should be false in saved state.
	if bs.Sessions[0].OwnsBranch {
		t.Error("expected OwnsBranch=false for attached session")
	}

	// Resume and verify.
	mgr2 := NewManager(repo, defaultTestSettings())
	defer mgr2.Shutdown()

	if err := mgr2.ResumeSession(bs.Sessions[0], Config{Rows: 24, Cols: 80}); err != nil {
		t.Fatal(err)
	}

	resumedSess := mgr2.GetSession(sess.ID)
	if resumedSess == nil {
		t.Fatal("resumed session not found")
	}
	if resumedSess.Worktree.Path != wtPath {
		t.Errorf("expected worktree path %s, got %s", wtPath, resumedSess.Worktree.Path)
	}

	// Kill the resumed session — branch should be preserved (ownsBranch=false).
	if err := mgr2.KillSession(sess.ID); err != nil {
		t.Fatal(err)
	}

	branchCmd := exec.Command("git", "branch", "--list", "feature/test-detach")
	branchCmd.Dir = repo
	out, err := branchCmd.Output()
	if err != nil {
		t.Fatalf("git branch --list: %v", err)
	}
	if !strings.Contains(string(out), "feature/test-detach") {
		t.Error("branch should be preserved after killing resumed attached session")
	}
}

func TestDetachResumePreservesHasClaudeName(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not in PATH")
	}
	repo := setupTestRepo(t)

	mgr1 := NewManager(repo, defaultTestSettings())
	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, _, err := mgr1.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 60")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Rename the branch before detaching.
	if _, err := sess.RenameBranch(repo, "refrain/add-feature"); err != nil {
		t.Fatal(err)
	}
	if !sess.HasClaudeName() {
		t.Fatal("expected HasClaudeName true after rename")
	}

	sessID := sess.ID
	bs := mgr1.Detach()
	if bs == nil {
		t.Fatal("expected non-nil RefrainState")
	}

	if !bs.Sessions[0].HasClaudeName {
		t.Error("detached state should record HasClaudeName=true")
	}
	if bs.Sessions[0].Branch != "refrain/add-feature" {
		t.Errorf("detached state branch = %q, want refrain/add-feature", bs.Sessions[0].Branch)
	}

	// Resume.
	mgr2 := NewManager(repo, defaultTestSettings())
	defer mgr2.Shutdown()

	if err := mgr2.ResumeSession(bs.Sessions[0], Config{Rows: 24, Cols: 80}); err != nil {
		t.Fatal(err)
	}

	resumed := mgr2.GetSession(sessID)
	if resumed == nil {
		t.Fatal("resumed session not found")
	}
	if !resumed.HasClaudeName() {
		t.Error("resumed session should have HasClaudeName=true")
	}
	if resumed.Worktree.Branch != "refrain/add-feature" {
		t.Errorf("resumed branch = %q, want refrain/add-feature", resumed.Worktree.Branch)
	}

	// A subsequent UserPromptSubmit must NOT trigger another rename.
	prevBranch := resumed.Worktree.Branch
	agents := resumed.Agents()
	if len(agents) == 0 {
		t.Fatal("resumed session has no agents")
	}
	ag := agents[0]
	select {
	case <-mgr2.Events():
	case <-time.After(time.Second):
		// Drain is best-effort; resume may not emit if the agent immediately fails.
	}

	if mgr2.HookSocketPath() != "" {
		_ = mgr2.HookSocketPath()
	}
	// Directly invoke the rename path since the Claude binary may not be
	// genuinely running — the check is purely about state.
	mgr2.maybeRenameFromPrompt(resumed, ag, "now change the layout")
	if resumed.Worktree.Branch != prevBranch {
		t.Errorf("resumed session should not rename again; got %q, want %q",
			resumed.Worktree.Branch, prevBranch)
	}
	_ = ag
}

// TestResumeSessionPreservesWorktreeOnAgentFailure verifies that when
// AddAgentResumed fails, ResumeSession:
//   - removes the session from the manager,
//   - emits an EventError so the TUI can surface the failure, and
//   - does NOT delete the on-disk worktree (which may contain user work).
func TestResumeSessionPreservesWorktreeOnAgentFailure(t *testing.T) {
	repo := setupTestRepo(t)

	// First manager: create a real worktree to resume against.
	mgr1 := NewManager(repo, defaultTestSettings())
	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, _, err := mgr1.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 60")
	})
	if err != nil {
		t.Fatal(err)
	}
	wtPath := sess.Worktree.Path
	bs := mgr1.Detach()
	if bs == nil {
		t.Fatal("expected non-nil RefrainState")
	}

	// Second manager: settings point AgentProgram at a missing binary so
	// PTY.Start in newResumedAgent fails deterministically.
	settings := config.Resolve(nil, nil)
	settings.AgentProgram = "/definitely/not/a/real/binary/refrain-test-missing"
	mgr2 := NewManager(repo, settings)
	defer mgr2.Shutdown()

	// Drain events in the background so emit() doesn't block on the buffered
	// channel, and capture EventError.
	var errEvents []Event
	var evMu sync.Mutex
	doneCh := make(chan struct{})
	go func() {
		for ev := range mgr2.Events() {
			if ev.Type == EventError {
				evMu.Lock()
				errEvents = append(errEvents, ev)
				evMu.Unlock()
			}
		}
		close(doneCh)
	}()

	err = mgr2.ResumeSession(bs.Sessions[0], Config{Rows: 24, Cols: 80})
	if err == nil {
		t.Fatal("expected ResumeSession to fail when AgentProgram is missing")
	}

	// Session must be gone from the manager.
	if got := mgr2.GetSession(bs.Sessions[0].ID); got != nil {
		t.Errorf("expected session to be removed after resume failure, found %+v", got)
	}

	// Worktree on disk must be preserved (user data may be there).
	if _, err := os.Stat(wtPath); err != nil {
		t.Errorf("worktree should still exist after resume failure: %v", err)
	}

	// EventError should have been emitted. Give the drain goroutine a tick.
	time.Sleep(50 * time.Millisecond)
	evMu.Lock()
	gotErr := len(errEvents) > 0
	evMu.Unlock()
	if !gotErr {
		t.Errorf("expected EventError emit on resume failure, got none")
	}
}

// TestCreateSessionConcurrentNameUniqueness verifies that concurrent
// CreateSession calls never produce two sessions with the same slug. Without
// the pendingNames reservation, two goroutines that both pass the existing-
// name check before either has inserted into m.sessions can collide on the
// same slug. Some attempts may fail with git contention errors (the on-disk
// index is a single locked resource); we assert only that *successful*
// creations have unique names.
func TestCreateSessionConcurrentNameUniqueness(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	const N = 4
	var wg sync.WaitGroup
	wg.Add(N)
	results := make(chan string, N)

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	for range N {
		go func() {
			defer wg.Done()
			sess, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
				return exec.Command("bash", "-c", "sleep 30")
			})
			if err != nil {
				// Git contention is environmental — log but don't fail.
				t.Logf("CreateSession returned error (likely git contention): %v", err)
				return
			}
			results <- sess.Name
		}()
	}
	wg.Wait()
	close(results)

	seen := make(map[string]int)
	for name := range results {
		seen[name]++
	}
	if len(seen) < 2 {
		t.Skipf("got fewer than 2 successful creates; cannot exercise uniqueness invariant (counts: %+v)", seen)
	}
	for name, count := range seen {
		if count > 1 {
			t.Errorf("name %q reused %d times — pendingNames reservation is broken", name, count)
		}
	}
}

func TestParseSessionNum(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"session-1", 1},
		{"session-50", 50},
		{"session-0", 0},
		{"invalid", 0},
		{"", 0},
	}
	for _, tt := range tests {
		got := parseSessionNum(tt.input)
		if got != tt.want {
			t.Errorf("parseSessionNum(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

// TestDetach_CalledTwiceDoesNotPanic pins the idempotency of Detach: calling
// it twice must not panic on close-of-closed-channel for m.done or m.events.
// Before the shared shutdownOnce gate, the second call panicked.
func TestDetach_CalledTwiceDoesNotPanic(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())

	if got := mgr.Detach(); got != nil {
		t.Fatalf("first Detach on empty manager: want nil, got %+v", got)
	}
	if got := mgr.Detach(); got != nil {
		t.Errorf("second Detach: want nil (no-op), got %+v", got)
	}
}

// TestDetach_ThenShutdownDoesNotPanic pins the production teardown order: the
// TUI snapshots state via Detach() and then a deferred mgr.Shutdown() fires.
// The second call must be a no-op rather than panic on close-of-closed-channel.
func TestDetach_ThenShutdownDoesNotPanic(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	_, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 60")
	})
	if err != nil {
		t.Fatalf("CreateSessionWithCommand: %v", err)
	}

	bs := mgr.Detach()
	if bs == nil {
		t.Fatal("Detach returned nil; expected snapshot with one session")
	}
	if len(bs.Sessions) != 1 {
		t.Fatalf("Detach snapshot: want 1 session, got %d", len(bs.Sessions))
	}

	// Must not panic. Shutdown after Detach is the documented production
	// pattern (defer mgr.Shutdown() paired with an explicit Detach on quit).
	mgr.Shutdown()
}

// TestShutdown_ThenDetachReturnsNil pins the reverse order: if Shutdown has
// already torn down the manager, a subsequent Detach must return nil rather
// than panic. The shared shutdownOnce gate makes this a no-op.
func TestShutdown_ThenDetachReturnsNil(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())

	mgr.Shutdown()

	if got := mgr.Detach(); got != nil {
		t.Errorf("Detach after Shutdown: want nil, got %+v", got)
	}
}
