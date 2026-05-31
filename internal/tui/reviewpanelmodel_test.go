package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/config"
	"github.com/devenjarvis/refrain/internal/github"
)

// panel-level test pattern (post-§3 fold): instantiate the panel directly with
// a reviewDeps built from a stub App, drive it with messages, and assert on the
// panel's own state plus the tea.Cmds/msgs it returns. Cross-cutting effects
// (close, error, open-in-launch, PR draft) now flow as messages, so the helpers
// below run a returned cmd and classify the resulting message.

// reviewTestApp returns an App seeded with empty caches plus a stub manager
// factory so buildReviewDeps yields live, no-op-friendly handles. Tests mutate
// app.* maps to inject behaviour, then call app.buildReviewDeps().
func reviewTestApp() App {
	app := NewApp()
	app.width = 120
	app.height = 40
	return app
}

// runReviewCmd runs cmd (if non-nil) and returns the single message it yields,
// or nil. tea.Batch results are flattened one level via runCmdAll.
func runReviewCmd(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	return runCmdAll(t, cmd)
}

// cmdClosesPanel reports whether cmd yields a panelCloseMsg.
func cmdClosesPanel(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	_, found := findMsg[panelCloseMsg](runReviewCmd(t, cmd))
	return found
}

// cmdSetsError returns the error text from a setErrorMsg in cmd's output, or "".
func cmdSetsError(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if m, ok := findMsg[setErrorMsg](runReviewCmd(t, cmd)); ok {
		return m.text
	}
	return ""
}

// TestReviewPanel_NoTabKeys verifies that the former tab-switch keys (1, 2, 3,
// tab, shift+tab) are no longer bound. Each key must leave the rendered view
// unchanged and emit no panelCloseMsg.
func TestReviewPanel_NoTabKeys(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	app := reviewTestApp()
	// Seed a diff entry so the Tasks-tab body is non-empty — this ensures that
	// switching to Diff/Checks tab produces a visibly different View() output,
	// making the "view unchanged" assertion fail before the refactor.
	app.reviewDiffCache[cacheKey("", sess.ID)] = &reviewDiffEntry{
		tasks:    []agent.PlanTask{{Index: 1, Text: "task one"}},
		verdicts: map[int]*taskVerdictRecord{1: {state: verdictPending}},
	}

	keys := []tea.KeyPressMsg{
		{Code: '2', Text: "2"},
		{Code: '3', Text: "3"},
		{Code: tea.KeyTab},
		{Code: tea.KeyTab, Mod: tea.ModShift},
	}
	for _, k := range keys {
		// Fresh panel for each key so prior key presses don't interfere.
		panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())
		viewBefore := panel.View()
		_, cmd := panel.Update(k)
		if cmd != nil {
			msgs := runCmdAll(t, cmd)
			if _, found := findMsg[panelCloseMsg](msgs); found {
				t.Errorf("key %v emitted panelCloseMsg, want no-op", k)
			}
		}
		if panel.View() != viewBefore {
			t.Errorf("key %v changed panel view, want no-op (tab keys must be unbound)", k)
		}
	}
}

// TestReviewPanelModel_ChecksTab_JKMovesCursor verifies j/k navigate the task list.
// (Checks tab no longer exists; j/k always move the task cursor.)
func TestReviewPanelModel_ChecksTab_JKMovesCursor(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)

	app := reviewTestApp()
	app.reviewDiffCache[cacheKey("", sess.ID)] = &reviewDiffEntry{
		tasks: []agent.PlanTask{
			{Index: 1, Text: "A"},
			{Index: 2, Text: "B"},
			{Index: 3, Text: "C"},
		},
	}
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())

	// Press j twice.
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if panel.taskCursor != 1 {
		t.Errorf("after j: taskCursor = %d, want 1", panel.taskCursor)
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if panel.taskCursor != 2 {
		t.Errorf("after j j: taskCursor = %d, want 2", panel.taskCursor)
	}
	// Press k.
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if panel.taskCursor != 1 {
		t.Errorf("after k: taskCursor = %d, want 1", panel.taskCursor)
	}
}

// TestReviewPanelModel_ChecksTab_RKeyTriggersRerun verifies r starts a new run
// regardless of which section is active (there are no tabs anymore).
func TestReviewPanelModel_ChecksTab_RKeyTriggersRerun(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)

	app := reviewTestApp()
	app.resolvedCache["/repo"] = config.ResolvedSettings{
		ValidationChecks: []config.ValidationCheck{
			{Name: "Tests", Command: "echo test"},
		},
	}
	app.validationRuns[cacheKey("/repo", sess.ID)] = &validationRunState{
		runID:   1,
		checks:  []config.ValidationCheck{{Name: "Tests", Command: "echo test"}},
		results: []validationCheckResult{{state: checkPassed}},
	}
	panel := newReviewPanel(sess, "/repo", 120, 40, app.buildReviewDeps())

	priorRunID := app.validationRuns[cacheKey("/repo", sess.ID)].runID

	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if cmd == nil {
		t.Fatal("r must return a non-nil cmd when checks are configured")
	}

	if app.validationRuns[cacheKey("/repo", sess.ID)].runID <= priorRunID {
		t.Errorf("runID should have incremented; got %d, prior %d",
			app.validationRuns[cacheKey("/repo", sess.ID)].runID, priorRunID)
	}
}

// TestHandleValidationResult_MatchingRunID verifies that a result message with
// the correct runID updates the matching check result without touching others.
func TestHandleValidationResult_MatchingRunID(t *testing.T) {
	app := NewApp()
	app.validationRuns[cacheKey("", "s1")] = &validationRunState{
		runID: 1,
		checks: []config.ValidationCheck{
			{Name: "A", Command: "echo a"},
			{Name: "B", Command: "echo b"},
		},
		results: []validationCheckResult{
			{state: checkRunning},
			{state: checkRunning},
		},
	}

	msg := validationCheckResultMsg{
		sessionID:  "s1",
		checkIndex: 0,
		runID:      1,
		state:      checkPassed,
		output:     "ok",
		exitCode:   0,
	}
	app.handleValidationCheckResult(msg)

	run := app.validationRuns[cacheKey("", "s1")]
	if run.results[0].state != checkPassed {
		t.Errorf("results[0].state = %v, want checkPassed", run.results[0].state)
	}
	if run.results[1].state != checkRunning {
		t.Errorf("results[1].state = %v, want checkRunning (independent)", run.results[1].state)
	}
}

// TestHandleValidationResult_StaleRunID verifies that a result with a stale
// runID is silently discarded.
func TestHandleValidationResult_StaleRunID(t *testing.T) {
	app := NewApp()
	app.validationRuns[cacheKey("", "s1")] = &validationRunState{
		runID:  2,
		checks: []config.ValidationCheck{{Name: "A", Command: "echo a"}},
		results: []validationCheckResult{
			{state: checkRunning},
		},
	}

	msg := validationCheckResultMsg{
		sessionID:  "s1",
		checkIndex: 0,
		runID:      1, // stale
		state:      checkPassed,
	}
	app.handleValidationCheckResult(msg)

	run := app.validationRuns[cacheKey("", "s1")]
	if run.results[0].state != checkRunning {
		t.Errorf("stale result should be discarded; results[0].state = %v, want checkRunning", run.results[0].state)
	}
}

// TestReviewPanelModel_ChecksFieldsZeroValue confirms that newReviewPanel
// initialises checksCursor and checksScroll to 0.
func TestReviewPanelModel_ChecksFieldsZeroValue(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	app := reviewTestApp()
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())
	if panel.checksCursor != 0 {
		t.Errorf("checksCursor = %d, want 0", panel.checksCursor)
	}
	if panel.checksScroll != 0 {
		t.Errorf("checksScroll = %d, want 0", panel.checksScroll)
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
	app := reviewTestApp()
	panel := newReviewPanel(nil, "", 80, 40, app.buildReviewDeps())
	type refresher interface {
		RefreshDiffViewport()
	}
	if _, ok := any(panel).(refresher); ok {
		t.Error("reviewPanelModel must not implement RefreshDiffViewport")
	}
}

// TestReviewPanelModel_EscCloses verifies that pressing esc emits a
// panelCloseMsg and otherwise leaves session lifecycle untouched.
func TestReviewPanelModel_EscCloses(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	app := reviewTestApp()
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())

	_, cmd := panel.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	if !cmdClosesPanel(t, cmd) {
		t.Fatal("expected panelCloseMsg on esc")
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
	app := reviewTestApp()
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())

	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})

	if !cmdClosesPanel(t, cmd) {
		t.Fatal("expected panelCloseMsg on d")
	}
	if sess.LifecyclePhase() != agent.LifecycleReadyForReview {
		t.Errorf("d must transition to ReadyForReview, got %v", sess.LifecyclePhase())
	}
}

// TestReviewPanelModel_TKeyClosesEvenOnFailure verifies the 't' key always
// closes the panel and requests the agent terminal with a fallback error.
// Mirrors pre-refactor behaviour: 't' is exit-the-panel-intent regardless of
// whether the open succeeds.
func TestReviewPanelModel_TKeyClosesEvenOnFailure(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	app := reviewTestApp()
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())

	_, cmd := panel.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	msgs := runReviewCmd(t, cmd)

	if _, ok := findMsg[panelCloseMsg](msgs); !ok {
		t.Fatal("expected panelCloseMsg even on open failure")
	}
	req, ok := findMsg[openAgentTerminalRequestMsg](msgs)
	if !ok {
		t.Fatal("expected openAgentTerminalRequestMsg")
	}
	if req.fallbackError == "" {
		t.Error("expected fallbackError set so App surfaces an error when no agents")
	}
}

// TestReviewPanelModel_CKeyMarksComplete verifies 'c' transitions the
// session to LifecycleComplete and triggers the kill-session command.
func TestReviewPanelModel_CKeyMarksComplete(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	repo := "/repo"
	app := reviewTestApp()
	// Non-nil manager so the panel reaches the kill path; the fake records the
	// kill call.
	fake := newFakeManager(repo, sess)
	app.managers[repo] = fake
	panel := newReviewPanel(sess, repo, 120, 40, app.buildReviewDeps())

	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})

	if !cmdClosesPanel(t, cmd) {
		t.Fatal("expected panelCloseMsg on c")
	}
	if sess.LifecyclePhase() != agent.LifecycleComplete {
		t.Errorf("c must transition to Complete, got %v", sess.LifecyclePhase())
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
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{
			{Index: 1, Text: "task one"},
			{Index: 2, Text: "task two"},
			{Index: 3, Text: "task three"},
		},
	}
	app := reviewTestApp()
	app.reviewDiffCache[cacheKey("", sess.ID)] = entry
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())

	if got := panel.TaskCursor(); got != 0 {
		t.Fatalf("cursor starts at 0, got %d", got)
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if got := panel.TaskCursor(); got != 1 {
		t.Errorf("after j, cursor=%d, want 1", got)
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if got := panel.TaskCursor(); got != 2 {
		t.Errorf("after second j, cursor=%d, want 2", got)
	}
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if got := panel.TaskCursor(); got != 1 {
		t.Errorf("after k, cursor=%d, want 1", got)
	}
}

func TestReviewPanelModel_TaskCursorClampsAtTopAndBottom(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 1}, {Index: 2}},
	}
	app := reviewTestApp()
	app.reviewDiffCache[cacheKey("", sess.ID)] = entry
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())

	// k at top is a no-op.
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if got := panel.TaskCursor(); got != 0 {
		t.Errorf("k at top moved cursor to %d, want 0", got)
	}
	// j past last clamps.
	for range 5 {
		_, _ = panel.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	if got := panel.TaskCursor(); got != reviewTaskCount(entry)-1 {
		t.Errorf("over-scroll cursor=%d, want %d", got, reviewTaskCount(entry)-1)
	}
}

func TestReviewPanelModel_FKey_TogglesUserFlag(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 7, Text: "task one"}},
	}
	app := reviewTestApp()
	app.reviewDiffCache[cacheKey("", sess.ID)] = entry
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())

	_, _ = panel.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	rec := entry.verdicts[7]
	if rec == nil || !rec.userFlagged {
		t.Fatalf("expected verdict[7].userFlagged=true, got %+v", rec)
	}
	// Toggle off.
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	if entry.verdicts[7].userFlagged {
		t.Error("second f should toggle userFlagged back to false")
	}
}

func TestReviewPanelModel_FKey_NoCache_NoOp(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	app := reviewTestApp() // ReviewCache returns nil (empty map)
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())
	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	if cmd != nil {
		t.Errorf("f without cache produced cmd %T, want nil", cmd())
	}
}

func TestReviewPanelModel_BKey_NoFlags_SetsError(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 1}},
	}
	app := reviewTestApp()
	app.reviewDiffCache[cacheKey("", sess.ID)] = entry
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())
	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	if _, ok := findMsg[reviewReworkRequestMsg](runReviewCmd(t, cmd)); ok {
		t.Error("b without flagged tasks should not emit a rework request")
	}
	if cmdSetsError(t, cmd) == "" {
		t.Error("expected error message when no tasks are flagged")
	}
}

func TestReviewPanelModel_BKey_WithFlag_EmitsReworkMsg(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 1, Text: "task one"}},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictPending, userFlagged: true},
		},
	}
	app := reviewTestApp()
	app.reviewDiffCache[cacheKey("/repoB", sess.ID)] = entry
	panel := newReviewPanel(sess, "/repoB", 120, 40, app.buildReviewDeps())
	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
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
	rawDiff := "diff --git a/a.go b/a.go\nindex 1234567..abcdefg 100644\n--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,4 @@\n package main\n \n+// marker\n func A() {}\n"
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 3, Text: "Add tab-switching keys"}},
		groups: []taskReviewGroup{{
			taskIndex: 3,
			rawDiff:   rawDiff,
		}},
		verdicts: map[int]*taskVerdictRecord{3: {state: verdictPending}},
	}
	app := reviewTestApp()
	app.reviewDiffCache[cacheKey("", sess.ID)] = entry
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())

	_, cmd := panel.Update(keyNamed(tea.KeyEnter))

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
	if _, closed := findMsg[panelCloseMsg](msgs); closed {
		t.Error("enter must not close the review panel")
	}
}

// TestReviewPanelModel_EnterNoopOnEmptyDiff verifies that enter on a task with
// no rawDiff produces no command.
func TestReviewPanelModel_EnterNoopOnEmptyDiff(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	entry := &reviewDiffEntry{
		tasks:    []agent.PlanTask{{Index: 1, Text: "task one"}},
		groups:   []taskReviewGroup{{taskIndex: 1, rawDiff: ""}},
		verdicts: map[int]*taskVerdictRecord{1: {state: verdictPending}},
	}
	app := reviewTestApp()
	app.reviewDiffCache[cacheKey("", sess.ID)] = entry
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())

	_, cmd := panel.Update(keyNamed(tea.KeyEnter))
	if cmd != nil {
		t.Errorf("enter on task with no rawDiff must be a no-op, got cmd %T", cmd())
	}
}

// TestReviewPanelModel_SpaceIsNoOp verifies that space still produces no cmd.
func TestReviewPanelModel_SpaceIsNoOp(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	app := reviewTestApp()
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())

	_, cmd := panel.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	if cmd != nil {
		t.Errorf("space should be a no-op, got cmd %T", cmd())
	}
}

// TestReviewPanelModel_ClickSetsTaskCursor verifies that a mouse click on the
// task list pane moves the task cursor to the correct row.
// Panel width 120; dashboardTopY defaults to 0.
// renderReviewHeader returns 3 lines for this session (title+prompt+divider) → headerH=3.
// No tab bar → paneTop=3; listHeaderLines=2 → first task row at Y=5.
func TestReviewPanelModel_ClickSetsTaskCursor(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
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
	app := reviewTestApp()
	app.reviewDiffCache[cacheKey("", sess.ID)] = entry
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())

	// Move cursor to row 1 first so we can verify the click brings it back to 0.
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if panel.TaskCursor() != 1 {
		t.Fatalf("precondition: cursor should be 1 after j, got %d", panel.TaskCursor())
	}

	// Click at the first task row: Y=5 (headerH=3 + no tabBar + listHeaderLines=2).
	click := tea.MouseClickMsg{Button: tea.MouseLeft, X: 30, Y: 5}
	_, _ = panel.Update(click)

	if panel.TaskCursor() != 0 {
		t.Errorf("click at Y=5 should move cursor to row 0, got %d", panel.TaskCursor())
	}
}

func TestReviewPanelModel_FormerScrollKeys_AreNoOp(t *testing.T) {
	// pgdown / pgup / ctrl+d / ctrl+u are now unbound no-ops (the inline
	// viewport was removed). g / G are still unbound. None may change the
	// cursor or close the panel.
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	app := reviewTestApp()
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())

	keys := []tea.KeyPressMsg{
		{Code: tea.KeyPgDown},
		{Code: tea.KeyPgUp},
		{Code: 'd', Mod: tea.ModCtrl},
		{Code: 'u', Mod: tea.ModCtrl},
		{Code: 'g', Text: "g"},
		{Code: 'G', Text: "G"},
	}
	for _, k := range keys {
		_, cmd := panel.Update(k)
		if cmd != nil {
			t.Errorf("formerly-scroll key %v produced cmd %T, want nil", k, cmd())
		}
	}
	if panel.TaskCursor() != 0 {
		t.Errorf("taskCursor = %d after keys, want 0", panel.TaskCursor())
	}
}

func TestReviewPanelModel_QKey_NoPlan_DoesNotOpenSpec(t *testing.T) {
	// ? toggles the spec overlay but only when the session has a plan.
	sess := agent.NewSessionForTest("s1", "fix-auth")
	if sess.HasPlan() {
		t.Skip("test prereq: session should not have a plan")
	}
	app := reviewTestApp()
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())
	_, _ = panel.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
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
	app := reviewTestApp()
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())
	_, _ = panel.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
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
	app := reviewTestApp()
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())
	_, _ = panel.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	if !panel.specOverlay {
		t.Fatal("test prereq: spec overlay should be open")
	}

	// pgdown advances scroll.
	_, _ = panel.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if panel.specOverlayScroll <= 0 {
		t.Errorf("specOverlayScroll = %d after pgdown, want > 0", panel.specOverlayScroll)
	}
	// pgup back to 0 (clamped).
	_, _ = panel.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if panel.specOverlayScroll != 0 {
		t.Errorf("specOverlayScroll = %d after pgup, want 0 (clamp)", panel.specOverlayScroll)
	}
	// G jumps to bottom.
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	if panel.specOverlayScroll != 9999 {
		t.Errorf("specOverlayScroll = %d after G, want 9999", panel.specOverlayScroll)
	}
	// g jumps to top.
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	if panel.specOverlayScroll != 0 {
		t.Errorf("specOverlayScroll = %d after g, want 0", panel.specOverlayScroll)
	}
	// esc closes the overlay.
	_, _ = panel.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
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
	app := reviewTestApp()
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())
	_, _ = panel.Update(tea.KeyPressMsg{Code: '?', Text: "?"})

	// 'd' in main mode would defer the panel — must NOT do that while spec
	// overlay is open.
	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if cmdClosesPanel(t, cmd) {
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
	app := reviewTestApp()
	app.prCache[cacheKey("", sess.ID)] = &prCacheEntry{pr: &github.PRState{URL: "https://example.com/pr/1"}}

	var openedURL string
	openURL = func(u string) error { openedURL = u; return nil }
	t.Cleanup(restoreOpenURL())

	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())
	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	msgs := runReviewCmd(t, cmd)

	if openedURL != "https://example.com/pr/1" {
		t.Errorf("openURL got %q, want %q", openedURL, "https://example.com/pr/1")
	}
	if sess.LifecyclePhase() != agent.LifecycleShipping {
		t.Errorf("p with existing PR should transition to Shipping, got %v", sess.LifecyclePhase())
	}
	if _, closed := findMsg[panelCloseMsg](msgs); !closed {
		t.Error("p with existing PR should close the panel")
	}
}

func TestReviewPanelModel_PKey_NoPR_NoGHClient_SetsError(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	app := reviewTestApp() // ghClient is nil
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())
	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if _, ok := findMsg[startPRDraftRequestMsg](runReviewCmd(t, cmd)); ok {
		t.Error("p with no GH client should not request a PR draft")
	}
	if cmdSetsError(t, cmd) == "" {
		t.Error("expected error when GitHub auth missing")
	}
}

func TestReviewPanelModel_EKey_NoIDECommand_SetsError(t *testing.T) {
	sess := agent.NewSessionForTestWithPath("s1", "fix-auth", t.TempDir())
	app := reviewTestApp()
	app.resolvedCache["/repo"] = config.ResolvedSettings{IDECommand: ""}
	panel := newReviewPanel(sess, "/repo", 120, 40, app.buildReviewDeps())
	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if cmdSetsError(t, cmd) == "" {
		t.Error("expected error when IDE command not configured")
	}
}

func TestReviewPanelModel_UnknownKey_NoOp(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	app := reviewTestApp()
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())

	before := struct {
		cursor int
		spec   bool
		phase  agent.LifecyclePhase
	}{panel.TaskCursor(), panel.specOverlay, sess.LifecyclePhase()}

	for _, k := range []tea.KeyPressMsg{
		{Code: 'z', Text: "z"},
		{Code: 'x', Text: "x"},
		{Code: 'w', Mod: tea.ModCtrl},
	} {
		_, cmd := panel.Update(k)
		if cmd != nil {
			t.Errorf("unknown key %v produced cmd %T, want nil", k, cmd())
		}
	}

	after := struct {
		cursor int
		spec   bool
		phase  agent.LifecyclePhase
	}{panel.TaskCursor(), panel.specOverlay, sess.LifecyclePhase()}

	if before != after {
		t.Errorf("unknown keys changed state: before=%+v after=%+v", before, after)
	}
}

// TestReviewPanel_PKey_UsesPinnedRepoPath_NotFirstMatch verifies that pressing
// 'p' (draft PR) emits a startPRDraftRequestMsg carrying the panel's pinned
// repoPath, not a first-match repoPath — the multi-repo collision fix.
func TestReviewPanel_PKey_UsesPinnedRepoPath_NotFirstMatch(t *testing.T) {
	sess := agent.NewSessionForTest("session-1", "my-task")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	app := reviewTestApp()
	app.ghClient = new(github.Client) // non-nil so the panel reaches the draft path
	panel := newReviewPanel(sess, "/repoB", 120, 40, app.buildReviewDeps())

	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	req, ok := findMsg[startPRDraftRequestMsg](runReviewCmd(t, cmd))
	if !ok {
		t.Fatal("expected startPRDraftRequestMsg")
	}
	if req.repoPath != "/repoB" {
		t.Errorf("startPRDraftRequestMsg repoPath=%q, want /repoB (pinned)", req.repoPath)
	}
}

// TestReviewPanel_CKey_UsesPinnedManager verifies that pressing 'c' (mark
// complete) resolves the manager via the panel's pinned repoPath, not the
// first-match lookup. The kill cmd only fires when the pinned repo has a
// manager, so a kill-result msg confirms the pinned lookup succeeded.
func TestReviewPanel_CKey_UsesPinnedManager(t *testing.T) {
	sess := agent.NewSessionForTest("session-1", "my-task")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	repo := "/repoB"
	app := reviewTestApp()
	fake := newFakeManager(repo, sess)
	app.managers[repo] = fake
	panel := newReviewPanel(sess, repo, 120, 40, app.buildReviewDeps())

	_, cmd := panel.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	msgs := runReviewCmd(t, cmd)
	if _, ok := findMsg[killResultMsg](msgs); !ok {
		t.Error("expected killResultMsg — manager must resolve via pinned repoPath")
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

// TestReviewPanel_DiffCacheReusesParse verifies that focusedDiffModel returns
// the same *diffmodel.Model pointer for the same cursor position (cache hit)
// and a new pointer after moving to a different task (cache miss).
func TestReviewPanel_DiffCacheReusesParse(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	rawDiff1 := "diff --git a/a.go b/a.go\nindex 000..111 100644\n--- a/a.go\n+++ b/a.go\n@@ -1 +1,2 @@\n package main\n+// marker1\n"
	rawDiff2 := "diff --git a/b.go b/b.go\nindex 000..222 100644\n--- a/b.go\n+++ b/b.go\n@@ -1 +1,2 @@\n package main\n+// marker2\n"

	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{
			{Index: 1, Text: "task one"},
			{Index: 2, Text: "task two"},
		},
		groups: []taskReviewGroup{
			{taskIndex: 1, rawDiff: rawDiff1},
			{taskIndex: 2, rawDiff: rawDiff2},
		},
	}
	app := reviewTestApp()
	app.reviewDiffCache[cacheKey("", sess.ID)] = entry
	panel := newReviewPanel(sess, "", 120, 40, app.buildReviewDeps())

	// First call for cursor=0 (task 1).
	m1a, _, ok1 := panel.focusedDiffModel(entry)
	if !ok1 {
		t.Fatal("focusedDiffModel returned ok=false for task with rawDiff")
	}
	if m1a == nil {
		t.Fatal("focusedDiffModel returned nil model for task 1")
	}

	// Second call at same cursor must return the same pointer (cache hit).
	m1b, _, _ := panel.focusedDiffModel(entry)
	if m1a != m1b {
		t.Error("second call for same cursor must return cached *diffmodel.Model (same pointer)")
	}

	// Move cursor to task 2 → cache miss → new pointer.
	panel.taskCursor = 1
	m2, _, ok2 := panel.focusedDiffModel(entry)
	if !ok2 {
		t.Fatal("focusedDiffModel returned ok=false for task 2 with rawDiff")
	}
	if m2 == m1a {
		t.Error("different task must return a different *diffmodel.Model pointer")
	}
}

// restoreOpenURL captures the current openURL and returns a cleanup that
// restores it, so tests that swap openURL don't leak across cases.
func restoreOpenURL() func() {
	orig := openURL
	return func() { openURL = orig }
}

// TestReviewPanel_View_FooterOnLastRow verifies that renderReviewPanel always
// returns exactly height rows with the ESC hint on the last row.
func TestReviewPanel_View_FooterOnLastRow(t *testing.T) {
	const w, h = 120, 40
	assertFooter := func(t *testing.T, view string) {
		t.Helper()
		n := strings.Count(view, "\n") + 1
		if n != h {
			t.Errorf("View() returned %d lines, want %d", n, h)
		}
		lines := strings.Split(view, "\n")
		last := ansi.Strip(lines[len(lines)-1])
		if !strings.Contains(last, "ESC") {
			t.Errorf("last line %q does not contain 'ESC' hint", last)
		}
	}

	t.Run("loading (entry nil)", func(t *testing.T) {
		sess := agent.NewSessionForTest("s1", "fix-auth")
		app := reviewTestApp()
		panel := newReviewPanel(sess, "", w, h, app.buildReviewDeps())
		// reviewDiffCache is empty → ReviewCache returns nil
		assertFooter(t, panel.View())
	})

	t.Run("entry with empty tasks and groups", func(t *testing.T) {
		sess := agent.NewSessionForTest("s2", "fix-auth")
		app := reviewTestApp()
		// entry present but no tasks/groups → overview path
		app.reviewDiffCache[cacheKey("", sess.ID)] = &reviewDiffEntry{}
		panel := newReviewPanel(sess, "", w, h, app.buildReviewDeps())
		assertFooter(t, panel.View())
	})

	t.Run("checks tab with nil checkState", func(t *testing.T) {
		sess := agent.NewSessionForTest("s3", "fix-auth")
		app := reviewTestApp()
		panel := newReviewPanel(sess, "", w, h, app.buildReviewDeps())
		panel.activeTab = reviewTabChecks
		// validationRuns is nil → checkState is nil → placeholder tab
		assertFooter(t, panel.View())
	})

	t.Run("tasks tab with entry", func(t *testing.T) {
		sess := agent.NewSessionForTest("s4", "fix-auth")
		app := reviewTestApp()
		app.reviewDiffCache[cacheKey("", sess.ID)] = &reviewDiffEntry{
			tasks: []agent.PlanTask{
				{Index: 1, Text: "task one"},
				{Index: 2, Text: "task two"},
			},
		}
		panel := newReviewPanel(sess, "", w, h, app.buildReviewDeps())
		assertFooter(t, panel.View())
	})

	t.Run("draft PR in flight", func(t *testing.T) {
		sess := agent.NewSessionForTest("s5", "fix-auth")
		app := reviewTestApp()
		panel := newReviewPanel(sess, "", w, h, app.buildReviewDeps())
		panel.drafting = true
		assertFooter(t, panel.View())
	})
}
