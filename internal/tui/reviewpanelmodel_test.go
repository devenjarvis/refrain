package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/config"
	"github.com/devenjarvis/refrain/internal/github"
)

// panel-level test pattern: instantiate the panel directly, supply a stub
// PanelServices, drive it with messages, assert state. No full App spin-up,
// no goroutines, no claude binary.

func newTestSvc() (PanelServices, *testServiceState) {
	state := &testServiceState{}
	svc := PanelServices{
		Width:  120,
		Height: 40,
		Manager: func(string) SessionManager {
			return nil
		},
		Resolved:    func(string) config.ResolvedSettings { return config.ResolvedSettings{} },
		GHClient:    func() *github.Client { return nil },
		PRCache:     func(string, string) *prCacheEntry { return nil },
		ReviewCache: func(string, string) *reviewDiffEntry { return nil },
		ClosePanel: func() {
			state.closed = true
		},
		OpenInLaunch: func(*agent.Session, string) bool {
			state.openInLaunchCalled = true
			return state.openInLaunchResult
		},
		OpenPlanEditor: func(*agent.Session, string) {},
		OpenURL:        func(string) error { return nil },
		SetError: func(msg string) {
			state.errMsg = msg
		},
		MergePRCmd: func(string, string, bool) tea.Cmd { return nil },
		StartPRDraftCmd: func(*agent.Session, string, bool) tea.Cmd {
			return func() tea.Msg { return nil }
		},
		KillSessionCmd: func(*agent.Session, string) tea.Cmd {
			state.killSessionCalled = true
			return func() tea.Msg { return nil }
		},
		FetchReviewDiff: func(*agent.Session, string) tea.Cmd { return nil },
		FeedbackTriage: func(string, string) map[string]*feedbackTriageEntry {
			return nil
		},
		SetFeedbackVerdict: func(string, string, string, feedbackVerdict) {},
		SetFeedbackNote:    func(string, string, string, string) {},
		prDraftInFlightFor: func(string, string) bool { return false },
	}
	return svc, state
}

type testServiceState struct {
	closed             bool
	errMsg             string
	openInLaunchCalled bool
	openInLaunchResult bool
	killSessionCalled  bool
}

// TestReviewPanelModel_TabSwitching verifies 1–3 and tab/shift+tab change activeTab.
func TestReviewPanelModel_TabSwitching(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	panel := newReviewPanel(sess, "", 120, 40)
	svc, _ := newTestSvc()

	press := func(msg tea.KeyPressMsg) {
		t.Helper()
		_, _ = panel.Update(msg, svc)
	}

	// Numeric keys jump directly.
	press(keyRune('2'))
	if panel.activeTab != reviewTabDiff {
		t.Errorf("'2': activeTab=%d, want %d (Diff)", panel.activeTab, reviewTabDiff)
	}
	press(keyRune('3'))
	if panel.activeTab != reviewTabChecks {
		t.Errorf("'3': activeTab=%d, want %d (Checks)", panel.activeTab, reviewTabChecks)
	}
	press(keyRune('1'))
	if panel.activeTab != reviewTabTasks {
		t.Errorf("'1': activeTab=%d, want %d (Tasks)", panel.activeTab, reviewTabTasks)
	}

	// tab increments with wrap.
	press(keyNamed(tea.KeyTab))
	if panel.activeTab != reviewTabDiff {
		t.Errorf("tab: activeTab=%d, want %d (Diff)", panel.activeTab, reviewTabDiff)
	}
	press(keyNamed(tea.KeyTab))
	press(keyNamed(tea.KeyTab)) // wraps from Checks back to Tasks
	if panel.activeTab != reviewTabTasks {
		t.Errorf("tab wrap: activeTab=%d, want %d (Tasks)", panel.activeTab, reviewTabTasks)
	}

	// shift+tab decrements with wrap (from Tasks wraps to Checks).
	press(keyShiftNamed(tea.KeyTab))
	if panel.activeTab != reviewTabChecks {
		t.Errorf("shift+tab from Tasks: activeTab=%d, want %d (Checks)", panel.activeTab, reviewTabChecks)
	}
}

// TestReviewPanelModel_ChecksFieldsZeroValue confirms that newReviewPanel
// initialises checksCursor and checksScroll to 0.
func TestReviewPanelModel_ChecksFieldsZeroValue(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	panel := newReviewPanel(sess, "", 120, 40)
	if panel.checksCursor != 0 {
		t.Errorf("checksCursor = %d, want 0", panel.checksCursor)
	}
	if panel.checksScroll != 0 {
		t.Errorf("checksScroll = %d, want 0", panel.checksScroll)
	}
}

// TestReviewPanelModel_DefaultTab confirms that newReviewPanel initialises
// activeTab to 0 (Tasks tab). Zero-value initialisation covers this, but the
// test pins the contract so a future refactor can't silently break it.
func TestReviewPanelModel_DefaultTab(t *testing.T) {
	panel := newReviewPanel(nil, "", 80, 40)
	if panel.activeTab != 0 {
		t.Errorf("activeTab = %d, want 0 (Tasks)", panel.activeTab)
	}
}

// TestReviewPanelModel_NoDiffViewport confirms that reviewPanelModel has no
// diffVP or diffCacheByTask fields and no RefreshDiffViewport method — the
// inline viewport was removed in favour of full-screen drill-in via enter.
func TestReviewPanelModel_NoDiffViewport(t *testing.T) {
	var m reviewPanelModel
	v := reflect.TypeOf(m)
	if _, found := v.FieldByName("diffVP"); found {
		t.Error("reviewPanelModel must not have a diffVP field")
	}
	if _, found := v.FieldByName("diffCacheByTask"); found {
		t.Error("reviewPanelModel must not have a diffCacheByTask field")
	}
	panel := newReviewPanel(nil, "", 80, 40)
	type refresher interface {
		RefreshDiffViewport(PanelServices)
	}
	if _, ok := any(panel).(refresher); ok {
		t.Error("reviewPanelModel must not implement RefreshDiffViewport")
	}
}

// TestReviewPanelModel_EscCloses verifies that pressing esc invokes
// svc.ClosePanel() and otherwise leaves session lifecycle untouched.
func TestReviewPanelModel_EscCloses(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	panel := newReviewPanel(sess, "", 120, 40)
	svc, state := newTestSvc()

	_, _ = panel.Update(tea.KeyPressMsg{Code: tea.KeyEscape}, svc)

	if !state.closed {
		t.Fatal("expected ClosePanel to fire on esc")
	}
	if sess.LifecyclePhase() != agent.LifecycleInReview {
		t.Errorf("esc must preserve InReview phase, got %v", sess.LifecyclePhase())
	}
}

// TestReviewPanelModel_DKeyDefers verifies that 'd' resets the session to
// ReadyForReview and closes the panel.
func TestReviewPanelModel_DKeyDefers(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	panel := newReviewPanel(sess, "", 120, 40)
	svc, state := newTestSvc()

	_, _ = panel.Update(tea.KeyPressMsg{Code: 'd', Text: "d"}, svc)

	if !state.closed {
		t.Fatal("expected ClosePanel to fire on d")
	}
	if sess.LifecyclePhase() != agent.LifecycleReadyForReview {
		t.Errorf("d must transition to ReadyForReview, got %v", sess.LifecyclePhase())
	}
}

// TestReviewPanelModel_TKeyClosesEvenOnFailure verifies the 't' key always
// closes the panel and surfaces an error when the session has no agents.
// Mirrors pre-refactor behaviour: 't' is exit-the-panel-intent regardless
// of whether the open succeeds.
func TestReviewPanelModel_TKeyClosesEvenOnFailure(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	panel := newReviewPanel(sess, "", 120, 40)
	svc, state := newTestSvc()
	state.openInLaunchResult = false

	_, _ = panel.Update(tea.KeyPressMsg{Code: 't', Text: "t"}, svc)

	if !state.closed {
		t.Fatal("expected ClosePanel even on open failure")
	}
	if state.errMsg == "" {
		t.Error("expected error surfaced when session has no agents")
	}
}

// TestReviewPanelModel_CKeyMarksComplete verifies 'c' transitions the
// session to LifecycleComplete and triggers the kill-session command.
func TestReviewPanelModel_CKeyMarksComplete(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	panel := newReviewPanel(sess, "", 120, 40)
	svc, state := newTestSvc()
	// Wire Manager to return a non-nil SessionManager so the panel reaches the
	// kill path. The actual manager pointer is not exercised here because
	// KillSessionCmd is stubbed.
	svc.Manager = func(string) SessionManager {
		return &agent.Manager{}
	}

	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'c', Text: "c"}, svc)

	if !state.closed {
		t.Fatal("expected ClosePanel on c")
	}
	if sess.LifecyclePhase() != agent.LifecycleComplete {
		t.Errorf("c must transition to Complete, got %v", sess.LifecyclePhase())
	}
	if !state.killSessionCalled {
		t.Error("expected KillSessionCmd to be invoked")
	}
	if cmd == nil {
		t.Error("expected a cmd batch (close + kill); got nil")
	}
}

// TestReviewPanelModel_TaskCursorMovesWithJK verifies the j/k keys advance
// and retreat the task cursor when the review cache has entries.
func TestReviewPanelModel_TaskCursorMovesWithJK(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	panel := newReviewPanel(sess, "", 120, 40)
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{
			{Index: 1, Text: "task one"},
			{Index: 2, Text: "task two"},
			{Index: 3, Text: "task three"},
		},
	}
	svc, _ := newTestSvc()
	svc.ReviewCache = func(string, string) *reviewDiffEntry { return entry }

	if got := panel.TaskCursor(); got != 0 {
		t.Fatalf("cursor starts at 0, got %d", got)
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}, svc)
	if got := panel.TaskCursor(); got != 1 {
		t.Errorf("after j, cursor=%d, want 1", got)
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}, svc)
	if got := panel.TaskCursor(); got != 2 {
		t.Errorf("after second j, cursor=%d, want 2", got)
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'k', Text: "k"}, svc)
	if got := panel.TaskCursor(); got != 1 {
		t.Errorf("after k, cursor=%d, want 1", got)
	}
}

func TestReviewPanelModel_TaskCursorClampsAtTopAndBottom(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	panel := newReviewPanel(sess, "", 120, 40)
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 1}, {Index: 2}},
	}
	svc, _ := newTestSvc()
	svc.ReviewCache = func(string, string) *reviewDiffEntry { return entry }

	// k at top is a no-op.
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'k', Text: "k"}, svc)
	if got := panel.TaskCursor(); got != 0 {
		t.Errorf("k at top moved cursor to %d, want 0", got)
	}
	// j past last clamps.
	for range 5 {
		_, _ = panel.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}, svc)
	}
	if got := panel.TaskCursor(); got != reviewTaskCount(entry)-1 {
		t.Errorf("over-scroll cursor=%d, want %d", got, reviewTaskCount(entry)-1)
	}
}

func TestReviewPanelModel_FKey_TogglesUserFlag(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	panel := newReviewPanel(sess, "", 120, 40)
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 7, Text: "task one"}},
	}
	svc, _ := newTestSvc()
	svc.ReviewCache = func(string, string) *reviewDiffEntry { return entry }

	_, _ = panel.Update(tea.KeyPressMsg{Code: 'f', Text: "f"}, svc)
	rec := entry.verdicts[7]
	if rec == nil || !rec.userFlagged {
		t.Fatalf("expected verdict[7].userFlagged=true, got %+v", rec)
	}
	// Toggle off.
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'f', Text: "f"}, svc)
	if entry.verdicts[7].userFlagged {
		t.Error("second f should toggle userFlagged back to false")
	}
}

func TestReviewPanelModel_FKey_NoCache_NoOp(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	panel := newReviewPanel(sess, "", 120, 40)
	svc, _ := newTestSvc() // ReviewCache returns nil
	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'f', Text: "f"}, svc)
	if cmd != nil {
		t.Errorf("f without cache produced cmd %T, want nil", cmd())
	}
}

func TestReviewPanelModel_BKey_NoFlags_SetsError(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	panel := newReviewPanel(sess, "", 120, 40)
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 1}},
	}
	svc, state := newTestSvc()
	svc.ReviewCache = func(string, string) *reviewDiffEntry { return entry }
	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'b', Text: "b"}, svc)
	if cmd != nil {
		t.Errorf("b without flagged tasks should return nil cmd, got %T", cmd())
	}
	if state.errMsg == "" {
		t.Error("expected error message when no tasks are flagged")
	}
}

func TestReviewPanelModel_BKey_WithFlag_EmitsReworkMsg(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	panel := newReviewPanel(sess, "/repoB", 120, 40)
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 1, Text: "task one"}},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictPending, userFlagged: true},
		},
	}
	svc, _ := newTestSvc()
	svc.ReviewCache = func(string, string) *reviewDiffEntry { return entry }
	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'b', Text: "b"}, svc)
	if cmd == nil {
		t.Fatal("expected rework cmd")
	}
	msg, ok := cmd().(reviewReworkRequestMsg)
	if !ok {
		t.Fatalf("got %T, want reviewReworkRequestMsg", cmd())
	}
	if msg.sessionID != "s1" {
		t.Errorf("sessionID = %q, want s1", msg.sessionID)
	}
	if msg.prompt == "" {
		t.Error("prompt should be non-empty when tasks are flagged")
	}
	if msg.repoPath != "/repoB" {
		t.Errorf("repoPath = %q, want /repoB (pinned from panel)", msg.repoPath)
	}
}

// TestReviewPanelModel_EnterEmitsDiffMsg verifies that pressing enter on a task
// with a non-empty rawDiff returns a cmd that produces reviewOpenTaskDiffMsg.
func TestReviewPanelModel_EnterEmitsDiffMsg(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	panel := newReviewPanel(sess, "", 120, 40)
	rawDiff := "diff --git a/a.go b/a.go\nindex 1234567..abcdefg 100644\n--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,4 @@\n package main\n \n+// marker\n func A() {}\n"
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 3, Text: "Add tab-switching keys"}},
		groups: []taskReviewGroup{{
			taskIndex: 3,
			rawDiff:   rawDiff,
		}},
		verdicts: map[int]*taskVerdictRecord{3: {state: verdictPending}},
	}
	svc, state := newTestSvc()
	svc.ReviewCache = func(string, string) *reviewDiffEntry { return entry }

	_, cmd := panel.Update(keyNamed(tea.KeyEnter), svc)

	if cmd == nil {
		t.Fatal("enter on task with rawDiff must return a cmd")
	}
	msgs := runCmdAll(t, cmd)
	diffMsg, found := findMsg[reviewOpenTaskDiffMsg](msgs)
	if !found {
		t.Fatalf("expected reviewOpenTaskDiffMsg in cmd output; got: %v", msgs)
	}
	if diffMsg.rawDiff != rawDiff {
		t.Errorf("rawDiff mismatch: got %q, want %q", diffMsg.rawDiff, rawDiff)
	}
	if diffMsg.taskLabel != "[3] Add tab-switching keys" {
		t.Errorf("taskLabel = %q, want %q", diffMsg.taskLabel, "[3] Add tab-switching keys")
	}
	if state.closed {
		t.Error("enter must not close the review panel")
	}
}

// TestReviewPanelModel_EnterNoopOnEmptyDiff verifies that enter on a task with
// no rawDiff produces no command.
func TestReviewPanelModel_EnterNoopOnEmptyDiff(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	panel := newReviewPanel(sess, "", 120, 40)
	entry := &reviewDiffEntry{
		tasks:    []agent.PlanTask{{Index: 1, Text: "task one"}},
		groups:   []taskReviewGroup{{taskIndex: 1, rawDiff: ""}},
		verdicts: map[int]*taskVerdictRecord{1: {state: verdictPending}},
	}
	svc, _ := newTestSvc()
	svc.ReviewCache = func(string, string) *reviewDiffEntry { return entry }

	_, cmd := panel.Update(keyNamed(tea.KeyEnter), svc)
	if cmd != nil {
		t.Errorf("enter on task with no rawDiff must be a no-op, got cmd %T", cmd())
	}
}

// TestReviewPanelModel_SpaceIsNoOp verifies that space still produces no cmd.
func TestReviewPanelModel_SpaceIsNoOp(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	panel := newReviewPanel(sess, "", 120, 40)
	svc, state := newTestSvc()

	_, cmd := panel.Update(tea.KeyPressMsg{Code: ' ', Text: " "}, svc)
	if cmd != nil {
		t.Errorf("space should be a no-op, got cmd %T", cmd())
	}
	if state.closed {
		t.Error("space must not close the panel")
	}
}

// TestReviewPanelModel_ClickWithTabBarOffset verifies that a mouse click on the
// task list pane accounts for the 2-line tab bar inserted between the header
// and the pane body. Without the +2 offset the click lands 2 rows too high,
// causing the cursor to land on the wrong task or be ignored as out-of-bounds.
func TestReviewPanelModel_ClickWithTabBarOffset(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	// Panel width 120; DashboardTopY defaults to 0 in newTestSvc.
	// renderReviewHeader returns 3 lines for this session (title+prompt+divider) → headerH=3.
	// Tab bar adds 2 lines → paneTop should be 5.
	// listHeaderLines=2 → first task row at Y=7.
	panel := newReviewPanel(sess, "", 120, 40)
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{
			{Index: 1, Text: "task one"},
			{Index: 2, Text: "task two"},
		},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictPending},
			2: {state: verdictPending},
		},
	}
	svc, _ := newTestSvc()
	svc.ReviewCache = func(string, string) *reviewDiffEntry { return entry }

	// Move cursor to row 1 first so we can verify the click brings it back to 0.
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}, svc)
	if panel.TaskCursor() != 1 {
		t.Fatalf("precondition: cursor should be 1 after j, got %d", panel.TaskCursor())
	}

	// Click at the first task row: Y=7 (headerH=3 + tabBarH=2 + listHeaderLines=2).
	click := tea.MouseClickMsg{Button: tea.MouseLeft, X: 30, Y: 7}
	_, _ = panel.Update(click, svc)

	if panel.TaskCursor() != 0 {
		t.Errorf("click at Y=7 should move cursor to row 0, got %d (tab bar offset missing?)", panel.TaskCursor())
	}
}

func TestReviewPanelModel_FormerScrollKeys_AreNoOp(t *testing.T) {
	// pgdown / pgup / ctrl+d / ctrl+u are now unbound no-ops (the inline
	// viewport was removed). g / G are still unbound. None may change the
	// cursor or close the panel.
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	panel := newReviewPanel(sess, "", 120, 40)
	svc, state := newTestSvc()

	keys := []tea.KeyPressMsg{
		{Code: tea.KeyPgDown},
		{Code: tea.KeyPgUp},
		{Code: 'd', Mod: tea.ModCtrl},
		{Code: 'u', Mod: tea.ModCtrl},
		{Code: 'g', Text: "g"},
		{Code: 'G', Text: "G"},
	}
	for _, k := range keys {
		_, cmd := panel.Update(k, svc)
		if cmd != nil {
			t.Errorf("formerly-scroll key %v produced cmd %T, want nil", k, cmd())
		}
	}
	if panel.TaskCursor() != 0 {
		t.Errorf("taskCursor = %d after keys, want 0", panel.TaskCursor())
	}
	if state.closed {
		t.Error("these keys must not close the panel")
	}
}

func TestReviewPanelModel_QKey_NoPlan_DoesNotOpenSpec(t *testing.T) {
	// ? toggles the spec overlay but only when the session has a plan.
	sess := agent.NewSessionForTest("s1", "fix-auth")
	if sess.HasPlan() {
		t.Skip("test prereq: session should not have a plan")
	}
	panel := newReviewPanel(sess, "", 120, 40)
	svc, _ := newTestSvc()
	_, _ = panel.Update(tea.KeyPressMsg{Code: '?', Text: "?"}, svc)
	if panel.specOverlay {
		t.Error("? opened spec overlay without a plan present")
	}
}

func TestReviewPanelModel_QKey_WithPlan_OpensSpec(t *testing.T) {
	worktree := t.TempDir()
	planDir := filepath.Join(worktree, ".claude")
	sess := agent.NewSessionForTestWithPath("s1", "fix-auth", worktree)
	// Write a plan file so HasPlan() returns true.
	if err := writePlanForTest(planDir, sess, "# Goal\nSomething\n"); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	if !sess.HasPlan() {
		t.Fatal("test prereq: HasPlan should be true after writing plan.md")
	}
	panel := newReviewPanel(sess, "", 120, 40)
	svc, _ := newTestSvc()
	_, _ = panel.Update(tea.KeyPressMsg{Code: '?', Text: "?"}, svc)
	if !panel.specOverlay {
		t.Error("? should have opened spec overlay")
	}
}

func TestReviewPanelModel_SpecOverlay_Keys(t *testing.T) {
	worktree := t.TempDir()
	planDir := filepath.Join(worktree, ".claude")
	sess := agent.NewSessionForTestWithPath("s1", "fix-auth", worktree)
	if err := writePlanForTest(planDir, sess, "# Goal\n"); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	panel := newReviewPanel(sess, "", 120, 40)
	svc, _ := newTestSvc()
	_, _ = panel.Update(tea.KeyPressMsg{Code: '?', Text: "?"}, svc)
	if !panel.specOverlay {
		t.Fatal("test prereq: spec overlay should be open")
	}

	// pgdown advances scroll.
	_, _ = panel.Update(tea.KeyPressMsg{Code: tea.KeyPgDown}, svc)
	if panel.specOverlayScroll <= 0 {
		t.Errorf("specOverlayScroll = %d after pgdown, want > 0", panel.specOverlayScroll)
	}
	// pgup back to 0 (clamped).
	_, _ = panel.Update(tea.KeyPressMsg{Code: tea.KeyPgUp}, svc)
	if panel.specOverlayScroll != 0 {
		t.Errorf("specOverlayScroll = %d after pgup, want 0 (clamp)", panel.specOverlayScroll)
	}
	// G jumps to bottom.
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'G', Text: "G"}, svc)
	if panel.specOverlayScroll != 9999 {
		t.Errorf("specOverlayScroll = %d after G, want 9999", panel.specOverlayScroll)
	}
	// g jumps to top.
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'g', Text: "g"}, svc)
	if panel.specOverlayScroll != 0 {
		t.Errorf("specOverlayScroll = %d after g, want 0", panel.specOverlayScroll)
	}
	// esc closes the overlay.
	_, _ = panel.Update(tea.KeyPressMsg{Code: tea.KeyEscape}, svc)
	if panel.specOverlay {
		t.Error("esc should close spec overlay")
	}
}

func TestReviewPanelModel_SpecOverlay_OtherKeys_AreSwallowed(t *testing.T) {
	// While the spec overlay is open, keys not in the small switch are
	// silently swallowed — they must NOT bleed through to the main switch.
	worktree := t.TempDir()
	planDir := filepath.Join(worktree, ".claude")
	sess := agent.NewSessionForTestWithPath("s1", "fix-auth", worktree)
	if err := writePlanForTest(planDir, sess, "# Goal\n"); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	panel := newReviewPanel(sess, "", 120, 40)
	svc, state := newTestSvc()
	_, _ = panel.Update(tea.KeyPressMsg{Code: '?', Text: "?"}, svc)

	// 'd' in main mode would defer the panel — must NOT do that while spec
	// overlay is open.
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'd', Text: "d"}, svc)
	if state.closed {
		t.Error("'d' while spec overlay open closed the panel — should be swallowed")
	}
	if sess.LifecyclePhase() != agent.LifecycleInReview {
		t.Errorf("'d' while spec overlay open changed phase to %v", sess.LifecyclePhase())
	}
	if !panel.specOverlay {
		t.Error("'d' while spec overlay open changed overlay state")
	}
}

func TestReviewPanelModel_PKey_WithCachedPR_OpensURL(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	panel := newReviewPanel(sess, "", 120, 40)
	svc, state := newTestSvc()
	var openedURL string
	svc.PRCache = func(string, string) *prCacheEntry {
		return &prCacheEntry{pr: &github.PRState{URL: "https://example.com/pr/1"}}
	}
	svc.OpenURL = func(u string) error {
		openedURL = u
		return nil
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'p', Text: "p"}, svc)
	if openedURL != "https://example.com/pr/1" {
		t.Errorf("OpenURL got %q, want %q", openedURL, "https://example.com/pr/1")
	}
	if sess.LifecyclePhase() != agent.LifecycleShipping {
		t.Errorf("p with existing PR should transition to Shipping, got %v", sess.LifecyclePhase())
	}
	if !state.closed {
		t.Error("p with existing PR should close the panel")
	}
}

func TestReviewPanelModel_PKey_NoPR_NoGHClient_SetsError(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	panel := newReviewPanel(sess, "", 120, 40)
	svc, state := newTestSvc()
	svc.GHClient = func() *github.Client { return nil }
	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'p', Text: "p"}, svc)
	if cmd != nil {
		t.Errorf("p with no PR and no GH client should return nil cmd, got %T", cmd())
	}
	if state.errMsg == "" {
		t.Error("expected error when GitHub auth missing")
	}
}

func TestReviewPanelModel_EKey_NoIDECommand_SetsError(t *testing.T) {
	sess := agent.NewSessionForTestWithPath("s1", "fix-auth", t.TempDir())
	panel := newReviewPanel(sess, "/repo", 120, 40)
	svc, state := newTestSvc()
	svc.Resolved = func(string) config.ResolvedSettings {
		return config.ResolvedSettings{IDECommand: ""}
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'e', Text: "e"}, svc)
	if state.errMsg == "" {
		t.Error("expected error when IDE command not configured")
	}
}

func TestReviewPanelModel_UnknownKey_NoOp(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	panel := newReviewPanel(sess, "", 120, 40)
	svc, state := newTestSvc()

	before := struct {
		cursor int
		spec   bool
		closed bool
		errMsg string
		phase  agent.LifecyclePhase
	}{panel.TaskCursor(), panel.specOverlay, state.closed, state.errMsg, sess.LifecyclePhase()}

	for _, k := range []tea.KeyPressMsg{
		{Code: 'z', Text: "z"},
		{Code: 'x', Text: "x"},
		{Code: 'w', Mod: tea.ModCtrl},
	} {
		_, cmd := panel.Update(k, svc)
		if cmd != nil {
			t.Errorf("unknown key %v produced cmd %T, want nil", k, cmd())
		}
	}

	after := struct {
		cursor int
		spec   bool
		closed bool
		errMsg string
		phase  agent.LifecyclePhase
	}{panel.TaskCursor(), panel.specOverlay, state.closed, state.errMsg, sess.LifecyclePhase()}

	if before != after {
		t.Errorf("unknown keys changed state: before=%+v after=%+v", before, after)
	}
}

// TestReviewPanel_PKey_UsesPinnedRepoPath_NotFirstMatch verifies that pressing
// 'p' (draft PR) passes the panel's pinned repoPath to StartPRDraftCmd, not
// the first-match repoPath from ManagerFor — the multi-repo collision fix.
func TestReviewPanel_PKey_UsesPinnedRepoPath_NotFirstMatch(t *testing.T) {
	sess := agent.NewSessionForTest("session-1", "my-task")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	panel := newReviewPanel(sess, "/repoB", 120, 40)
	svc, _ := newTestSvc()

	var recordedRepoPath string
	svc.StartPRDraftCmd = func(_ *agent.Session, repoPath string, _ bool) tea.Cmd {
		recordedRepoPath = repoPath
		return func() tea.Msg { return nil }
	}
	// non-nil GHClient so the code passes the auth check and reaches StartPRDraftCmd
	svc.GHClient = func() *github.Client { return new(github.Client) }
	svc.PRCache = func(string, string) *prCacheEntry { return nil }

	_, _ = panel.Update(tea.KeyPressMsg{Code: 'p', Text: "p"}, svc)

	if recordedRepoPath != "/repoB" {
		t.Errorf("StartPRDraftCmd received repoPath=%q, want /repoB (pinned)", recordedRepoPath)
	}
}

// TestReviewPanel_CKey_UsesPinnedManager verifies that pressing 'c' (mark
// complete) resolves the manager via the panel's pinned repoPath, not the
// first-match lookup.
func TestReviewPanel_CKey_UsesPinnedManager(t *testing.T) {
	sess := agent.NewSessionForTest("session-1", "my-task")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	panel := newReviewPanel(sess, "/repoB", 120, 40)
	svc, state := newTestSvc()

	var managerCalledWith string
	svc.Manager = func(repoPath string) SessionManager {
		managerCalledWith = repoPath
		return &agent.Manager{} // non-nil so kill path is taken
	}

	_, _ = panel.Update(tea.KeyPressMsg{Code: 'c', Text: "c"}, svc)

	if managerCalledWith != "/repoB" {
		t.Errorf("Manager called with %q, want /repoB (pinned)", managerCalledWith)
	}
	if !state.killSessionCalled {
		t.Error("expected KillSessionCmd to be invoked on non-nil manager")
	}
}

// writePlanForTest creates worktreePath/.claude/plan.md with the given content
// so the session's HasPlan/CachedPlan helpers can find it.
func writePlanForTest(planDir string, sess *agent.Session, content string) error {
	_ = sess // accept sess so call sites can pass it for readability
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(planDir, "plan.md"), []byte(content), 0o644)
}
