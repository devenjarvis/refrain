package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/github"
)

// --- layout -----------------------------------------------------------------

func TestBuildSessionListLayout_GroupsAndOffsets(t *testing.T) {
	sessA := agent.NewSessionForTest("a", "alpha")
	sessB := agent.NewSessionForTest("b", "beta")
	sessC := agent.NewSessionForTest("c", "gamma")
	items := listItems{
		{kind: listItemRepo, repoPath: "/one", repoName: "one"},
		{kind: listItemSession, repoPath: "/one", session: sessA},
		{kind: listItemSession, repoPath: "/one", session: sessB},
		{kind: listItemRepo, repoPath: "/two", repoName: "two"},
		{kind: listItemSession, repoPath: "/two", session: sessC},
	}

	l := buildSessionListLayout(items)
	if len(l.groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(l.groups))
	}
	if len(l.rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(l.rows))
	}
	// one(0) a(1-2) b(3-4) two(5) c(6-7)
	wantStarts := []int{1, 3, 6}
	for i, want := range wantStarts {
		if l.rowStart[i] != want {
			t.Errorf("rowStart[%d] = %d, want %d", i, l.rowStart[i], want)
		}
	}
	if l.total != 8 {
		t.Errorf("total = %d, want 8", l.total)
	}
}

func TestBuildSessionListLayout_EmptyRepoGetsHintLine(t *testing.T) {
	sess := agent.NewSessionForTest("a", "alpha")
	items := listItems{
		{kind: listItemRepo, repoPath: "/empty", repoName: "empty"},
		{kind: listItemRepo, repoPath: "/busy", repoName: "busy"},
		{kind: listItemSession, repoPath: "/busy", session: sess},
	}
	l := buildSessionListLayout(items)
	// empty(0) "no sessions"(1) busy(2) a(3-4)
	if len(l.rows) != 1 || l.rowStart[0] != 3 {
		t.Fatalf("rowStart = %v, want [3]", l.rowStart)
	}
	if l.total != 5 {
		t.Errorf("total = %d, want 5", l.total)
	}
}

func TestBuildSessionListLayout_HidesCompleteSessions(t *testing.T) {
	done := agent.NewSessionForTest("done", "merged-work")
	done.SetLifecyclePhase(agent.LifecycleComplete)
	live := agent.NewSessionForTest("live", "active-work")
	items := listItems{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: done},
		{kind: listItemSession, repoPath: "/r", session: live},
	}
	l := buildSessionListLayout(items)
	if len(l.rows) != 1 {
		t.Fatalf("rows = %d, want 1 (Complete session hidden)", len(l.rows))
	}
	if l.rows[0].session != live {
		t.Error("surviving row should be the live session")
	}
}

// --- cursor & scroll ----------------------------------------------------------

func TestSessionListCursor_MoveAndClamp(t *testing.T) {
	items := listItems{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: agent.NewSessionForTest("a", "a")},
		{kind: listItemSession, repoPath: "/r", session: agent.NewSessionForTest("b", "b")},
	}
	l := buildSessionListLayout(items)
	m := newSessionListModel()
	m.SetSize(80, 24)

	m.moveCursor(1, l)
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.cursor)
	}
	m.moveCursor(1, l)
	if m.cursor != 1 {
		t.Fatalf("cursor should clamp at last row, got %d", m.cursor)
	}
	m.moveCursor(-1, l)
	m.moveCursor(-1, l)
	if m.cursor != 0 {
		t.Fatalf("cursor should clamp at 0, got %d", m.cursor)
	}

	// Shrink the list: clamp pulls an out-of-range cursor back in.
	m.cursor = 5
	m.clamp(l)
	if m.cursor != 1 {
		t.Fatalf("clamp: cursor = %d, want 1", m.cursor)
	}
}

func TestSessionListScroll_KeepsCursorVisible(t *testing.T) {
	items := listItems{{kind: listItemRepo, repoPath: "/r", repoName: "repo"}}
	for i := 0; i < 10; i++ {
		items = append(items, listItem{
			kind: listItemSession, repoPath: "/r",
			session: agent.NewSessionForTest(string(rune('a'+i)), "s"),
		})
	}
	l := buildSessionListLayout(items)
	m := newSessionListModel()
	m.SetSize(80, 6) // 6 visible lines; 21 content lines

	for i := 0; i < 9; i++ {
		m.moveCursor(1, l)
	}
	// Last row's lines are 19-20; scroll must include them.
	bottom := l.rowStart[9] + sessionCardLines - 1
	if bottom < m.scroll || bottom >= m.scroll+m.height {
		t.Fatalf("cursor card (line %d) not visible in window [%d,%d)", bottom, m.scroll, m.scroll+m.height)
	}
	// Move back to the top: scroll follows.
	for i := 0; i < 9; i++ {
		m.moveCursor(-1, l)
	}
	if m.scroll > l.rowStart[0] {
		t.Fatalf("scroll = %d, want <= %d after returning to top", m.scroll, l.rowStart[0])
	}
}

func TestSessionListRowAt_HitTest(t *testing.T) {
	items := listItems{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: agent.NewSessionForTest("a", "a")},
		{kind: listItemSession, repoPath: "/r", session: agent.NewSessionForTest("b", "b")},
	}
	l := buildSessionListLayout(items)
	m := newSessionListModel()
	m.SetSize(80, 24)

	if _, _, ok := m.rowAt(l, 0); ok {
		t.Error("repo header line must not hit a row")
	}
	idx, first, ok := m.rowAt(l, 1)
	if !ok || idx != 0 || !first {
		t.Errorf("line 1: got (%d, %v, %v), want (0, true, true)", idx, first, ok)
	}
	idx, first, ok = m.rowAt(l, 2)
	if !ok || idx != 0 || first {
		t.Errorf("line 2: got (%d, %v, %v), want (0, false, true)", idx, first, ok)
	}
	idx, _, ok = m.rowAt(l, 4)
	if !ok || idx != 1 {
		t.Errorf("line 4: got (%d, %v), want row 1", idx, ok)
	}
	if _, _, ok := m.rowAt(l, 9); ok {
		t.Error("line past content must not hit")
	}

	// Scrolled: content line = viewport line + scroll.
	m.scroll = 3
	idx, _, ok = m.rowAt(l, 0)
	if !ok || idx != 1 {
		t.Errorf("scrolled line 0: got (%d, %v), want row 1", idx, ok)
	}
}

// --- rendering ----------------------------------------------------------------

func listPropsFor(items listItems) sessionListProps {
	return sessionListProps{
		items:           items,
		prCache:         map[string]*prCacheEntry{},
		closingSessions: map[string]bool{},
	}
}

func TestSessionListView_RendersCardsAndHeaders(t *testing.T) {
	sess := agent.NewSessionForTest("a", "add-dark-mode")
	sess.AddTestAgent("ag-1", false, agent.StatusWaiting)
	items := listItems{
		{kind: listItemRepo, repoPath: "/r", repoName: "refrain"},
		{kind: listItemSession, repoPath: "/r", session: sess},
	}
	m := newSessionListModel()
	m.SetSize(120, 30)
	m.now = time.Now()
	props := listPropsFor(items)
	props.activeRepoPath = "/r"

	out := ansi.Strip(m.View(props))
	for _, want := range []string{"refrain", "add-dark-mode", "waiting", "worktree", "1 agent"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "▍") {
		t.Errorf("selected row should render the cursor bar glyph:\n%s", out)
	}
	if !strings.Contains(out, "⏸") {
		t.Errorf("waiting session should render the waiting glyph:\n%s", out)
	}
}

func TestSessionListView_StatusSeverityAggregation(t *testing.T) {
	cases := []struct {
		name     string
		statuses []agent.Status
		want     string
	}{
		{"error wins", []agent.Status{agent.StatusActive, agent.StatusError}, "error"},
		{"waiting over active", []agent.Status{agent.StatusActive, agent.StatusWaiting}, "waiting"},
		{"active over idle", []agent.Status{agent.StatusIdle, agent.StatusActive}, "active"},
		{"all idle", []agent.Status{agent.StatusIdle, agent.StatusIdle}, "idle"},
		{"all done", []agent.Status{agent.StatusDone}, "done"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sess := agent.NewSessionForTest("s", "sess")
			for i, st := range tc.statuses {
				sess.AddTestAgent("ag-"+string(rune('a'+i)), false, st)
			}
			_, word, _, ok := sessionListStatus(sess)
			if !ok || word != tc.want {
				t.Errorf("sessionListStatus = (%q, %v), want %q", word, ok, tc.want)
			}
		})
	}
}

func TestSessionListView_CheckoutTag(t *testing.T) {
	sess := agent.NewSessionForTest("c", "main")
	sess.SetKindForTest(agent.KindCheckout)
	sess.UpdateBranch("main")
	sess.AddTestAgent("ag-1", false, agent.StatusActive)
	items := listItems{
		{kind: listItemRepo, repoPath: "/r", repoName: "dotfiles"},
		{kind: listItemSession, repoPath: "/r", session: sess},
	}
	m := newSessionListModel()
	m.SetSize(120, 30)
	m.now = time.Now()

	out := ansi.Strip(m.View(listPropsFor(items)))
	if !strings.Contains(out, "checkout @ main") {
		t.Errorf("checkout session should render its distinct context tag:\n%s", out)
	}
	if strings.Contains(out, "worktree") {
		t.Errorf("checkout session must not read as a worktree:\n%s", out)
	}
}

func TestSessionListView_Badges(t *testing.T) {
	planSess := agent.NewSessionForTestWithPath("p", "with-plan", t.TempDir())
	if err := planSess.WritePlan("# Goal\n\n## Tasks\n- [ ] do it\n"); err != nil {
		t.Fatal(err)
	}
	prSess := agent.NewSessionForTest("pr", "with-pr")
	closing := agent.NewSessionForTest("x", "closing-now")
	items := listItems{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: planSess},
		{kind: listItemSession, repoPath: "/r", session: prSess},
		{kind: listItemSession, repoPath: "/r", session: closing},
	}
	props := listPropsFor(items)
	props.prCache[cacheKey("/r", "pr")] = &prCacheEntry{
		pr:     &github.PRState{Number: 42, URL: "https://example.com/42"},
		checks: &github.CheckStatus{State: "failure", Failed: 2, Total: 3},
	}
	props.closingSessions[cacheKey("/r", "x")] = true

	m := newSessionListModel()
	m.SetSize(120, 30)
	m.now = time.Now()
	out := ansi.Strip(m.View(props))

	for _, want := range []string{"plan", "#42", "CI 2/3 failing", "closing…"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing badge %q:\n%s", want, out)
		}
	}
}

func TestSessionListView_EmptyStates(t *testing.T) {
	m := newSessionListModel()
	m.SetSize(80, 20)
	m.now = time.Now()

	// No repos at all → centered empty state with the three hints.
	out := ansi.Strip(m.View(listPropsFor(nil)))
	for _, want := range []string{"No sessions", "new session", "open a branch or PR", "add repo"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty view missing %q:\n%s", want, out)
		}
	}

	// Repos but no sessions → headers plus the hint block.
	items := listItems{{kind: listItemRepo, repoPath: "/r", repoName: "refrain"}}
	out = ansi.Strip(m.View(listPropsFor(items)))
	for _, want := range []string{"refrain", "no sessions", "new session"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty-repo view missing %q:\n%s", want, out)
		}
	}
}

func TestSessionListView_DraftingBadge(t *testing.T) {
	sess := agent.NewSessionForTest("d", "drafting-sess")
	if !sess.TryStartDraft(func() {}) {
		t.Fatal("TryStartDraft failed")
	}
	items := listItems{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sess},
	}
	m := newSessionListModel()
	m.SetSize(120, 30)
	m.now = time.Now()
	out := ansi.Strip(m.View(listPropsFor(items)))
	if !strings.Contains(out, "drafting…") {
		t.Errorf("drafting session should render the drafting badge:\n%s", out)
	}
}

// --- App integration ------------------------------------------------------------

func newListApp(t *testing.T, sessions ...*agent.Session) App {
	t.Helper()
	app := NewApp()
	app.width = 120
	app.height = 40
	app.sessionList.SetSize(120, 39)
	app.launch.SetSize(120, 39)
	items := []listItem{{kind: listItemRepo, repoPath: "/r", repoName: "repo"}}
	for _, s := range sessions {
		items = append(items, listItem{kind: listItemSession, repoPath: "/r", session: s})
	}
	seedDashboardItems(&app, items)
	return app
}

func TestSessionList_JKMovesFlatCursor(t *testing.T) {
	app := newListApp(t,
		agent.NewSessionForTest("a", "a"),
		agent.NewSessionForTest("b", "b"),
		agent.NewSessionForTest("c", "c"),
	)

	for i, want := range []int{1, 2, 2} {
		model, _ := app.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		app = model.(App)
		if app.sessionList.cursor != want {
			t.Fatalf("after %d j presses: cursor = %d, want %d", i+1, app.sessionList.cursor, want)
		}
	}
	model, _ := app.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	app = model.(App)
	if app.sessionList.cursor != 1 {
		t.Fatalf("after k: cursor = %d, want 1", app.sessionList.cursor)
	}
}

func TestSessionList_EnterOpensTerminalForAnySession(t *testing.T) {
	// Even a Shipping-phase session opens the terminal — the old per-phase
	// dispatch (review panel / PR panel) is gone (§4.2).
	sess := agent.NewSessionForTest("s", "shipping-s")
	sess.SetLifecyclePhase(agent.LifecycleShipping)
	ag := sess.AddTestAgent("ag-1", false, agent.StatusIdle)
	app := newListApp(t, sess)

	model, _ := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Text: ""})
	app = model.(App)
	if app.modals.Current() != focusLaunch {
		t.Fatalf("expected focusLaunch after enter, got %v", app.modals.Current())
	}
	if app.modals.LaunchAgent() == nil || app.modals.LaunchAgent().ID != ag.ID {
		t.Fatalf("expected launch agent %q", ag.ID)
	}
}

func TestSessionList_EnterOnPlanSessionOpensPlanEditor(t *testing.T) {
	sess := agent.NewSessionForTestWithPath("p", "planned", t.TempDir())
	if err := sess.WritePlan("# Goal\n"); err != nil {
		t.Fatal(err)
	}
	app := newListApp(t, sess)

	model, _ := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Text: ""})
	app = model.(App)
	if app.modals.Current() != focusPlanEditor {
		t.Fatalf("expected focusPlanEditor for an agent-less planned session, got %v", app.modals.Current())
	}
}

func TestSessionList_ClickMovesCursor_DoubleClickActivates(t *testing.T) {
	sessA := agent.NewSessionForTest("a", "a")
	sessB := agent.NewSessionForTest("b", "b")
	sessB.AddTestAgent("ag-b", false, agent.StatusIdle)
	app := newListApp(t, sessA, sessB)

	// Layout: header(0) a(1-2) b(3-4). Click sessB's second line.
	click := tea.MouseClickMsg{Button: tea.MouseLeft, X: 10, Y: 4}
	model, _ := app.Update(click)
	app = model.(App)
	if app.sessionList.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 after click", app.sessionList.cursor)
	}
	if app.modals.Current() == focusLaunch {
		t.Fatal("single click must not activate")
	}

	model, _ = app.Update(click)
	app = model.(App)
	if app.modals.Current() != focusLaunch {
		t.Fatalf("expected focusLaunch after double-click, got %v", app.modals.Current())
	}

	// Right-click does nothing.
	app = newListApp(t, sessA, sessB)
	model, _ = app.Update(tea.MouseClickMsg{Button: tea.MouseRight, X: 10, Y: 4})
	app = model.(App)
	if app.sessionList.cursor != 0 {
		t.Fatal("right-click must not move the cursor")
	}
}

func TestSessionList_PKeyWithCachedPROpensPRPanel(t *testing.T) {
	sess := agent.NewSessionForTest("s", "shippable")
	app := newListApp(t, sess)
	app.prCache[cacheKey("/r", sess.ID)] = &prCacheEntry{
		pr: &github.PRState{Number: 7, URL: "https://example.com/7"},
	}

	model, _ := app.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	app = model.(App)
	if app.modals.Current() != focusPRPanel {
		t.Fatalf("expected PR panel (focusPRPanel) after p with cached PR, got %v", app.modals.Current())
	}
}

func TestSessionList_PKeyBusySessionRefusesDraft(t *testing.T) {
	sess := agent.NewSessionForTest("s", "busy")
	sess.AddTestAgent("ag", false, agent.StatusActive)
	app := newListApp(t, sess)
	app.ghClient = &github.Client{}

	model, _ := app.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	app = model.(App)
	if app.prDraftInFlight {
		t.Fatal("p on a busy session must not start a PR draft")
	}
	if app.err == "" {
		t.Fatal("expected a transient error explaining why the draft was refused")
	}
}

// TestSessionList_PlanKeyOnPlannedSessionOpensPlanEditor: `P` on a session
// that already has a plan opens the plan editor directly (rollback design
// §4.5 — plan is an action bound to a key, not a phase).
func TestSessionList_PlanKeyOnPlannedSessionOpensPlanEditor(t *testing.T) {
	sess := agent.NewSessionForTestWithPath("p", "planned", t.TempDir())
	if err := sess.WritePlan("# Goal\n"); err != nil {
		t.Fatal(err)
	}
	app := newListApp(t, sess)

	model, _ := app.Update(tea.KeyPressMsg{Code: 'P', Text: "P"})
	app = model.(App)
	if app.modals.Current() != focusPlanEditor {
		t.Fatalf("expected focusPlanEditor after P on a planned session, got %v", app.modals.Current())
	}
	if app.planGoal.Active() {
		t.Fatal("goal modal must not open when a plan already exists")
	}
}

// TestSessionList_PlanKeyOnPlanlessSessionOpensGoalModal: `P` with no plan
// prompts for a goal; esc dismisses without side effects.
func TestSessionList_PlanKeyOnPlanlessSessionOpensGoalModal(t *testing.T) {
	sess := agent.NewSessionForTest("s", "no-plan")
	app := newListApp(t, sess)

	model, _ := app.Update(tea.KeyPressMsg{Code: 'P', Text: "P"})
	app = model.(App)
	if !app.planGoal.Active() {
		t.Fatal("expected the plan-goal modal to open on P with no plan")
	}
	if app.modals.Current() == focusPlanEditor {
		t.Fatal("plan editor must not open before a goal is submitted")
	}

	model, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEscape, Text: ""})
	app = model.(App)
	if app.planGoal.Active() {
		t.Fatal("esc should close the goal modal")
	}
	if app.modals.Current() == focusPlanEditor {
		t.Fatal("dismissing the goal modal must not open the plan editor")
	}
}

// TestSessionListView_MergedBadgeAndCleanupHint: a merged PR renders the
// Merged phrase plus the "X to clean up" hint on the session's row — merged
// sessions park in the list until the user removes them (§4.7).
func TestSessionListView_MergedBadgeAndCleanupHint(t *testing.T) {
	sess := agent.NewSessionForTest("m", "merged-work")
	items := listItems{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sess},
	}
	props := listPropsFor(items)
	props.prCache[cacheKey("/r", "m")] = &prCacheEntry{
		pr: &github.PRState{Number: 9, State: "merged"},
	}

	m := newSessionListModel()
	m.SetSize(120, 30)
	m.now = time.Now()
	out := ansi.Strip(m.View(props))

	for _, want := range []string{"#9", "Merged", "X to clean up"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
}

func TestSessionList_NOpensNewSessionScreen(t *testing.T) {
	app := newListApp(t, agent.NewSessionForTest("a", "a"))

	model, _ := app.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	app = model.(App)
	if app.view != ViewNewSession {
		t.Fatalf("expected ViewNewSession after n, got %v", app.view)
	}
}

func TestSubmitPromptModal_CheckoutContext_UsesCreateSessionInDir(t *testing.T) {
	app := newListApp(t)
	fake := app.managers["/r"].(*fakeManager)
	app.activeRepo = "/r"

	model, cmd := app.Update(promptModalSubmitMsg{prompt: "debug it", context: contextCheckout})
	app = asApp(model)
	if cmd == nil {
		t.Fatal("expected create cmd")
	}
	msg := cmd()
	res, ok := msg.(createResultMsg)
	if !ok {
		t.Fatalf("got %T, want createResultMsg", msg)
	}
	if res.err != nil {
		t.Fatalf("unexpected error: %v", res.err)
	}
	if fake.lastCreateSessionInDirCfg.Task != "debug it" {
		t.Errorf("CreateSessionInDir Task = %q, want %q", fake.lastCreateSessionInDirCfg.Task, "debug it")
	}
	// The worktree path must not have been called.
	if fake.lastCreateSessionCfg.Task != "" {
		t.Error("CreateSession should not be called for a checkout submit")
	}
}

func TestSubmitPromptModal_PlanFirstCheckout_Refused(t *testing.T) {
	app := newListApp(t)
	app.activeRepo = "/r"

	model, cmd := app.Update(promptModalSubmitMsg{prompt: "plan it", planFirst: true, context: contextCheckout})
	app = asApp(model)
	if cmd != nil {
		t.Fatal("plan-first on a checkout context must not create anything yet")
	}
	if app.err == "" {
		t.Fatal("expected a transient error for plan-first + checkout")
	}
	if app.view != ViewDashboard {
		t.Fatalf("expected return to the session list, got %v", app.view)
	}
}

func TestSubmitPromptModal_EmptyPromptSpawnsBlankREPL(t *testing.T) {
	app := newListApp(t)
	fake := app.managers["/r"].(*fakeManager)
	app.activeRepo = "/r"

	model, cmd := app.Update(promptModalSubmitMsg{prompt: ""})
	_ = asApp(model)
	if cmd == nil {
		t.Fatal("expected create cmd for the blank-REPL submit")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("expected createResultMsg")
	}
	if fake.lastCreateSessionCfg.Task != "" {
		t.Errorf("blank REPL should carry no task, got %q", fake.lastCreateSessionCfg.Task)
	}
}
