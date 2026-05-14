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

	"github.com/devenjarvis/baton/internal/planner"
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

func TestManager_RevisePlan_Success(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	const v1 = "# Goal\nv1\n\n## Tasks\n- [ ] one\n"
	const v2 = "# Goal\nv2 (revised)\n\n## Tasks\n- [ ] one\n- [ ] two\n"
	stub := &stubPlanDrafter{
		reviseFn: func(ctx context.Context, req ReviseRequest) (string, error) {
			if req.CurrentPlan != v1 {
				return "", errors.New("unexpected current plan")
			}
			if req.Critique != "add a task" {
				return "", errors.New("unexpected critique")
			}
			return v2, nil
		},
	}
	mgr.SetPlanDrafter(stub)

	sess, _, err := mgr.CreateSessionWithCommand(
		Config{Task: "t", Rows: 24, Cols: 80},
		func(name string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 5") },
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.WritePlan(v1); err != nil {
		t.Fatal(err)
	}

	if err := mgr.RevisePlan(sess.ID, "add a task"); err != nil {
		t.Fatalf("RevisePlan: %v", err)
	}
	waitForCondition(t, 2*time.Second, func() bool { return !sess.IsRevising() })

	got, err := sess.ReadPlan()
	if err != nil {
		t.Fatal(err)
	}
	if got != v2 {
		t.Errorf("plan after revise = %q, want %q", got, v2)
	}
	if sess.ReviseError() != nil {
		t.Errorf("ReviseError = %v, want nil", sess.ReviseError())
	}
	if !sess.HasPrevPlan() {
		t.Error("HasPrevPlan should be true after a successful revise")
	}
	prev, restored, err := sess.RestorePrevPlan()
	if err != nil {
		t.Fatal(err)
	}
	if !restored {
		t.Fatal("RestorePrevPlan should restore after a revise")
	}
	if prev != v1 {
		t.Errorf("snapshot = %q, want %q (pre-revise content)", prev, v1)
	}
}

func TestManager_RevisePlan_DrafterErrorLeavesPlanUntouched(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	const v1 = "# Goal\nkeep me\n\n## Tasks\n- [ ] x\n"
	mgr.SetPlanDrafter(&stubPlanDrafter{
		reviseFn: func(ctx context.Context, req ReviseRequest) (string, error) {
			return "", errors.New("boom")
		},
	})

	sess, _, _ := mgr.CreateSessionWithCommand(
		Config{Task: "t", Rows: 24, Cols: 80},
		func(name string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 5") },
	)
	if err := sess.WritePlan(v1); err != nil {
		t.Fatal(err)
	}

	if err := mgr.RevisePlan(sess.ID, "improve"); err != nil {
		t.Fatalf("RevisePlan: %v", err)
	}
	waitForCondition(t, 2*time.Second, func() bool { return !sess.IsRevising() })

	got, _ := sess.ReadPlan()
	if got != v1 {
		t.Errorf("plan after failed revise = %q, want unchanged %q", got, v1)
	}
	if sess.ReviseError() == nil {
		t.Error("ReviseError should be set after drafter failure")
	}
	// Snapshot is still around so the user can `u` to recover even after a
	// failed revise — we don't drop it on error.
	if !sess.HasPrevPlan() {
		t.Error("HasPrevPlan should remain true after failed revise (snapshot preserved)")
	}
}

func TestManager_RevisePlan_EmptyOutputTreatedAsError(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	mgr.SetPlanDrafter(&stubPlanDrafter{
		reviseFn: func(ctx context.Context, req ReviseRequest) (string, error) {
			return "   \n", nil
		},
	})

	sess, _, _ := mgr.CreateSessionWithCommand(
		Config{Task: "t", Rows: 24, Cols: 80},
		func(name string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 5") },
	)
	if err := sess.WritePlan("# Goal\nx\n"); err != nil {
		t.Fatal(err)
	}

	if err := mgr.RevisePlan(sess.ID, "x"); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, 2*time.Second, func() bool { return !sess.IsRevising() })

	if sess.ReviseError() == nil {
		t.Error("ReviseError should be set when drafter returns empty output")
	}
}

func TestManager_RevisePlan_DoubleDispatchReturnsErrReviseInFlight(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	release := make(chan struct{})
	mgr.SetPlanDrafter(&stubPlanDrafter{
		reviseFn: func(ctx context.Context, req ReviseRequest) (string, error) {
			<-release
			return "# Goal\nrevised\n", nil
		},
	})
	defer close(release)

	sess, _, _ := mgr.CreateSessionWithCommand(
		Config{Task: "t", Rows: 24, Cols: 80},
		func(name string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 5") },
	)
	if err := sess.WritePlan("# Goal\nv1\n"); err != nil {
		t.Fatal(err)
	}

	if err := mgr.RevisePlan(sess.ID, "first"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.RevisePlan(sess.ID, "second"); !errors.Is(err, ErrReviseInFlight) {
		t.Errorf("second RevisePlan err = %v, want ErrReviseInFlight", err)
	}
}

func TestManager_RevisePlan_NoPlanReturnsError(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	mgr.SetPlanDrafter(&stubPlanDrafter{
		reviseFn: func(ctx context.Context, req ReviseRequest) (string, error) {
			return "should not be called", nil
		},
	})

	sess, _, _ := mgr.CreateSessionWithCommand(
		Config{Task: "t", Rows: 24, Cols: 80},
		func(name string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 5") },
	)

	if err := mgr.RevisePlan(sess.ID, "improve"); !errors.Is(err, ErrNoPlanToRevise) {
		t.Errorf("RevisePlan err = %v, want ErrNoPlanToRevise", err)
	}
}

func TestManager_RevisePlan_EmptyCritiqueRejected(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	mgr.SetPlanDrafter(&stubPlanDrafter{
		reviseFn: func(ctx context.Context, req ReviseRequest) (string, error) {
			return "x", nil
		},
	})

	sess, _, _ := mgr.CreateSessionWithCommand(
		Config{Task: "t", Rows: 24, Cols: 80},
		func(name string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 5") },
	)
	if err := sess.WritePlan("# Goal\nx\n"); err != nil {
		t.Fatal(err)
	}

	for _, c := range []string{"", "   ", "\t\n"} {
		if err := mgr.RevisePlan(sess.ID, c); !errors.Is(err, ErrEmptyCritique) {
			t.Errorf("RevisePlan(%q) err = %v, want ErrEmptyCritique", c, err)
		}
	}
}

func TestManager_RevisePlan_KillSessionCancelsSubprocess(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	release := make(chan struct{})
	var observedCancel atomic.Bool
	mgr.SetPlanDrafter(&stubPlanDrafter{
		reviseFn: func(ctx context.Context, req ReviseRequest) (string, error) {
			select {
			case <-ctx.Done():
				observedCancel.Store(true)
				return "", ctx.Err()
			case <-release:
				return "# Goal\nx", nil
			}
		},
	})

	sess, _, _ := mgr.CreateSessionWithCommand(
		Config{Task: "t", Rows: 24, Cols: 80},
		func(name string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 5") },
	)
	sessID := sess.ID
	if err := sess.WritePlan("# Goal\nx\n"); err != nil {
		t.Fatal(err)
	}

	if err := mgr.RevisePlan(sessID, "x"); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, time.Second, func() bool { return sess.IsRevising() })

	if err := mgr.KillSession(sessID); err != nil {
		t.Fatal(err)
	}
	close(release)

	waitForCondition(t, 2*time.Second, func() bool { return !sess.IsRevising() })
	if !observedCancel.Load() {
		t.Error("revise subprocess context was not cancelled by KillSession")
	}
}

func TestManager_RevisePlan_ShutdownDrainsGoroutine(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())

	finished := atomic.Bool{}
	mgr.SetPlanDrafter(&stubPlanDrafter{
		reviseFn: func(ctx context.Context, req ReviseRequest) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	})

	sess, _, _ := mgr.CreateSessionWithCommand(
		Config{Task: "test", Rows: 24, Cols: 80},
		func(name string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 5") },
	)
	if err := sess.WritePlan("# Goal\nx\n"); err != nil {
		t.Fatal(err)
	}

	if err := mgr.RevisePlan(sess.ID, "improve"); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, time.Second, func() bool { return sess.IsRevising() })

	go func() {
		mgr.Shutdown()
		finished.Store(true)
	}()

	// Shutdown must complete in bounded time even with a revise in flight.
	// The runRevise goroutine is registered in m.watchers and listens on
	// m.done — Shutdown closes m.done, the inner goroutine cancels the
	// drafter context, and runRevise exits via its defer chain.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if finished.Load() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Shutdown did not return within 3s while a revise was in flight")
}

// TestManager_StartDraft_QuestionRoundTrip verifies the full question IPC
// lifecycle: StartDraft creates a planner.Server and threads its socket path
// through DraftRequest; the drafter stub uses planner.AskQuestion to dial it,
// the manager forwards the event onto PlannerQuestions() with the right
// session ID, the test answers it, and the drafter resumes and produces a
// plan. Also confirms the socket file is removed when the draft completes.
func TestManager_StartDraft_QuestionRoundTrip(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	mgr.SetPlanDrafter(&stubPlanDrafter{
		draftFn: func(ctx context.Context, req DraftRequest) (string, error) {
			if req.QuestionSocket == "" {
				return "", errors.New("expected non-empty QuestionSocket")
			}
			answer, err := planner.AskQuestion(req.QuestionSocket, "what color?")
			if err != nil {
				return "", err
			}
			if answer != "blue" {
				return "", errors.New("unexpected answer: " + answer)
			}
			return "# Goal\nDo X\n\n## Tasks\n- [ ] step\n", nil
		},
	})

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Subscribe before StartDraft so we don't miss the event on the buffered
	// channel. The pump goroutine emits onto m.plannerQuestions; drafting
	// runs in a separate goroutine, so the question event will be on the
	// channel by the time the drafter calls AskQuestion.
	if err := mgr.StartDraft(sess.ID, "add dark mode"); err != nil {
		t.Fatalf("StartDraft: %v", err)
	}

	select {
	case q := <-mgr.PlannerQuestions():
		if q.SessionID != sess.ID {
			t.Errorf("question SessionID = %q, want %q", q.SessionID, sess.ID)
		}
		if q.Question != "what color?" {
			t.Errorf("question text = %q, want what color?", q.Question)
		}
		q.AnswerCh <- "blue"
	case <-time.After(3 * time.Second):
		t.Fatal("never received planner question on PlannerQuestions()")
	}

	waitForCondition(t, 3*time.Second, func() bool {
		return !sess.IsDrafting() && sess.LifecyclePhase() == LifecyclePlanning && sess.HasPlan()
	})
	if sess.DraftError() != nil {
		t.Fatalf("DraftError = %v, want nil", sess.DraftError())
	}

	// Socket file should be removed once the draft (and its planner.Server)
	// has been closed in runDraft's defer.
	socketPath := plannerQuestionSocketPath(repo, sess.ID)
	waitForCondition(t, time.Second, func() bool {
		_, err := os.Stat(socketPath)
		return os.IsNotExist(err)
	})
}

// TestManager_StartDraft_NoQuestionsByDefault verifies the channel stays
// quiet when the drafter never calls ask_user — the typical case. The pump
// must not synthesize spurious events.
func TestManager_StartDraft_NoQuestionsByDefault(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	mgr.SetPlanDrafter(&stubPlanDrafter{
		draftFn: func(ctx context.Context, req DraftRequest) (string, error) {
			return "# Goal\nstub\n\n## Tasks\n- [ ] step\n", nil
		},
	})

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := mgr.StartDraft(sess.ID, "add thing"); err != nil {
		t.Fatalf("StartDraft: %v", err)
	}

	select {
	case q := <-mgr.PlannerQuestions():
		t.Errorf("unexpected planner question: %+v", q)
	case <-time.After(500 * time.Millisecond):
		// good — no question raised.
	}

	waitForCondition(t, 2*time.Second, func() bool {
		return !sess.IsDrafting() && sess.HasPlan()
	})
}

// TestManager_AgentCount_ExcludesExitedAgents pins the fix for the inflated
// quit/concurrency warnings: an agent that has exited stays in the session
// map (so the user can inspect it) but must not count toward
// Manager.AgentCount, which is used to gate the "agents running" detach
// confirmation and the soft concurrent-agent limit. Both Done (clean exit)
// and Error (non-zero exit) are exercised so the symmetric arms of
// LiveAgentCount's switch are both covered.
func TestManager_AgentCount_ExcludesExitedAgents(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    Status
	}{
		{"error_exit", "exit 1", StatusError},
		{"clean_exit", "exit 0", StatusDone},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := setupTestRepo(t)
			mgr := NewManager(repo, defaultTestSettings())
			defer mgr.Shutdown()

			sess, ag, err := mgr.CreateSessionWithCommand(
				Config{Task: "test", Rows: 24, Cols: 80},
				func(name string) *exec.Cmd { return exec.Command("bash", "-c", tc.command) },
			)
			if err != nil {
				t.Fatal(err)
			}

			select {
			case <-ag.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for agent to exit")
			}

			if got := ag.Status(); got != tc.want {
				t.Fatalf("expected %s after %q, got %s", tc.want, tc.command, got)
			}
			if got := sess.AgentCount(); got != 1 {
				t.Errorf("session should retain exited agent in its map, got AgentCount=%d", got)
			}
			if got := mgr.AgentCount(); got != 0 {
				t.Errorf("expected Manager.AgentCount=0 (exited agent must not count), got %d", got)
			}
			if got := sess.LiveAgentCount(); got != 0 {
				t.Errorf("expected Session.LiveAgentCount=0 (exited agent must not count), got %d", got)
			}
		})
	}
}

// TestManager_EmitDuringShutdownDoesNotPanic pins C1: emit() must short-circuit
// once Shutdown begins closing m.events. Without the sendsClosed guard, any
// concurrent caller (StartDraft, CreateAgent, ResumeSession, a late watchAgent
// returning) would panic with "send on closed channel" — the select+default
// in emit only protects against a full buffer, not a closed one.
func TestManager_EmitDuringShutdownDoesNotPanic(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())

	// Drive emit() from many goroutines while Shutdown runs. Under -race the
	// data-race detector also catches any sendsClosed/closed-channel ordering
	// mistakes. The events channel is intentionally not drained so emits can
	// race the close.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				mgr.emit(Event{Type: EventStatusChanged})
			}
		}()
	}

	// Let the emitters get into their loop, then tear down.
	time.Sleep(10 * time.Millisecond)
	mgr.Shutdown()

	// After Shutdown returns, signal stop and wait for goroutines. Any
	// closed-channel send before this point would have panicked the test.
	close(stop)
	wg.Wait()
}
