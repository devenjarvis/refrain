package agent

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	xvt "github.com/charmbracelet/x/vt"
	"github.com/devenjarvis/baton/internal/config"
	"github.com/devenjarvis/baton/internal/hook"
)

// hookEvent is a test helper that builds a hook.Event for a given CLI-name event.
func hookEvent(kind string) hook.Event {
	return hook.Event{Kind: hook.Kind(kind)}
}

// hookEventWithSessionID builds a SessionStart event carrying a session ID.
func hookEventWithSessionID(kind, sessionID string) hook.Event {
	return hook.Event{Kind: hook.Kind(kind), SessionID: sessionID}
}

// setupTestRepo creates a temporary git repo with an initial commit.
func setupTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "commit.gpgsign", "false"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup %v: %v\n%s", args, err, out)
		}
	}

	// Create initial file and commit.
	if err := os.WriteFile(dir+"/README.md", []byte("# Test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "initial"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup %v: %v\n%s", args, err, out)
		}
	}

	return dir
}

func defaultTestSettings() config.ResolvedSettings {
	// Branch renaming always goes through Manager.branchNamer. Tests that
	// exercise rename behavior MUST inject a stub via SetBranchNamer before
	// dispatching an actionable UserPromptSubmit — otherwise the default
	// DefaultBranchNamer() will spawn the real `claude` binary if it is on
	// PATH (true on developer machines), making tests slow and non-hermetic.
	return config.Resolve(nil, nil)
}

func TestAgentRenderContainsOutput(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "test-echo", Task: "echo hello", Rows: 24, Cols: 80}
	a, err := mgr.CreateWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "echo hello; sleep 0.5")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for output.
	time.Sleep(500 * time.Millisecond)

	render := a.Render()
	if !strings.Contains(render, "hello") {
		t.Errorf("expected render to contain 'hello', got: %q", render)
	}
}

func TestAgentStatusTransitions(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "test-status", Task: "test", Rows: 24, Cols: 80}
	a, err := mgr.CreateWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "echo started; sleep 0.3")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Should start as Starting.
	initialStatus := a.Status()
	if initialStatus != StatusStarting && initialStatus != StatusActive {
		t.Errorf("expected Starting or Active initially, got %s", initialStatus)
	}

	// Wait for output to trigger Active.
	time.Sleep(300 * time.Millisecond)
	if s := a.Status(); s != StatusActive && s != StatusDone {
		t.Errorf("expected Active or Done after output, got %s", s)
	}

	// Wait for process to exit.
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for agent to finish")
	}

	if s := a.Status(); s != StatusDone {
		t.Errorf("expected Done after exit, got %s", s)
	}
}

func TestMultipleSessionsUniqueWorktrees(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	sessions := make([]*Session, 3)
	for i := 0; i < 3; i++ {
		cfg := Config{Task: "test", Rows: 24, Cols: 80}
		sess, _, err := mgr.CreateSessionWithCommand(cfg, func(n string) *exec.Cmd {
			return exec.Command("bash", "-c", "sleep 2")
		})
		if err != nil {
			t.Fatal(err)
		}
		sessions[i] = sess
	}

	// Check unique worktree paths.
	paths := make(map[string]bool)
	for _, s := range sessions {
		if paths[s.Worktree.Path] {
			t.Errorf("duplicate worktree path: %s", s.Worktree.Path)
		}
		paths[s.Worktree.Path] = true
	}

	if mgr.AgentCount() != 3 {
		t.Errorf("expected 3 agents, got %d", mgr.AgentCount())
	}
}

func TestKillAndCleanup(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "test-kill", Task: "test", Rows: 24, Cols: 80}
	sess, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 60")
	})
	if err != nil {
		t.Fatal(err)
	}

	wtPath := sess.Worktree.Path

	if err := mgr.KillSession(sess.ID); err != nil {
		t.Fatal(err)
	}

	// Worktree directory should be gone.
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("expected worktree dir to be removed, but it still exists")
	}

	if mgr.AgentCount() != 0 {
		t.Errorf("expected 0 agents after kill, got %d", mgr.AgentCount())
	}
}

func TestConfig_BypassPermissionsField(t *testing.T) {
	// Verify the BypassPermissions field exists and can be set
	cfg := Config{
		Name:              "test",
		Task:              "do something",
		Rows:              24,
		Cols:              80,
		BypassPermissions: true,
	}
	if !cfg.BypassPermissions {
		t.Error("BypassPermissions field should be settable to true")
	}

	cfg2 := Config{Name: "test2", Task: "task"}
	if cfg2.BypassPermissions {
		t.Error("BypassPermissions should default to false")
	}
}

func TestIdleSuppressedWhileTyping(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "test-idle-typing", Task: "test", Rows: 24, Cols: 80}
	a, err := mgr.CreateWithCommand(cfg, func(name string) *exec.Cmd {
		// cat reads stdin forever, producing initial output then waiting.
		return exec.Command("bash", "-c", "echo ready; cat")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for initial output to set status to Active.
	time.Sleep(500 * time.Millisecond)
	if s := a.Status(); s != StatusActive {
		t.Fatalf("expected Active after output, got %s", s)
	}

	// Simulate user typing every 500ms for 5 seconds (well past the 3s idle timeout).
	for i := 0; i < 10; i++ {
		a.SendText("x")
		time.Sleep(500 * time.Millisecond)
	}

	// Agent should still be Active because user input keeps it non-idle.
	if s := a.Status(); s != StatusActive {
		t.Errorf("expected Active while typing, got %s", s)
	}
}

func TestIdleDrivenByStopHook(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "test-idle-via-hook", Task: "test", Rows: 24, Cols: 80}
	a, err := mgr.CreateWithCommand(cfg, func(name string) *exec.Cmd {
		// Produce output then stay alive. Without a hook, Active must persist.
		return exec.Command("bash", "-c", "echo ready; sleep 60")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for initial output.
	time.Sleep(500 * time.Millisecond)
	if s := a.Status(); s != StatusActive {
		t.Fatalf("expected Active after output, got %s", s)
	}

	// With no hook, Active MUST NOT flip to Idle on its own.
	time.Sleep(4 * time.Second)
	if s := a.Status(); s != StatusActive {
		t.Errorf("expected Active without hook event, got %s", s)
	}

	// Simulated Stop hook flips the agent to Idle.
	a.OnHookEvent(hookEvent("stop"))
	if s := a.Status(); s != StatusIdle {
		t.Errorf("expected Idle after Stop hook, got %s", s)
	}
}

func TestSessionStartHookCapturesSessionID(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "test-session-start", Task: "test", Rows: 24, Cols: 80}
	a, err := mgr.CreateWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 2")
	})
	if err != nil {
		t.Fatal(err)
	}

	changed := a.OnHookEvent(hookEventWithSessionID("session-start", "uuid-abc-123"))
	if !changed {
		t.Error("expected status change on SessionStart from Starting")
	}
	if s := a.Status(); s != StatusActive {
		t.Errorf("expected Active after SessionStart, got %s", s)
	}
	if got := a.ClaudeSessionID(); got != "uuid-abc-123" {
		t.Errorf("expected session ID uuid-abc-123, got %q", got)
	}
}

func TestStopHookClearsComposing(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "test-stop-hook", Task: "test", Rows: 24, Cols: 80}
	a, err := mgr.CreateWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "echo ready; cat")
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(300 * time.Millisecond)
	a.SendText("hello")
	a.mu.RLock()
	if !a.composing {
		a.mu.RUnlock()
		t.Fatal("expected composing true before Stop hook")
	}
	a.mu.RUnlock()

	a.OnHookEvent(hookEvent("stop"))

	a.mu.RLock()
	if a.composing {
		a.mu.RUnlock()
		t.Error("expected composing cleared after Stop hook")
	}
	a.mu.RUnlock()
}

func TestComposingClearedOnEnter(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "test-composing-clear", Task: "test", Rows: 24, Cols: 80}
	a, err := mgr.CreateWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "echo ready; cat")
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(500 * time.Millisecond)

	// SendText sets composing = true.
	a.SendText("hello")
	a.mu.RLock()
	if !a.composing {
		a.mu.RUnlock()
		t.Fatal("expected composing to be true after SendText")
	}
	a.mu.RUnlock()

	// SendKey(Enter) clears composing.
	a.SendKey(xvt.KeyPressEvent{Code: xvt.KeyEnter})
	a.mu.RLock()
	if a.composing {
		a.mu.RUnlock()
		t.Fatal("expected composing to be false after Enter")
	}
	a.mu.RUnlock()
}

func TestPasteSetsComposing(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "test-paste", Task: "test", Rows: 24, Cols: 80}
	a, err := mgr.CreateWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "echo ready; cat")
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(500 * time.Millisecond)

	// Paste sets composing = true and updates lastInput.
	a.Paste("pasted content")
	a.mu.RLock()
	if !a.composing {
		a.mu.RUnlock()
		t.Fatal("expected composing to be true after Paste")
	}
	if a.lastInput.IsZero() {
		a.mu.RUnlock()
		t.Fatal("expected lastInput to be set after Paste")
	}
	a.mu.RUnlock()
}

func TestNewShellAgent(t *testing.T) {
	dir := t.TempDir()

	cfg := Config{Rows: 24, Cols: 80}
	a, err := newShellAgent("test-shell-1", cfg, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Kill()

	// Verify IsShell flag.
	if !a.IsShell {
		t.Error("expected IsShell to be true")
	}
	if a.Name != "shell" {
		t.Errorf("expected Name 'shell', got %q", a.Name)
	}
	if a.GetDisplayName() != "shell" {
		t.Errorf("expected display name 'shell', got %q", a.GetDisplayName())
	}

	// Send a command to trigger output.
	a.SendText("echo hello\n")
	time.Sleep(500 * time.Millisecond)

	// Should transition to Active on output.
	if s := a.Status(); s != StatusActive {
		t.Errorf("expected Active after shell output, got %s", s)
	}

	// Verify output appears in render.
	render := a.Render()
	if !strings.Contains(render, "hello") {
		t.Errorf("expected render to contain 'hello', got: %q", render)
	}

	// Shell agents should NOT transition to Idle (no statusLoop).
	time.Sleep(4 * time.Second)
	if s := a.Status(); s == StatusIdle {
		t.Error("shell agent should not transition to Idle (no statusLoop)")
	}
}

func TestNaturalExitCleansUpGoroutines(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "test-natural-exit", Task: "test", Rows: 24, Cols: 80}
	a, err := mgr.CreateWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "echo done")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for the agent to exit naturally.
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for agent to finish")
	}

	// writeLoopDone should already be closed (goroutine cleaned up).
	select {
	case <-a.writeLoopDone:
	default:
		t.Error("writeLoopDone should be closed after natural exit")
	}

	if s := a.Status(); s != StatusDone {
		t.Errorf("expected Done, got %s", s)
	}
}

func TestKillAfterNaturalExitDoesNotPanic(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "test-kill-after-exit", Task: "test", Rows: 24, Cols: 80}
	a, err := mgr.CreateWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "echo done")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for natural exit.
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for agent to finish")
	}

	// Kill on an already-exited agent must not panic.
	a.Kill()
}

func TestChimedForTurnResetsOnEnterOnly(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "test-chime-turn", Task: "test", Rows: 24, Cols: 80}
	a, err := mgr.CreateWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "echo ready; cat")
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(300 * time.Millisecond)

	// MarkChimedForTurn flips the flag.
	a.MarkChimedForTurn()
	if !a.ChimedForTurn() {
		t.Fatal("expected ChimedForTurn true after MarkChimedForTurn")
	}

	// SendText must NOT reset the flag.
	a.SendText("hello")
	if !a.ChimedForTurn() {
		t.Error("expected ChimedForTurn to remain true after SendText")
	}

	// Paste must NOT reset the flag.
	a.Paste("pasted")
	if !a.ChimedForTurn() {
		t.Error("expected ChimedForTurn to remain true after Paste")
	}

	// Non-Enter keys must NOT reset the flag.
	a.SendKey(xvt.KeyPressEvent{Code: xvt.KeyBackspace})
	if !a.ChimedForTurn() {
		t.Error("expected ChimedForTurn to remain true after Backspace")
	}

	// Enter RESETS the flag.
	a.SendKey(xvt.KeyPressEvent{Code: xvt.KeyEnter})
	if a.ChimedForTurn() {
		t.Error("expected ChimedForTurn to reset to false after Enter")
	}
}

func TestShutdownCleansAll(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())

	for i := 0; i < 3; i++ {
		cfg := Config{Task: "test", Rows: 24, Cols: 80}
		_, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
			return exec.Command("bash", "-c", "sleep 60")
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	mgr.Shutdown()

	if mgr.AgentCount() != 0 {
		t.Errorf("expected 0 agents after shutdown, got %d", mgr.AgentCount())
	}
}

func TestStopHookSetsAskingQuestionOnTrailingQuestion(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "test-asking-q", Task: "test", Rows: 24, Cols: 80}
	a, err := mgr.CreateWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "printf 'Is this working?'; cat")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for the output to render into the terminal.
	time.Sleep(500 * time.Millisecond)

	a.OnHookEvent(hookEvent("stop"))

	if !a.AskingQuestion() {
		t.Error("expected AskingQuestion true after Stop hook with trailing '?'")
	}
}

func TestStopHookNoAskingQuestionWithoutTrailingQuestion(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "test-no-asking-q", Task: "test", Rows: 24, Cols: 80}
	a, err := mgr.CreateWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "printf 'Task complete.'; cat")
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(500 * time.Millisecond)

	a.OnHookEvent(hookEvent("stop"))

	if a.AskingQuestion() {
		t.Error("expected AskingQuestion false after Stop hook with non-question output")
	}
}

func TestBuildSpawnArgs_AgentModel(t *testing.T) {
	wt := t.TempDir()
	tests := []struct {
		name     string
		cfg      Config
		wantArgs []string
	}{
		{
			name:     "model prepended for default claude program",
			cfg:      Config{AgentModel: "claude-opus-4-7", BypassPermissions: true, Task: "do stuff"},
			wantArgs: []string{"--model", "claude-opus-4-7", "--dangerously-skip-permissions", "do stuff"},
		},
		{
			name:     "empty model no flag",
			cfg:      Config{BypassPermissions: true, Task: "hi"},
			wantArgs: []string{"--dangerously-skip-permissions", "hi"},
		},
		{
			name:     "model ignored for non-claude program",
			cfg:      Config{AgentProgram: "bash", AgentModel: "claude-opus-4-7", Task: "hi"},
			wantArgs: []string{"hi"},
		},
		{
			name:     "model without bypass or task",
			cfg:      Config{AgentModel: "claude-sonnet-4-6"},
			wantArgs: []string{"--model", "claude-sonnet-4-6"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Pass empty socketPath so buildHookArgs returns no --settings pair.
			got, err := buildSpawnArgs(tt.cfg, wt, "")
			if err != nil {
				t.Fatalf("buildSpawnArgs error: %v", err)
			}
			if len(got) != len(tt.wantArgs) {
				t.Fatalf("expected %d args, got %d: %v", len(tt.wantArgs), len(got), got)
			}
			for i, want := range tt.wantArgs {
				if got[i] != want {
					t.Errorf("arg[%d]: expected %q, got %q (full=%v)", i, want, got[i], got)
				}
			}
		})
	}
}

func TestBuildSpawnArgs_BuildSystemPrompt(t *testing.T) {
	wt := t.TempDir()
	tests := []struct {
		name     string
		cfg      Config
		wantArgs []string
	}{
		{
			name: "prompt set emits flag",
			cfg: Config{
				BuildSystemPrompt: "use TodoWrite and commit per task",
				BypassPermissions: true,
				Task:              "do work",
			},
			wantArgs: []string{
				"--append-system-prompt", "use TodoWrite and commit per task",
				"--dangerously-skip-permissions",
				"do work",
			},
		},
		{
			name:     "empty prompt no flag",
			cfg:      Config{BypassPermissions: true, Task: "hi"},
			wantArgs: []string{"--dangerously-skip-permissions", "hi"},
		},
		{
			name: "prompt ignored for non-claude program",
			cfg: Config{
				AgentProgram:      "bash",
				BuildSystemPrompt: "ignored",
				Task:              "hi",
			},
			wantArgs: []string{"hi"},
		},
		{
			name: "model and prompt both prepend, model first",
			cfg: Config{
				AgentModel:        "claude-opus-4-7",
				BuildSystemPrompt: "p",
				Task:              "t",
			},
			wantArgs: []string{
				"--model", "claude-opus-4-7",
				"--append-system-prompt", "p",
				"t",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildSpawnArgs(tt.cfg, wt, "")
			if err != nil {
				t.Fatalf("buildSpawnArgs error: %v", err)
			}
			if len(got) != len(tt.wantArgs) {
				t.Fatalf("expected %d args, got %d: %v", len(tt.wantArgs), len(got), got)
			}
			for i, want := range tt.wantArgs {
				if got[i] != want {
					t.Errorf("arg[%d]: expected %q, got %q (full=%v)", i, want, got[i], got)
				}
			}
		})
	}
}

func TestBuildResumeArgs_BuildSystemPrompt(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		sid      string
		wantArgs []string
	}{
		{
			name: "prompt set emits flag on resume",
			cfg: Config{
				BuildSystemPrompt: "use TodoWrite",
				BypassPermissions: true,
			},
			sid:      "abc123",
			wantArgs: []string{"--append-system-prompt", "use TodoWrite", "--dangerously-skip-permissions", "--resume", "abc123"},
		},
		{
			name:     "empty prompt no flag on resume",
			cfg:      Config{BypassPermissions: true},
			sid:      "abc123",
			wantArgs: []string{"--dangerously-skip-permissions", "--resume", "abc123"},
		},
		{
			name:     "prompt ignored for non-claude program on resume",
			cfg:      Config{AgentProgram: "bash", BuildSystemPrompt: "ignored"},
			sid:      "",
			wantArgs: []string{"--continue"},
		},
		{
			name: "model and prompt both prepend on resume, model first",
			cfg: Config{
				AgentModel:        "claude-opus-4-7",
				BuildSystemPrompt: "p",
			},
			sid:      "sid",
			wantArgs: []string{"--model", "claude-opus-4-7", "--append-system-prompt", "p", "--resume", "sid"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildResumeArgs(tt.cfg, tt.sid)
			if len(got) != len(tt.wantArgs) {
				t.Fatalf("expected %d args, got %d: %v", len(tt.wantArgs), len(got), got)
			}
			for i, want := range tt.wantArgs {
				if got[i] != want {
					t.Errorf("arg[%d]: expected %q, got %q (full=%v)", i, want, got[i], got)
				}
			}
		})
	}
}

func TestUserPromptSubmitClearsAskingQuestion(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Name: "test-clear-asking-q", Task: "test", Rows: 24, Cols: 80}
	a, err := mgr.CreateWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "echo ready; cat")
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(300 * time.Millisecond)

	// Set askingQuestion directly to simulate a prior Stop with trailing "?".
	a.mu.Lock()
	a.askingQuestion = true
	a.mu.Unlock()

	a.OnHookEvent(hookEvent("user-prompt-submit"))

	if a.AskingQuestion() {
		t.Error("expected AskingQuestion cleared after KindUserPromptSubmit")
	}
}
