package tui

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	xvt "github.com/charmbracelet/x/vt"
	"github.com/devenjarvis/baton/internal/agent"
	"github.com/devenjarvis/baton/internal/audio"
	"github.com/devenjarvis/baton/internal/config"
	"github.com/devenjarvis/baton/internal/github"
)

func requireClaude(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not in PATH")
	}
}

// returnToList exits the focusLaunch overlay (where keys forward to the agent
// terminal) so app-level key handlers can fire.
func returnToList(app App) App {
	if app.dashboard.panelFocus == focusLaunch {
		model, _ := app.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		return model.(App)
	}
	return app
}

// createAgent presses 'n' and executes the async create cmd, returning the
// updated app. If the focusLaunch overlay is active it escapes back first so
// the 'n' key isn't forwarded to the agent.
func createAgent(t *testing.T, app App) App {
	t.Helper()

	app = returnToList(app)

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

	app = returnToList(app)

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
	// After creation the agent is auto-opened in focusLaunch (the fullscreen
	// pipeline view's per-agent terminal).
	if app.dashboard.panelFocus != focusLaunch {
		t.Errorf("Expected focusLaunch after creation, got %v", app.dashboard.panelFocus)
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

	// Create second session+agent (createAgent escapes any focusLaunch overlay first)
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

// TestPanelFocusSwitching exercises the focusLaunch entry/exit flow that
// replaces the old split-panel focusTerminal toggling.
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

	// After creation focusLaunch is open on the new agent.
	if app.dashboard.panelFocus != focusLaunch {
		t.Fatalf("Expected focusLaunch after creation, got %v", app.dashboard.panelFocus)
	}

	// Esc returns to the pipeline (focusList).
	model, _ := app.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	app = model.(App)
	if app.dashboard.panelFocus != focusList {
		t.Fatalf("Expected focusList after esc, got %v", app.dashboard.panelFocus)
	}

	// Enter on the cursor-selected session re-opens focusLaunch.
	model, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	app = model.(App)
	if app.dashboard.panelFocus != focusLaunch {
		t.Fatalf("Expected focusLaunch after enter, got %v", app.dashboard.panelFocus)
	}
}

// TestActionKeysBlockedInFocusLaunch verifies that pipeline action keys (n, c,
// etc.) are forwarded to the agent terminal when focusLaunch is active rather
// than triggering pipeline actions. Replaces the old split-panel focusTerminal
// guard test.
func TestActionKeysBlockedInFocusLaunch(t *testing.T) {
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

	// After creation focusLaunch is the active panel.
	if app.dashboard.panelFocus != focusLaunch {
		t.Fatalf("Expected focusLaunch after creation, got %v", app.dashboard.panelFocus)
	}

	// Press "n" — should be forwarded to agent, NOT create a new agent.
	// panelFocus must stay focusLaunch and view must stay ViewDashboard.
	model, _ := app.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	app = model.(App)
	if app.view != ViewDashboard {
		t.Fatalf("Expected ViewDashboard (n forwarded to agent, not new-agent), got %v", app.view)
	}
	if app.dashboard.panelFocus != focusLaunch {
		t.Fatalf("Expected focusLaunch to persist after 'n', got %v", app.dashboard.panelFocus)
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
	if app.dashboard.panelFocus != focusLaunch {
		t.Fatalf("Expected focusLaunch after creation, got %v", app.dashboard.panelFocus)
	}

	// Press shift+esc — should stay in focusLaunch (escape forwarded as
	// interrupt to the agent, not a panel exit).
	model, _ := app.Update(tea.KeyPressMsg{Code: tea.KeyEscape, Mod: tea.ModShift})
	app = model.(App)
	if app.dashboard.panelFocus != focusLaunch {
		t.Fatalf("Expected focusLaunch after shift+esc (should forward, not exit), got %v", app.dashboard.panelFocus)
	}

	// Press plain esc — should exit to focusList.
	model, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	app = model.(App)
	if app.dashboard.panelFocus != focusList {
		t.Fatalf("Expected focusList after esc, got %v", app.dashboard.panelFocus)
	}
}

// TestPipelineClickMovesCursor verifies that a left click on a session card in
// the BUILDING section moves focusBuildingIdx to the clicked session, and a
// click on a REVIEWING row sets the cursor section + index accordingly. Single
// click does not activate; double-click within 500ms does.
func TestPipelineClickMovesCursor(t *testing.T) {
	sessA := agent.NewSessionForTest("a", "active-a")
	sessA.SetLifecyclePhase(agent.LifecycleInProgress)
	sessB := agent.NewSessionForTest("b", "active-b")
	sessB.SetLifecyclePhase(agent.LifecycleInProgress)
	sessR := agent.NewSessionForTest("r", "review-r")
	sessR.SetLifecyclePhase(agent.LifecycleReadyForReview)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sessA},
		{kind: listItemSession, repoPath: "/r", session: sessB},
		{kind: listItemSession, repoPath: "/r", session: sessR},
	}
	app.focusCursorSection = focusSectionBuilding
	app.focusBuildingIdx = 0

	// Pipeline layout (Planning is empty so its label/rows are skipped):
	// header(0) + sep(1) + pipeline widget(2..5) + blank(6)
	// + "BUILDING"(7) + card0(8..11) + blank(12) + card1(13..16) + blank(17)
	// + "REVIEWING"(18) + queue0(19..20).
	// Click on card 1 (building session B) at Y=14.
	model, _ := app.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 30, Y: 14})
	app = model.(App)
	if app.focusCursorSection != focusSectionBuilding || app.focusBuildingIdx != 1 {
		t.Fatalf("expected cursor on building[1] after click on sessB card, got section=%v idx=%d", app.focusCursorSection, app.focusBuildingIdx)
	}

	// Click on the reviewing row at Y=19.
	model, _ = app.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 30, Y: 19})
	app = model.(App)
	if app.focusCursorSection != focusSectionReview || app.focusReviewIdx != 0 {
		t.Fatalf("expected cursor on review[0] after click on queue row, got section=%v idx=%d", app.focusCursorSection, app.focusReviewIdx)
	}

	// Right-click does nothing.
	app.focusBuildingIdx = 0
	app.focusCursorSection = focusSectionBuilding
	model, _ = app.Update(tea.MouseClickMsg{Button: tea.MouseRight, X: 30, Y: 14})
	app = model.(App)
	if app.focusCursorSection != focusSectionBuilding || app.focusBuildingIdx != 0 {
		t.Errorf("right-click should not move cursor")
	}
}

// TestPipelineClickMovesCursor_PlanningAndShipping covers the two new sections
// added in the four-phase refactor. The hit-test uses the same pointer-
// assignment path (*focusSectionIdx(section) = idx) for every section, so a
// click on a Planning card must set focusPlanningIdx and a click on a Shipping
// row must set focusShippingIdx.
func TestPipelineClickMovesCursor_PlanningAndShipping(t *testing.T) {
	sessP := agent.NewSessionForTest("p", "planning-p")
	sessP.SetLifecyclePhase(agent.LifecyclePlanning)
	sessS := agent.NewSessionForTest("s", "shipping-s")
	sessS.SetLifecyclePhase(agent.LifecycleShipping)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sessP},
		{kind: listItemSession, repoPath: "/r", session: sessS},
	}
	// Start the cursor on Building so a successful click has somewhere to move
	// the selection FROM (Building is empty here, but the cursor is held there
	// until the first click).
	app.focusCursorSection = focusSectionBuilding

	// Pipeline layout (Building + Reviewing are empty so their rows are
	// skipped): header(0) + sep(1) + pipeline widget(2..5) + blank(6)
	// + "PLANNING"(7) + card0(8..11) + blank(12)
	// + "SHIPPING"(13) + ship0(14..15).
	model, _ := app.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 30, Y: 9})
	app = model.(App)
	if app.focusCursorSection != focusSectionPlanning || app.focusPlanningIdx != 0 {
		t.Fatalf("expected cursor on planning[0] after click on planning card, got section=%v idx=%d", app.focusCursorSection, app.focusPlanningIdx)
	}

	model, _ = app.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 30, Y: 14})
	app = model.(App)
	if app.focusCursorSection != focusSectionShipping || app.focusShippingIdx != 0 {
		t.Fatalf("expected cursor on shipping[0] after click on shipping row, got section=%v idx=%d", app.focusCursorSection, app.focusShippingIdx)
	}
}

// TestPipelineDoubleClickActivatesReview verifies that a double-click on a
// REVIEW QUEUE row opens the review panel for that session.
func TestPipelineDoubleClickActivatesReview(t *testing.T) {
	sessR := agent.NewSessionForTest("r", "review-r")
	sessR.SetLifecyclePhase(agent.LifecycleReadyForReview)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sessR},
	}

	// With no active sessions, REVIEW QUEUE starts at row 7 (header(0) + sep(1)
	// + pipeline(2..5) + blank(6) + "REVIEW QUEUE"(7) + queue0(8..9)).
	first := tea.MouseClickMsg{Button: tea.MouseLeft, X: 30, Y: 8}
	model, _ := app.Update(first)
	app = model.(App)
	if app.dashboard.panelFocus == focusReview {
		t.Fatal("single click should not enter focusReview")
	}

	// Second click within the double-click window opens the review panel.
	model, _ = app.Update(first)
	app = model.(App)
	if app.dashboard.panelFocus != focusReview {
		t.Fatalf("expected focusReview after double-click, got %v", app.dashboard.panelFocus)
	}
	if app.reviewSession != sessR {
		t.Fatalf("expected reviewSession=sessR, got %v", app.reviewSession)
	}
}

// Wheel-scroll handling for the agent terminal is exercised end-to-end via
// focusLaunch in the e2e suite; the previous focusTerminal-only test became
// unreachable when the split-panel layout was removed.

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

// TestMouseWheelForwardsInAltScreen verifies that wheel events on the
// focusLaunch agent terminal do NOT mutate scrollOffset when the agent is in
// alt-screen mode. Alt-screen apps drive their own scrollback, so baton's
// scrollOffset must stay frozen and the wheel event should be forwarded to
// the agent instead.
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
	app.dashboard.panelFocus = focusLaunch
	app.focusLaunchAgent = ag
	app.focusLaunchSession = sess

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

// Cursor-placement regression coverage for focusLaunch lives in the e2e suite;
// the original split-panel preview cursor tests targeted screen offsets that
// only exist in the deleted layout.

// TestChimeSuppressionByStatus verifies that StatusIdle events do not mark
// chime-for-turn, but StatusWaiting events still do.
// When an audio player is available, the test asserts ChimedForTurn state
// directly; otherwise it still validates the gate logic runs without error.
func TestChimeSuppressionByStatus(t *testing.T) {
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

	// Case 1: StatusIdle — chime should be suppressed.
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
	if app.audioPlayer != nil && ag.ChimedForTurn() {
		t.Error("Expected ChimedForTurn=false after idle event (chime suppressed)")
	}

	// Case 2: StatusWaiting — chime should fire.
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
	if app.audioPlayer != nil && !ag.ChimedForTurn() {
		t.Error("Expected ChimedForTurn=true after waiting event (chime allowed)")
	}
}

func TestBacklogGate_WarnOnN(t *testing.T) {
	app := NewApp()
	two := 2
	app.globalSettings = &config.GlobalSettings{MaxReviewBacklog: &two}

	// First n when no backlog — no warning, focusBacklogWarning stays false.
	model, _ := app.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
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

func TestRKey_NoopWithEmptyQueue(t *testing.T) {
	app := NewApp()

	// r with no queued sessions should be a no-op.
	model, _ := app.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
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
	app, sessR := makeFocusModeApp(t)

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
	beforeIdx := app.focusBuildingIdx
	beforeQueue := app.focusReviewIdx
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
	if app.focusBuildingIdx != beforeIdx || app.focusReviewIdx != beforeQueue || app.focusCursorSection != beforeSection {
		t.Fatalf("Expected focus cursor unchanged after dismiss key; before=(idx=%d,q=%d,sec=%v) after=(idx=%d,q=%d,sec=%v)",
			beforeIdx, beforeQueue, beforeSection, app.focusBuildingIdx, app.focusReviewIdx, app.focusCursorSection)
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

	// Create the first agent to reach the limit (single-repo cfg so 'n' creates
	// directly rather than opening the repo picker).
	app = createAgent(t, app)
	if app.err != "" {
		t.Fatalf("Error creating first agent: %s", app.err)
	}
	if mgr.AgentCount() != 1 {
		t.Fatalf("Expected 1 agent, got %d", mgr.AgentCount())
	}

	// Now expand cfg to two repos so 'n' takes the multi-repo branch.
	app.cfg = &config.Config{Repos: []config.Repo{{Path: dir}, {Path: "/fake/other"}}}

	// Return to list focus so 'n' reaches the app-level handler.
	app = returnToList(app)
	if app.dashboard.panelFocus != focusList {
		t.Fatalf("Expected focusList after exit, got %v", app.dashboard.panelFocus)
	}

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

// makeFocusModeApp wires up an App in focus mode with two in-progress sessions
// and one ready-for-review session. Used by the tests below to exercise unified
// cursor navigation across the Building and Reviewing sections.
func makeFocusModeApp(t *testing.T) (App, *agent.Session) {
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
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sessA},
		{kind: listItemSession, repoPath: "/r", session: sessB},
		{kind: listItemSession, repoPath: "/r", session: sessR},
	}
	return app, sessR
}

// TestFocusModeNavigationCrossesSections verifies that j/k in focus mode
// traverses Active → Review in render order, transitioning between sections at
// the boundaries instead of bouncing two indices in lockstep.
func TestFocusModeNavigationCrossesSections(t *testing.T) {
	app, _ := makeFocusModeApp(t)
	if app.focusCursorSection != focusSectionBuilding {
		// clampFocusCursor only runs from refreshAgentList; here we drive the
		// state directly. Make the starting condition explicit.
		app.focusCursorSection = focusSectionBuilding
	}

	// Start at active[0].
	if app.focusBuildingIdx != 0 || app.focusCursorSection != focusSectionBuilding {
		t.Fatalf("expected start at active[0], got section=%v active=%d", app.focusCursorSection, app.focusBuildingIdx)
	}

	// j: active[0] → active[1].
	model, _ := app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	app = model.(App)
	if app.focusCursorSection != focusSectionBuilding || app.focusBuildingIdx != 1 {
		t.Fatalf("after 1st j: expected active[1], got section=%v active=%d", app.focusCursorSection, app.focusBuildingIdx)
	}

	// j: active[1] (last) → review[0].
	model, _ = app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	app = model.(App)
	if app.focusCursorSection != focusSectionReview || app.focusReviewIdx != 0 {
		t.Fatalf("after 2nd j: expected review[0], got section=%v review=%d", app.focusCursorSection, app.focusReviewIdx)
	}

	// j: review[0] (last review, no further section) → no-op.
	model, _ = app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	app = model.(App)
	if app.focusCursorSection != focusSectionReview || app.focusReviewIdx != 0 {
		t.Fatalf("after 3rd j: expected review[0] (no-op), got section=%v review=%d", app.focusCursorSection, app.focusReviewIdx)
	}

	// k: review[0] → active[1].
	model, _ = app.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	app = model.(App)
	if app.focusCursorSection != focusSectionBuilding || app.focusBuildingIdx != 1 {
		t.Fatalf("after k from review: expected active[1], got section=%v active=%d", app.focusCursorSection, app.focusBuildingIdx)
	}

	// Verify the dashboard model received the synced state immediately.
	if app.dashboard.focusCursorSection != focusSectionBuilding || app.dashboard.focusBuildingIdx != 1 {
		t.Fatalf("dashboard not synced: section=%v active=%d", app.dashboard.focusCursorSection, app.dashboard.focusBuildingIdx)
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
	// New sessions land in Planning by default; this test wants the row in the
	// Building section so the focusSectionBuilding cursor lands on it.
	sess.SetLifecyclePhase(agent.LifecycleInProgress)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: dir, repoName: "repo"},
		{kind: listItemSession, repoPath: dir, session: sess},
		{kind: listItemAgent, repoPath: dir, session: sess, agent: ag},
	}
	app.focusCursorSection = focusSectionBuilding
	app.focusBuildingIdx = 0

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
// the dashboard sees the updated focusBuildingIdx so the selection marker can
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
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sessA},
		{kind: listItemSession, repoPath: "/r", session: sessB},
	}
	app.focusCursorSection = focusSectionBuilding

	// j moves within active (active is the only non-empty section).
	model, _ := app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	app = model.(App)
	if app.dashboard.focusBuildingIdx != 1 {
		t.Fatalf("expected dashboard.focusBuildingIdx=1 after j, got %d", app.dashboard.focusBuildingIdx)
	}

	// j again at the bottom: no-op (no other section to fall through to).
	model, _ = app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	app = model.(App)
	if app.dashboard.focusBuildingIdx != 1 {
		t.Fatalf("expected dashboard.focusBuildingIdx=1 (no-op at end), got %d", app.dashboard.focusBuildingIdx)
	}

	// k moves back up.
	model, _ = app.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	app = model.(App)
	if app.dashboard.focusBuildingIdx != 0 {
		t.Fatalf("expected dashboard.focusBuildingIdx=0 after k, got %d", app.dashboard.focusBuildingIdx)
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
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sessR},
	}
	app.focusCursorSection = focusSectionBuilding
	app.focusBuildingIdx = 0

	app.clampFocusCursor()
	if app.focusCursorSection != focusSectionReview {
		t.Fatalf("expected hop to review section, got %v", app.focusCursorSection)
	}
	if app.focusReviewIdx != 0 {
		t.Fatalf("expected review index 0, got %d", app.focusReviewIdx)
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
// view marks the session under the cursor (focusBuildingIdx), not the first matching
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
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sessA},
		{kind: listItemSession, repoPath: "/r", session: sessB},
	}
	app.focusCursorSection = focusSectionBuilding
	app.focusBuildingIdx = 1 // cursor on sessB, not sessA

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
	app.focusReviewIdx = 0

	model, _ := app.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	app = model.(App)

	if app.err == "" {
		t.Fatal("expected error message when pressing m with cursor on review section")
	}
	if !strings.Contains(app.err, "Planning or Building") {
		t.Errorf("expected error to mention Planning or Building, got %q", app.err)
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
	app.focusCursorSection = focusSectionBuilding
	app.focusBuildingIdx = 0

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
	app.focusCursorSection = focusSectionBuilding
	app.focusBuildingIdx = 0

	model, cmd := app.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	app = model.(App)

	if sessA.LifecyclePhase() != agent.LifecycleReadyForReview {
		t.Errorf("expected sessA phase=ReadyForReview, got %v", sessA.LifecyclePhase())
	}
	if app.focusReviewIdx != 0 {
		t.Errorf("expected focusReviewIdx reset to 0, got %d", app.focusReviewIdx)
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
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sessA},
	}
	app.focusCursorSection = focusSectionBuilding
	app.focusBuildingIdx = 0

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
// session at focusReviewIdx.
func TestFocusMode_RKey_NonEmptyQueue_OpensReviewPanel(t *testing.T) {
	app, _, sessR := makeFocusModeMRApp(t)
	app.focusCursorSection = focusSectionReview
	app.focusReviewIdx = 0

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

// TestReviewPanel_CKey_MarksComplete verifies that pressing "c" in the review
// panel transitions the session to LifecycleComplete and closes the panel.
// This unblocks workflows where a session has no PR (design docs, exploration)
// and the user wants to take it off the review queue.
func TestReviewPanel_CKey_MarksComplete(t *testing.T) {
	app, _, sessR := makeFocusModeMRApp(t)
	sessR.SetLifecyclePhase(agent.LifecycleInReview)
	app.reviewSession = sessR
	app.dashboard.panelFocus = focusReview

	model, _ := app.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	app = model.(App)

	if sessR.LifecyclePhase() != agent.LifecycleComplete {
		t.Errorf("expected sessR phase=Complete, got %v", sessR.LifecyclePhase())
	}
	if app.dashboard.panelFocus != focusList {
		t.Errorf("expected panelFocus=focusList after c, got %v", app.dashboard.panelFocus)
	}
	if app.reviewSession != nil {
		t.Errorf("expected reviewSession cleared, got %v", app.reviewSession)
	}
}

// TestReviewPanel_TKey_NoAgents_ShowsError verifies that pressing "t" in the
// review panel when the session has no agents (synthetic test scenario)
// surfaces an error and clears reviewSession. Spawning a real-PTY-backed
// agent for the success path is covered by TestFocusModeEnterOnActiveOpensFocusLaunch
// since the underlying helper (openSessionInFocusLaunch) is shared.
func TestReviewPanel_TKey_NoAgents_ShowsError(t *testing.T) {
	app, _, sessR := makeFocusModeMRApp(t)
	sessR.SetLifecyclePhase(agent.LifecycleInReview)
	app.reviewSession = sessR
	app.dashboard.panelFocus = focusReview

	model, _ := app.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	app = model.(App)

	if app.err == "" {
		t.Fatal("expected error when session has no agents")
	}
	if !strings.Contains(app.err, "no agents") {
		t.Errorf("expected error to mention no agents, got %q", app.err)
	}
	if app.reviewSession != nil {
		t.Errorf("expected reviewSession cleared after t, got %v", app.reviewSession)
	}
	// Phase preserved so the session is still in REVIEW QUEUE.
	if sessR.LifecyclePhase() != agent.LifecycleInReview {
		t.Errorf("expected sessR phase preserved=InReview, got %v", sessR.LifecyclePhase())
	}
}

// TestReviewPanel_PKey_NoPR_DoesNotOrphan verifies the regression: pressing
// "p" with no PR cached must NOT make the session unreachable. Even though
// the session is already in LifecycleInReview, reviewQueueSessions() must
// still surface it so the user can re-enter the panel after pressing ESC.
func TestReviewPanel_PKey_NoPR_DoesNotOrphan(t *testing.T) {
	app, _, sessR := makeFocusModeMRApp(t)
	sessR.SetLifecyclePhase(agent.LifecycleInReview)
	app.reviewSession = sessR
	app.dashboard.panelFocus = focusReview

	model, _ := app.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	app = model.(App)

	if app.err == "" {
		t.Fatal("expected error message when pressing p with no cached PR")
	}
	if !strings.Contains(app.err, "terminal") || !strings.Contains(app.err, "complete") {
		t.Errorf("expected error to suggest t/c alternatives, got %q", app.err)
	}

	// Press ESC to close the panel — session stays InReview.
	model, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	app = model.(App)

	if app.dashboard.panelFocus != focusList {
		t.Errorf("expected panelFocus=focusList after esc, got %v", app.dashboard.panelFocus)
	}
	if sessR.LifecyclePhase() != agent.LifecycleInReview {
		t.Errorf("expected sessR phase=InReview after esc, got %v", sessR.LifecyclePhase())
	}

	// Regression: the InReview session must still appear in the review queue.
	queue := app.dashboard.reviewQueueSessions()
	found := false
	for _, item := range queue {
		if item.session == sessR {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("InReview session orphaned: not present in reviewQueueSessions() after p+esc with no PR")
	}
}

// TestPipeline_DKey_OpensDiffViewer verifies that pressing 'd' on a session in
// the pipeline opens the diff viewer for that session's worktree. (When the
// worktree is empty/unwritten, diffmodel.Parse returns an empty model, but the
// viewer should still open.)
func TestPipeline_DKey_OpensDiffViewer(t *testing.T) {
	dir, err := os.MkdirTemp("", "baton-pipeline-d-*")
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

	sess, _, err := mgr.CreateSessionWithCommand(agent.Config{
		Name: "diff-d", Task: "test", RepoPath: dir, Rows: 24, Cols: 80,
	}, func(_ string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 30") })
	if err != nil {
		t.Fatal(err)
	}
	sess.SetLifecyclePhase(agent.LifecycleInProgress)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: dir, repoName: "repo"},
		{kind: listItemSession, repoPath: dir, session: sess},
	}
	app.focusCursorSection = focusSectionBuilding
	app.focusBuildingIdx = 0

	model, _ := app.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	app = model.(App)
	if app.view != ViewDiff {
		t.Errorf("expected view=ViewDiff after d, got %v", app.view)
	}
}

// TestPipeline_SKey_OpensSettings verifies that 's' opens the global settings overlay.
func TestPipeline_SKey_OpensSettings(t *testing.T) {
	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39

	model, _ := app.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	app = model.(App)
	if app.view != ViewGlobalConfig {
		t.Errorf("expected view=ViewGlobalConfig after s, got %v", app.view)
	}
}

// TestPipeline_AKey_OpensFileBrowser verifies that 'a' opens the file browser.
func TestPipeline_AKey_OpensFileBrowser(t *testing.T) {
	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39

	model, _ := app.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	app = model.(App)
	if app.view != ViewFileBrowser {
		t.Errorf("expected view=ViewFileBrowser after a, got %v", app.view)
	}
}

// TestPipeline_XKey_NoSession verifies that 'x' on an empty pipeline produces
// a friendly error and does not crash.
func TestPipeline_XKey_NoSession(t *testing.T) {
	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39

	model, _ := app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	app = model.(App)
	if app.err == "" {
		t.Errorf("expected an error message when pressing x with no session selected")
	}
	if app.view != ViewDashboard {
		t.Errorf("expected view=ViewDashboard, got %v", app.view)
	}
}

// TestPipeline_PKey_NoPRSilent verifies that 'p' with no cached PR is a no-op
// (doesn't surface an error).
func TestPipeline_PKey_NoPRSilent(t *testing.T) {
	sess := agent.NewSessionForTest("s", "active-a")
	sess.SetLifecyclePhase(agent.LifecycleInProgress)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sess},
	}
	app.focusCursorSection = focusSectionBuilding
	app.focusBuildingIdx = 0

	model, _ := app.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	app = model.(App)
	if app.err != "" {
		t.Errorf("expected no error from p with no cached PR, got %q", app.err)
	}
}

// TestPrimaryAgent_PrefersActiveOverIdle verifies the priority order used by
// pipeline workflow keys (c, x) when picking a deterministic agent.
func TestPrimaryAgent_PrefersActiveOverIdle(t *testing.T) {
	sess := agent.NewSessionForTest("s", "session")
	idleAgent := sess.AddTestAgent("a-idle", false, agent.StatusIdle)
	activeAgent := sess.AddTestAgent("a-active", false, agent.StatusActive)

	if got := sess.PrimaryAgent(); got != activeAgent {
		t.Errorf("PrimaryAgent should prefer Active over Idle: got=%v want=%v", got, activeAgent)
	}
	_ = idleAgent
}

// TestPipelinePRClickResetsDoubleClick guards against a phantom double-click:
// when the user clicks the PR-indicator on a review row and then quickly
// clicks the same review card, the second click must NOT be interpreted as a
// double-click that opens the review panel. The fix is to reset
// lastPipelineClick after the PR-activation early return.
func TestPipelinePRClickResetsDoubleClick(t *testing.T) {
	sessR := agent.NewSessionForTest("r", "review-r")
	sessR.SetLifecyclePhase(agent.LifecycleReadyForReview)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sessR},
	}
	// Seed a PR cache entry so the indicator shows up and the early-return path is reachable.
	app.prCache = map[string]*prCacheEntry{
		sessR.ID: {pr: &github.PRState{Number: 42, URL: ""}},
	}

	// First click on the right edge of the review row triggers the PR
	// early-return path. URL is empty so openURL is skipped, but the early
	// return runs.
	app.dashboard.prCache = app.prCache
	prClick := tea.MouseClickMsg{Button: tea.MouseLeft, X: app.width - 2, Y: 8}
	model, _ := app.Update(prClick)
	app = model.(App)
	if !app.lastPipelineClick.IsZero() {
		// Empty URL skipped the early return — set a URL and try again so
		// the test exercises the path we actually care about.
		app.prCache[sessR.ID].pr.URL = "https://example/pr/42"
		app.dashboard.prCache = app.prCache
		model, _ = app.Update(prClick)
		app = model.(App)
	}

	// Second click within the double-click window on the same card. With the
	// fix, lastPipelineClick was zeroed by the PR-click early return, so this
	// is a fresh single click — not a double-click — and panelFocus stays out
	// of focusReview.
	cardClick := tea.MouseClickMsg{Button: tea.MouseLeft, X: 30, Y: 8}
	model, _ = app.Update(cardClick)
	app = model.(App)

	if app.dashboard.panelFocus == focusReview {
		t.Fatalf("expected card click after PR click to single-click only, but it triggered focusReview (phantom double-click)")
	}
}

// TestRepoPathForSession_FindsSessionsAcrossMultiRepo verifies that
// repoPathForSession returns the owning repo of a session even when
// activeRepo points elsewhere — the multi-repo correctness condition the
// review-panel `'e'` key (and the defensive fetchReviewDiffCmd update) both
// depend on. Without this, pressing `'e'` on a session in a non-active repo
// would resolve the IDE command from the wrong repo's resolvedCache.
func TestRepoPathForSession_FindsSessionsAcrossMultiRepo(t *testing.T) {
	dir, err := os.MkdirTemp("", "baton-repopath-*")
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

	sess, _, err := mgr.CreateSessionWithCommand(agent.Config{
		Name: "lookup", Task: "test", RepoPath: dir, Rows: 24, Cols: 80,
	}, func(_ string) *exec.Cmd { return exec.Command("bash", "-c", "sleep 30") })
	if err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.managers[dir] = mgr
	// activeRepo deliberately points elsewhere so the test fails loudly if
	// repoPathForSession ever falls back to activeRepo.
	app.activeRepo = "/nonexistent/wrong-repo"
	// cfg must list the real repo for repoPathForSession to walk it.
	app.cfg = &config.Config{Repos: []config.Repo{{Path: dir}}}

	got := app.repoPathForSession(sess.ID)
	if got != dir {
		t.Fatalf("repoPathForSession = %q, want %q (must not fall back to activeRepo)", got, dir)
	}

	// Unknown session ID returns "" so call sites can fall back to activeRepo.
	if got := app.repoPathForSession("does-not-exist"); got != "" {
		t.Errorf("repoPathForSession(unknown) = %q, want \"\"", got)
	}
}

// makeFourPhaseApp wires up an app with one session in each of the four
// pipeline phases so tests can exercise navigation across all sections without
// requiring a real manager / process. Sessions are constructed via
// NewSessionForTest so callers can attach AddTestAgent rows when they need
// agent state to drive IsReviewable / status badges.
func makeFourPhaseApp(t *testing.T) (App, *agent.Session, *agent.Session, *agent.Session, *agent.Session) {
	t.Helper()
	sessP := agent.NewSessionForTest("p", "planning")
	sessP.SetLifecyclePhase(agent.LifecyclePlanning)
	sessB := agent.NewSessionForTest("b", "building")
	sessB.SetLifecyclePhase(agent.LifecycleInProgress)
	sessR := agent.NewSessionForTest("r", "review")
	sessR.SetLifecyclePhase(agent.LifecycleReadyForReview)
	sessS := agent.NewSessionForTest("s", "shipping")
	sessS.SetLifecyclePhase(agent.LifecycleShipping)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sessP},
		{kind: listItemSession, repoPath: "/r", session: sessB},
		{kind: listItemSession, repoPath: "/r", session: sessR},
		{kind: listItemSession, repoPath: "/r", session: sessS},
	}
	app.focusCursorSection = focusSectionPlanning
	return app, sessP, sessB, sessR, sessS
}

// TestFourPhaseNavigation_JKWalksAllSections verifies that j moves through the
// pipeline in render order Planning → Building → Reviewing → Shipping with one
// keystroke per row, and k walks back the same path.
func TestFourPhaseNavigation_JKWalksAllSections(t *testing.T) {
	app, _, _, _, _ := makeFourPhaseApp(t)

	want := []focusSection{
		focusSectionBuilding,
		focusSectionReview,
		focusSectionShipping,
	}
	for i, section := range want {
		model, _ := app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		app = model.(App)
		if app.focusCursorSection != section {
			t.Fatalf("after %d j-presses: expected section %v, got %v", i+1, section, app.focusCursorSection)
		}
	}
	// k walks back: shipping → review → building → planning.
	wantBack := []focusSection{
		focusSectionReview,
		focusSectionBuilding,
		focusSectionPlanning,
	}
	for i, section := range wantBack {
		model, _ := app.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
		app = model.(App)
		if app.focusCursorSection != section {
			t.Fatalf("after %d k-presses: expected section %v, got %v", i+1, section, app.focusCursorSection)
		}
	}
}

// TestFourPhaseNavigation_SkipsEmptySections verifies that with an empty
// Building section, j moves directly Planning → Reviewing instead of stopping
// on an empty Building cursor.
func TestFourPhaseNavigation_SkipsEmptySections(t *testing.T) {
	app, _, sessB, _, _ := makeFourPhaseApp(t)
	// Demote the Building session into Planning so Building is empty; the
	// cursor should still skip across to Reviewing on j.
	sessB.SetLifecyclePhase(agent.LifecyclePlanning)

	// Cursor starts on Planning section; press j to move past Planning's two
	// rows (the original planning session + the demoted one).
	model, _ := app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	app = model.(App)
	if app.focusCursorSection != focusSectionPlanning || app.focusPlanningIdx != 1 {
		t.Fatalf("first j: expected planning[1], got section=%v idx=%d", app.focusCursorSection, app.focusPlanningIdx)
	}
	model, _ = app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	app = model.(App)
	if app.focusCursorSection != focusSectionReview {
		t.Fatalf("second j (past empty Building): expected Reviewing, got %v", app.focusCursorSection)
	}
}

// TestBKey_AdvancesPlanningToBuilding verifies that pressing 'b' on a Planning
// row transitions it to LifecycleInProgress and the cursor lands on a still-
// non-empty section after clamp.
func TestBKey_AdvancesPlanningToBuilding(t *testing.T) {
	app, sessP, _, _, _ := makeFourPhaseApp(t)
	app.focusCursorSection = focusSectionPlanning
	app.focusPlanningIdx = 0

	model, _ := app.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	app = model.(App)
	if sessP.LifecyclePhase() != agent.LifecycleInProgress {
		t.Fatalf("expected Planning → InProgress on 'b', got %v", sessP.LifecyclePhase())
	}
	// Planning had only one row, so the cursor should clamp to a non-empty
	// section (Building, where the row just moved).
	if app.focusCursorSection != focusSectionBuilding {
		t.Fatalf("after 'b': expected cursor on Building, got %v", app.focusCursorSection)
	}
}

// TestMKey_FromPlanning_AdvancesToReady verifies that pressing 'm' on a
// Planning session whose agent is idle skips Building and transitions
// directly to ReadyForReview. This matches the "press m to review" cue
// rendered on any idle-reviewable card and supports the natural flow when
// Claude finishes the requested work in one shot.
func TestMKey_FromPlanning_AdvancesToReady(t *testing.T) {
	app, sessP, _, _, _ := makeFourPhaseApp(t)
	sessP.AddTestAgent("p-1", false, agent.StatusIdle)
	app.focusCursorSection = focusSectionPlanning
	app.focusPlanningIdx = 0

	model, _ := app.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	app = model.(App)
	if sessP.LifecyclePhase() != agent.LifecycleReadyForReview {
		t.Fatalf("expected Planning → ReadyForReview on 'm' (skipping Building), got %v", sessP.LifecyclePhase())
	}
}

// TestBKey_OutsidePlanning_FallsThroughToBreak verifies that 'b' on a
// non-Planning section is NOT a session transition — it falls through to the
// existing global "take a break" handler so the Planning advance and the
// wellness break can share the same physical key.
func TestBKey_OutsidePlanning_FallsThroughToBreak(t *testing.T) {
	app, _, sessB, _, _ := makeFourPhaseApp(t)
	app.focusCursorSection = focusSectionBuilding
	app.focusBuildingIdx = 0
	if app.focusBreakMode {
		t.Fatal("precondition: focusBreakMode should be false")
	}

	model, _ := app.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	app = model.(App)
	if !app.focusBreakMode {
		t.Fatal("expected 'b' outside Planning to engage the break overlay")
	}
	if sessB.LifecyclePhase() != agent.LifecycleInProgress {
		t.Errorf("Building session phase changed unexpectedly: %v", sessB.LifecyclePhase())
	}
}

// TestActivateFocusCursor_Shipping_OpensPRWhenURLCached verifies that pressing
// enter (or double-clicking) a Shipping row with a cached PR URL takes the
// PR-open branch: it returns ok=true without dropping the user into
// focusLaunch, so they end up in the browser rather than back-to-back agent
// terminal + browser tab. openURL itself fires fire-and-forget and may launch
// a real browser in the test environment — the existing review-queue PR-click
// tests rely on the same pattern, so we stay consistent.
func TestActivateFocusCursor_Shipping_OpensPRWhenURLCached(t *testing.T) {
	sessS := agent.NewSessionForTest("s", "ship-s")
	sessS.SetLifecyclePhase(agent.LifecycleShipping)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sessS},
	}
	app.prCache = map[string]*prCacheEntry{
		sessS.ID: {pr: &github.PRState{Number: 7, URL: "https://example/pr/7"}},
	}
	app.focusCursorSection = focusSectionShipping
	app.focusShippingIdx = 0

	_, ok := app.activateFocusCursor()
	if !ok {
		t.Fatal("expected activateFocusCursor on Shipping with cached URL to return ok=true")
	}
	if app.dashboard.panelFocus == focusLaunch {
		t.Fatalf("expected panelFocus to stay out of focusLaunch when PR URL was opened, got %v", app.dashboard.panelFocus)
	}
}

// TestActivateFocusCursor_Shipping_FallsBackToTerminalWithoutURL verifies that
// activating a Shipping row whose PR isn't cached yet (or has no URL) falls
// through to openSessionInFocusLaunch instead of silently no-op'ing — so the
// user can still drive the agent (e.g. run gh pr create manually). With a test
// session that has zero agents, openSessionInFocusLaunch returns false; we
// only need to assert that the PR-open early return is NOT taken (panelFocus
// would stay out of focusLaunch in either case, but ok=false distinguishes
// "no agents to open" from the URL-present "ok=true and skipped focusLaunch"
// path above).
func TestActivateFocusCursor_Shipping_FallsBackToTerminalWithoutURL(t *testing.T) {
	sessS := agent.NewSessionForTest("s", "ship-s")
	sessS.SetLifecyclePhase(agent.LifecycleShipping)

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.dashboard.items = []listItem{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sessS},
	}
	// No prCache entry: the URL branch is unreachable so activate falls
	// through to openSessionInFocusLaunch, which returns false because the
	// test session has no agents — exactly the dispatch we want to pin.
	app.focusCursorSection = focusSectionShipping
	app.focusShippingIdx = 0

	_, ok := app.activateFocusCursor()
	if ok {
		t.Fatalf("expected ok=false from openSessionInFocusLaunch fallback (no agents), got ok=true")
	}
}

// TestNKeyOpensPromptModal_WhenPlanFirstEnabled verifies the new plan-first
// gate: with PlanFirstEnabled=true, pressing `n` opens the prompt modal
// instead of immediately creating a session. With the flag off (today's
// default), `n` continues the legacy spawn path covered by
// TestCreateAgentViaN.
func TestNKeyOpensPromptModal_WhenPlanFirstEnabled(t *testing.T) {
	dir, err := os.MkdirTemp("", "baton-tui-planfirst-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	for _, args := range [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "t@t.com"},
		{"git", "config", "user.name", "T"},
		{"git", "config", "commit.gpgsign", "false"},
		{"git", "commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup %v: %v\n%s", args, err, out)
		}
	}

	enabled := true
	resolved := config.Resolve(&config.GlobalSettings{PlanFirstEnabled: &enabled}, nil)
	mgr := agent.NewManager(dir, resolved)
	defer mgr.Shutdown()

	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.managers[dir] = mgr
	app.activeRepo = dir
	app.resolvedCache[dir] = resolved
	app.promptModal.SetSize(app.width, app.height-1)

	model, cmd := app.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	app = model.(App)
	if !app.promptModal.Active() {
		t.Fatal("PlanFirstEnabled n press should open the prompt modal")
	}
	if mgr.AgentCount() != 0 {
		t.Errorf("no agent should spawn when modal is open, got %d", mgr.AgentCount())
	}
	// Drain the focus cmd if any (textarea blink scheduler).
	if cmd != nil {
		_ = cmd()
	}

	// Esc dismisses without side effects.
	model, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	app = model.(App)
	if cmd != nil {
		// Cancel cmd needs to flow through Update.
		model, _ = app.Update(cmd())
		app = model.(App)
	}
	if app.promptModal.Active() {
		t.Error("modal should close on esc")
	}
	if mgr.AgentCount() != 0 {
		t.Errorf("no agent should spawn after esc, got %d", mgr.AgentCount())
	}
	if len(mgr.ListSessions()) != 0 {
		t.Errorf("no session should exist after esc, got %d", len(mgr.ListSessions()))
	}
}
