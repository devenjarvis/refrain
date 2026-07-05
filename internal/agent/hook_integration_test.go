package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devenjarvis/refrain/internal/hook"
)

// TestManagerDispatchesHookEventsToAgent verifies the full path: an external
// process (simulated via hook.SendEvent from this test) writes to the socket
// at <repoPath>/.refrain/hook.sock, the Manager's dispatcher routes by agent ID,
// and the agent's state transitions accordingly.
func TestManagerDispatchesHookEventsToAgent(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	if mgr.HookSocketPath() == "" {
		t.Fatal("expected hook socket path to be set after NewManager")
	}

	cfg := Config{Name: "hook-dispatch", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		// Long-running process so the agent doesn't exit mid-test.
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = sess

	// Drain the initial EventCreated event so we can detect EventStatusChanged
	// below deterministically.
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	// Send SessionStart and assert it routes to the agent.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:      hook.KindSessionStart,
		AgentID:   ag.ID,
		SessionID: "claude-uuid-42",
	}); err != nil {
		t.Fatalf("SendEvent SessionStart: %v", err)
	}

	// Manager emits EventStatusChanged on each hook-driven status mutation.
	if !waitForStatus(t, ag, StatusActive) {
		t.Fatalf("expected Active after SessionStart, got %s", ag.Status())
	}
	if got := ag.ClaudeSessionID(); got != "claude-uuid-42" {
		t.Errorf("expected claude session id %q, got %q", "claude-uuid-42", got)
	}

	// Simulate Claude's Stop hook at the end of a turn.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindStop,
		AgentID: ag.ID,
	}); err != nil {
		t.Fatalf("SendEvent Stop: %v", err)
	}

	if !waitForStatus(t, ag, StatusIdle) {
		t.Fatalf("expected Idle after Stop, got %s", ag.Status())
	}
}

// TestManagerDropsUnknownAgentID confirms hook events for an unknown agent are
// silently dropped (e.g. late Stop arriving after a kill).
func TestManagerDropsUnknownAgentID(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	// Send an event for an agent that doesn't exist — must not panic.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindSessionStart,
		AgentID: "does-not-exist",
	}); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}

	// Give the dispatcher time to process the drop.
	time.Sleep(200 * time.Millisecond)
}

// TestSocketPathPerRepo ensures two managers on different repos use distinct sockets.
func TestSocketPathPerRepo(t *testing.T) {
	repo1 := setupTestRepo(t)
	repo2 := setupTestRepo(t)
	mgr1 := NewManager(repo1, defaultTestSettings())
	defer mgr1.Shutdown()
	mgr2 := NewManager(repo2, defaultTestSettings())
	defer mgr2.Shutdown()

	if mgr1.HookSocketPath() == "" || mgr2.HookSocketPath() == "" {
		t.Fatal("expected both managers to have socket paths")
	}
	if mgr1.HookSocketPath() == mgr2.HookSocketPath() {
		t.Errorf("expected distinct socket paths; got %q", mgr1.HookSocketPath())
	}
}

// TestManagerDispatchesNotificationAndStop verifies the Notification hook drives
// the agent to StatusWaiting and a subsequent Stop returns it to StatusIdle.
func TestManagerDispatchesNotificationAndStop(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "notif-dispatch", Task: "test", Rows: 24, Cols: 80}
	_, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Drain EventCreated.
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	// SessionStart → Active.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindSessionStart,
		AgentID: ag.ID,
	}); err != nil {
		t.Fatalf("SendEvent SessionStart: %v", err)
	}
	if !waitForStatus(t, ag, StatusActive) {
		t.Fatalf("expected Active after SessionStart, got %s", ag.Status())
	}

	// Notification → Waiting.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindNotification,
		AgentID: ag.ID,
		Message: "Claude needs your permission to use Bash",
	}); err != nil {
		t.Fatalf("SendEvent Notification: %v", err)
	}
	if !waitForStatus(t, ag, StatusWaiting) {
		t.Fatalf("expected Waiting after Notification, got %s", ag.Status())
	}

	// Stop → Idle.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindStop,
		AgentID: ag.ID,
	}); err != nil {
		t.Fatalf("SendEvent Stop: %v", err)
	}
	if !waitForStatus(t, ag, StatusIdle) {
		t.Fatalf("expected Idle after Stop, got %s", ag.Status())
	}
}

// TestManagerUserPromptSubmitRearmsChime verifies UserPromptSubmit both
// re-arms the chime flag and transitions Idle→Active.
func TestManagerUserPromptSubmitRearmsChime(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "ups-dispatch", Task: "test", Rows: 24, Cols: 80}
	_, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	// Seed: agent is Idle and the chime has already fired this turn.
	ag.mu.Lock()
	ag.status = StatusIdle
	ag.chimedForTurn = true
	ag.mu.Unlock()

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
	}); err != nil {
		t.Fatalf("SendEvent UserPromptSubmit: %v", err)
	}

	if !waitForStatus(t, ag, StatusActive) {
		t.Fatalf("expected Active after UserPromptSubmit, got %s", ag.Status())
	}
	if ag.ChimedForTurn() {
		t.Error("expected chimedForTurn to be reset by UserPromptSubmit")
	}
}

// TestDoneAgentIgnoresLateStop verifies that a Done agent stays Done when a
// late Stop event arrives — the common race where the PTY closes (readLoop
// sets StatusDone) while Claude's in-flight Stop hook is still in the socket
// queue.  Without the guard, the agent's status would flip to Idle and the
// dashboard would show the wrong indicator.
func TestDoneAgentIgnoresLateStop(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "done-ignore-stop", Task: "test", Rows: 24, Cols: 80}
	_, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	ag.mu.Lock()
	ag.status = StatusDone
	ag.mu.Unlock()

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindStop,
		AgentID: ag.ID,
	}); err != nil {
		t.Fatalf("SendEvent Stop: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	if got := ag.Status(); got != StatusDone {
		t.Errorf("expected Done to be preserved after late Stop, got %s", got)
	}
}

// TestErrorAgentIgnoresLateStop verifies that an Error agent stays Error when a
// late Stop hook arrives after the process has already exited with an error.
func TestErrorAgentIgnoresLateStop(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "error-ignore-stop", Task: "test", Rows: 24, Cols: 80}
	_, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	ag.mu.Lock()
	ag.status = StatusError
	ag.mu.Unlock()

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindStop,
		AgentID: ag.ID,
	}); err != nil {
		t.Fatalf("SendEvent Stop: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	if got := ag.Status(); got != StatusError {
		t.Errorf("expected Error to be preserved after late Stop, got %s", got)
	}
}

// TestDoneAgentIgnoresLateSessionStart verifies that a Done agent stays Done
// when a late SessionStart event arrives.  This mirrors the same scenario for
// Stop: the process may exit before all hook events are drained from the socket.
func TestDoneAgentIgnoresLateSessionStart(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "done-ignore-ss", Task: "test", Rows: 24, Cols: 80}
	_, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	ag.mu.Lock()
	ag.status = StatusDone
	ag.mu.Unlock()

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:      hook.KindSessionStart,
		AgentID:   ag.ID,
		SessionID: "stray-session-id",
	}); err != nil {
		t.Fatalf("SendEvent SessionStart: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	if got := ag.Status(); got != StatusDone {
		t.Errorf("expected Done to be preserved after late SessionStart, got %s", got)
	}
}

// TestNotificationIgnoredWhenIdle verifies that a Notification arriving after
// Stop (when the agent is already Idle) does not flip the agent to Waiting.
// This prevents a trailing Notification from re-attentioning a row that Claude
// has already finished with.
func TestNotificationIgnoredWhenIdle(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "idle-ignore-notif", Task: "test", Rows: 24, Cols: 80}
	_, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	// Drive to Idle via Stop hook (normal end-of-turn path).
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindSessionStart,
		AgentID: ag.ID,
	}); err != nil {
		t.Fatalf("SendEvent SessionStart: %v", err)
	}
	if !waitForStatus(t, ag, StatusActive) {
		t.Fatalf("expected Active after SessionStart, got %s", ag.Status())
	}
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindStop,
		AgentID: ag.ID,
	}); err != nil {
		t.Fatalf("SendEvent Stop: %v", err)
	}
	if !waitForStatus(t, ag, StatusIdle) {
		t.Fatalf("expected Idle after Stop, got %s", ag.Status())
	}

	// A trailing Notification must not flip Idle → Waiting.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindNotification,
		AgentID: ag.ID,
		Message: "stray notification after turn ended",
	}); err != nil {
		t.Fatalf("SendEvent Notification: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	if got := ag.Status(); got != StatusIdle {
		t.Errorf("expected Idle to be preserved after stray Notification, got %s", got)
	}
}

// TestPreToolUseTodoWritePopulatesTodos is the primary regression test for the
// TodoWrite progress-badge feature. It feeds the exact JSON shape Claude emits
// through the full hook path (SendEvent → Manager → OnHookEvent) and asserts
// that Todos() is populated with the expected items.
func TestPreToolUseTodoWritePopulatesTodos(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "todo-write", Task: "test", Rows: 24, Cols: 80}
	_, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindSessionStart,
		AgentID: ag.ID,
	}); err != nil {
		t.Fatalf("SendEvent SessionStart: %v", err)
	}
	if !waitForStatus(t, ag, StatusActive) {
		t.Fatalf("expected Active after SessionStart, got %s", ag.Status())
	}

	// Exact JSON shape Claude emits for a TodoWrite PreToolUse event.
	todoInput := json.RawMessage(`{"todos":[` +
		`{"content":"write unit tests","status":"in_progress","activeForm":"writing unit tests"},` +
		`{"content":"run tests","status":"pending"}` +
		`]}`)

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:      hook.KindPreToolUse,
		AgentID:   ag.ID,
		ToolName:  "TodoWrite",
		ToolInput: todoInput,
	}); err != nil {
		t.Fatalf("SendEvent PreToolUse TodoWrite: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		todos := ag.Todos()
		if len(todos) >= 2 {
			if todos[0].Status != "in_progress" {
				t.Errorf("todos[0].Status = %q, want %q", todos[0].Status, "in_progress")
			}
			if todos[0].ActiveForm != "writing unit tests" {
				t.Errorf("todos[0].ActiveForm = %q, want %q", todos[0].ActiveForm, "writing unit tests")
			}
			if todos[1].Status != "pending" {
				t.Errorf("todos[1].Status = %q, want %q", todos[1].Status, "pending")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out: Todos() never populated (len=%d)", len(ag.Todos()))
}

// TestPreToolUseTodoWrite_SnakeCaseActiveForm verifies that TodoItem correctly
// accepts the snake_case "active_form" key as a fallback for "activeForm".
func TestPreToolUseTodoWrite_SnakeCaseActiveForm(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "todo-snake", Task: "test", Rows: 24, Cols: 80}
	_, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindSessionStart,
		AgentID: ag.ID,
	}); err != nil {
		t.Fatalf("SendEvent SessionStart: %v", err)
	}
	if !waitForStatus(t, ag, StatusActive) {
		t.Fatalf("expected Active after SessionStart, got %s", ag.Status())
	}

	// Use snake_case active_form key to exercise the fallback path.
	todoInput := json.RawMessage(`{"todos":[` +
		`{"content":"refactor auth","status":"in_progress","active_form":"refactoring auth"}` +
		`]}`)

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:      hook.KindPreToolUse,
		AgentID:   ag.ID,
		ToolName:  "TodoWrite",
		ToolInput: todoInput,
	}); err != nil {
		t.Fatalf("SendEvent PreToolUse TodoWrite: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		todos := ag.Todos()
		if len(todos) >= 1 {
			if todos[0].ActiveForm != "refactoring auth" {
				t.Errorf("ActiveForm = %q, want %q", todos[0].ActiveForm, "refactoring auth")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out: Todos() never populated with snake_case active_form")
}

// TestDoneAgentIgnoresLateNotification verifies a Done agent stays Done
// when a late Notification event arrives (e.g. race between Claude emitting
// a prompt and the agent process having already been killed/finished).
func TestDoneAgentIgnoresLateNotification(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "done-ignore", Task: "test", Rows: 24, Cols: 80}
	_, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	// Force the agent to Done.
	ag.mu.Lock()
	ag.status = StatusDone
	ag.mu.Unlock()

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindNotification,
		AgentID: ag.ID,
	}); err != nil {
		t.Fatalf("SendEvent Notification: %v", err)
	}

	// Give the dispatcher time to process — the status must remain Done.
	time.Sleep(200 * time.Millisecond)
	if got := ag.Status(); got != StatusDone {
		t.Errorf("expected Done to be preserved, got %s", got)
	}
}

// TestDoneAgentIgnoresLateUserPromptSubmit verifies a Done agent stays Done
// (and its chimedForTurn flag is NOT reset) when a stray UserPromptSubmit
// event arrives after the agent has already exited.
func TestDoneAgentIgnoresLateUserPromptSubmit(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "done-ignore-ups", Task: "test", Rows: 24, Cols: 80}
	_, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	// Force Done, and set chimedForTurn=true so a silent reset would be
	// observable if the guard regresses.
	ag.mu.Lock()
	ag.status = StatusDone
	ag.chimedForTurn = true
	ag.mu.Unlock()

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
	}); err != nil {
		t.Fatalf("SendEvent UserPromptSubmit: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	if got := ag.Status(); got != StatusDone {
		t.Errorf("expected Done to be preserved, got %s", got)
	}
	ag.mu.Lock()
	chimed := ag.chimedForTurn
	ag.mu.Unlock()
	if !chimed {
		t.Error("expected chimedForTurn to stay true on Done agent, got reset")
	}
}

// TestManagerPreToolUseClearsWaiting verifies PreToolUse transitions a Waiting
// agent back to Active — the fix path for approved permission prompts, where
// Claude does not fire UserPromptSubmit but does fire PreToolUse when it
// resumes tool execution.
func TestManagerPreToolUseClearsWaiting(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "pretooluse-dispatch", Task: "test", Rows: 24, Cols: 80}
	_, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	// SessionStart → Active.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindSessionStart,
		AgentID: ag.ID,
	}); err != nil {
		t.Fatalf("SendEvent SessionStart: %v", err)
	}
	if !waitForStatus(t, ag, StatusActive) {
		t.Fatalf("expected Active after SessionStart, got %s", ag.Status())
	}

	// Notification → Waiting.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindNotification,
		AgentID: ag.ID,
		Message: "Claude needs your permission to use Bash",
	}); err != nil {
		t.Fatalf("SendEvent Notification: %v", err)
	}
	if !waitForStatus(t, ag, StatusWaiting) {
		t.Fatalf("expected Waiting after Notification, got %s", ag.Status())
	}

	// Mark chimedForTurn so we can verify PreToolUse does NOT reset it —
	// chime re-arming is a per-turn signal, not a per-tool-call signal.
	ag.mu.Lock()
	ag.chimedForTurn = true
	ag.mu.Unlock()

	// PreToolUse → Active (permission approved, Claude resumed).
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindPreToolUse,
		AgentID: ag.ID,
	}); err != nil {
		t.Fatalf("SendEvent PreToolUse: %v", err)
	}
	if !waitForStatus(t, ag, StatusActive) {
		t.Fatalf("expected Active after PreToolUse, got %s", ag.Status())
	}
	if !ag.ChimedForTurn() {
		t.Error("expected chimedForTurn to remain true across PreToolUse (it's per-turn, not per-tool)")
	}
}

// TestDoneAgentIgnoresLatePreToolUse verifies a Done agent stays Done when a
// stray PreToolUse event arrives after the agent has already exited.
func TestDoneAgentIgnoresLatePreToolUse(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "done-ignore-ptu", Task: "test", Rows: 24, Cols: 80}
	_, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	ag.mu.Lock()
	ag.status = StatusDone
	ag.chimedForTurn = true
	ag.mu.Unlock()

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindPreToolUse,
		AgentID: ag.ID,
	}); err != nil {
		t.Fatalf("SendEvent PreToolUse: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	if got := ag.Status(); got != StatusDone {
		t.Errorf("expected Done to be preserved, got %s", got)
	}
	ag.mu.Lock()
	chimed := ag.chimedForTurn
	ag.mu.Unlock()
	if !chimed {
		t.Error("expected chimedForTurn to stay true on Done agent, got reset")
	}
}

// TestUserPromptSubmitRenamesBranch verifies the first actionable
// UserPromptSubmit drives the namer-based rename to completion, and a second
// prompt is a no-op because HasClaudeName is now set on the session.
func TestUserPromptSubmitRenamesBranch(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	stub := &stubNamer{result: "add-dark-mode-to-dashboard"}
	mgr.SetBranchNamer(stub.fn())

	cfg := Config{Name: "warm-ibis", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	originalBranch := sess.Worktree.Branch

	// Slash-only prompt is non-actionable: no rename, namer never invoked.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "/clear",
	}); err != nil {
		t.Fatalf("SendEvent slash: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if sess.HasClaudeName() {
		t.Error("slash-only prompt should not flip HasClaudeName")
	}
	if sess.Branch() != originalBranch {
		t.Errorf("slash-only prompt should not rename; got %q", sess.Branch())
	}
	if stub.calls.Load() != 0 {
		t.Errorf("namer should not be called for slash-only prompt, got %d", stub.calls.Load())
	}

	// Real prompt triggers rename via the stub namer.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "add dark mode to dashboard",
	}); err != nil {
		t.Fatalf("SendEvent prompt: %v", err)
	}

	got := waitForBranch(t, sess, "refrain/add-dark-mode-to-dashboard")
	if got != "refrain/add-dark-mode-to-dashboard" {
		t.Errorf("expected branch refrain/add-dark-mode-to-dashboard, got %q", got)
	}
	if !sess.HasClaudeName() {
		t.Fatal("expected HasClaudeName true after prompt")
	}
	if got := sess.CurrentName(); got != "add-dark-mode-to-dashboard" {
		t.Errorf("expected Name add-dark-mode-to-dashboard, got %q", got)
	}

	// Second real prompt is a no-op (gate already consumed at the session level).
	prevBranch := sess.Branch()
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "now add light mode too",
	}); err != nil {
		t.Fatalf("SendEvent second: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if got := sess.Branch(); got != prevBranch {
		t.Errorf("second prompt should be no-op; got %q, want %q", got, prevBranch)
	}
	if stub.calls.Load() != 1 {
		t.Errorf("namer should only be called once total, got %d", stub.calls.Load())
	}
}

// TestUserPromptSubmitSlashWithArgRenamesBranch verifies that a skill
// invocation like "/plan-it add dark mode" is treated as actionable and
// reaches the namer (which sees the full prompt; how it summarizes is up to
// the model). A bare "/plan-it" with no args remains non-actionable.
func TestUserPromptSubmitSlashWithArgRenamesBranch(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	stub := &stubNamer{result: "add-dark-mode"}
	mgr.SetBranchNamer(stub.fn())

	cfg := Config{Name: "cold-ferret", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "/plan-it add dark mode",
	}); err != nil {
		t.Fatalf("SendEvent slash+arg: %v", err)
	}

	got := waitForBranch(t, sess, "refrain/add-dark-mode")
	if got != "refrain/add-dark-mode" {
		t.Errorf("expected branch refrain/add-dark-mode, got %q", got)
	}
	if !sess.HasClaudeName() {
		t.Fatal("expected HasClaudeName true after slash+arg prompt")
	}
	if stub.calls.Load() != 1 {
		t.Errorf("namer call count = %d, want 1", stub.calls.Load())
	}
}

// TestWaitingReasonStoredAndCleared verifies that WaitingReason() is set when a
// KindNotification event fires and cleared when KindPreToolUse follows.
func TestWaitingReasonStoredAndCleared(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "waiting-reason", Task: "test", Rows: 24, Cols: 80}
	_, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	// SessionStart → Active.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindSessionStart,
		AgentID: ag.ID,
	}); err != nil {
		t.Fatalf("SendEvent SessionStart: %v", err)
	}
	if !waitForStatus(t, ag, StatusActive) {
		t.Fatalf("expected Active after SessionStart, got %s", ag.Status())
	}

	// Notification → Waiting; reason must be stored.
	const wantReason = "Claude needs permission"
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindNotification,
		AgentID: ag.ID,
		Message: wantReason,
	}); err != nil {
		t.Fatalf("SendEvent Notification: %v", err)
	}
	if !waitForStatus(t, ag, StatusWaiting) {
		t.Fatalf("expected Waiting after Notification, got %s", ag.Status())
	}
	if got := ag.WaitingReason(); got != wantReason {
		t.Errorf("WaitingReason() = %q, want %q", got, wantReason)
	}

	// PreToolUse → Active; reason must be cleared.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindPreToolUse,
		AgentID: ag.ID,
	}); err != nil {
		t.Fatalf("SendEvent PreToolUse: %v", err)
	}
	if !waitForStatus(t, ag, StatusActive) {
		t.Fatalf("expected Active after PreToolUse, got %s", ag.Status())
	}
	if got := ag.WaitingReason(); got != "" {
		t.Errorf("WaitingReason() = %q after PreToolUse, want empty", got)
	}
}

// waitForStatus polls the agent status up to 2s for the desired value.
func waitForStatus(t *testing.T, a *Agent, want Status) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if a.Status() == want {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// waitForClaudeName polls HasClaudeName() up to d for the desired value.
// The rename side-effect runs after OnHookEvent inside the dispatcher goroutine,
// so tests that care about naming must wait on this rather than on status.
func waitForClaudeName(t *testing.T, a *Agent, want bool, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if a.HasClaudeName() == want {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// sendUserPromptSubmit is a convenience wrapper that dispatches a
// UserPromptSubmit event through the manager's hook socket and waits for
// Agent.HasClaudeName() to flip — the only reliable signal that the
// dispatcher goroutine has processed the rename request. The caller must
// pass an actionable prompt (non-empty, not a bare slash command); empty or
// slash-only prompts skip the rename flow and never flip the flag.
func sendUserPromptSubmit(t *testing.T, mgr *Manager, a *Agent, prompt string) {
	t.Helper()
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: a.ID,
		Prompt:  prompt,
	}); err != nil {
		t.Fatalf("SendEvent UserPromptSubmit: %v", err)
	}
	if !waitForClaudeName(t, a, true, 2*time.Second) {
		t.Fatalf("HasClaudeName did not flip true after UserPromptSubmit")
	}
}

// TestManagerRenamesOnFirstUserPromptSubmit verifies that the first
// actionable UserPromptSubmit drives the namer-based rename and applies the
// resulting slug as the display name on both the agent and its session.
func TestManagerRenamesOnFirstUserPromptSubmit(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	stub := &stubNamer{result: "investigate-flaky-checkout-test"}
	mgr.SetBranchNamer(stub.fn())

	cfg := Config{Name: "rename-first", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	if sess.HasDisplayName() {
		t.Fatalf("precondition: fresh random-named session should not have a display name")
	}

	sendUserPromptSubmit(t, mgr, ag, "Investigate flaky checkout test!")

	const want = "investigate-flaky-checkout-test"
	// The session display-name update happens in the rename goroutine after
	// the namer returns; it's the last write before EventBranchRenamed fires
	// so wait on it.
	if got := waitForSessionDisplayName(t, sess, want, 2*time.Second); got != want {
		t.Fatalf("session display name: got %q, want %q", got, want)
	}
	if got := sess.Branch(); got != "refrain/"+want {
		t.Errorf("branch: got %q, want refrain/%s", got, want)
	}
	// Agent display name stays at its original value — agents keep their
	// track identities; only the session separator picks up the task name.
	if got := ag.GetDisplayName(); got != "rename-first" {
		t.Errorf("agent display name changed: got %q, want rename-first", got)
	}
}

// TestManagerSecondUserPromptSubmitDoesNotRename verifies that once the
// session's HasClaudeName is set (after a successful first rename), subsequent
// UserPromptSubmit events do not invoke the namer or change display names.
func TestManagerSecondUserPromptSubmitDoesNotRename(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	stub := &stubNamer{result: "first-prompt-wins"}
	mgr.SetBranchNamer(stub.fn())

	cfg := Config{Name: "rename-second", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	sendUserPromptSubmit(t, mgr, ag, "first prompt wins")

	const want = "first-prompt-wins"
	if got := waitForSessionDisplayName(t, sess, want, 2*time.Second); got != want {
		t.Fatalf("after first prompt: session display name = %q, want %q", got, want)
	}
	if got := sess.Branch(); got != "refrain/"+want {
		t.Fatalf("after first prompt: branch = %q, want refrain/%s", got, want)
	}
	// Agent display name must not have changed.
	if got := ag.GetDisplayName(); got != "rename-second" {
		t.Errorf("agent display name changed: got %q, want rename-second", got)
	}

	// Second prompt must be a no-op. HasClaudeName is already true on the
	// session, so we use status as a barrier confirming the dispatcher
	// processed the event before we assert.
	ag.mu.Lock()
	ag.status = StatusIdle
	ag.mu.Unlock()
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "later prompt should be ignored",
	}); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}
	if !waitForStatus(t, ag, StatusActive) {
		t.Fatalf("expected Active after second UserPromptSubmit")
	}

	// Agent display name still unchanged after second prompt.
	if got := ag.GetDisplayName(); got != "rename-second" {
		t.Errorf("agent display name changed: got %q, want rename-second", got)
	}
	// Session display name still "first-prompt-wins" — second prompt is a no-op.
	if got := sess.GetDisplayName(); got != want {
		t.Errorf("session display name changed on second prompt: got %q, want %q", got, want)
	}
	if stub.calls.Load() != 1 {
		t.Errorf("namer call count = %d, want 1", stub.calls.Load())
	}
}

// TestManagerEmptyPromptDoesNotConsumeGate verifies the new flow: an empty
// UserPromptSubmit skips the rename pipeline entirely (no namer call, gate
// stays open), so a follow-up non-empty prompt still renames normally.
func TestManagerEmptyPromptDoesNotConsumeGate(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	stub := &stubNamer{result: "real-prompt-result"}
	mgr.SetBranchNamer(stub.fn())

	cfg := Config{Name: "rename-empty", Task: "initial task", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	const interim = "rename-empty"
	if got := ag.GetDisplayName(); got != interim {
		t.Fatalf("precondition: agent display name, got %q want %q", got, interim)
	}

	// Empty prompt: namer never invoked, gate stays open.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "",
	}); err != nil {
		t.Fatalf("SendEvent empty: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if ag.HasClaudeName() {
		t.Error("empty prompt should not flip Agent.HasClaudeName")
	}
	if sess.HasClaudeName() {
		t.Error("empty prompt should not flip Session.HasClaudeName")
	}
	if got := ag.GetDisplayName(); got != interim {
		t.Errorf("empty-prompt UPS renamed agent: got %q, want %q", got, interim)
	}
	if sess.HasDisplayName() {
		t.Errorf("empty-prompt UPS set a session display name")
	}
	if stub.calls.Load() != 0 {
		t.Errorf("namer should not be called for empty prompt, got %d", stub.calls.Load())
	}

	// Follow-up actionable prompt: gate is still open, rename succeeds.
	sendUserPromptSubmit(t, mgr, ag, "the real prompt")
	if got := waitForBranch(t, sess, "refrain/real-prompt-result"); got != "refrain/real-prompt-result" {
		t.Errorf("retry rename: branch = %q, want refrain/real-prompt-result", got)
	}
}

// TestManagerDoneAgentIgnoresLateRename verifies that a stray UserPromptSubmit
// event for a Done/Error agent doesn't auto-rename the terminal row. Mirrors
// the Done-agent guard in maybeRenameFromPrompt.
func TestManagerDoneAgentIgnoresLateRename(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	stub := &stubNamer{result: "should-not-be-called"}
	mgr.SetBranchNamer(stub.fn())

	cfg := Config{Name: "rename-done", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	ag.mu.Lock()
	ag.status = StatusDone
	ag.mu.Unlock()

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "too late to rename",
	}); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}
	// Give the dispatcher time to process — the status guard short-circuits
	// before SetClaudeName, so HasClaudeName stays false.
	time.Sleep(200 * time.Millisecond)

	if ag.HasClaudeName() {
		t.Error("HasClaudeName flipped true on a Done agent")
	}
	if got := ag.GetDisplayName(); got != "rename-done" {
		t.Errorf("Done agent renamed: got %q, want %q", got, "rename-done")
	}
	if sess.HasDisplayName() {
		t.Error("session renamed by late UPS on Done agent")
	}
	if stub.calls.Load() != 0 {
		t.Errorf("namer should not be called for Done agent, got %d", stub.calls.Load())
	}
}

// TestManagerNamerErrorLeavesNamesUnchanged verifies that when the namer
// returns an error, the agent and session display names stay at their
// pre-prompt values and the random branch is preserved. Agent.HasClaudeName
// still flips so the resume restore path can distinguish "naming chance was
// taken" from "fresh placeholder".
func TestManagerNamerErrorLeavesNamesUnchanged(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	stub := &stubNamer{err: errors.New("haiku unavailable")}
	mgr.SetBranchNamer(stub.fn())

	cfg := Config{Name: "rename-punct", Task: "keep me", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	const interim = "rename-punct"
	originalBranch := sess.Worktree.Branch

	sendUserPromptSubmit(t, mgr, ag, "!!! ??? ...")

	// Wait for the rename goroutine to finish so we don't race the assertions.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && stub.calls.Load() < 1 {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)

	if got := ag.GetDisplayName(); got != interim {
		t.Errorf("agent display name overwritten on namer error: got %q, want %q", got, interim)
	}
	if sess.HasDisplayName() {
		t.Errorf("session display name set despite namer error")
	}
	if sess.HasClaudeName() {
		t.Error("Session.HasClaudeName should stay false when namer errors (so retries can fire)")
	}
	if got := sess.Branch(); got != originalBranch {
		t.Errorf("branch should be unchanged on namer error: got %q, want %q", got, originalBranch)
	}
}

// waitForBranch polls Session.Branch() until it matches want or 2s elapses.
// Returns the last-observed branch on timeout for useful error output.
func waitForBranch(t *testing.T, sess *Session, want string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := sess.Branch(); got == want {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	return sess.Branch()
}

// waitForBranchChanged polls until Session.Branch() is no longer equal to
// original, or the deadline elapses.
func waitForBranchChanged(t *testing.T, sess *Session, original string, d time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if got := sess.Branch(); got != original {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	return sess.Branch()
}

// waitForSessionDisplayName polls until Session.GetDisplayName() returns want or
// the deadline elapses. Use this after a successful Haiku rename to confirm the
// session separator updated (the display-name write happens inside the rename
// goroutine, after the branch rename itself).
func waitForSessionDisplayName(t *testing.T, sess *Session, want string, d time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if got := sess.GetDisplayName(); got == want {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	return sess.GetDisplayName()
}

// stubNamer is a BranchNamer that returns a fixed result and counts calls.
// lastInstruction captures the most recent rendered template the manager
// passed in so tests can assert template substitution.
type stubNamer struct {
	result          string
	err             error
	calls           atomic.Int32
	block           chan struct{} // if non-nil, the namer blocks on receive before returning
	mu              sync.Mutex
	lastInstruction string
}

func (s *stubNamer) fn() BranchNamer {
	return func(ctx context.Context, instruction string) (string, error) {
		s.mu.Lock()
		s.lastInstruction = instruction
		s.mu.Unlock()
		s.calls.Add(1)
		if s.block != nil {
			select {
			case <-s.block:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return s.result, s.err
	}
}

func (s *stubNamer) instruction() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastInstruction
}

// TestSmartBranchRename_HappyPath verifies the stub namer's result is
// applied to the session's branch through the Haiku path.
func TestSmartBranchRename_HappyPath(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	stub := &stubNamer{result: "fix-login-flow"}
	mgr.SetBranchNamer(stub.fn())

	cfg := Config{Name: "smart-happy", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	original := sess.Branch()

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "we need to fix the broken login flow asap",
	}); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}

	got := waitForBranchChanged(t, sess, original, 2*time.Second)
	if got != "refrain/fix-login-flow" {
		t.Errorf("branch = %q, want refrain/fix-login-flow", got)
	}
	if !sess.HasClaudeName() {
		t.Error("HasClaudeName should be true after rename")
	}
	if stub.calls.Load() != 1 {
		t.Errorf("namer call count = %d, want 1", stub.calls.Load())
	}
}

// TestSmartBranchRename_NamerErrorRetriesNextPrompt verifies the new
// no-fallback contract: when the namer errors, the random branch persists
// and Session.HasClaudeName stays false so the next prompt can retry.
func TestSmartBranchRename_NamerErrorRetriesNextPrompt(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	failing := &stubNamer{err: errors.New("haiku unavailable")}
	mgr.SetBranchNamer(failing.fn())

	cfg := Config{Name: "namer-error", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	originalBranch := sess.Branch()

	// First prompt: namer errors → no rename, gate stays open.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "first attempt with broken namer",
	}); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && failing.calls.Load() < 1 {
		time.Sleep(20 * time.Millisecond)
	}
	// Wait for finishRename to clear the in-flight flag.
	time.Sleep(100 * time.Millisecond)

	if sess.HasClaudeName() {
		t.Error("Session.HasClaudeName should stay false after namer error")
	}
	if got := sess.Branch(); got != originalBranch {
		t.Errorf("branch should be unchanged after namer error: got %q, want %q", got, originalBranch)
	}

	// Swap in a successful stub and send another prompt — retry succeeds.
	good := &stubNamer{result: "second-attempt"}
	mgr.SetBranchNamer(good.fn())

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "this is the real prompt",
	}); err != nil {
		t.Fatalf("SendEvent retry: %v", err)
	}

	got := waitForBranch(t, sess, "refrain/second-attempt")
	if got != "refrain/second-attempt" {
		t.Errorf("retry branch = %q, want refrain/second-attempt", got)
	}
	if good.calls.Load() != 1 {
		t.Errorf("retry namer call count = %d, want 1", good.calls.Load())
	}
}

// TestSmartBranchRename_SlashOnlyDoesNotInvokeNamer verifies that "/clear"-
// style prompts skip the rename pipeline entirely without consuming the
// in-flight gate, and a real follow-up prompt still renames.
func TestSmartBranchRename_SlashOnlyDoesNotInvokeNamer(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	stub := &stubNamer{result: "real-result"}
	mgr.SetBranchNamer(stub.fn())

	cfg := Config{Name: "slash-skip", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	originalBranch := sess.Branch()

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "/clear",
	}); err != nil {
		t.Fatalf("SendEvent /clear: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	if stub.calls.Load() != 0 {
		t.Errorf("namer should not be called for slash-only prompt, got %d", stub.calls.Load())
	}
	if sess.HasClaudeName() {
		t.Error("Session.HasClaudeName should stay false after slash-only prompt")
	}
	if got := sess.Branch(); got != originalBranch {
		t.Errorf("slash-only prompt should not rename: got %q", got)
	}

	// Real prompt: rename succeeds, gate consumed.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "now rename for real",
	}); err != nil {
		t.Fatalf("SendEvent retry: %v", err)
	}

	got := waitForBranch(t, sess, "refrain/real-result")
	if got != "refrain/real-result" {
		t.Errorf("retry branch = %q, want refrain/real-result", got)
	}
	if stub.calls.Load() != 1 {
		t.Errorf("namer call count = %d, want 1", stub.calls.Load())
	}
}

// TestSmartBranchRename_DoubleDispatchGated verifies that a second
// UserPromptSubmit arriving while the first Haiku call is still running does
// not dispatch a second namer invocation.
func TestSmartBranchRename_DoubleDispatchGated(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	release := make(chan struct{})
	stub := &stubNamer{result: "slow-result", block: release}
	mgr.SetBranchNamer(stub.fn())

	cfg := Config{Name: "smart-gate", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "first prompt",
	}); err != nil {
		t.Fatalf("SendEvent 1: %v", err)
	}

	// Wait for the first call to actually enter the stub.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && stub.calls.Load() < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	if stub.calls.Load() != 1 {
		t.Fatal("first namer call did not start")
	}

	// Send a second prompt — it must NOT trigger a second namer call.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "second prompt (should be gated)",
	}); err != nil {
		t.Fatalf("SendEvent 2: %v", err)
	}

	// Give the dispatcher enough time to try to re-enter.
	time.Sleep(200 * time.Millisecond)
	if got := stub.calls.Load(); got != 1 {
		t.Errorf("namer call count = %d, want 1 (second prompt should be gated)", got)
	}

	// Release the first call and ensure the rename completes.
	close(release)

	got := waitForBranch(t, sess, "refrain/slow-result")
	if got != "refrain/slow-result" {
		t.Errorf("branch = %q, want refrain/slow-result", got)
	}

	// Even after completion, total call count must still be 1 — the second
	// prompt must never have invoked the namer.
	if n := stub.calls.Load(); n != 1 {
		t.Errorf("final namer call count = %d, want 1", n)
	}
}

// TestSmartBranchRename_ShutdownCancelsInflight verifies that Manager.Shutdown
// cancels the in-flight rename goroutine so it doesn't outlive the manager.
func TestSmartBranchRename_ShutdownCancelsInflight(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())

	// Block the namer forever — only ctx cancellation should release it.
	stub := &stubNamer{result: "never-returns", block: make(chan struct{})}
	mgr.SetBranchNamer(stub.fn())

	cfg := Config{Name: "shutdown-cancel", Task: "test", Rows: 24, Cols: 80}
	_, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "trigger a rename that will block",
	}); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}

	// Wait for the goroutine to actually enter the namer.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && stub.calls.Load() < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	if stub.calls.Load() < 1 {
		t.Fatal("namer did not run before shutdown")
	}

	done := make(chan struct{})
	var once sync.Once
	go func() {
		defer once.Do(func() { close(done) })
		mgr.Shutdown()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not return — rename goroutine likely leaked")
	}
}

// TestSmartBranchRename_EmitsBranchRenamedEvent asserts that a successful
// rename via applyRename emits an EventBranchRenamed carrying the new branch
// name. The TUI scheduler uses this event to burst-refresh PR state and
// recover from the rename/PR race.
func TestSmartBranchRename_EmitsBranchRenamedEvent(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	stub := &stubNamer{result: "add-dark-mode"}
	mgr.SetBranchNamer(stub.fn())

	cfg := Config{Name: "rename-event", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	// Drain EventCreated.
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	original := sess.Branch()

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "add dark mode to dashboard",
	}); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}

	got := waitForBranchChanged(t, sess, original, 2*time.Second)
	if got != "refrain/add-dark-mode" {
		t.Fatalf("branch = %q, want refrain/add-dark-mode", got)
	}

	// Drain events until we see EventBranchRenamed. Other events
	// (EventStatusChanged, etc.) may interleave; skip them.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-mgr.Events():
			if ev.Type == EventBranchRenamed {
				if ev.SessionID != sess.ID {
					t.Errorf("SessionID = %q, want %q", ev.SessionID, sess.ID)
				}
				if ev.Branch != got {
					t.Errorf("Branch = %q, want %q", ev.Branch, got)
				}
				return
			}
		case <-deadline:
			t.Fatal("did not receive EventBranchRenamed within 2s")
		}
	}
}

// TestSmartBranchRename_CustomTemplateRendered verifies that a user-provided
// BranchNamePrompt has the {prompt} token substituted with the user's prompt
// and the rendered string is what the namer receives.
func TestSmartBranchRename_CustomTemplateRendered(t *testing.T) {
	repo := setupTestRepo(t)
	settings := defaultTestSettings()
	settings.BranchNamePrompt = "You are naming a git branch. Use 2 words. {prompt} -- end"
	mgr := NewManager(repo, settings)
	defer mgr.Shutdown()

	stub := &stubNamer{result: "fix-login"}
	mgr.SetBranchNamer(stub.fn())

	cfg := Config{Name: "tmpl-render", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	const userPrompt = "fix the login redirect bug"
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  userPrompt,
	}); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}

	got := waitForBranch(t, sess, "refrain/fix-login")
	if got != "refrain/fix-login" {
		t.Fatalf("branch = %q, want refrain/fix-login", got)
	}

	const wantInstruction = "You are naming a git branch. Use 2 words. fix the login redirect bug -- end"
	if got := stub.instruction(); got != wantInstruction {
		t.Errorf("rendered instruction = %q, want %q", got, wantInstruction)
	}
}

// TestManager_StoresOriginalPromptOnFirstActionableHook verifies that the
// first actionable UserPromptSubmit stores the prompt on the session, and
// subsequent actionable prompts do not overwrite it.
func TestManager_StoresOriginalPromptOnFirstActionableHook(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "original-prompt-test", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 60")
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Drain the initial EventCreated event.
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	// Non-actionable prompt — must not store.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "  ",
	}); err != nil {
		t.Fatalf("SendEvent empty prompt: %v", err)
	}
	// Wait for the dispatcher to process the event.
	time.Sleep(150 * time.Millisecond)
	if sess.OriginalPrompt() != "" {
		t.Error("empty prompt must not be stored")
	}

	// Actionable prompt — must store.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "fix the auth bug",
	}); err != nil {
		t.Fatalf("SendEvent first prompt: %v", err)
	}
	// Wait for the dispatcher to process the event.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sess.OriginalPrompt() == "fix the auth bug" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := sess.OriginalPrompt(); got != "fix the auth bug" {
		t.Errorf("OriginalPrompt() = %q, want %q", got, "fix the auth bug")
	}

	// Second actionable prompt — must NOT overwrite.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "second prompt",
	}); err != nil {
		t.Fatalf("SendEvent second prompt: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if got := sess.OriginalPrompt(); got != "fix the auth bug" {
		t.Errorf("OriginalPrompt() after second hook = %q, want first prompt", got)
	}
}

// TestSmartBranchRename_TemplateWithoutPlaceholderAppendsPrompt verifies the
// defensive forgiveness: if a custom BranchNamePrompt forgets the {prompt}
// token, the user's prompt is appended on its own paragraph rather than
// silently dropped.
func TestSmartBranchRename_TemplateWithoutPlaceholderAppendsPrompt(t *testing.T) {
	repo := setupTestRepo(t)
	settings := defaultTestSettings()
	settings.BranchNamePrompt = "Header without placeholder"
	mgr := NewManager(repo, settings)
	defer mgr.Shutdown()

	stub := &stubNamer{result: "ok"}
	mgr.SetBranchNamer(stub.fn())

	cfg := Config{Name: "tmpl-append", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	const userPrompt = "do the thing"
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  userPrompt,
	}); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}

	if got := waitForBranch(t, sess, "refrain/ok"); got != "refrain/ok" {
		t.Fatalf("branch = %q, want refrain/ok", got)
	}

	const wantInstruction = "Header without placeholder\n\ndo the thing"
	if got := stub.instruction(); got != wantInstruction {
		t.Errorf("rendered instruction = %q, want %q", got, wantInstruction)
	}
}

// stubTaskSummarizer is a TaskSummarizer that returns a fixed result and counts calls.
type stubTaskSummarizer struct {
	result string
	calls  atomic.Int32
	block  chan struct{} // if non-nil, blocks on receive before returning
}

func (s *stubTaskSummarizer) fn() TaskSummarizer {
	return func(ctx context.Context, prompt string) (string, error) {
		s.calls.Add(1)
		if s.block != nil {
			select {
			case <-s.block:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return s.result, nil
	}
}

// waitForTaskSummary polls sess.HasTaskSummary() until it returns true or the deadline elapses.
func waitForTaskSummary(t *testing.T, sess *Session, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if sess.HasTaskSummary() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// TestTaskSummarizerTriggeredOnFirstActionablePrompt verifies that the first
// actionable UserPromptSubmit causes the TaskSummarizer goroutine to run and
// sets sess.HasTaskSummary() to true with the expected summary text.
func TestTaskSummarizerTriggeredOnFirstActionablePrompt(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	stub := &stubTaskSummarizer{result: "fix the broken login flow"}
	mgr.SetTaskSummarizer(stub.fn())
	// Disable branch namer to keep the test focused on task summary.
	mgr.SetBranchNamer(func(_ context.Context, _ string) (string, error) {
		return "stub-branch", nil
	})

	cfg := Config{Name: "task-summary-test", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "fix the broken login flow",
	}); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}

	if !waitForTaskSummary(t, sess, 3*time.Second) {
		t.Fatal("HasTaskSummary() did not become true within 3s")
	}
	if got := sess.TaskSummary(); got != "fix the broken login flow" {
		t.Errorf("TaskSummary() = %q, want %q", got, "fix the broken login flow")
	}
	if stub.calls.Load() != 1 {
		t.Errorf("summarizer call count = %d, want 1", stub.calls.Load())
	}
}

// TestTaskSummarizerSkipsNonActionablePrompt verifies that slash-only and
// empty prompts do not trigger the task summarizer.
func TestTaskSummarizerSkipsNonActionablePrompt(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	stub := &stubTaskSummarizer{result: "should not be called"}
	mgr.SetTaskSummarizer(stub.fn())
	mgr.SetBranchNamer(nil)

	cfg := Config{Name: "task-summary-skip", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	// Non-actionable: slash-only prompt.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "/clear",
	}); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	if sess.HasTaskSummary() {
		t.Error("slash-only prompt should not set HasTaskSummary")
	}
	if stub.calls.Load() != 0 {
		t.Errorf("summarizer should not be called for slash-only prompt, got %d", stub.calls.Load())
	}
}

// TestTaskSummarizerNilDisablesFeature verifies that SetTaskSummarizer(nil)
// disables the feature gracefully — no panic, no goroutine spawned.
func TestTaskSummarizerNilDisablesFeature(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	mgr.SetTaskSummarizer(nil)
	mgr.SetBranchNamer(nil)

	cfg := Config{Name: "task-summary-nil", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "do some real work",
	}); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	if sess.HasTaskSummary() {
		t.Error("nil summarizer should not set HasTaskSummary")
	}
}

// TestTaskSummarizerDoubleDispatchGated verifies that a second UserPromptSubmit
// arriving while the first summarizer call is still running does not dispatch
// a second goroutine.
func TestTaskSummarizerDoubleDispatchGated(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	release := make(chan struct{})
	stub := &stubTaskSummarizer{result: "slow-summary", block: release}
	mgr.SetTaskSummarizer(stub.fn())
	mgr.SetBranchNamer(func(_ context.Context, _ string) (string, error) {
		return "stub", nil
	})

	cfg := Config{Name: "task-summary-gate", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "first prompt",
	}); err != nil {
		t.Fatalf("SendEvent 1: %v", err)
	}

	// Wait for the first call to actually enter the stub.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && stub.calls.Load() < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	if stub.calls.Load() != 1 {
		t.Fatal("first summarizer call did not start")
	}

	// Send a second prompt — it must NOT trigger a second summarizer call.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "second prompt (should be gated)",
	}); err != nil {
		t.Fatalf("SendEvent 2: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if got := stub.calls.Load(); got != 1 {
		t.Errorf("summarizer call count = %d, want 1 (second prompt should be gated)", got)
	}

	// Release the first call and check HasTaskSummary becomes true.
	close(release)
	if !waitForTaskSummary(t, sess, 2*time.Second) {
		t.Fatal("HasTaskSummary() did not become true after release")
	}
	if n := stub.calls.Load(); n != 1 {
		t.Errorf("final summarizer call count = %d, want 1", n)
	}
}

// TestTaskSummarizerShutdownCancelsInflight verifies that Manager.Shutdown
// cancels the in-flight summarizer goroutine so it doesn't outlive the manager.
func TestTaskSummarizerShutdownCancelsInflight(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())

	// Block the summarizer forever — only ctx cancellation should release it.
	stub := &stubTaskSummarizer{result: "never-returns", block: make(chan struct{})}
	mgr.SetTaskSummarizer(stub.fn())
	mgr.SetBranchNamer(func(_ context.Context, _ string) (string, error) {
		return "stub", nil
	})

	cfg := Config{Name: "task-summary-shutdown", Task: "test", Rows: 24, Cols: 80}
	_, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "trigger a summary that will block",
	}); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}

	// Wait for the goroutine to actually enter the summarizer.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && stub.calls.Load() < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	if stub.calls.Load() < 1 {
		t.Fatal("summarizer did not run before shutdown")
	}

	done := make(chan struct{})
	var once sync.Once
	go func() {
		defer once.Do(func() { close(done) })
		mgr.Shutdown()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not return — summarizer goroutine likely leaked")
	}
}

// TestSmartBranchRename_RetryUsesOriginalPrompt verifies that when the namer
// fails on the first prompt and a second UserPromptSubmit arrives with
// different text, the namer is re-invoked with the ORIGINAL prompt text rather
// than the second prompt. This preserves the user's original intent across
// retries (which would otherwise be lost when Haiku exhausted its first-prompt
// retry budget and a follow-up prompt landed before the gate cleared).
func TestSmartBranchRename_RetryUsesOriginalPrompt(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	failing := &stubNamer{err: errors.New("haiku transient")}
	mgr.SetBranchNamer(failing.fn())

	cfg := Config{Name: "retry-orig-prompt", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	const originalPrompt = "implement focus mode pomodoro timer"
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  originalPrompt,
	}); err != nil {
		t.Fatalf("SendEvent first: %v", err)
	}
	// Wait for the namer to be called at least once AND for the rename
	// goroutine to release the in-flight gate. Polling both conditions
	// avoids a timing race on slow CI hosts where a fixed sleep would let
	// the follow-up prompt arrive before TryStartRename is reset.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if failing.calls.Load() >= 1 && !sess.IsRenaming() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if sess.IsRenaming() {
		t.Fatal("rename gate did not clear after first-prompt failure")
	}
	if sess.HasClaudeName() {
		t.Fatal("Session.HasClaudeName should stay false after namer error")
	}

	// Swap in a stub that records its instruction so we can assert which
	// prompt drove the retry.
	good := &stubNamer{result: "focus-mode-pomodoro"}
	mgr.SetBranchNamer(good.fn())

	const followUpPrompt = "now also wire up break reminders"
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  followUpPrompt,
	}); err != nil {
		t.Fatalf("SendEvent retry: %v", err)
	}

	got := waitForBranch(t, sess, "refrain/focus-mode-pomodoro")
	if got != "refrain/focus-mode-pomodoro" {
		t.Errorf("retry branch = %q, want refrain/focus-mode-pomodoro", got)
	}
	instr := good.instruction()
	if !strings.Contains(instr, originalPrompt) {
		t.Errorf("retry instruction did not include original prompt:\n got=%q\nwant substring %q", instr, originalPrompt)
	}
	if strings.Contains(instr, followUpPrompt) {
		t.Errorf("retry instruction unexpectedly included follow-up prompt:\n got=%q", instr)
	}
}

// TestTaskSummarizerRetriesOnError verifies that subprocess errors in the
// summarizer trigger the retry helper, that each attempt is logged with
// kind=summary, and that the final outcome line records the failure. The
// session's TaskSummary stays empty (current swallow-and-coerce behavior).
func TestTaskSummarizerRetriesOnError(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	// Stub returns an error on every call so all attempts fail.
	var calls atomic.Int32
	mgr.SetTaskSummarizer(func(ctx context.Context, prompt string) (string, error) {
		calls.Add(1)
		return "", errors.New("haiku exec failed")
	})
	mgr.SetBranchNamer(func(_ context.Context, _ string) (string, error) {
		return "stub-branch", nil
	})

	cfg := Config{Name: "summary-retries", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "rewrite the websocket reconnect logic",
	}); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}

	// Wait for HasTaskSummary to flip true: the goroutine always calls
	// SetTaskSummary at the end so the gate clears even after exhausting retries.
	if !waitForTaskSummary(t, sess, 5*time.Second) {
		t.Fatal("HasTaskSummary did not become true after retries exhausted")
	}
	if got := sess.TaskSummary(); got != "" {
		t.Errorf("TaskSummary = %q, want empty after retries exhausted", got)
	}
	if got := calls.Load(); got != int32(haikuSummaryAttempts) {
		t.Errorf("summarizer call count = %d, want %d", got, haikuSummaryAttempts)
	}

	// Inspect the haiku log: expect attempts and a final outcome line, all
	// tagged kind=summary.
	body, err := os.ReadFile(haikuLogPath(repo))
	if err != nil {
		t.Fatalf("read haiku.log: %v", err)
	}
	attemptCount := 0
	outcomeCount := 0
	for _, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
		if !strings.Contains(line, "kind=summary") {
			continue
		}
		switch {
		case strings.Contains(line, " attempt="):
			attemptCount++
		case strings.Contains(line, "status=fail") || strings.Contains(line, "status=ok"):
			outcomeCount++
		}
	}
	if attemptCount != haikuSummaryAttempts {
		t.Errorf("kind=summary attempt lines = %d, want %d\nlog:\n%s", attemptCount, haikuSummaryAttempts, body)
	}
	if outcomeCount != 1 {
		t.Errorf("kind=summary outcome lines = %d, want 1\nlog:\n%s", outcomeCount, body)
	}
}

// TestTaskSummarizerSucceedsAfterTransientError verifies that a transient
// error on the first attempt is recovered by a successful second attempt,
// and that the session ends up with the summary text.
func TestTaskSummarizerSucceedsAfterTransientError(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	var calls atomic.Int32
	mgr.SetTaskSummarizer(func(ctx context.Context, prompt string) (string, error) {
		n := calls.Add(1)
		if n == 1 {
			return "", errors.New("first call flakes")
		}
		return "rewrite websocket reconnect", nil
	})
	mgr.SetBranchNamer(func(_ context.Context, _ string) (string, error) {
		return "stub-branch", nil
	})

	cfg := Config{Name: "summary-recover", Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-mgr.Events():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for EventCreated")
	}

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindUserPromptSubmit,
		AgentID: ag.ID,
		Prompt:  "rewrite websocket reconnect",
	}); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}

	if !waitForTaskSummary(t, sess, 5*time.Second) {
		t.Fatal("HasTaskSummary did not become true")
	}
	if got := sess.TaskSummary(); got != "rewrite websocket reconnect" {
		t.Errorf("TaskSummary = %q, want %q", got, "rewrite websocket reconnect")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("summarizer call count = %d, want 2", got)
	}
}

// TestManager_StopHookRefreshesCommitTaskCount verifies that a KindStop event
// for a session with a plan triggers a background refresh of the commit-task
// cache and that CommitTaskCount() converges to the correct values.
func TestManager_StopHookRefreshesCommitTaskCount(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Write a plan so the Stop handler fires the refresh (the gate is plan
	// presence, not lifecycle phase — rollback design §4.1).
	if err := sess.WritePlan("# Goal\nx\n\n## Tasks\n- [ ] first\n- [ ] second\n"); err != nil {
		t.Fatal(err)
	}

	// Make two task commits in the worktree.
	makeTestCommitWithTaskTrailer(t, sess.Worktree.Path, "feat: first", 1)
	makeTestCommitWithTaskTrailer(t, sess.Worktree.Path, "feat: second", 2)

	// Send a Stop event — dispatcher should fire RefreshCommitTaskCount in a goroutine.
	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindStop,
		AgentID: ag.ID,
	}); err != nil {
		t.Fatalf("SendEvent Stop: %v", err)
	}

	// Poll until cache is populated (goroutine may lag a few ms).
	waitForCondition(t, 3*time.Second, func() bool {
		done, maxIdx := sess.CommitTaskCount()
		return done == 2 && maxIdx == 2
	})

	done, maxIdx := sess.CommitTaskCount()
	if done != 2 || maxIdx != 2 {
		t.Errorf("CommitTaskCount() = (%d, %d), want (2, 2)", done, maxIdx)
	}
}

// TestManager_StopHookSkipsRefreshWithoutPlan verifies that a KindStop event
// for a session with no plan.md does NOT overwrite a previously-seeded
// commit-task cache. This pins the HasPlan guard in dispatchHookEvents
// (rollback design §4.1: the refresh is gated on plan presence, not phase)
// against regression.
func TestManager_StopHookSkipsRefreshWithoutPlan(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Seed the cache so we can detect if it gets overwritten.
	sess.SetCommitTaskCountForTest(7, 7)

	// Make commits that would produce a smaller count if incorrectly refreshed.
	makeTestCommitWithTaskTrailer(t, sess.Worktree.Path, "feat: should not be counted", 1)

	if err := hook.SendEvent(mgr.HookSocketPath(), hook.Event{
		Kind:    hook.KindStop,
		AgentID: ag.ID,
	}); err != nil {
		t.Fatalf("SendEvent Stop: %v", err)
	}

	// Wait long enough for any goroutine to have run if incorrectly spawned.
	time.Sleep(200 * time.Millisecond)

	done, maxIdx := sess.CommitTaskCount()
	if done != 7 || maxIdx != 7 {
		t.Errorf("CommitTaskCount() = (%d, %d) after plan-less Stop, want (7, 7) — cache must not be overwritten", done, maxIdx)
	}
}
