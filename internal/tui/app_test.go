package tui

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	xvt "github.com/charmbracelet/x/vt"
	"github.com/devenjarvis/baton/internal/agent"
	"github.com/devenjarvis/baton/internal/audio"
	"github.com/devenjarvis/baton/internal/config"
)

func requireClaude(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not in PATH")
	}
}

// createAgent presses 'n' and executes the async create cmd, returning the updated app.
// If the terminal panel is already focused it presses Ctrl+E first so the 'n' key isn't
// forwarded to the agent.
func createAgent(t *testing.T, app App) App {
	t.Helper()

	// Return to list focus if terminal has focus so 'n' is handled by the app.
	if app.dashboard.panelFocus == focusTerminal {
		model, _ := app.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
		app = model.(App)
	}

	model, cmd := app.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	app = model.(App)

	if cmd == nil {
		t.Fatal("Expected cmd from 'n', got nil")
	}

	msg := cmd()
	model, _ = app.Update(msg)
	app = model.(App)

	return app
}

// addAgentToSession presses 'c' and executes the async add cmd, returning the updated app.
// If the terminal panel is already focused it presses Ctrl+E first so the 'c' key isn't
// forwarded to the agent.
func addAgentToSession(t *testing.T, app App) App {
	t.Helper()

	// Return to list focus if terminal has focus so 'c' is handled by the app.
	if app.dashboard.panelFocus == focusTerminal {
		model, _ := app.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
		app = model.(App)
	}

	model, cmd := app.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	app = model.(App)

	if cmd == nil {
		t.Fatal("Expected cmd from 'c', got nil")
	}

	msg := cmd()
	model, _ = app.Update(msg)
	app = model.(App)

	return app
}

func TestCreateAgentViaN(t *testing.T) {
	requireClaude(t)
	dir, err := os.MkdirTemp("", "baton-tui-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir, config.Resolve(nil, nil))
	defer mgr.Shutdown()

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir

	app = createAgent(t, app)

	t.Logf("After creation: view=%v, err=%q, agents=%d, dashboard=%d, focus=%v",
		app.view, app.err, mgr.AgentCount(), len(app.dashboard.agentItems()), app.dashboard.panelFocus)

	if app.view != ViewDashboard {
		t.Errorf("Expected ViewDashboard, got %v", app.view)
	}
	if app.err != "" {
		t.Errorf("Error: %s", app.err)
	}
	if mgr.AgentCount() != 1 {
		t.Errorf("Expected 1 agent, got %d", mgr.AgentCount())
	}
	if len(app.dashboard.agentItems()) != 1 {
		t.Errorf("Expected 1 dashboard agent, got %d", len(app.dashboard.agentItems()))
	}
	// After creation the terminal panel is auto-focused.
	if app.dashboard.panelFocus != focusTerminal {
		t.Errorf("Expected focusTerminal after creation, got %v", app.dashboard.panelFocus)
	}
	// Session should be present.
	sessions := mgr.ListSessions()
	if len(sessions) != 1 {
		t.Errorf("Expected 1 session, got %d", len(sessions))
	}
}

func TestCreateMultipleAgentsViaTUI(t *testing.T) {
	requireClaude(t)
	dir, err := os.MkdirTemp("", "baton-tui-multi-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir, config.Resolve(nil, nil))
	defer mgr.Shutdown()

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir

	// Create first session+agent
	t.Log("=== Creating session 1 ===")
	app = createAgent(t, app)
	t.Logf("After session1: view=%v, err=%q, agents=%d, dashboard=%d",
		app.view, app.err, mgr.AgentCount(), len(app.dashboard.agentItems()))

	if app.err != "" {
		t.Fatalf("Session 1 error: %s", app.err)
	}
	if mgr.AgentCount() != 1 {
		t.Fatalf("Expected 1 agent, got %d", mgr.AgentCount())
	}

	// Create second session+agent (createAgent presses Ctrl+E first to exit focusTerminal)
	t.Log("=== Creating session 2 ===")
	app = createAgent(t, app)
	t.Logf("After session2: view=%v, err=%q, agents=%d, dashboard=%d",
		app.view, app.err, mgr.AgentCount(), len(app.dashboard.agentItems()))

	if app.err != "" {
		t.Fatalf("Session 2 error: %s", app.err)
	}
	if mgr.AgentCount() != 2 {
		t.Fatalf("Expected 2 agents, got %d", mgr.AgentCount())
	}
	if len(app.dashboard.agentItems()) != 2 {
		t.Fatalf("Expected 2 dashboard agents, got %d", len(app.dashboard.agentItems()))
	}

	// Create third session+agent
	t.Log("=== Creating session 3 ===")
	app = createAgent(t, app)
	t.Logf("After session3: view=%v, err=%q, agents=%d, dashboard=%d",
		app.view, app.err, mgr.AgentCount(), len(app.dashboard.agentItems()))

	if app.err != "" {
		t.Fatalf("Session 3 error: %s", app.err)
	}
	if mgr.AgentCount() != 3 {
		t.Fatalf("Expected 3 agents, got %d", mgr.AgentCount())
	}
	if len(app.dashboard.agentItems()) != 3 {
		t.Fatalf("Expected 3 dashboard agents, got %d", len(app.dashboard.agentItems()))
	}

	// Should have 3 sessions.
	sessions := mgr.ListSessions()
	if len(sessions) != 3 {
		t.Fatalf("Expected 3 sessions, got %d", len(sessions))
	}

	t.Logf("SUCCESS: Created %d sessions with %d agents", len(sessions), len(app.dashboard.agentItems()))
}

func TestAddAgentToSessionViaC(t *testing.T) {
	requireClaude(t)
	dir, err := os.MkdirTemp("", "baton-tui-addagent-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir, config.Resolve(nil, nil))
	defer mgr.Shutdown()

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir

	// Create first session with 'n'.
	app = createAgent(t, app)
	if app.err != "" {
		t.Fatalf("Error creating session: %s", app.err)
	}
	if mgr.AgentCount() != 1 {
		t.Fatalf("Expected 1 agent, got %d", mgr.AgentCount())
	}

	sessions := mgr.ListSessions()
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}

	// Navigate to the session row or agent row (either works for 'c').
	// The agent row should be selected already after creation + esc.

	// Add second agent with 'c'.
	app = addAgentToSession(t, app)
	if app.err != "" {
		t.Fatalf("Error adding agent: %s", app.err)
	}

	if mgr.AgentCount() != 2 {
		t.Fatalf("Expected 2 agents, got %d", mgr.AgentCount())
	}
	// Should still be the same single session.
	sessions = mgr.ListSessions()
	if len(sessions) != 1 {
		t.Fatalf("Expected still 1 session after 'c', got %d", len(sessions))
	}
	if sessions[0].AgentCount() != 2 {
		t.Fatalf("Expected 2 agents in session, got %d", sessions[0].AgentCount())
	}
}

func TestPanelFocusSwitching(t *testing.T) {
	requireClaude(t)
	dir, err := os.MkdirTemp("", "baton-focus-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir, config.Resolve(nil, nil))
	defer mgr.Shutdown()

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir

	app = createAgent(t, app)
	if len(app.dashboard.agentItems()) == 0 {
		t.Fatal("Expected at least one agent")
	}

	// After creation the terminal is auto-focused.
	if app.dashboard.panelFocus != focusTerminal {
		t.Fatalf("Expected focusTerminal after creation, got %v", app.dashboard.panelFocus)
	}

	// Ctrl+E returns to focusList.
	model, _ := app.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	app = model.(App)
	if app.dashboard.panelFocus != focusList {
		t.Fatalf("Expected focusList after ctrl+e, got %v", app.dashboard.panelFocus)
	}

	// Right arrow enters focusTerminal.
	model, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	app = model.(App)
	if app.dashboard.panelFocus != focusTerminal {
		t.Fatalf("Expected focusTerminal after →, got %v", app.dashboard.panelFocus)
	}

	// Esc returns to focusList.
	model, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	app = model.(App)
	if app.dashboard.panelFocus != focusList {
		t.Fatalf("Expected focusList after esc, got %v", app.dashboard.panelFocus)
	}

	// Right arrow enters focusTerminal again.
	model, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	app = model.(App)
	if app.dashboard.panelFocus != focusTerminal {
		t.Fatalf("Expected focusTerminal after →, got %v", app.dashboard.panelFocus)
	}

	// Enter stays in focusTerminal (it forwards the key to the agent).
	model, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	app = model.(App)
	if app.dashboard.panelFocus != focusTerminal {
		t.Fatalf("Expected focusTerminal to persist after enter, got %v", app.dashboard.panelFocus)
	}
}

func TestActionKeysBlockedInFocusTerminal(t *testing.T) {
	requireClaude(t)
	dir, err := os.MkdirTemp("", "baton-block-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir, config.Resolve(nil, nil))
	defer mgr.Shutdown()

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir

	app = createAgent(t, app)

	// After creation the terminal is already focused.
	if app.dashboard.panelFocus != focusTerminal {
		t.Fatalf("Expected focusTerminal after creation, got %v", app.dashboard.panelFocus)
	}

	// Press "n" — should be forwarded to agent, NOT create a new agent.
	// panelFocus must stay focusTerminal and view must stay ViewDashboard.
	model, _ := app.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	app = model.(App)
	if app.view != ViewDashboard {
		t.Fatalf("Expected ViewDashboard (n forwarded to agent, not new-agent), got %v", app.view)
	}
	if app.dashboard.panelFocus != focusTerminal {
		t.Fatalf("Expected focusTerminal to persist after 'n', got %v", app.dashboard.panelFocus)
	}
}

func TestShiftEscForwardsEscapeToAgent(t *testing.T) {
	requireClaude(t)
	dir, err := os.MkdirTemp("", "baton-shiftesc-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir, config.Resolve(nil, nil))
	defer mgr.Shutdown()

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir

	app = createAgent(t, app)
	if app.dashboard.panelFocus != focusTerminal {
		t.Fatalf("Expected focusTerminal after creation, got %v", app.dashboard.panelFocus)
	}

	// Press shift+esc — should stay in focusTerminal (not exit).
	model, _ := app.Update(tea.KeyPressMsg{Code: tea.KeyEscape, Mod: tea.ModShift})
	app = model.(App)
	if app.dashboard.panelFocus != focusTerminal {
		t.Fatalf("Expected focusTerminal after shift+esc (should forward, not exit), got %v", app.dashboard.panelFocus)
	}

	// Press plain esc — should exit to focusList.
	model, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	app = model.(App)
	if app.dashboard.panelFocus != focusList {
		t.Fatalf("Expected focusList after esc, got %v", app.dashboard.panelFocus)
	}
}

func TestMouseClickSelectsListItem(t *testing.T) {
	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39

	// Directly populate the list with fake items (no real processes needed).
	app.dashboard.items = []listItem{
		{kind: listItemAgent, repoPath: "/fake/repo"},
		{kind: listItemAgent, repoPath: "/fake/repo"},
		{kind: listItemAgent, repoPath: "/fake/repo"},
	}

	if app.dashboard.selected != 0 {
		t.Fatalf("Expected selected=0 initially, got %d", app.dashboard.selected)
	}

	// Click item 1: Y = dashboardTopY(0) + 2 header rows + 1 = 3
	model, _ := app.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 5, Y: 3})
	app = model.(App)
	if app.dashboard.selected != 1 {
		t.Fatalf("Expected selected=1 after click, got %d", app.dashboard.selected)
	}
	if app.dashboard.panelFocus != focusList {
		t.Fatalf("Expected focusList after list click, got %v", app.dashboard.panelFocus)
	}

	// Click item 2: Y=4
	model, _ = app.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 5, Y: 4})
	app = model.(App)
	if app.dashboard.selected != 2 {
		t.Fatalf("Expected selected=2 after click, got %d", app.dashboard.selected)
	}

	// Click on title row (Y=0) — ignored (itemIndex = -2), selection unchanged.
	model, _ = app.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 5, Y: 0})
	app = model.(App)
	if app.dashboard.selected != 2 {
		t.Fatalf("Expected selected unchanged (=2) after title click, got %d", app.dashboard.selected)
	}

	// Click on separator row (Y=1) — ignored (itemIndex = -1), selection unchanged.
	model, _ = app.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 5, Y: 1})
	app = model.(App)
	if app.dashboard.selected != 2 {
		t.Fatalf("Expected selected unchanged (=2) after separator click, got %d", app.dashboard.selected)
	}

	// Right-click on item 0 — ignored (not MouseLeft), selection unchanged.
	model, _ = app.Update(tea.MouseClickMsg{Button: tea.MouseRight, X: 5, Y: 2})
	app = model.(App)
	if app.dashboard.selected != 2 {
		t.Fatalf("Expected selected unchanged (=2) after right-click, got %d", app.dashboard.selected)
	}

	// With an active error banner (dashboardTopY=1), item 0 is now at Y=2+1=3.
	// Click Y=3 should still select item 0, not item 1.
	app.dashboard.selected = 2
	app.setError("test error")
	model, _ = app.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 5, Y: 3})
	app = model.(App)
	if app.dashboard.selected != 0 {
		t.Fatalf("Expected selected=0 with error banner offset (Y=3 → item 0), got %d", app.dashboard.selected)
	}

	// With confirmQuit=true (dashboardTopY=1 when no error), item 1 is at Y=3+1=4.
	app.err = ""
	app.errTicks = 0
	app.dashboard.selected = 0
	app.confirmQuit = true
	model, _ = app.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 5, Y: 4})
	app = model.(App)
	if app.dashboard.selected != 1 {
		t.Fatalf("Expected selected=1 with confirmQuit offset (Y=4 → item 1), got %d", app.dashboard.selected)
	}
	// Mouse click should also clear confirmQuit.
	if app.confirmQuit {
		t.Fatalf("Expected confirmQuit=false after mouse click, got true")
	}
}

func TestMouseClickPreviewEntersFocus(t *testing.T) {
	requireClaude(t)
	dir, err := os.MkdirTemp("", "baton-mouse-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir, config.Resolve(nil, nil))
	defer mgr.Shutdown()

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir

	app = createAgent(t, app)
	if len(app.dashboard.agentItems()) == 0 {
		t.Fatal("Expected at least one agent")
	}

	// After creation the terminal is auto-focused; press Ctrl+E to return to list.
	model, _ := app.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	app = model.(App)
	if app.dashboard.panelFocus != focusList {
		t.Fatalf("Expected focusList after ctrl+e, got %v", app.dashboard.panelFocus)
	}

	// Click the preview panel (X >= 32) — should enter focusTerminal.
	model, _ = app.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 60, Y: 10})
	app = model.(App)
	if app.dashboard.panelFocus != focusTerminal {
		t.Fatalf("Expected focusTerminal after preview click, got %v", app.dashboard.panelFocus)
	}

	// Ctrl+E returns to focusList.
	model, _ = app.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	app = model.(App)
	if app.dashboard.panelFocus != focusList {
		t.Fatalf("Expected focusList after ctrl+e, got %v", app.dashboard.panelFocus)
	}
}

func TestMouseWheelScrollInFocusTerminal(t *testing.T) {
	requireClaude(t)
	dir, err := os.MkdirTemp("", "baton-wheel-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir, config.Resolve(nil, nil))
	defer mgr.Shutdown()

	// Create a session with an agent that writes 40 lines.
	sess, ag, err := mgr.CreateSessionWithCommand(agent.Config{
		Name:     "wheel-test",
		Task:     "test",
		RepoPath: dir,
		Rows:     24,
		Cols:     80,
	}, func(_ string) *exec.Cmd {
		return exec.Command("bash", "-c", "for i in $(seq 1 40); do echo Line $i; done; sleep 10")
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = sess // used for session creation

	// Wait for bash output to be processed into scrollback.
	time.Sleep(300 * time.Millisecond)

	if len(ag.ScrollbackLines()) == 0 {
		t.Fatal("Expected scrollback lines after bash output")
	}

	// Build an app with this agent directly in dashboard items.
	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir
	app.dashboard.items = []listItem{
		{kind: listItemAgent, repoPath: dir, session: sess, agent: ag},
	}
	app.dashboard.panelFocus = focusTerminal

	// a. WheelUp in focusTerminal increases scrollOffset by 3.
	app.dashboard.scrollOffset = 0
	model, _ := app.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	app = model.(App)
	if app.dashboard.scrollOffset != 3 {
		t.Fatalf("Expected scrollOffset=3 after WheelUp, got %d", app.dashboard.scrollOffset)
	}

	// b. WheelDown in focusTerminal decreases scrollOffset (clamped to 0).
	app.dashboard.scrollOffset = 3
	model, _ = app.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	app = model.(App)
	if app.dashboard.scrollOffset != 0 {
		t.Fatalf("Expected scrollOffset=0 after WheelDown from 3, got %d", app.dashboard.scrollOffset)
	}

	// Another WheelDown should not go negative.
	model, _ = app.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	app = model.(App)
	if app.dashboard.scrollOffset != 0 {
		t.Fatalf("Expected scrollOffset=0 after WheelDown from 0 (no negative), got %d", app.dashboard.scrollOffset)
	}

	// a2. WheelUp ceiling clamp: offset above sbLen is clamped to sbLen.
	app.dashboard.panelFocus = focusTerminal
	sbLen := len(ag.ScrollbackLines())
	app.dashboard.scrollOffset = sbLen + 100
	model, _ = app.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	app = model.(App)
	if app.dashboard.scrollOffset != sbLen {
		t.Fatalf("Expected scrollOffset clamped to %d, got %d", sbLen, app.dashboard.scrollOffset)
	}

	// c. WheelUp in focusList is a no-op.
	app.dashboard.panelFocus = focusList
	app.dashboard.scrollOffset = 0
	model, _ = app.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	app = model.(App)
	if app.dashboard.scrollOffset != 0 {
		t.Fatalf("Expected scrollOffset=0 (no-op in focusList), got %d", app.dashboard.scrollOffset)
	}
}

// waitForAltScreen polls ag.IsAltScreen() until true or the timeout expires.
func waitForAltScreen(t *testing.T, ag *agent.Agent) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ag.IsAltScreen() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("agent did not enter alt-screen within timeout")
}

// altScreenBashCmd emits DECSET 1049 (alt-screen) + 1002 (button-event mouse)
// + 1006 (SGR ext encoding) so the agent both enters alt-screen AND accepts
// SendMouse events, then sleeps so the process stays alive.
func altScreenBashCmd(_ string) *exec.Cmd {
	return exec.Command("bash", "-c", `printf '\033[?1049h\033[?1002h\033[?1006h'; sleep 10`)
}

func TestMouseWheelForwardsInAltScreen(t *testing.T) {
	dir, err := os.MkdirTemp("", "baton-alt-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir, config.Resolve(nil, nil))
	defer mgr.Shutdown()

	sess, ag, err := mgr.CreateSessionWithCommand(agent.Config{
		Name: "alt-wheel", Task: "test", RepoPath: dir, Rows: 24, Cols: 80,
	}, altScreenBashCmd)
	if err != nil {
		t.Fatal(err)
	}
	waitForAltScreen(t, ag)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir
	app.dashboard.items = []listItem{
		{kind: listItemAgent, repoPath: dir, session: sess, agent: ag},
	}
	app.dashboard.panelFocus = focusTerminal

	// Set a non-zero offset so we can tell the wheel branch didn't mutate it.
	app.dashboard.scrollOffset = 5
	model, _ := app.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 40, Y: 10})
	app = model.(App)
	if app.dashboard.scrollOffset != 5 {
		t.Fatalf("expected scrollOffset untouched (=5) when agent is in alt-screen, got %d", app.dashboard.scrollOffset)
	}

	model, _ = app.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 40, Y: 10})
	app = model.(App)
	if app.dashboard.scrollOffset != 5 {
		t.Fatalf("expected scrollOffset untouched on WheelDown in alt-screen, got %d", app.dashboard.scrollOffset)
	}
}

func TestScrollOffsetResetsOnAltScreenEntry(t *testing.T) {
	dir, err := os.MkdirTemp("", "baton-alt-reset-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir, config.Resolve(nil, nil))
	defer mgr.Shutdown()

	sess, ag, err := mgr.CreateSessionWithCommand(agent.Config{
		Name: "alt-reset", Task: "test", RepoPath: dir, Rows: 24, Cols: 80,
	}, altScreenBashCmd)
	if err != nil {
		t.Fatal(err)
	}
	waitForAltScreen(t, ag)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir
	app.dashboard.items = []listItem{
		{kind: listItemAgent, repoPath: dir, session: sess, agent: ag},
	}
	app.dashboard.selected = 0
	app.dashboard.scrollOffset = 42

	// A tickMsg drives the alt-screen-entered consumer. Since selected==ag,
	// the transition should reset scrollOffset to 0.
	model, _ := app.Update(tickMsg(time.Now()))
	app = model.(App)
	if app.dashboard.scrollOffset != 0 {
		t.Fatalf("expected scrollOffset reset to 0 after alt-screen entry tick, got %d", app.dashboard.scrollOffset)
	}
}

func TestErrorPersistsAcrossTicks(t *testing.T) {
	app := NewApp()
	app.width = 120
	app.height = 40

	// Set an error
	app.setError("test error")

	if app.err != "test error" {
		t.Fatalf("Expected error 'test error', got %q", app.err)
	}
	if app.errTicks != 30 {
		t.Fatalf("Expected 30 ticks, got %d", app.errTicks)
	}

	// Simulate a few ticks — error should persist
	for i := 0; i < 5; i++ {
		model, _ := app.Update(tickMsg{})
		app = model.(App)
	}

	if app.err != "test error" {
		t.Fatalf("Error should persist after 5 ticks, got %q", app.err)
	}
	if app.errTicks != 25 {
		t.Fatalf("Expected 25 ticks remaining, got %d", app.errTicks)
	}

	// Simulate remaining ticks
	for i := 0; i < 25; i++ {
		model, _ := app.Update(tickMsg{})
		app = model.(App)
	}

	if app.err != "" {
		t.Fatalf("Error should be cleared after 30 ticks, got %q", app.err)
	}
}

func TestNavigationSkipsSessionRows(t *testing.T) {
	sess := &agent.Session{Name: "test-session"}
	ag1 := &agent.Agent{Name: "agent-1"}
	ag2 := &agent.Agent{Name: "agent-2"}

	d := newDashboardModel()
	d.width = 120
	d.height = 39
	d.items = []listItem{
		{kind: listItemRepo, repoPath: "/fake/repo", repoName: "repo"},
		{kind: listItemSession, repoPath: "/fake/repo", session: sess},
		{kind: listItemAgent, repoPath: "/fake/repo", session: sess, agent: ag1},
		{kind: listItemSession, repoPath: "/fake/repo", session: sess},
		{kind: listItemAgent, repoPath: "/fake/repo", session: sess, agent: ag2},
	}
	d.selected = 0 // repo row

	// j from repo should skip session at index 1, land on agent at index 2.
	d, _ = d.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if d.selected != 2 {
		t.Fatalf("Expected selected=2 (agent), got %d", d.selected)
	}

	// j from agent at 2 should skip session at 3, land on agent at 4.
	d, _ = d.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if d.selected != 4 {
		t.Fatalf("Expected selected=4 (agent), got %d", d.selected)
	}

	// k from agent at 4 should skip session at 3, land on agent at 2.
	d, _ = d.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if d.selected != 2 {
		t.Fatalf("Expected selected=2 (agent), got %d", d.selected)
	}

	// k from agent at 2 should skip session at 1, land on repo at 0.
	d, _ = d.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if d.selected != 0 {
		t.Fatalf("Expected selected=0 (repo), got %d", d.selected)
	}
}

func TestMouseClickSessionSnapsToAgent(t *testing.T) {
	requireClaude(t)
	dir, err := os.MkdirTemp("", "baton-snap-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir, config.Resolve(nil, nil))
	defer mgr.Shutdown()

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir

	// Create a session with an agent.
	app = createAgent(t, app)
	if app.err != "" {
		t.Fatalf("Error creating session: %s", app.err)
	}

	// Return to list focus.
	model, _ := app.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	app = model.(App)

	// Find the session row index.
	sessionIdx := -1
	for i, item := range app.dashboard.items {
		if item.kind == listItemSession {
			sessionIdx = i
			break
		}
	}
	if sessionIdx < 0 {
		t.Fatal("No session row found in dashboard items")
	}

	// Click the session header row (Y = 2 header rows + sessionIdx).
	model, _ = app.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 5, Y: 2 + sessionIdx})
	app = model.(App)

	// Should snap from session to the nearest agent.
	if app.dashboard.items[app.dashboard.selected].kind == listItemSession {
		t.Fatalf("Expected selection to snap away from session row, but selected=%d is a session", app.dashboard.selected)
	}
}

// TestKillAgentAsyncMarksClosing verifies that pressing 'x' marks the agent in
// closingAgents and returns a non-nil Cmd without having called KillAgent
// synchronously — so the UI stays responsive while the teardown runs in a
// goroutine.
func TestKillAgentAsyncMarksClosing(t *testing.T) {
	dir, err := os.MkdirTemp("", "baton-killasync-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir, config.Resolve(nil, nil))
	defer mgr.Shutdown()

	// Create a long-running bash agent (sleep 999) so KillAgent has real work
	// to do and the async path is exercised.
	sess, ag, err := mgr.CreateSessionWithCommand(
		agent.Config{Name: "kill-async", Task: "test", Rows: 24, Cols: 80},
		func(_ string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 999") },
	)
	if err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir
	app.refreshAgentList()

	// Select the agent row.
	for i, it := range app.dashboard.items {
		if it.kind == listItemAgent && it.agent != nil && it.agent.ID == ag.ID {
			app.dashboard.selected = i
			break
		}
	}

	// Press 'x' — should mark closing and return a non-nil cmd. The agent
	// must still be present in the manager because the kill is now async.
	model, cmd := app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	app = model.(App)

	if cmd == nil {
		t.Fatal("Expected non-nil cmd from 'x' (async kill), got nil")
	}
	if !app.closingAgents[ag.ID] {
		t.Fatalf("Expected closingAgents[%s]=true, got %v", ag.ID, app.closingAgents)
	}
	// Dashboard should see the closing flag too.
	if !app.dashboard.closingAgents[ag.ID] {
		t.Fatalf("Expected dashboard.closingAgents[%s]=true", ag.ID)
	}
	// The manager still has the agent because the goroutine hasn't run yet.
	if mgr.Get(ag.ID) == nil {
		t.Fatalf("Expected agent still present in manager before cmd runs, got nil")
	}

	// Second press on the same agent is a no-op: must return nil cmd so we
	// don't double-dispatch.
	_, cmd2 := app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if cmd2 != nil {
		t.Fatal("Expected nil cmd from second 'x' on same agent (no double-dispatch)")
	}

	// Run the kill cmd; it should return a killResultMsg.
	msg := cmd()
	kr, ok := msg.(killResultMsg)
	if !ok {
		t.Fatalf("Expected killResultMsg from cmd, got %T", msg)
	}
	if kr.scope != killScopeAgent || kr.agentID != ag.ID || kr.sessionID != sess.ID {
		t.Fatalf("Unexpected killResultMsg: %+v", kr)
	}

	// Feed the killResultMsg back into the app — closing set should clear
	// and refreshAgentList should drop the agent from dashboard items.
	model, _ = app.Update(kr)
	app = model.(App)
	if app.closingAgents[ag.ID] {
		t.Fatalf("Expected closingAgents[%s] cleared after killResultMsg, still set", ag.ID)
	}
	for _, it := range app.dashboard.items {
		if it.kind == listItemAgent && it.agent != nil && it.agent.ID == ag.ID {
			t.Fatalf("Expected agent %s removed from dashboard after killResultMsg", ag.ID)
		}
	}
}

// TestKillResultMsgClearsClosingSet verifies the session-scope killResultMsg
// path: closingSessions and closingAgents are both cleared, diff stats cache
// is invalidated, and refreshAgentList runs.
func TestKillResultMsgClearsClosingSet(t *testing.T) {
	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39

	// Pre-populate closing sets and diff cache as if 'X' had dispatched.
	app.closingSessions["sess-1"] = true
	app.closingAgents["agent-a"] = true
	app.closingAgents["agent-b"] = true
	app.lastKnownStatus["agent-a"] = agent.StatusActive
	app.lastKnownStatus["agent-b"] = agent.StatusActive
	app.diffStatsCache["sess-1"] = &diffStatsEntry{}

	model, _ := app.Update(killResultMsg{
		scope:     killScopeSession,
		sessionID: "sess-1",
		agentIDs:  []string{"agent-a", "agent-b"},
	})
	app = model.(App)

	if app.closingSessions["sess-1"] {
		t.Fatal("Expected closingSessions[sess-1] cleared")
	}
	if app.closingAgents["agent-a"] || app.closingAgents["agent-b"] {
		t.Fatal("Expected closingAgents cleared for both agents")
	}
	if _, ok := app.diffStatsCache["sess-1"]; ok {
		t.Fatal("Expected diffStatsCache[sess-1] removed")
	}
	if _, ok := app.lastKnownStatus["agent-a"]; ok {
		t.Fatal("Expected lastKnownStatus cleared for agent-a")
	}
	// Dashboard should also see the cleared maps (refreshAgentList was called).
	if app.dashboard.closingSessions == nil {
		t.Fatal("Expected dashboard.closingSessions wired up after refresh")
	}
	if app.dashboard.closingSessions["sess-1"] {
		t.Fatal("Expected dashboard.closingSessions[sess-1] cleared after refresh")
	}
}

// TestKillResultMsgClearsClosingSetOnError verifies that if KillAgent returns
// an error, the closing-set entry is still cleared (so the row doesn't get
// stuck rendering "closing…") and the error is surfaced via setError.
func TestKillResultMsgClearsClosingSetOnError(t *testing.T) {
	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39

	app.closingAgents["agent-x"] = true

	model, _ := app.Update(killResultMsg{
		scope:     killScopeAgent,
		sessionID: "sess-1",
		agentID:   "agent-x",
		err:       errors.New("kill failed"),
	})
	app = model.(App)

	if app.closingAgents["agent-x"] {
		t.Fatal("Expected closingAgents[agent-x] cleared even on error")
	}
	if app.err != "kill failed" {
		t.Fatalf("Expected err %q, got %q", "kill failed", app.err)
	}
}

// TestRefreshAgentListRepoAffinity verifies that after killing the last agent
// in a repo's session, the cursor lands on that repo's header — not on an item
// in a different repo.
func TestRefreshAgentListRepoAffinity(t *testing.T) {
	requireClaude(t)
	// Set up two temp repos with git init.
	dir1, err := os.MkdirTemp("", "baton-repo1-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir1) }()
	dir2, err := os.MkdirTemp("", "baton-repo2-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir2) }()

	initRepo := func(dir string) {
		for _, args := range [][]string{
			{"git", "init"},
			{"git", "config", "commit.gpgsign", "false"},
			{"git", "commit", "--allow-empty", "-m", "init"},
		} {
			cmd := exec.Command(args[0], args[1:]...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("cmd %v in %s: %v\n%s", args, dir, err, out)
			}
		}
	}
	initRepo(dir1)
	initRepo(dir2)

	mgr1 := agent.NewManager(dir1, config.Resolve(nil, nil))
	defer mgr1.Shutdown()
	mgr2 := agent.NewManager(dir2, config.Resolve(nil, nil))
	defer mgr2.Shutdown()

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir1] = mgr1
	app.managers[dir2] = mgr2
	app.activeRepo = dir1

	app.cfg = &config.Config{
		Repos: []config.Repo{
			{Path: dir1, Name: "repo1"},
			{Path: dir2, Name: "repo2"},
		},
	}

	// Create an agent under repo1 (the first repo).
	sess1, _, err := mgr1.CreateSessionWithCommand(
		agent.Config{Name: "test-agent", Task: "test"},
		func(name string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 999") },
	)
	if err != nil {
		t.Fatal(err)
	}

	app.refreshAgentList()
	// List should be: [repo1, session1, agent1, repo2]
	// Select the agent in repo1 (index 2).
	for i, it := range app.dashboard.items {
		if it.kind == listItemAgent && it.repoPath == dir1 {
			app.dashboard.selected = i
			break
		}
	}
	if app.dashboard.items[app.dashboard.selected].repoPath != dir1 {
		t.Fatalf("setup: expected selected item in repo1")
	}

	// Now kill the session, simulating what happens when 'X' is pressed.
	_ = mgr1.KillSession(sess1.ID)
	// Give the agent time to exit.
	time.Sleep(200 * time.Millisecond)
	// Refresh the list — list becomes [repo1, repo2].
	// Without the fix, selected=2 clamps to 1 which is repo2 (wrong repo).
	app.refreshAgentList()

	// After refresh, the cursor should still be on a repo1 item (the repo header).
	if len(app.dashboard.items) == 0 {
		t.Fatal("expected non-empty items after refresh")
	}
	selected := app.dashboard.items[app.dashboard.selected]
	if selected.repoPath != dir1 {
		t.Errorf("cursor jumped to repo %q, want %q (selected=%d, kind=%v)",
			selected.repoPath, dir1, app.dashboard.selected, selected.kind)
	}
	if selected.kind != listItemRepo {
		t.Errorf("expected cursor on repo header, got kind=%v", selected.kind)
	}
}

// waitForCursorAt polls ag.CursorPosition() until it reports (wantX, wantY)
// or the deadline expires. Used by the preview-cursor placement test to wait
// for a positioning escape sequence to be processed by the emulator.
func waitForCursorAt(t *testing.T, ag *agent.Agent, wantX, wantY int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if x, y := ag.CursorPosition(); x == wantX && y == wantY {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	x, y := ag.CursorPosition()
	t.Fatalf("cursor did not reach (%d, %d) within timeout; got (%d, %d)", wantX, wantY, x, y)
}

// waitForCursorHidden polls ag.CursorVisible() until it reports false or the
// deadline expires.
func waitForCursorHidden(t *testing.T, ag *agent.Agent) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !ag.CursorVisible() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("cursor did not hide within timeout")
}

// TestPreviewCursorPlacement verifies that App.View() places the host cursor
// on the screen cell the agent's VT cursor occupies. Regression test for the
// off-by-one introduced in PR #95 (previewColOffset = 32 placed it one column
// too far right) — with previewColOffset = 31 the host cursor lands exactly
// on top of VT cell 0's screen column.
func TestPreviewCursorPlacement(t *testing.T) {
	dir, err := os.MkdirTemp("", "baton-cursorpos-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir, config.Resolve(nil, nil))
	defer mgr.Shutdown()

	// Position cursor at row 10, col 15 (1-indexed CUP) — that is VT cell
	// (14, 9) in 0-indexed coordinates. Sleeping keeps the bash process alive
	// so the agent stays in StatusActive.
	sess, ag, err := mgr.CreateSessionWithCommand(agent.Config{
		Name: "cursor-pos", Task: "test", RepoPath: dir, Rows: 24, Cols: 80,
	}, func(_ string) *exec.Cmd {
		return exec.Command("bash", "-c", `printf '\033[10;15H'; sleep 10`)
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForCursorAt(t, ag, 14, 9)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir
	app.dashboard.items = []listItem{
		{kind: listItemAgent, repoPath: dir, session: sess, agent: ag},
	}
	app.dashboard.panelFocus = focusTerminal

	v := app.View()
	if v.Cursor == nil {
		t.Fatal("expected non-nil view.Cursor in focusTerminal with visible cursor")
	}
	// Expected screen position:
	//   X = previewColOffset(31) + previewLeftBorder(1) + cursorX(14) = 46
	//   Y = dashboardTopY(0) + previewTopBorder(1) + previewMetadataRows(2) + cursorY(9) = 12
	// With the pre-fix off-by-one (previewColOffset=32) X would be 47, which
	// is the visible "shifted one to the right" symptom the user reported.
	if v.Cursor.X != 46 || v.Cursor.Y != 12 {
		t.Fatalf("expected cursor at screen (46, 12), got (%d, %d)", v.Cursor.X, v.Cursor.Y)
	}
}

// TestPreviewCursorHiddenWhenAgentHidesIt verifies that App.View() leaves
// view.Cursor nil after the inner program emits DECRST 25 (\e[?25l). Regression
// test for the doubled-cursor symptom: full-screen TUIs (Claude Code) draw
// their own visual cursor and hide the host terminal's — without this gate
// baton would draw an extra blinking block on top.
func TestPreviewCursorHiddenWhenAgentHidesIt(t *testing.T) {
	dir, err := os.MkdirTemp("", "baton-cursorhide-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir, config.Resolve(nil, nil))
	defer mgr.Shutdown()

	sess, ag, err := mgr.CreateSessionWithCommand(agent.Config{
		Name: "cursor-hide", Task: "test", RepoPath: dir, Rows: 24, Cols: 80,
	}, func(_ string) *exec.Cmd {
		return exec.Command("bash", "-c", `printf '\033[?25l'; sleep 10`)
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForCursorHidden(t, ag)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir
	app.dashboard.items = []listItem{
		{kind: listItemAgent, repoPath: dir, session: sess, agent: ag},
	}
	app.dashboard.panelFocus = focusTerminal

	v := app.View()
	if v.Cursor != nil {
		t.Fatalf("expected view.Cursor nil when agent hid cursor, got %+v", v.Cursor)
	}
}

// TestFocusModeToggle verifies that pressing 'f' toggles focusModeActive and
// increments focusModeSwitches. Toggling off (focus→review) also sets lastReviewAt.
func TestFocusModeToggle(t *testing.T) {
	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39

	if app.focusModeActive {
		t.Fatal("Expected focusModeActive=false initially")
	}

	// First toggle: off → on.
	model, _ := app.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	app = model.(App)
	if !app.focusModeActive {
		t.Fatal("Expected focusModeActive=true after first f press")
	}
	if app.focusModeSwitches != 1 {
		t.Errorf("Expected focusModeSwitches=1, got %d", app.focusModeSwitches)
	}
	// lastReviewAt should not be set when entering focus mode.
	if !app.lastReviewAt.IsZero() {
		t.Error("Expected lastReviewAt unset when entering focus mode")
	}

	// Second toggle: on → off (entering review). lastReviewAt should be set.
	model, _ = app.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	app = model.(App)
	if app.focusModeActive {
		t.Fatal("Expected focusModeActive=false after second f press")
	}
	if app.focusModeSwitches != 2 {
		t.Errorf("Expected focusModeSwitches=2, got %d", app.focusModeSwitches)
	}
	if app.lastReviewAt.IsZero() {
		t.Error("Expected lastReviewAt set when exiting focus mode (entering review)")
	}
}

// TestFocusModeStartupDefault verifies that when global settings have
// focus_mode_enabled=true, an initAppMsg starts the app with focus mode active.
// The setting is the startup default; the runtime `f` toggle is independent.
func TestFocusModeStartupDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	enabled := true
	gs := config.GlobalSettings{FocusModeEnabled: &enabled}
	dir := filepath.Join(home, ".baton")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data, err := json.Marshal(&gs)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	app := NewApp()
	if app.focusModeActive {
		t.Fatal("focusModeActive should default to false before init")
	}

	model, _ := app.Update(initAppMsg{cfg: &config.Config{}})
	app = model.(App)

	if !app.focusModeActive {
		t.Fatal("focusModeActive should be true after init when focus_mode_enabled=true")
	}
}

// TestGlobalConfigSaveDoesNotOverrideRuntimeFocus verifies that saving global
// settings does not toggle the live focus mode state. The setting is the
// startup default; the runtime `f` key is the only thing that switches it
// mid-session.
func TestGlobalConfigSaveDoesNotOverrideRuntimeFocus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	app := NewApp()
	app.focusModeActive = true // user pressed `f`

	enabled := false
	settings := &config.GlobalSettings{FocusModeEnabled: &enabled}
	model, _ := app.Update(globalConfigSaveMsg{settings: settings})
	app = model.(App)

	if !app.focusModeActive {
		t.Fatal("focusModeActive should remain true after save; the saved setting is the startup default, not a runtime override")
	}
}

// TestFocusModeChimeSuppression verifies that in focus mode StatusIdle events
// do not mark chime-for-turn, but StatusWaiting events still do.
// When an audio player is available, the test asserts ChimedForTurn state
// directly; otherwise it still validates the gate logic runs without error.
func TestFocusModeChimeSuppression(t *testing.T) {
	dir, err := os.MkdirTemp("", "baton-chime-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir, config.Resolve(nil, nil))
	defer mgr.Shutdown()

	sess, ag, err := mgr.CreateSessionWithCommand(agent.Config{
		Name: "chime-test", Task: "test", RepoPath: dir, Rows: 24, Cols: 80,
	}, func(_ string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 10")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate that the agent has received input, then reset ChimedForTurn so
	// the gate logic can fire in the expected direction.
	ag.SendKey(xvt.KeyPressEvent{Code: tea.KeyEnter})

	resolved := config.Resolve(nil, nil)
	resolved.AudioEnabled = true

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir
	app.resolvedCache[dir] = resolved
	app.dashboard.items = []listItem{
		{kind: listItemAgent, repoPath: dir, session: sess, agent: ag},
	}

	// Try to wire up a real audio player so MarkChimedForTurn() can actually
	// fire. Best-effort — if the environment has no audio device the player
	// will be nil and we fall back to the structural assertion below.
	if p, playerErr := audio.NewPlayer(); playerErr == nil {
		app.audioPlayer = p
		defer p.Close()
	}

	app.focusModeActive = true

	// Case 1: StatusIdle in focus mode — chime should be suppressed.
	// Reset chimed flag by simulating Enter (which resets ChimedForTurn).
	ag.SendKey(xvt.KeyPressEvent{Code: tea.KeyEnter})
	idleEvent := agentEventMsg{
		event: agent.Event{
			Type:    agent.EventStatusChanged,
			AgentID: ag.ID,
			Status:  agent.StatusIdle,
		},
		repoPath: dir,
	}
	model, _ := app.Update(idleEvent)
	app = model.(App)
	if !app.focusModeActive {
		t.Error("Expected focusModeActive unchanged after idle event")
	}
	if app.audioPlayer != nil && ag.ChimedForTurn() {
		t.Error("Expected ChimedForTurn=false after idle event in focus mode (chime suppressed)")
	}

	// Case 2: StatusWaiting in focus mode — chime should fire.
	ag.SendKey(xvt.KeyPressEvent{Code: tea.KeyEnter}) // reset ChimedForTurn
	waitEvent := agentEventMsg{
		event: agent.Event{
			Type:    agent.EventStatusChanged,
			AgentID: ag.ID,
			Status:  agent.StatusWaiting,
		},
		repoPath: dir,
	}
	model, _ = app.Update(waitEvent)
	app = model.(App)
	if !app.focusModeActive {
		t.Error("Expected focusModeActive unchanged after waiting event")
	}
	if app.audioPlayer != nil && !ag.ChimedForTurn() {
		t.Error("Expected ChimedForTurn=true after waiting event in focus mode (chime allowed)")
	}
}

func TestFocusMode_BacklogGate_WarnOnN(t *testing.T) {
	app := NewApp()
	two := 2
	app.globalSettings = &config.GlobalSettings{MaxReviewBacklog: &two}

	// Activate focus mode.
	model, _ := app.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	app = model.(App)

	// First n when no backlog — no warning, focusBacklogWarning stays false.
	model, _ = app.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	app = model.(App)
	if app.focusBacklogWarning {
		t.Error("focusBacklogWarning should not be set when backlog is below limit")
	}

	// Verify the warning is cleared when a non-n key is pressed after being set.
	app.focusBacklogWarning = true
	model, _ = app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	app = model.(App)
	if app.focusBacklogWarning {
		t.Error("focusBacklogWarning should be cleared by a non-n key press")
	}
}

func TestFocusMode_RKey_NoopWithEmptyQueue(t *testing.T) {
	app := NewApp()
	model, _ := app.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	app = model.(App)

	// r with no queued sessions should be a no-op.
	model, _ = app.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	app = model.(App)
	if app.dashboard.panelFocus == focusReview {
		t.Error("r with empty review queue should not enter focusReview")
	}
}

// TestFocusMode_RKey_OpensReviewWithItems verifies that pressing "r" in focus
// mode with a non-empty review queue switches to the review panel and that
// the review-panel view stays inside alt-screen. Without AltScreen=true on the
// review-panel branch of View(), the framework drops out of alt-screen each
// frame and the user sees nothing change — which is the "r doesn't do anything"
// bug this guards against.
func TestFocusMode_RKey_OpensReviewWithItems(t *testing.T) {
	app, _, _, sessR := makeFocusModeApp(t)

	model, _ := app.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	app = model.(App)

	if app.dashboard.panelFocus != focusReview {
		t.Fatalf("expected panelFocus=focusReview after r, got %v", app.dashboard.panelFocus)
	}
	if app.reviewSession != sessR {
		t.Fatalf("expected reviewSession=sessR, got %v", app.reviewSession)
	}
	if sessR.LifecyclePhase() != agent.LifecycleInReview {
		t.Errorf("expected sessR phase=InReview, got %v", sessR.LifecyclePhase())
	}

	view := app.View()
	if !view.AltScreen {
		t.Error("review panel View must keep AltScreen=true; otherwise focus mode flickers out of alt-screen and r looks like a no-op")
	}
}

// TestSoftAgentLimitGuard verifies the two-press 'n' guard in focus mode:
// first press shows the modal and sets agentLimitModalActive; second press proceeds.
func TestSoftAgentLimitGuard(t *testing.T) {
	requireClaude(t)
	dir, err := os.MkdirTemp("", "baton-softlimit-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir, config.ResolvedSettings{
		BypassPermissions:   true,
		AgentProgram:        "claude",
		MaxConcurrentAgents: 1, // limit to 1 so we hit it immediately
	})
	defer mgr.Shutdown()

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir
	app.resolvedCache[dir] = config.ResolvedSettings{
		BypassPermissions:   true,
		AgentProgram:        "claude",
		MaxConcurrentAgents: 1,
	}

	// Create the first agent to reach the limit.
	app = createAgent(t, app)
	if app.err != "" {
		t.Fatalf("Error creating first agent: %s", app.err)
	}
	if mgr.AgentCount() != 1 {
		t.Fatalf("Expected 1 agent, got %d", mgr.AgentCount())
	}

	// Return to list focus so 'n' reaches the app-level handler.
	model, _ := app.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	app = model.(App)
	if app.dashboard.panelFocus != focusList {
		t.Fatalf("Expected focusList after ctrl+e, got %v", app.dashboard.panelFocus)
	}

	// Enable focus mode.
	app.focusModeActive = true

	// First 'n' press: should set agentLimitModalActive and not create agent.
	model, cmd := app.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	app = model.(App)
	if cmd != nil {
		// If a cmd was returned, execute it to check if it created an agent.
		// We don't expect this in the first press.
		msg := cmd()
		if _, ok := msg.(createResultMsg); ok {
			t.Fatal("Expected no agent creation on first 'n' press at limit")
		}
	}
	if !app.agentLimitModalActive {
		t.Fatal("Expected agentLimitModalActive=true after first 'n' at limit")
	}
	if v := app.View(); !strings.Contains(v.Content, "Focus limit reached") {
		t.Fatalf("Expected rendered View to contain 'Focus limit reached' modal title; got:\n%s", v.Content)
	}

	// Second 'n' press: should clear modal and proceed with creation.
	model, cmd = app.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	app = model.(App)
	if app.agentLimitModalActive {
		t.Fatal("Expected agentLimitModalActive=false after second 'n'")
	}
	if v := app.View(); strings.Contains(v.Content, "Focus limit reached") {
		t.Fatal("Expected rendered View to NOT contain 'Focus limit reached' after override")
	}
	// cmd should be non-nil (agent creation is dispatched).
	if cmd == nil {
		t.Fatal("Expected non-nil cmd from second 'n' press (agent creation)")
	}

	// Any other key press should dismiss the modal without spawning and without
	// performing its normal action (e.g. 'j' must not move the focus cursor).
	app.agentLimitModalActive = true
	beforeIdx := app.focusActiveIdx
	beforeQueue := app.focusQueueIndex
	beforeSection := app.focusCursorSection
	model, dismissCmd := app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	app = model.(App)
	if app.agentLimitModalActive {
		t.Fatal("Expected agentLimitModalActive cleared by non-n key press")
	}
	if dismissCmd != nil {
		if msg := dismissCmd(); msg != nil {
			if _, ok := msg.(createResultMsg); ok {
				t.Fatal("Expected no agent creation when dismissing modal with 'j'")
			}
		}
	}
	if v := app.View(); strings.Contains(v.Content, "Focus limit reached") {
		t.Fatal("Expected rendered View to NOT contain 'Focus limit reached' after cancel")
	}
	if app.focusActiveIdx != beforeIdx || app.focusQueueIndex != beforeQueue || app.focusCursorSection != beforeSection {
		t.Fatalf("Expected focus cursor unchanged after dismiss key; before=(idx=%d,q=%d,sec=%v) after=(idx=%d,q=%d,sec=%v)",
			beforeIdx, beforeQueue, beforeSection, app.focusActiveIdx, app.focusQueueIndex, app.focusCursorSection)
	}
}

// TestSoftAgentLimitGuardMultiRepo verifies the two-press 'n' guard in focus
// mode applies to the multi-repo path (picker) just like the single-repo path.
func TestSoftAgentLimitGuardMultiRepo(t *testing.T) {
	requireClaude(t)
	dir, err := os.MkdirTemp("", "baton-softlimit-multi-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir, config.ResolvedSettings{
		BypassPermissions:   true,
		AgentProgram:        "claude",
		MaxConcurrentAgents: 1,
	})
	defer mgr.Shutdown()

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir
	app.resolvedCache[dir] = config.ResolvedSettings{
		BypassPermissions:   true,
		AgentProgram:        "claude",
		MaxConcurrentAgents: 1,
	}
	// Two repos triggers the multi-repo branch.
	app.cfg = &config.Config{Repos: []config.Repo{{Path: dir}, {Path: "/fake/other"}}}

	// Create the first agent to reach the limit.
	app = createAgent(t, app)
	if app.err != "" {
		t.Fatalf("Error creating first agent: %s", app.err)
	}
	if mgr.AgentCount() != 1 {
		t.Fatalf("Expected 1 agent, got %d", mgr.AgentCount())
	}

	// Return to list focus so 'n' reaches the app-level handler.
	model, _ := app.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	app = model.(App)
	if app.dashboard.panelFocus != focusList {
		t.Fatalf("Expected focusList after ctrl+e, got %v", app.dashboard.panelFocus)
	}

	// Enable focus mode.
	app.focusModeActive = true

	// First 'n' press: should show modal and set agentLimitModalActive; picker must NOT open.
	model, cmd := app.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	app = model.(App)
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(createResultMsg); ok {
			t.Fatal("Expected no agent creation on first 'n' press at limit")
		}
	}
	if !app.agentLimitModalActive {
		t.Fatal("Expected agentLimitModalActive=true after first 'n' at limit")
	}
	if v := app.View(); !strings.Contains(v.Content, "Focus limit reached") {
		t.Fatalf("Expected rendered View to contain 'Focus limit reached' modal title; got:\n%s", v.Content)
	}
	if app.view == ViewRepoPicker {
		t.Fatal("Expected view != ViewRepoPicker on first 'n' at limit")
	}

	// Second 'n' press: should clear modal and open the picker.
	model, _ = app.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	app = model.(App)
	if app.agentLimitModalActive {
		t.Fatal("Expected agentLimitModalActive=false after second 'n'")
	}
	if app.view != ViewRepoPicker {
		t.Fatal("Expected view == ViewRepoPicker after second 'n' override")
	}
}

// TestClampToRepo verifies that clampToRepo always lands on a listItemRepo row.
func TestClampToRepo(t *testing.T) {
	sess := &agent.Session{Name: "s"}
	ag := &agent.Agent{Name: "a"}

	items := []listItem{
		{kind: listItemRepo, repoPath: "/r1", repoName: "repo1"},         // 0
		{kind: listItemSession, repoPath: "/r1", session: sess},          // 1
		{kind: listItemAgent, repoPath: "/r1", session: sess, agent: ag}, // 2
		{kind: listItemRepo, repoPath: "/r2", repoName: "repo2"},         // 3
		{kind: listItemAgent, repoPath: "/r2", session: sess, agent: ag}, // 4
	}

	// Already on a repo row: no-op.
	d := newDashboardModel()
	d.items = items
	d.selected = 0
	d.clampToRepo()
	if d.selected != 0 {
		t.Fatalf("no-op case: expected 0, got %d", d.selected)
	}

	// On an agent row: should find the owning repo header above (backward search).
	d.selected = 2
	d.clampToRepo()
	if d.selected != 0 {
		t.Fatalf("agent in repo1: expected 0 (repo1 header), got %d", d.selected)
	}

	// On the session row: should find the repo header above.
	d.selected = 1
	d.clampToRepo()
	if d.selected != 0 {
		t.Fatalf("session row: expected 0 (repo1 header), got %d", d.selected)
	}

	// On agent in repo2 (index 4): repo2 header is at index 3, above.
	d.selected = 4
	d.clampToRepo()
	if d.selected != 3 {
		t.Fatalf("agent in repo2: expected 3 (repo2 header), got %d", d.selected)
	}

	// Out-of-range selected: should clamp down and then find a repo.
	d.selected = 99
	d.clampToRepo()
	if d.items[d.selected].kind != listItemRepo {
		t.Fatalf("out-of-range: expected listItemRepo, got kind=%d at selected=%d", d.items[d.selected].kind, d.selected)
	}
}

// TestFocusModeNavigationOnlyLandsOnRepos verifies that j/k in focus mode only
// move between listItemRepo rows, skipping sessions and agents entirely.
func TestFocusModeNavigationOnlyLandsOnRepos(t *testing.T) {
	sess := &agent.Session{Name: "s"}
	ag := &agent.Agent{Name: "a"}

	d := newDashboardModel()
	d.width = 120
	d.height = 39
	d.focusModeActive = true
	d.items = []listItem{
		{kind: listItemRepo, repoPath: "/r1", repoName: "repo1"},         // 0
		{kind: listItemSession, repoPath: "/r1", session: sess},          // 1
		{kind: listItemAgent, repoPath: "/r1", session: sess, agent: ag}, // 2
		{kind: listItemRepo, repoPath: "/r2", repoName: "repo2"},         // 3
		{kind: listItemAgent, repoPath: "/r2", session: sess, agent: ag}, // 4
	}
	d.selected = 0

	// j from repo1 should skip session and agent, land on repo2.
	d, _ = d.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if d.selected != 3 {
		t.Fatalf("j from repo1: expected 3 (repo2), got %d", d.selected)
	}

	// j from repo2 (last repo): no-op, stays at 3.
	d, _ = d.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if d.selected != 3 {
		t.Fatalf("j from last repo: expected 3 (no-op), got %d", d.selected)
	}

	// k from repo2 should land on repo1.
	d, _ = d.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if d.selected != 0 {
		t.Fatalf("k from repo2: expected 0 (repo1), got %d", d.selected)
	}

	// k from repo1 (first repo): no-op, stays at 0.
	d, _ = d.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if d.selected != 0 {
		t.Fatalf("k from first repo: expected 0 (no-op), got %d", d.selected)
	}
}

// makeFocusModeApp wires up an App in focus mode with two in-progress sessions
// and one ready-for-review session. Used by the tests below to exercise unified
// cursor navigation across the Active and Review sections.
func makeFocusModeApp(t *testing.T) (App, *agent.Session, *agent.Session, *agent.Session) {
	t.Helper()
	sessA := &agent.Session{Name: "active-a"}
	sessA.SetLifecyclePhase(agent.LifecycleInProgress)
	sessB := &agent.Session{Name: "active-b"}
	sessB.SetLifecyclePhase(agent.LifecycleInProgress)
	sessR := &agent.Session{Name: "review-r"}
	sessR.SetLifecyclePhase(agent.LifecycleReadyForReview)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.focusModeActive = true
	app.dashboard.focusModeActive = true
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sessA},
		{kind: listItemSession, repoPath: "/r", session: sessB},
		{kind: listItemSession, repoPath: "/r", session: sessR},
	}
	return app, sessA, sessB, sessR
}

// TestFocusModeNavigationCrossesSections verifies that j/k in focus mode
// traverses Active → Review in render order, transitioning between sections at
// the boundaries instead of bouncing two indices in lockstep.
func TestFocusModeNavigationCrossesSections(t *testing.T) {
	app, _, _, _ := makeFocusModeApp(t)
	if app.focusCursorSection != focusSectionActive {
		// clampFocusCursor only runs from refreshAgentList; here we drive the
		// state directly. Make the starting condition explicit.
		app.focusCursorSection = focusSectionActive
	}

	// Start at active[0].
	if app.focusActiveIdx != 0 || app.focusCursorSection != focusSectionActive {
		t.Fatalf("expected start at active[0], got section=%v active=%d", app.focusCursorSection, app.focusActiveIdx)
	}

	// j: active[0] → active[1].
	model, _ := app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	app = model.(App)
	if app.focusCursorSection != focusSectionActive || app.focusActiveIdx != 1 {
		t.Fatalf("after 1st j: expected active[1], got section=%v active=%d", app.focusCursorSection, app.focusActiveIdx)
	}

	// j: active[1] (last) → review[0].
	model, _ = app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	app = model.(App)
	if app.focusCursorSection != focusSectionReview || app.focusQueueIndex != 0 {
		t.Fatalf("after 2nd j: expected review[0], got section=%v review=%d", app.focusCursorSection, app.focusQueueIndex)
	}

	// j: review[0] (last review, no further section) → no-op.
	model, _ = app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	app = model.(App)
	if app.focusCursorSection != focusSectionReview || app.focusQueueIndex != 0 {
		t.Fatalf("after 3rd j: expected review[0] (no-op), got section=%v review=%d", app.focusCursorSection, app.focusQueueIndex)
	}

	// k: review[0] → active[1].
	model, _ = app.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	app = model.(App)
	if app.focusCursorSection != focusSectionActive || app.focusActiveIdx != 1 {
		t.Fatalf("after k from review: expected active[1], got section=%v active=%d", app.focusCursorSection, app.focusActiveIdx)
	}

	// Verify the dashboard model received the synced state immediately.
	if app.dashboard.focusCursorSection != focusSectionActive || app.dashboard.focusActiveIdx != 1 {
		t.Fatalf("dashboard not synced: section=%v active=%d", app.dashboard.focusCursorSection, app.dashboard.focusActiveIdx)
	}
}

// TestFocusModeEnterOnActiveOpensFocusLaunch verifies that pressing enter
// while the cursor is on an active session selects the first non-shell agent
// in that session and switches to focusLaunch.
func TestFocusModeEnterOnActiveOpensFocusLaunch(t *testing.T) {
	requireClaude(t)
	dir, err := os.MkdirTemp("", "baton-focus-active-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir, config.Resolve(nil, nil))
	defer mgr.Shutdown()

	sess, ag, err := mgr.CreateSessionWithCommand(agent.Config{
		Name: "focus-active", Task: "test", RepoPath: dir, Rows: 24, Cols: 80,
	}, func(_ string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 30")
	})
	if err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir
	app.focusModeActive = true
	app.dashboard.focusModeActive = true
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: dir, repoName: "repo"},
		{kind: listItemSession, repoPath: dir, session: sess},
		{kind: listItemAgent, repoPath: dir, session: sess, agent: ag},
	}
	app.focusCursorSection = focusSectionActive
	app.focusActiveIdx = 0

	// Press enter on the active section: should jump into focusLaunch on ag.
	model, _ := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Text: ""})
	app = model.(App)
	if app.dashboard.panelFocus != focusLaunch {
		t.Fatalf("expected panelFocus=focusLaunch after enter on active, got %v", app.dashboard.panelFocus)
	}
	if app.focusLaunchAgent == nil || app.focusLaunchAgent.ID != ag.ID {
		t.Fatalf("expected focusLaunchAgent=ag, got %v", app.focusLaunchAgent)
	}
}

// TestFocusModeNavigationVisibleOnActiveOnly verifies that when only Active
// rows exist (no review queue), j/k still move within the active section and
// the dashboard sees the updated focusActiveIdx so the selection marker can
// render. This is the bug the user reported: an invisible cursor.
func TestFocusModeNavigationVisibleOnActiveOnly(t *testing.T) {
	sessA := &agent.Session{Name: "active-a"}
	sessA.SetLifecyclePhase(agent.LifecycleInProgress)
	sessB := &agent.Session{Name: "active-b"}
	sessB.SetLifecyclePhase(agent.LifecycleInProgress)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.focusModeActive = true
	app.dashboard.focusModeActive = true
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sessA},
		{kind: listItemSession, repoPath: "/r", session: sessB},
	}
	app.focusCursorSection = focusSectionActive

	// j moves within active (active is the only non-empty section).
	model, _ := app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	app = model.(App)
	if app.dashboard.focusActiveIdx != 1 {
		t.Fatalf("expected dashboard.focusActiveIdx=1 after j, got %d", app.dashboard.focusActiveIdx)
	}

	// j again at the bottom: no-op (no other section to fall through to).
	model, _ = app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	app = model.(App)
	if app.dashboard.focusActiveIdx != 1 {
		t.Fatalf("expected dashboard.focusActiveIdx=1 (no-op at end), got %d", app.dashboard.focusActiveIdx)
	}

	// k moves back up.
	model, _ = app.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	app = model.(App)
	if app.dashboard.focusActiveIdx != 0 {
		t.Fatalf("expected dashboard.focusActiveIdx=0 after k, got %d", app.dashboard.focusActiveIdx)
	}
}

// TestClampFocusCursorHopsToNonEmptySection verifies that when the cursor's
// section becomes empty (e.g. all in-progress sessions transition to ready),
// clampFocusCursor moves the cursor to the next non-empty section so the
// selection marker stays visible.
func TestClampFocusCursorHopsToNonEmptySection(t *testing.T) {
	sessR := &agent.Session{Name: "review-r"}
	sessR.SetLifecyclePhase(agent.LifecycleReadyForReview)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.focusModeActive = true
	app.dashboard.focusModeActive = true
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sessR},
	}
	app.focusCursorSection = focusSectionActive
	app.focusActiveIdx = 0

	app.clampFocusCursor()
	if app.focusCursorSection != focusSectionReview {
		t.Fatalf("expected hop to review section, got %v", app.focusCursorSection)
	}
	if app.focusQueueIndex != 0 {
		t.Fatalf("expected review index 0, got %d", app.focusQueueIndex)
	}
}

// TestFocusModeResetsPanelFocus verifies that entering focus mode resets
// panelFocus to focusList so that focus mode key handlers are reachable.
// Reproduces the bug where panelFocus==focusReview with no reviewSession left
// focus mode's j/k/m/r guard (`panelFocus != focusReview`) permanently false.
func TestFocusModeResetsPanelFocus(t *testing.T) {
	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39

	// Simulate panelFocus==focusReview with a nil reviewSession (e.g. after the
	// review was handled but panelFocus was not cleaned up). From this state the
	// focusReview guard at the top of the key handler does NOT fire (it requires
	// a non-nil reviewSession), so "f" falls through to the toggle handler.
	app.dashboard.panelFocus = focusReview
	app.reviewSession = nil

	// Press "f" to enter focus mode.
	model, _ := app.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	app = model.(App)

	if !app.focusModeActive {
		t.Fatal("expected focusModeActive=true after pressing f")
	}
	// Without the fix panelFocus would stay focusReview, making the focus-mode
	// handler guard (`panelFocus != focusReview`) permanently false and blocking j/k/m/r.
	if app.dashboard.panelFocus != focusList {
		t.Errorf("expected panelFocus=focusList after entering focus mode, got %v", app.dashboard.panelFocus)
	}
}

// TestFocusModeBlocksDKey verifies that pressing "d" in focus mode is a no-op
// and does not delete repos or trigger the diff view.
func TestFocusModeBlocksDKey(t *testing.T) {
	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39

	app.cfg = &config.Config{
		Repos: []config.Repo{
			{Path: "/repo1", Name: "repo1"},
		},
	}
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: "/repo1", repoName: "repo1"},
	}
	app.dashboard.selected = 0

	// Enter focus mode.
	model, _ := app.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	app = model.(App)
	if !app.focusModeActive {
		t.Fatal("expected focusModeActive=true after pressing f")
	}

	// Press "d" in focus mode — should be a no-op.
	model, _ = app.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	app = model.(App)

	if len(app.cfg.Repos) != 1 {
		t.Errorf("expected repo count=1 after d in focus mode, got %d", len(app.cfg.Repos))
	}
	if app.view != ViewDashboard {
		t.Errorf("expected view=ViewDashboard after d in focus mode, got %v", app.view)
	}
}

// TestFocusLaunch_FocusModeKeysForwardToAgent verifies that single-letter
// focus-mode pipeline keybindings ("m", "r") are forwarded to the agent
// terminal when focusLaunch is active, instead of triggering focus-mode
// actions. Keybindings must not bleed across screens — focusLaunch is a
// fullscreen Claude session and the user should be able to type any character.
func TestFocusLaunch_FocusModeKeysForwardToAgent(t *testing.T) {
	dir, err := os.MkdirTemp("", "baton-focuslaunch-keys-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cmd %v: %v\n%s", args, err, out)
		}
	}
	run("git", "init")
	run("git", "config", "commit.gpgsign", "false")
	run("git", "commit", "--allow-empty", "-m", "init")

	mgr := agent.NewManager(dir, config.Resolve(nil, nil))
	defer mgr.Shutdown()

	sess, ag, err := mgr.CreateSessionWithCommand(agent.Config{
		Name: "fl-keys", Task: "test", RepoPath: dir, Rows: 24, Cols: 80,
	}, func(_ string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 30")
	})
	if err != nil {
		t.Fatal(err)
	}
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	sess.MarkDone() // would qualify the session for "m" if it were intercepted

	// Also queue a ReadyForReview session — would be picked up by "r" if intercepted.
	sessR := &agent.Session{Name: "ready"}
	sessR.SetLifecyclePhase(agent.LifecycleReadyForReview)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir
	app.focusModeActive = true
	app.dashboard.focusModeActive = true
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: dir, repoName: "repo"},
		{kind: listItemSession, repoPath: dir, session: sess},
		{kind: listItemAgent, repoPath: dir, session: sess, agent: ag},
		{kind: listItemSession, repoPath: dir, session: sessR},
	}
	app.dashboard.panelFocus = focusLaunch
	app.focusLaunchAgent = ag
	app.focusLaunchSession = sess

	for _, ch := range []rune{'m', 'r'} {
		model, _ := app.Update(tea.KeyPressMsg{Code: ch, Text: string(ch)})
		app = model.(App)

		if app.dashboard.panelFocus != focusLaunch {
			t.Fatalf("press %q: expected panelFocus=focusLaunch (key forwarded to agent), got %v", ch, app.dashboard.panelFocus)
		}
		if app.focusLaunchAgent == nil || app.focusLaunchAgent.ID != ag.ID {
			t.Fatalf("press %q: expected focusLaunchAgent unchanged, got %v", ch, app.focusLaunchAgent)
		}
		if app.focusLaunchSession == nil || app.focusLaunchSession.ID != sess.ID {
			t.Fatalf("press %q: expected focusLaunchSession unchanged, got %v", ch, app.focusLaunchSession)
		}
		if sess.LifecyclePhase() != agent.LifecycleInProgress {
			t.Fatalf("press %q: expected sess phase unchanged=InProgress, got %v", ch, sess.LifecyclePhase())
		}
		if sessR.LifecyclePhase() != agent.LifecycleReadyForReview {
			t.Fatalf("press %q: expected sessR phase unchanged=ReadyForReview, got %v", ch, sessR.LifecyclePhase())
		}
		if app.reviewSession != nil {
			t.Fatalf("press %q: expected reviewSession=nil, got %v", ch, app.reviewSession)
		}
	}
}

// TestFocusMode_MKey_IsCursorAware verifies that pressing "m" in focus pipeline
// view marks the session under the cursor (focusActiveIdx), not the first matching
// session in the list.
func TestFocusMode_MKey_IsCursorAware(t *testing.T) {
	sessA := agent.NewSessionForTest("a", "active-a")
	sessA.SetLifecyclePhase(agent.LifecycleInProgress)
	sessA.AddTestAgent("a-1", false, agent.StatusIdle)
	sessB := agent.NewSessionForTest("b", "active-b")
	sessB.SetLifecyclePhase(agent.LifecycleInProgress)
	sessB.AddTestAgent("b-1", false, agent.StatusIdle)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.focusModeActive = true
	app.dashboard.focusModeActive = true
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sessA},
		{kind: listItemSession, repoPath: "/r", session: sessB},
	}
	app.focusCursorSection = focusSectionActive
	app.focusActiveIdx = 1 // cursor on sessB, not sessA

	model, _ := app.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	app = model.(App)

	if sessB.LifecyclePhase() != agent.LifecycleReadyForReview {
		t.Errorf("expected sessB (cursor session) phase=ReadyForReview, got %v", sessB.LifecyclePhase())
	}
	if sessA.LifecyclePhase() != agent.LifecycleInProgress {
		t.Errorf("expected sessA (non-cursor session) phase unchanged=InProgress, got %v", sessA.LifecyclePhase())
	}
}

// makeFocusModeMRApp wires up an App in focus mode with one in-progress session
// (sessA) and one ready-for-review session (sessR). Used by the m/r handler tests
// below. The caller is responsible for adding agents to sessA via AddTestAgent.
func makeFocusModeMRApp(t *testing.T) (App, *agent.Session, *agent.Session) {
	t.Helper()
	sessA := agent.NewSessionForTest("a", "active-a")
	sessA.SetLifecyclePhase(agent.LifecycleInProgress)
	sessR := agent.NewSessionForTest("r", "review-r")
	sessR.SetLifecyclePhase(agent.LifecycleReadyForReview)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.focusModeActive = true
	app.dashboard.focusModeActive = true
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sessA},
		{kind: listItemSession, repoPath: "/r", session: sessR},
	}
	return app, sessA, sessR
}

// TestFocusMode_MKey_CursorOnReviewSection_ShowsError verifies that pressing "m"
// while the cursor is on the REVIEW QUEUE section is a no-op that surfaces an
// error explaining why nothing happened.
func TestFocusMode_MKey_CursorOnReviewSection_ShowsError(t *testing.T) {
	app, sessA, _ := makeFocusModeMRApp(t)
	sessA.AddTestAgent("a-1", false, agent.StatusIdle)
	app.focusCursorSection = focusSectionReview
	app.focusQueueIndex = 0

	model, _ := app.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	app = model.(App)

	if app.err == "" {
		t.Fatal("expected error message when pressing m with cursor on review section")
	}
	if !strings.Contains(app.err, "review queue") {
		t.Errorf("expected error to mention review queue, got %q", app.err)
	}
	// sessA must NOT have transitioned phase.
	if sessA.LifecyclePhase() != agent.LifecycleInProgress {
		t.Errorf("expected sessA phase unchanged=InProgress, got %v", sessA.LifecyclePhase())
	}
}

// TestFocusMode_MKey_ActiveSession_ShowsRunningError verifies that pressing "m"
// on an active (non-reviewable) session surfaces a "still running" error and
// does not transition the session phase.
func TestFocusMode_MKey_ActiveSession_ShowsRunningError(t *testing.T) {
	app, sessA, _ := makeFocusModeMRApp(t)
	sessA.AddTestAgent("a-1", false, agent.StatusActive)
	app.focusCursorSection = focusSectionActive
	app.focusActiveIdx = 0

	model, _ := app.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	app = model.(App)

	if app.err == "" {
		t.Fatal("expected error message when pressing m on active session")
	}
	if !strings.Contains(app.err, "still running") {
		t.Errorf("expected error to mention still running, got %q", app.err)
	}
	if sessA.LifecyclePhase() != agent.LifecycleInProgress {
		t.Errorf("expected sessA phase unchanged=InProgress, got %v", sessA.LifecyclePhase())
	}
}

// TestFocusMode_MKey_IdleSession_TransitionsToReady verifies that pressing "m"
// on a session whose agents are all idle (Claude finished a turn but did not
// /exit) transitions the session to ReadyForReview and fires the diff fetch.
func TestFocusMode_MKey_IdleSession_TransitionsToReady(t *testing.T) {
	app, sessA, _ := makeFocusModeMRApp(t)
	sessA.AddTestAgent("a-1", false, agent.StatusIdle)
	app.focusCursorSection = focusSectionActive
	app.focusActiveIdx = 0

	model, cmd := app.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	app = model.(App)

	if sessA.LifecyclePhase() != agent.LifecycleReadyForReview {
		t.Errorf("expected sessA phase=ReadyForReview, got %v", sessA.LifecyclePhase())
	}
	if app.focusQueueIndex != 0 {
		t.Errorf("expected focusQueueIndex reset to 0, got %d", app.focusQueueIndex)
	}
	if cmd == nil {
		t.Error("expected a diff-fetch Cmd, got nil")
	}
	if app.err != "" {
		t.Errorf("expected no error on success, got %q", app.err)
	}
}

// TestFocusMode_RKey_EmptyQueue_ShowsError verifies that pressing "r" with no
// review-phase sessions surfaces an error and does not change panelFocus.
func TestFocusMode_RKey_EmptyQueue_ShowsError(t *testing.T) {
	sessA := agent.NewSessionForTest("a", "active-a")
	sessA.SetLifecyclePhase(agent.LifecycleInProgress)
	sessA.AddTestAgent("a-1", false, agent.StatusActive)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.focusModeActive = true
	app.dashboard.focusModeActive = true
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sessA},
	}
	app.focusCursorSection = focusSectionActive
	app.focusActiveIdx = 0

	model, _ := app.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	app = model.(App)

	if app.err == "" {
		t.Fatal("expected error message when pressing r with empty queue")
	}
	if !strings.Contains(app.err, "review queue is empty") {
		t.Errorf("expected error to mention empty review queue, got %q", app.err)
	}
	if app.dashboard.panelFocus == focusReview {
		t.Error("expected panelFocus to stay focusList, got focusReview")
	}
	if app.reviewSession != nil {
		t.Errorf("expected reviewSession to stay nil, got %v", app.reviewSession)
	}
}

// TestFocusMode_RKey_NonEmptyQueue_OpensReviewPanel verifies that pressing "r"
// with at least one review-phase session opens the review panel and selects the
// session at focusQueueIndex.
func TestFocusMode_RKey_NonEmptyQueue_OpensReviewPanel(t *testing.T) {
	app, _, sessR := makeFocusModeMRApp(t)
	app.focusCursorSection = focusSectionReview
	app.focusQueueIndex = 0

	model, _ := app.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	app = model.(App)

	if app.err != "" {
		t.Errorf("expected no error, got %q", app.err)
	}
	if app.dashboard.panelFocus != focusReview {
		t.Errorf("expected panelFocus=focusReview, got %v", app.dashboard.panelFocus)
	}
	if app.reviewSession != sessR {
		t.Errorf("expected reviewSession=sessR, got %v", app.reviewSession)
	}
	if sessR.LifecyclePhase() != agent.LifecycleInReview {
		t.Errorf("expected sessR phase=InReview, got %v", sessR.LifecyclePhase())
	}
}
