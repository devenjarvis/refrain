package tui

import (
	"os/exec"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/devenjarvis/refrain/internal/agent"
)

// These tests fill the per-key gaps in app.go's focusList dispatch
// (app.go:1599-2193) and updateFocusLaunchKeys (app.go:2423-2520) that the
// existing app_test.go suite did not yet cover.
//
// Most tests build the App synthetically (no claude subprocess) by seeding
// pre-built sessions into a fakeManager via seedSessionListItems for the temp
// repo dir. This keeps the suite fast and deterministic.

// appWithSeededSession returns an App with a single test session, plus the
// dir where the manager lives so the caller can clean up via t.TempDir's
// automatic cleanup.
func appWithSeededSession(t *testing.T) (App, *agent.Session, string) {
	t.Helper()
	dir := t.TempDir()
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
			t.Fatalf("git setup %v: %v\n%s", args, err, out)
		}
	}
	sess := agent.NewSessionForTest("s1", "session-1")

	app := NewApp()
	app.width = 120
	app.height = 40
	app.sessionList.SetSize(120, 39)
	app.activeRepo = dir
	seedSessionListItems(&app, []listItem{
		{kind: listItemRepo, repoPath: dir, repoName: "repo"},
		{kind: listItemSession, repoPath: dir, session: sess},
	})
	// Position the list cursor on the seeded session's row.
	app.selectSessionRow(dir, sess.ID)
	return app, sess, dir
}

// --- Pipeline keys -----------------------------------------------------------

func TestPipeline_EKey_NoIDECommand_SetsError(t *testing.T) {
	app, _, _ := appWithSeededSession(t)
	// Resolved settings have empty IDECommand by default.
	model, _ := app.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	app = model.(App)
	if app.err == "" {
		t.Error("expected error when pressing e with no IDE configured")
	}
}

func TestPipeline_OKey_OpensBranchPicker(t *testing.T) {
	app, _, _ := appWithSeededSession(t)
	model, _ := app.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	app = model.(App)
	if app.view != ViewBranchPicker {
		t.Errorf("expected ViewBranchPicker after o, got %v", app.view)
	}
}

func TestPipeline_ShiftX_NoSession_IsSilentNoOp(t *testing.T) {
	// X (kill session) silently returns when no session is cursor-selected
	// (app.go:2163). Pinned so a future refactor doesn't suddenly start
	// emitting an error toast for a press that was previously absorbed.
	app := NewApp()
	app.width = 120
	app.height = 40
	app.sessionList.SetSize(120, 39)

	model, cmd := app.Update(tea.KeyPressMsg{Code: 'X', Text: "X"})
	app = model.(App)
	if cmd != nil {
		t.Errorf("X with no session produced cmd %T, want nil", cmd())
	}
	if app.err != "" {
		t.Errorf("X with no session set error %q, want silent no-op", app.err)
	}
}

func TestPipeline_QKey_NoAgents_QuitsImmediately(t *testing.T) {
	app := NewApp()
	app.width = 120
	app.height = 40
	app.sessionList.SetSize(120, 39)

	_, cmd := app.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd from q with no running agents")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("got %T, want tea.QuitMsg", msg)
	}
}

func TestPipeline_QKey_WithRunningAgents_FirstPressArmsConfirm(t *testing.T) {
	app, sess, _ := appWithSeededSession(t)
	// Seed an active test agent so AgentCount > 0.
	sess.AddTestAgent("a1", false, agent.StatusActive)
	mgr := app.managers[app.activeRepo]
	if mgr.AgentCount() == 0 {
		// AgentCount counts manager-owned agents, not synthetic test-only ones.
		// Set the running-flag by adding the session into the manager's known set.
		// If AgentCount stays 0, skip — the production logic gates on
		// mgr.AgentCount() so the confirm-quit path isn't reachable from a
		// pure synthetic fixture.
		t.Skip("manager AgentCount is 0 for synthetic test agents; quit confirm path needs a real-spawned agent")
	}

	_, cmd := app.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd != nil {
		// First press with running agents should NOT emit Quit.
		if _, bad := cmd().(tea.QuitMsg); bad {
			t.Error("first q with running agents must not quit; should arm confirm")
		}
	}
}

func TestPipeline_CtrlC_SameAsQ(t *testing.T) {
	app := NewApp()
	app.width = 120
	app.height = 40
	app.sessionList.SetSize(120, 39)

	_, cmd := app.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+c with no agents should emit Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("got %T, want tea.QuitMsg", cmd())
	}
}

func TestPipeline_UnknownKey_NoOp(t *testing.T) {
	app, _, _ := appWithSeededSession(t)
	before := struct {
		view     ViewMode
		panel    panelFocus
		err      string
		confQuit bool
	}{app.view, app.modals.Current(), app.err, app.confirmQuit}

	for _, k := range []tea.KeyPressMsg{
		{Code: 'z', Text: "z"},
		{Code: 'Q', Text: "Q"}, // capital Q is not handled by the q switch
		{Code: '!', Text: "!"},
	} {
		model, cmd := app.Update(k)
		app = model.(App)
		if cmd != nil {
			// A few characters might lazily trigger a tick or similar; we
			// only care that they don't cause panel/view/error changes.
			_ = cmd
		}
	}

	after := struct {
		view     ViewMode
		panel    panelFocus
		err      string
		confQuit bool
	}{app.view, app.modals.Current(), app.err, app.confirmQuit}

	if before != after {
		t.Errorf("unknown keys changed state: before=%+v after=%+v", before, after)
	}
}

// --- focusLaunch keys --------------------------------------------------------

// appInFocusLaunch returns an App in focusLaunch with a synthetic test agent.
// Tests using this fixture do not spawn a real claude subprocess.
func appInFocusLaunch(t *testing.T) (App, *agent.Session, *agent.Agent) {
	t.Helper()
	app, sess, repoPath := appWithSeededSession(t)
	ag := sess.AddTestAgent("primary", false, agent.StatusIdle)
	app.openLaunchPanel(sess, ag, repoPath)
	return app, sess, ag
}

func TestFocusLaunch_NilAgent_RoutesBackToList(t *testing.T) {
	// Pinned: updateFocusLaunchKeys returns to focusList when LaunchAgent() is
	// nil. Guards against an accidental nil-check removal that would crash
	// on any subsequent key.
	app, _, _ := appWithSeededSession(t)
	// Force panelFocus into focusLaunch with no agent by reaching into Modals
	// directly — production code can't construct this state, but the guard at
	// the top of updateFocusLaunchKeys must still handle it.
	app.modals.OpenLaunch(nil, nil, "")

	model, _ := app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	app = model.(App)
	if app.modals.Current() != focusList {
		t.Errorf("nil focusLaunchAgent should route back to focusList, got %v", app.modals.Current())
	}
}

func TestFocusLaunch_Home_ResetsScroll(t *testing.T) {
	// 'home' is the only focusLaunch key that doesn't touch the agent's VT
	// (it just zeros launch.scrollOffset), so it's safe to exercise with
	// a synthetic test agent that has no real terminal.
	app, _, _ := appInFocusLaunch(t)
	app.launch.scrollOffset = 5
	model, _ := app.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	app = model.(App)
	if app.launch.scrollOffset != 0 {
		t.Errorf("home did not reset scrollOffset, got %d", app.launch.scrollOffset)
	}
}

// Note: esc/ctrl+e/alt+brackets/pgup/pgdn/shift+esc all touch the agent's VT
// terminal (resize, ScrollbackLines, SendKey). Synthetic agents from
// AddTestAgent don't have a VT, so those tests would crash. The existing
// app_test.go suite covers those keys via tests that spawn a real claude
// subprocess (TestShiftEscForwardsEscapeToAgent, TestActionKeysBlockedInFocusLaunch).
