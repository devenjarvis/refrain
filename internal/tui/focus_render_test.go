package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/baton/internal/agent"
	"github.com/muesli/termenv"
)

// TestRenderFocusActiveCursor verifies that the selected session card in focus
// mode is visually distinct: the leading stripe glyph is rendered in
// ColorSecondary (cyan) for the selected session and a different color for
// unselected sessions. The "> " chevron is also present on the selected row
// (an ANSI-stripped fallback signal for screenshots / terminal recordings),
// but the cyan stripe is the assertion this test owns.
func TestRenderFocusActiveCursor(t *testing.T) {
	// Force TrueColor so the rendered ANSI escapes carry the foreground color
	// we want to assert against. Without this, lipgloss strips colors when
	// stdout is not a TTY and selection becomes indistinguishable in the
	// rendered string.
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	sessA := &agent.Session{Name: "active-a"}
	sessA.SetLifecyclePhase(agent.LifecycleInProgress)
	sessB := &agent.Session{Name: "active-b"}
	sessB.SetLifecyclePhase(agent.LifecycleInProgress)

	d := newDashboardModel()
	d.width = 120
	d.height = 39
	d.focusCursorSection = focusSectionBuilding
	d.focusBuildingIdx = 1
	d.items = []listItem{
		{kind: listItemRepo, repoPath: "/r", repoName: "repo"},
		{kind: listItemSession, repoPath: "/r", session: sessA},
		{kind: listItemSession, repoPath: "/r", session: sessB},
	}

	out := d.renderFullscreenFocus(120, 39)

	selectedStripe := lipgloss.NewStyle().Foreground(ColorSecondary).Render("▎")
	if !strings.Contains(selectedStripe, "▎") {
		t.Fatalf("expected styled stripe to contain glyph; got %q", selectedStripe)
	}

	var selectedLine, unselectedLine string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "active-b") && selectedLine == "":
			selectedLine = line
		case strings.Contains(line, "active-a") && unselectedLine == "":
			unselectedLine = line
		}
	}
	if selectedLine == "" || unselectedLine == "" {
		t.Fatalf("could not find both session header lines in output:\n%s", out)
	}

	if !strings.Contains(selectedLine, selectedStripe) {
		t.Fatalf("selected card missing cyan stripe %q\nselected line: %q\nfull:\n%s",
			selectedStripe, selectedLine, out)
	}
	// Unselected cards must still carry the stripe glyph (just in a different
	// color), so confirm the glyph is present before asserting the cyan color
	// is *not*.
	if !strings.Contains(unselectedLine, "▎") {
		t.Fatalf("unselected card missing stripe glyph entirely\nunselected line: %q\nfull:\n%s",
			unselectedLine, out)
	}
	if strings.Contains(unselectedLine, selectedStripe) {
		t.Fatalf("unselected card unexpectedly carries selection stripe color\nunselected line: %q\nfull:\n%s",
			unselectedLine, out)
	}
}

// TestSessionFocusStatus_IdleReviewableShowsCue verifies that a session whose
// non-shell agents are all Idle (and DoneAt is zero — i.e. Claude finished a
// turn but did not /exit) renders the "press m to review" cue. This makes the
// review affordance discoverable on every natural review point, not only after
// the user manually exits Claude.
func TestSessionFocusStatus_IdleReviewableShowsCue(t *testing.T) {
	sess := agent.NewSessionForTest("s", "active-a")
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	ag := sess.AddTestAgent("a-1", false, agent.StatusIdle)

	d := newDashboardModel()
	d.items = []listItem{
		{kind: listItemSession, repoPath: "/r", session: sess},
		{kind: listItemAgent, repoPath: "/r", session: sess, agent: ag},
	}

	badge := d.sessionFocusStatus(sess)
	if !strings.Contains(badge, "press m to review") {
		t.Errorf("expected reviewable cue, got %q", badge)
	}
}

// TestSessionFocusStatus_ActiveDoesNotShowCue verifies that the "press m to
// review" cue does not fire when at least one non-shell agent is Active —
// i.e. Claude is mid-turn and there is nothing to review yet.
func TestSessionFocusStatus_ActiveDoesNotShowCue(t *testing.T) {
	sess := agent.NewSessionForTest("s", "active-a")
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	ag := sess.AddTestAgent("a-1", false, agent.StatusActive)

	d := newDashboardModel()
	d.items = []listItem{
		{kind: listItemSession, repoPath: "/r", session: sess},
		{kind: listItemAgent, repoPath: "/r", session: sess, agent: ag},
	}

	badge := d.sessionFocusStatus(sess)
	if strings.Contains(badge, "press m to review") {
		t.Errorf("did not expect reviewable cue with active agent, got %q", badge)
	}
}

// TestSessionFocusStripeColor_IdleReviewableMatchesBadge verifies that the
// session stripe color and the badge color agree for the new idle-reviewable
// state — the function comment on sessionFocusStripeColor makes this an
// explicit invariant.
func TestSessionFocusStripeColor_IdleReviewableMatchesBadge(t *testing.T) {
	sess := agent.NewSessionForTest("s", "active-a")
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	ag := sess.AddTestAgent("a-1", false, agent.StatusIdle)

	d := newDashboardModel()
	d.items = []listItem{
		{kind: listItemSession, repoPath: "/r", session: sess},
		{kind: listItemAgent, repoPath: "/r", session: sess, agent: ag},
	}

	if got := d.sessionFocusStripeColor(sess); got != ColorSuccess {
		t.Errorf("expected stripe ColorSuccess for idle-reviewable session, got %v", got)
	}
}

// TestSessionFocusStatus_FinishedTakesPrecedence verifies that once DoneAt is
// set (Claude /exit'd), the existing "✓ finished — awaiting prompt" badge wins
// over the new idle cue, preserving the original badge ordering.
func TestSessionFocusStatus_FinishedTakesPrecedence(t *testing.T) {
	sess := agent.NewSessionForTest("s", "active-a")
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	ag := sess.AddTestAgent("a-1", false, agent.StatusDone)
	sess.MarkDone()

	d := newDashboardModel()
	d.items = []listItem{
		{kind: listItemSession, repoPath: "/r", session: sess},
		{kind: listItemAgent, repoPath: "/r", session: sess, agent: ag},
	}

	badge := d.sessionFocusStatus(sess)
	if !strings.Contains(badge, "finished") {
		t.Errorf("expected finished badge to win, got %q", badge)
	}
	if strings.Contains(badge, "press m to review") {
		t.Errorf("expected idle cue not to fire when DoneAt set, got %q", badge)
	}
}

// TestRenderFocusSessionCard_RepoPrefix verifies that a non-empty repoName is
// rendered as a "<repoName> › " prefix on the session card's name line. This
// locks in the cross-repo disambiguation contract — without it, two sessions
// with the same display name in different repos look identical on the
// dashboard.
func TestRenderFocusSessionCard_RepoPrefix(t *testing.T) {
	sessA := agent.NewSessionForTest("s-a", "add-dark-mode")
	sessA.SetLifecyclePhase(agent.LifecycleInProgress)
	sessB := agent.NewSessionForTest("s-b", "add-dark-mode")
	sessB.SetLifecyclePhase(agent.LifecycleInProgress)

	d := newDashboardModel()
	d.width = 120
	d.items = []listItem{
		{kind: listItemSession, repoPath: "/a", repoName: "repoA", session: sessA},
		{kind: listItemSession, repoPath: "/b", repoName: "repoB", session: sessB},
	}

	card := d.renderFocusSessionCard(sessA, "repoA", false, 120)
	if len(card) == 0 {
		t.Fatalf("expected at least one rendered line")
	}
	line1 := ansi.Strip(card[0])
	if !strings.Contains(line1, "repoA › ") {
		t.Errorf("expected repo prefix \"repoA › \" on line 1, got %q", line1)
	}
	if !strings.Contains(line1, "add-dark-mode") {
		t.Errorf("expected session name on line 1, got %q", line1)
	}
	if idx := strings.Index(line1, "repoA › "); idx >= 0 {
		nameIdx := strings.Index(line1, "add-dark-mode")
		if nameIdx < idx {
			t.Errorf("expected repo prefix to precede session name, got %q", line1)
		}
	}

	// Empty repoName must not render the separator (defensive — the prefix is
	// optional even though every real call passes a non-empty value).
	bare := d.renderFocusSessionCard(sessA, "", false, 120)
	if strings.Contains(ansi.Strip(bare[0]), "›") {
		t.Errorf("empty repoName should not emit › separator, got %q", ansi.Strip(bare[0]))
	}
}

// TestBuildingProgressBadge verifies renderCardProgressBar for various todo-count states.
func TestBuildingProgressBadge(t *testing.T) {
	tests := []struct {
		name      string
		done      int
		total     int
		wantEmpty bool
		wantStr   string
	}{
		{
			name:      "no todos returns empty",
			done:      0,
			total:     0,
			wantEmpty: true,
		},
		{
			name:    "2/5 shows correct counts",
			done:    2,
			total:   5,
			wantStr: "2/5 tasks",
		},
		{
			name:    "1/3 shows correct counts",
			done:    1,
			total:   3,
			wantStr: "1/3 tasks",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := renderCardProgressBar(tc.done, tc.total, 20, ColorPrimary)
			if tc.wantEmpty {
				if got != "" {
					t.Errorf("expected empty badge, got %q", got)
				}
				return
			}
			plain := ansi.Strip(got)
			if !strings.Contains(plain, tc.wantStr) {
				t.Errorf("expected badge to contain %q, got %q", tc.wantStr, plain)
			}
		})
	}
}

// TestSessionFocusStatus_BuildingWithTodosShowsProgressBadge verifies that
// sessionFocusStatus shows a "▸ done/total" progress badge for a Building
// session that has received ≥1 TodoWrite, instead of the plain "N active, M idle".
func TestSessionFocusStatus_BuildingWithTodosShowsProgressBadge(t *testing.T) {
	sess := agent.NewSessionForTest("s", "my-session")
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	ag := sess.AddTestAgent("a-1", false, agent.StatusActive)
	ag.SetTodos([]agent.TodoItem{
		{Content: "step one", Status: "completed", ActiveForm: ""},
		{Content: "step two", Status: "in_progress", ActiveForm: "Doing step two"},
		{Content: "step three", Status: "pending", ActiveForm: ""},
	})

	d := newDashboardModel()
	d.items = []listItem{
		{kind: listItemSession, repoPath: "/r", session: sess},
		{kind: listItemAgent, repoPath: "/r", session: sess, agent: ag},
	}

	badge := ansi.Strip(d.sessionFocusStatus(sess))
	if !strings.Contains(badge, "1/3") {
		t.Errorf("expected progress badge with 1/3, got %q", badge)
	}
	if strings.Contains(badge, "active, ") {
		t.Errorf("expected progress badge to replace 'N active, M idle', got %q", badge)
	}
}

// TestSessionFocusStatus_BuildingWithTodosErrorPreempts verifies that error
// status still preempts the todo progress badge.
func TestSessionFocusStatus_BuildingWithTodosErrorPreempts(t *testing.T) {
	sess := agent.NewSessionForTest("s", "my-session")
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	ag := sess.AddTestAgent("a-1", false, agent.StatusError)
	ag.SetTodos([]agent.TodoItem{
		{Content: "step one", Status: "in_progress", ActiveForm: "Doing step one"},
	})

	d := newDashboardModel()
	d.items = []listItem{
		{kind: listItemSession, repoPath: "/r", session: sess},
		{kind: listItemAgent, repoPath: "/r", session: sess, agent: ag},
	}

	badge := ansi.Strip(d.sessionFocusStatus(sess))
	if !strings.Contains(badge, "error") {
		t.Errorf("expected error badge to preempt todos, got %q", badge)
	}
}

// TestFocusTaskDescription_WithTodos verifies that focusTaskDescription returns
// the in_progress todo's activeForm on line1 and the first pending todo on line2.
func TestFocusTaskDescription_WithTodos(t *testing.T) {
	sess := agent.NewSessionForTest("s", "my-session")
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	ag := sess.AddTestAgent("a-1", false, agent.StatusActive)
	ag.SetTodos([]agent.TodoItem{
		{Content: "write unit tests", Status: "in_progress", ActiveForm: "Writing unit tests"},
		{Content: "open PR", Status: "pending", ActiveForm: ""},
	})
	// PrimaryAgent() reads from session's agents map.
	_ = ag

	line1, line2, pending := focusTaskDescription(sess, 80)
	if !strings.Contains(line1, "Writing unit tests") {
		t.Errorf("line1 should contain active todo activeForm, got %q", line1)
	}
	if !strings.Contains(line2, "open PR") {
		t.Errorf("line2 should contain next pending todo, got %q", line2)
	}
	if pending {
		t.Error("expected pending=false for todo-driven description")
	}
}

// TestFocusTaskDescription_WithoutTodos verifies that focusTaskDescription
// falls back to the task summary / original prompt when no todos are present.
func TestFocusTaskDescription_WithoutTodos(t *testing.T) {
	sess := agent.NewSessionForTest("s", "my-session")
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	sess.SetTaskSummary("implement oauth flow")

	line1, _, _ := focusTaskDescription(sess, 80)
	if !strings.Contains(line1, "implement oauth flow") {
		t.Errorf("expected task summary fallback, got %q", line1)
	}
}

// TestFocusTaskDescription_ReviewableFallsThrough verifies that stale todos
// do not surface on lines 2-3 when the session IsReviewable (all agents Idle).
// The badge on line 1 already shows "✓ idle — press m to review" in that state,
// so todo lines would be contradictory.
func TestFocusTaskDescription_ReviewableFallsThrough(t *testing.T) {
	sess := agent.NewSessionForTest("s", "my-session")
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	sess.SetTaskSummary("implement oauth flow")
	// Agent is Idle → IsReviewable() == true.
	ag := sess.AddTestAgent("a-1", false, agent.StatusIdle)
	ag.SetTodos([]agent.TodoItem{
		{Content: "stale task", Status: "in_progress", ActiveForm: "Stale active work"},
	})

	line1, line2, _ := focusTaskDescription(sess, 80)
	// Must NOT show the in_progress todo text.
	if strings.Contains(line1, "Stale active work") || strings.Contains(line2, "Stale active work") {
		t.Errorf("expected todo description suppressed for reviewable session, got line1=%q line2=%q", line1, line2)
	}
	// Should fall back to the task summary.
	if !strings.Contains(line1, "implement oauth flow") {
		t.Errorf("expected task summary fallback when reviewable, got %q", line1)
	}
}

// TestRenderQueueRow_RepoPrefix verifies the same cross-repo disambiguation
// for the REVIEWING / SHIPPING sections. renderQueueRow has independent
// rendering from renderFocusSessionCard, so it needs its own coverage to
// catch a silent regression in either path.
func TestRenderQueueRow_RepoPrefix(t *testing.T) {
	sess := agent.NewSessionForTest("s-a", "add-dark-mode")
	sess.SetLifecyclePhase(agent.LifecycleReadyForReview)

	d := newDashboardModel()
	d.width = 120
	d.items = []listItem{
		{kind: listItemSession, repoPath: "/a", repoName: "repoA", session: sess},
	}

	row := d.renderQueueRow(sess, "repoA", false, ColorWarning, 120)
	if len(row) == 0 {
		t.Fatalf("expected at least one rendered line")
	}
	line1 := ansi.Strip(row[0])
	if !strings.Contains(line1, "repoA › ") {
		t.Errorf("expected repo prefix \"repoA › \" on line 1, got %q", line1)
	}
	if !strings.Contains(line1, "add-dark-mode") {
		t.Errorf("expected session name on line 1, got %q", line1)
	}
	if idx := strings.Index(line1, "repoA › "); idx >= 0 {
		nameIdx := strings.Index(line1, "add-dark-mode")
		if nameIdx < idx {
			t.Errorf("expected repo prefix to precede session name, got %q", line1)
		}
	}

	bare := d.renderQueueRow(sess, "", false, ColorWarning, 120)
	if strings.Contains(ansi.Strip(bare[0]), "›") {
		t.Errorf("empty repoName should not emit › separator, got %q", ansi.Strip(bare[0]))
	}
}

// TestRenderFocusSessionCard_PlanBackedBuildingHasTaskProgressLine verifies
// that a Building session with a plan and open tasks produces a 4-line card
// where line 2 shows the first open task (bold, no leading ▸) and line 3
// shows "next: second open" (no leading ↳).
func TestRenderFocusSessionCard_PlanBackedBuildingHasTaskProgressLine(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSessionForTestWithPath("s", "my-session", dir)
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	sess.SetTaskSummary("implement oauth flow")
	if err := sess.WritePlan("- [x] done thing\n- [ ] write tests\n- [ ] open PR\n"); err != nil {
		t.Fatal(err)
	}
	ag := sess.AddTestAgent("a-1", false, agent.StatusActive)

	d := newDashboardModel()
	d.items = []listItem{
		{kind: listItemSession, repoPath: "/r", session: sess},
		{kind: listItemAgent, repoPath: "/r", session: sess, agent: ag},
	}

	card := d.renderFocusSessionCard(sess, "", false, 100)
	if len(card) != 4 {
		t.Fatalf("expected 4-line card for plan-backed building session, got %d lines: %v", len(card), card)
	}
	line2 := ansi.Strip(card[1])
	if strings.Contains(line2, "▸") {
		t.Errorf("line 2 must not contain ▸ prefix, got %q", line2)
	}
	if !strings.Contains(line2, "write tests") {
		t.Errorf("line 2 should contain first open task, got %q", line2)
	}
	line3 := ansi.Strip(card[2])
	if strings.Contains(line3, "↳") {
		t.Errorf("line 3 must not contain ↳ glyph, got %q", line3)
	}
	if !strings.Contains(line3, "next:") {
		t.Errorf("line 3 should contain \"next:\" prefix, got %q", line3)
	}
	if !strings.Contains(line3, "open PR") {
		t.Errorf("line 3 should contain second open task, got %q", line3)
	}
}

// TestRenderFocusSessionCard_PlanBackedBuilding_TodoOverridesPlan verifies that
// when the session's primary agent has an in_progress TodoItem, that item's
// content wins on line 2 over the plan's first uncompleted task.
func TestRenderFocusSessionCard_PlanBackedBuilding_TodoOverridesPlan(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSessionForTestWithPath("s", "my-session", dir)
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	if err := sess.WritePlan("- [ ] plan task one\n- [ ] plan task two\n"); err != nil {
		t.Fatal(err)
	}
	ag := sess.AddTestAgent("a-1", false, agent.StatusActive)
	ag.SetTodos([]agent.TodoItem{
		{Content: "todo task from agent", ActiveForm: "Running todo task", Status: "in_progress"},
		{Content: "next pending todo", Status: "pending"},
	})

	d := newDashboardModel()
	d.items = []listItem{
		{kind: listItemSession, repoPath: "/r", session: sess},
		{kind: listItemAgent, repoPath: "/r", session: sess, agent: ag},
	}

	card := d.renderFocusSessionCard(sess, "", false, 100)
	if len(card) != 4 {
		t.Fatalf("expected 4-line card, got %d", len(card))
	}
	line2 := ansi.Strip(card[1])
	if !strings.Contains(line2, "Running todo task") {
		t.Errorf("line 2 should show in_progress todo's ActiveForm, got %q", line2)
	}
	if strings.Contains(line2, "plan task") {
		t.Errorf("line 2 must not show plan task when todo overrides it, got %q", line2)
	}
}

// TestRenderFocusSessionCard_PlanWithSingleOpenTaskDropsNextSuffix verifies
// that when only one open task remains, line 2 shows the last task (bold, no ▸)
// and line 3 is blank (no "next:" suffix), card is exactly 4 lines.
func TestRenderFocusSessionCard_PlanWithSingleOpenTaskDropsNextSuffix(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSessionForTestWithPath("s", "my-session", dir)
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	if err := sess.WritePlan("- [x] done\n- [ ] last task\n"); err != nil {
		t.Fatal(err)
	}
	ag := sess.AddTestAgent("a-1", false, agent.StatusActive)

	d := newDashboardModel()
	d.items = []listItem{
		{kind: listItemSession, repoPath: "/r", session: sess},
		{kind: listItemAgent, repoPath: "/r", session: sess, agent: ag},
	}

	card := d.renderFocusSessionCard(sess, "", false, 100)
	if len(card) != 4 {
		t.Fatalf("expected 4-line card with single open task, got %d lines", len(card))
	}
	line2 := ansi.Strip(card[1])
	if !strings.Contains(line2, "last task") {
		t.Errorf("line 2 should contain open task name, got %q", line2)
	}
	line3 := ansi.Strip(card[2])
	if strings.Contains(line3, "next:") {
		t.Errorf("line 3 must not have 'next:' with single open task, got %q", line3)
	}
}

// TestRenderFocusSessionCard_PlanWithNoOpenTasksStaysFourLines verifies that a
// plan where all tasks are done produces a 4-line card (no task-progress line).
func TestRenderFocusSessionCard_PlanWithNoOpenTasksStaysFourLines(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSessionForTestWithPath("s", "my-session", dir)
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	if err := sess.WritePlan("- [x] one\n- [x] two\n"); err != nil {
		t.Fatal(err)
	}
	ag := sess.AddTestAgent("a-1", false, agent.StatusIdle)

	d := newDashboardModel()
	d.items = []listItem{
		{kind: listItemSession, repoPath: "/r", session: sess},
		{kind: listItemAgent, repoPath: "/r", session: sess, agent: ag},
	}

	card := d.renderFocusSessionCard(sess, "", false, 100)
	if len(card) != 4 {
		t.Fatalf("expected 4-line card when all tasks done, got %d lines", len(card))
	}
}

// TestRenderFocusSessionCard_NoPlanStaysFourLines verifies that a session with
// no worktree path (and therefore no plan) produces the regular 4-line card.
func TestRenderFocusSessionCard_NoPlanStaysFourLines(t *testing.T) {
	sess := agent.NewSessionForTest("s", "my-session")
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	ag := sess.AddTestAgent("a-1", false, agent.StatusActive)

	d := newDashboardModel()
	d.items = []listItem{
		{kind: listItemSession, repoPath: "/r", session: sess},
		{kind: listItemAgent, repoPath: "/r", session: sess, agent: ag},
	}

	card := d.renderFocusSessionCard(sess, "", false, 100)
	if len(card) != 4 {
		t.Fatalf("expected 4-line card with no plan, got %d lines", len(card))
	}
}

// TestSessionFocusStatus_BuildingWithPlanShowsProgressBadge verifies that a
// Building session backed by a plan file shows a progress bar with done/total,
// not the plain "N active, M idle" fallback.
func TestSessionFocusStatus_BuildingWithPlanShowsProgressBadge(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	dir := t.TempDir()
	sess := agent.NewSessionForTestWithPath("s", "my-session", dir)
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	if err := sess.WritePlan("- [x] one\n- [ ] two\n- [ ] three\n"); err != nil {
		t.Fatal(err)
	}
	ag := sess.AddTestAgent("a-1", false, agent.StatusActive)

	d := newDashboardModel()
	d.items = []listItem{
		{kind: listItemSession, repoPath: "/r", session: sess},
		{kind: listItemAgent, repoPath: "/r", session: sess, agent: ag},
	}

	badge := d.sessionFocusStatus(sess)
	stripped := ansi.Strip(badge)
	// Must contain the "1/3" count.
	if !strings.Contains(stripped, "1/3") {
		t.Errorf("expected plan progress badge '1/3', got %q", stripped)
	}
	// Must contain a progress bar block rune (▌ or █ or ░).
	if !strings.ContainsAny(badge, "▌█░") {
		t.Errorf("expected progress bar glyph (▌/█/░) in badge, got %q", badge)
	}
	// Must NOT contain the legacy "▸ " prefix.
	if strings.Contains(stripped, "▸ ") {
		t.Errorf("legacy ▸ prefix must not appear in new badge, got %q", stripped)
	}
	// Must NOT contain "· N active" suffix.
	if strings.Contains(stripped, "active") {
		t.Errorf("legacy '· N active' suffix must not appear in new badge, got %q", stripped)
	}
}

// TestSessionFocusStatus_BuildingWithPlanPreemptedByError verifies that an
// error badge still wins over the plan-derived progress badge.
func TestSessionFocusStatus_BuildingWithPlanPreemptedByError(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSessionForTestWithPath("s", "my-session", dir)
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	if err := sess.WritePlan("- [x] one\n- [ ] two\n- [ ] three\n"); err != nil {
		t.Fatal(err)
	}
	ag := sess.AddTestAgent("a-1", false, agent.StatusError)

	d := newDashboardModel()
	d.items = []listItem{
		{kind: listItemSession, repoPath: "/r", session: sess},
		{kind: listItemAgent, repoPath: "/r", session: sess, agent: ag},
	}

	badge := ansi.Strip(d.sessionFocusStatus(sess))
	if !strings.Contains(badge, "error") {
		t.Errorf("expected error badge to preempt plan progress, got %q", badge)
	}
	if strings.Contains(badge, "1/3") {
		t.Errorf("plan progress badge must not appear when agent has error, got %q", badge)
	}
}

// TestSessionFocusStatus_BuildingWithPlanPreemptedByWaiting verifies that a
// waiting badge still wins over the plan-derived progress badge.
func TestSessionFocusStatus_BuildingWithPlanPreemptedByWaiting(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSessionForTestWithPath("s", "my-session", dir)
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	if err := sess.WritePlan("- [x] one\n- [ ] two\n- [ ] three\n"); err != nil {
		t.Fatal(err)
	}
	ag := sess.AddTestAgent("a-1", false, agent.StatusWaiting)

	d := newDashboardModel()
	d.items = []listItem{
		{kind: listItemSession, repoPath: "/r", session: sess},
		{kind: listItemAgent, repoPath: "/r", session: sess, agent: ag},
	}

	badge := ansi.Strip(d.sessionFocusStatus(sess))
	if !strings.Contains(badge, "waiting") {
		t.Errorf("expected waiting badge to preempt plan progress, got %q", badge)
	}
	if strings.Contains(badge, "1/3") {
		t.Errorf("plan progress badge must not appear when agent is waiting, got %q", badge)
	}
}

// TestSessionFocusStatus_BuildingWithPlanAllDoneReviewablePrefersReviewBadge
// verifies that when all plan tasks are done and the session is reviewable, the
// "✓ idle — press m to review" badge wins over the plan "done/total" badge
// (Spec #6).
func TestSessionFocusStatus_BuildingWithPlanAllDoneReviewablePrefersReviewBadge(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSessionForTestWithPath("s", "my-session", dir)
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	if err := sess.WritePlan("- [x] one\n- [x] two\n"); err != nil {
		t.Fatal(err)
	}
	// Idle agent → IsReviewable() == true and DoneAt is zero.
	ag := sess.AddTestAgent("a-1", false, agent.StatusIdle)

	d := newDashboardModel()
	d.items = []listItem{
		{kind: listItemSession, repoPath: "/r", session: sess},
		{kind: listItemAgent, repoPath: "/r", session: sess, agent: ag},
	}

	badge := ansi.Strip(d.sessionFocusStatus(sess))
	if !strings.Contains(badge, "press m to review") {
		t.Errorf("expected review badge to win when all tasks done + reviewable, got %q", badge)
	}
	if strings.Contains(badge, "2/2") {
		t.Errorf("plan progress badge must not appear when reviewable, got %q", badge)
	}
}

// TestRenderCardProgressBar_DoneTotalAndColor verifies the renderCardProgressBar
// helper contract:
//
//	(a) returns "" when total == 0
//	(b) rendered string contains the "done/total" count suffix
//	(c) ansi.StringWidth of the output equals the requested width
//	(d) at 100% the rendered output uses ColorSuccess's hex
//	(e) at <100% it uses the passed primary color's hex
func TestRenderCardProgressBar_DoneTotalAndColor(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	// (a) total==0 → empty string
	if got := renderCardProgressBar(0, 0, 20, ColorPrimary); got != "" {
		t.Errorf("total==0: want \"\", got %q", got)
	}

	const width = 24
	// (b) contains "2/7 tasks" count suffix
	out := renderCardProgressBar(2, 7, width, ColorPrimary)
	if !strings.Contains(ansi.Strip(out), "2/7 tasks") {
		t.Errorf("expected \"2/7 tasks\" in output, got %q", ansi.Strip(out))
	}

	// (c) display width equals requested width
	if w := ansi.StringWidth(out); w != width {
		t.Errorf("display width = %d, want %d; raw=%q", w, width, out)
	}

	// (d) at 100% uses ColorSuccess (#10B981 → decimal 16;185;129 in ANSI).
	full := renderCardProgressBar(7, 7, width, ColorPrimary)
	// ColorSuccess = #10B981 → R=16, G=185, B=129.
	if !strings.Contains(full, "16;185;129") {
		t.Errorf("100%%: expected ColorSuccess (16;185;129) in output, got %q", full)
	}

	// (e) at <100% uses the passed primary color (#7C3AED → 124;58;237).
	// ColorPrimary = #7C3AED → R=124, G=58, B=237.
	if !strings.Contains(out, "124;58;237") {
		t.Errorf("<100%%: expected ColorPrimary (124;58;237) in output, got %q", out)
	}
}

// TestRenderFocusSessionCard_StatusGlyphMapping verifies that line 1 of each
// card carries the correct status glyph for each lifecycle/agent-state combination.
func TestRenderFocusSessionCard_StatusGlyphMapping(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	tests := []struct {
		name      string
		setup     func() (*agent.Session, []listItem)
		wantGlyph string
	}{
		{
			name: "error → ✗",
			setup: func() (*agent.Session, []listItem) {
				sess := agent.NewSessionForTest("s", "my-session")
				sess.SetLifecyclePhase(agent.LifecycleInProgress)
				ag := sess.AddTestAgent("a-1", false, agent.StatusError)
				return sess, []listItem{
					{kind: listItemSession, repoPath: "/r", session: sess},
					{kind: listItemAgent, repoPath: "/r", session: sess, agent: ag},
				}
			},
			wantGlyph: "✗",
		},
		{
			name: "waiting → ⏸",
			setup: func() (*agent.Session, []listItem) {
				sess := agent.NewSessionForTest("s", "my-session")
				sess.SetLifecyclePhase(agent.LifecycleInProgress)
				ag := sess.AddTestAgent("a-1", false, agent.StatusWaiting)
				return sess, []listItem{
					{kind: listItemSession, repoPath: "/r", session: sess},
					{kind: listItemAgent, repoPath: "/r", session: sess, agent: ag},
				}
			},
			wantGlyph: "⏸",
		},
		{
			name: "active → ●",
			setup: func() (*agent.Session, []listItem) {
				sess := agent.NewSessionForTest("s", "my-session")
				sess.SetLifecyclePhase(agent.LifecycleInProgress)
				ag := sess.AddTestAgent("a-1", false, agent.StatusActive)
				return sess, []listItem{
					{kind: listItemSession, repoPath: "/r", session: sess},
					{kind: listItemAgent, repoPath: "/r", session: sess, agent: ag},
				}
			},
			wantGlyph: "●",
		},
		{
			name: "reviewable → ✓",
			setup: func() (*agent.Session, []listItem) {
				sess := agent.NewSessionForTest("s", "my-session")
				sess.SetLifecyclePhase(agent.LifecycleInProgress)
				ag := sess.AddTestAgent("a-1", false, agent.StatusIdle)
				return sess, []listItem{
					{kind: listItemSession, repoPath: "/r", session: sess},
					{kind: listItemAgent, repoPath: "/r", session: sess, agent: ag},
				}
			},
			wantGlyph: "✓",
		},
		{
			name: "idle, no agents → ○",
			setup: func() (*agent.Session, []listItem) {
				sess := agent.NewSessionForTest("s", "my-session")
				sess.SetLifecyclePhase(agent.LifecycleInProgress)
				return sess, []listItem{
					{kind: listItemSession, repoPath: "/r", session: sess},
				}
			},
			wantGlyph: "○",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sess, items := tc.setup()
			d := newDashboardModel()
			d.items = items
			card := d.renderFocusSessionCard(sess, "", false, 100)
			line1 := ansi.Strip(card[0])
			if !strings.Contains(line1, tc.wantGlyph) {
				t.Errorf("line 1 should contain glyph %q, got %q", tc.wantGlyph, line1)
			}
		})
	}
}

// TestRenderFocusSessionCard_BranchChipAndElapsedGlyph verifies that line 4
// contains a ⎇ label before the branch name (no background tint), ⏱ before
// the elapsed token, and no label when the branch is empty.
func TestRenderFocusSessionCard_BranchChipAndElapsedGlyph(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	dir := t.TempDir()
	sess := agent.NewSessionForTestWithPath("s", "my-session", dir)
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	sess.UpdateBranch("baton/add-dark-mode")
	ag := sess.AddTestAgent("a-1", false, agent.StatusActive)

	d := newDashboardModel()
	d.items = []listItem{
		{kind: listItemSession, repoPath: "/r", session: sess},
		{kind: listItemAgent, repoPath: "/r", session: sess, agent: ag},
	}

	card := d.renderFocusSessionCard(sess, "", false, 120)
	if len(card) != 4 {
		t.Fatalf("expected 4-line card, got %d", len(card))
	}

	line4Raw := card[3]
	line4 := ansi.Strip(line4Raw)

	// ⎇ immediately before branch name (no background fill).
	if !strings.Contains(line4, "⎇") {
		t.Errorf("line 4 should contain ⎇ glyph, got %q", line4)
	}
	if !strings.Contains(line4, "add-dark-mode") {
		t.Errorf("line 4 should contain branch name, got %q", line4)
	}

	// ⏱ before the elapsed token.
	if !strings.Contains(line4, "⏱") {
		t.Errorf("line 4 should contain ⏱ glyph, got %q", line4)
	}

	// Branch label must NOT carry a background color escape (48;2; = background TrueColor).
	if strings.Contains(line4Raw, "48;2;") {
		t.Errorf("line 4 raw must NOT contain background ANSI sequence (48;2;) — branch is now a plain label, got %q", line4Raw)
	}

	// No branch → no label.
	sessNoBranch := agent.NewSessionForTest("s2", "no-branch-session")
	sessNoBranch.SetLifecyclePhase(agent.LifecycleInProgress)
	ag2 := sessNoBranch.AddTestAgent("a-2", false, agent.StatusActive)
	d2 := newDashboardModel()
	d2.items = []listItem{
		{kind: listItemSession, repoPath: "/r", session: sessNoBranch},
		{kind: listItemAgent, repoPath: "/r", session: sessNoBranch, agent: ag2},
	}
	card2 := d2.renderFocusSessionCard(sessNoBranch, "", false, 120)
	line4b := ansi.Strip(card2[3])
	if strings.Contains(line4b, "⎇") {
		t.Errorf("no-branch session should not render ⎇ label, got %q", line4b)
	}
}
