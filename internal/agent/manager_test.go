package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubPlanDrafter is a configurable PlanDrafter for tests. Each call records
// the request and either returns the configured response or invokes the
// configured callback for finer control (e.g. blocking until a channel
// closes, observing context cancellation).
type stubPlanDrafter struct {
	mu       sync.Mutex
	drafted  []DraftRequest
	revised  []ReviseRequest
	draftFn  func(ctx context.Context, req DraftRequest) (string, error)
	reviseFn func(ctx context.Context, req ReviseRequest) (string, error)
}

func (s *stubPlanDrafter) Draft(ctx context.Context, req DraftRequest) (string, error) {
	s.mu.Lock()
	s.drafted = append(s.drafted, req)
	fn := s.draftFn
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return "", errors.New("draftFn not configured")
}

func (s *stubPlanDrafter) Revise(ctx context.Context, req ReviseRequest) (string, error) {
	s.mu.Lock()
	s.revised = append(s.revised, req)
	fn := s.reviseFn
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return "", errors.New("reviseFn not configured")
}

func (s *stubPlanDrafter) DraftCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.drafted)
}

// waitForCondition polls cond until it returns true or timeout elapses.
// Fails the test on timeout. Used to await drafting goroutines without
// hard-coded sleeps.
func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not satisfied within %v", timeout)
}

func TestManager_StartDraft_Success(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	const planMD = "# Goal\nDo X\n\n## Tasks\n- [ ] step\n"
	mgr.SetPlanDrafter(&stubPlanDrafter{
		draftFn: func(ctx context.Context, req DraftRequest) (string, error) {
			return planMD, nil
		},
	})

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := mgr.StartDraft(sess.ID, "add dark mode"); err != nil {
		t.Fatalf("StartDraft: %v", err)
	}

	waitForCondition(t, 2*time.Second, func() bool {
		return !sess.IsDrafting() && sess.LifecyclePhase() == LifecyclePlanning && sess.HasPlan()
	})

	if got := sess.LifecyclePhase(); got != LifecyclePlanning {
		t.Errorf("phase after success = %v, want LifecyclePlanning", got)
	}
	body, err := sess.ReadPlan()
	if err != nil {
		t.Fatal(err)
	}
	if body != planMD {
		t.Errorf("plan = %q, want %q", body, planMD)
	}
	if sess.DraftError() != nil {
		t.Errorf("DraftError = %v, want nil after success", sess.DraftError())
	}
	if sess.OriginalPrompt() != "add dark mode" {
		t.Errorf("OriginalPrompt = %q, want %q", sess.OriginalPrompt(), "add dark mode")
	}
}

func TestManager_StartDraft_TransitionsToDraftingImmediately(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	block := make(chan struct{})
	defer close(block)
	mgr.SetPlanDrafter(&stubPlanDrafter{
		draftFn: func(ctx context.Context, req DraftRequest) (string, error) {
			<-block
			return "# Goal\nx", nil
		},
	})

	sess, _, err := mgr.CreateSessionWithCommand(
		Config{Task: "test", Rows: 24, Cols: 80},
		func(name string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 5") },
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := mgr.StartDraft(sess.ID, "add thing"); err != nil {
		t.Fatal(err)
	}

	// Drafting is set synchronously by StartDraft before returning.
	if got := sess.LifecyclePhase(); got != LifecycleDrafting {
		t.Errorf("phase after StartDraft = %v, want LifecycleDrafting", got)
	}
	if !sess.IsDrafting() {
		t.Error("IsDrafting() should be true while subprocess runs")
	}
}

func TestManager_StartDraft_DrafterErrorLandsInPlanning(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	wantErr := errors.New("sonnet boom")
	mgr.SetPlanDrafter(&stubPlanDrafter{
		draftFn: func(ctx context.Context, req DraftRequest) (string, error) {
			return "", wantErr
		},
	})

	sess, _, _ := mgr.CreateSessionWithCommand(
		Config{Task: "test", Rows: 24, Cols: 80},
		func(name string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 5") },
	)

	if err := mgr.StartDraft(sess.ID, "add thing"); err != nil {
		t.Fatal(err)
	}

	waitForCondition(t, 2*time.Second, func() bool { return !sess.IsDrafting() })

	if got := sess.LifecyclePhase(); got != LifecyclePlanning {
		t.Errorf("phase after error = %v, want LifecyclePlanning", got)
	}
	if sess.HasPlan() {
		t.Error("plan file should not exist after drafter error")
	}
	if !errors.Is(sess.DraftError(), wantErr) {
		t.Errorf("DraftError = %v, want errors.Is(err, %v)", sess.DraftError(), wantErr)
	}
}

func TestManager_StartDraft_EmptyPlanLandsInPlanningWithError(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	mgr.SetPlanDrafter(&stubPlanDrafter{
		draftFn: func(ctx context.Context, req DraftRequest) (string, error) {
			return "   ", nil // whitespace-only counts as empty
		},
	})

	sess, _, _ := mgr.CreateSessionWithCommand(
		Config{Task: "test", Rows: 24, Cols: 80},
		func(name string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 5") },
	)

	if err := mgr.StartDraft(sess.ID, "add thing"); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, 2*time.Second, func() bool { return !sess.IsDrafting() })

	if got := sess.LifecyclePhase(); got != LifecyclePlanning {
		t.Errorf("phase after empty plan = %v, want LifecyclePlanning", got)
	}
	if sess.HasPlan() {
		t.Error("plan file should not exist when planner returned empty body")
	}
	if sess.DraftError() == nil {
		t.Error("DraftError should be non-nil for empty plan")
	}
}

func TestManager_StartDraft_DoubleDispatchReturnsErrDraftInFlight(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	block := make(chan struct{})
	mgr.SetPlanDrafter(&stubPlanDrafter{
		draftFn: func(ctx context.Context, req DraftRequest) (string, error) {
			<-block
			return "# Goal\nx", nil
		},
	})

	sess, _, _ := mgr.CreateSessionWithCommand(
		Config{Task: "test", Rows: 24, Cols: 80},
		func(name string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 5") },
	)

	if err := mgr.StartDraft(sess.ID, "first"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.StartDraft(sess.ID, "second"); !errors.Is(err, ErrDraftInFlight) {
		t.Errorf("second StartDraft err = %v, want ErrDraftInFlight", err)
	}

	close(block)
	waitForCondition(t, 2*time.Second, func() bool { return !sess.IsDrafting() })
}

func TestManager_StartDraft_KillSessionCancelsSubprocess(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cancelled := make(chan struct{})
	mgr.SetPlanDrafter(&stubPlanDrafter{
		draftFn: func(ctx context.Context, req DraftRequest) (string, error) {
			<-ctx.Done()
			close(cancelled)
			return "", ctx.Err()
		},
	})

	sess, _, _ := mgr.CreateSessionWithCommand(
		Config{Task: "test", Rows: 24, Cols: 80},
		func(name string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 5") },
	)

	if err := mgr.StartDraft(sess.ID, "x"); err != nil {
		t.Fatal(err)
	}

	// Wait until the goroutine has actually entered Draft (so the cancel
	// signal has a subprocess to interrupt). One tick of the manager event
	// pump is enough to ensure StartDraft fully ran.
	waitForCondition(t, time.Second, func() bool { return sess.IsDrafting() })

	if err := mgr.KillSession(sess.ID); err != nil {
		t.Fatal(err)
	}

	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("draft was not cancelled within 2s of KillSession")
	}
}

func TestManager_StartDraft_ShutdownDrainsGoroutine(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())

	finished := atomic.Bool{}
	mgr.SetPlanDrafter(&stubPlanDrafter{
		draftFn: func(ctx context.Context, req DraftRequest) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	})

	sess, _, _ := mgr.CreateSessionWithCommand(
		Config{Task: "test", Rows: 24, Cols: 80},
		func(name string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 5") },
	)

	if err := mgr.StartDraft(sess.ID, "x"); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, time.Second, func() bool { return sess.IsDrafting() })

	go func() {
		mgr.Shutdown()
		finished.Store(true)
	}()

	// Shutdown must complete in bounded time even with a draft in flight.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if finished.Load() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Shutdown did not return within 3s while a draft was in flight")
}

func TestManager_StartDraft_NoDrafterConfigured(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	mgr.SetPlanDrafter(nil)

	sess, _, _ := mgr.CreateSessionWithCommand(
		Config{Task: "test", Rows: 24, Cols: 80},
		func(name string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 5") },
	)

	err := mgr.StartDraft(sess.ID, "add dark mode")
	if !errors.Is(err, ErrPlanDrafterNotConfigured) {
		t.Errorf("err = %v, want ErrPlanDrafterNotConfigured", err)
	}
}

func TestManager_StartDraft_UnknownSession(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	if err := mgr.StartDraft("not-a-session", "x"); err == nil {
		t.Fatal("expected error for unknown session")
	}
}

func TestManager_StartDraft_NonActionablePromptRejected(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	sess, _, _ := mgr.CreateSessionWithCommand(
		Config{Task: "test", Rows: 24, Cols: 80},
		func(name string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 5") },
	)

	for _, prompt := range []string{"", "   ", "/clear"} {
		if err := mgr.StartDraft(sess.ID, prompt); err == nil {
			t.Errorf("StartDraft(%q) should error", prompt)
		}
	}
	if sess.IsDrafting() {
		t.Error("IsDrafting should remain false after rejected prompts")
	}
}

// TestManager_StartDraft_GuardSkipsWritePlanAfterKillSession exercises the
// stillOpen guard in runDraft. The stub ignores context cancellation and
// returns a non-empty plan only after the test releases it — by which point
// KillSession has already removed the session from m.sessions. With the
// guard in place, WritePlan must not run, so no plan.md should land in the
// (now-removed) worktree path. Without the guard, WritePlan would race
// directory removal and the test would either flake or leave a stray file.
func TestManager_StartDraft_GuardSkipsWritePlanAfterKillSession(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	release := make(chan struct{})
	mgr.SetPlanDrafter(&stubPlanDrafter{
		draftFn: func(ctx context.Context, req DraftRequest) (string, error) {
			<-release
			return "# Goal\nshould-not-land", nil
		},
	})

	sess, _, err := mgr.CreateSessionWithCommand(
		Config{Task: "test", Rows: 24, Cols: 80},
		func(name string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 5") },
	)
	if err != nil {
		t.Fatal(err)
	}
	sessID := sess.ID
	worktreePath := sess.Worktree.Path

	if err := mgr.StartDraft(sessID, "x"); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, time.Second, func() bool { return sess.IsDrafting() })

	// KillSession is synchronous: by the time it returns, the session has
	// been removed from m.sessions. Releasing the stub afterward makes the
	// runDraft post-Draft re-check observe stillOpen=false deterministically.
	if err := mgr.KillSession(sessID); err != nil {
		t.Fatal(err)
	}
	close(release)

	waitForCondition(t, 2*time.Second, func() bool { return !sess.IsDrafting() })

	planPath := filepath.Join(worktreePath, ".claude", "plan.md")
	if _, err := os.Stat(planPath); !os.IsNotExist(err) {
		t.Errorf("plan file should not exist after stillOpen guard short-circuited; stat err=%v", err)
	}
}

func TestManager_StartDraft_DoesNotCountTowardAgentCount(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	mgr.SetPlanDrafter(&stubPlanDrafter{
		draftFn: func(ctx context.Context, req DraftRequest) (string, error) {
			return "# Goal\nx", nil
		},
	})

	sess, _, _ := mgr.CreateSessionWithCommand(
		Config{Task: "test", Rows: 24, Cols: 80},
		func(name string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 5") },
	)

	before := mgr.AgentCount()

	if err := mgr.StartDraft(sess.ID, "x"); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, 2*time.Second, func() bool { return !sess.IsDrafting() })

	if got := mgr.AgentCount(); got != before {
		t.Errorf("AgentCount changed across StartDraft: before=%d after=%d (drafting must not count as an agent)", before, got)
	}
}
