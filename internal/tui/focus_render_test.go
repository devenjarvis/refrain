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

// TestBuildingProgressBadge verifies the badge string for various todo states.
func TestBuildingProgressBadge(t *testing.T) {
	tests := []struct {
		name        string
		todos       []agent.TodoItem
		activeCount int
		wantEmpty   bool
		wantSubstr  string
	}{
		{
			name:      "no todos returns empty",
			todos:     nil,
			wantEmpty: true,
		},
		{
			name:        "2/5 with 1 active",
			todos:       makeTodos(5, 2),
			activeCount: 1,
			wantSubstr:  "2/5",
		},
		{
			name:        "active count included",
			todos:       makeTodos(3, 0),
			activeCount: 2,
			wantSubstr:  "2 active",
		},
		{
			name:        "no active count omitted when zero",
			todos:       makeTodos(3, 1),
			activeCount: 0,
			wantSubstr:  "1/3",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildingProgressBadge(tc.todos, tc.activeCount)
			if tc.wantEmpty {
				if got != "" {
					t.Errorf("expected empty badge, got %q", got)
				}
				return
			}
			plain := ansi.Strip(got)
			if !strings.Contains(plain, tc.wantSubstr) {
				t.Errorf("expected badge to contain %q, got %q", tc.wantSubstr, plain)
			}
		})
	}
}

// makeTodos builds a slice of n TodoItems with done of them marked completed.
func makeTodos(n, done int) []agent.TodoItem {
	items := make([]agent.TodoItem, n)
	for i := range items {
		if i < done {
			items[i] = agent.TodoItem{Content: "task", Status: "completed"}
		} else {
			items[i] = agent.TodoItem{Content: "task", Status: "pending"}
		}
	}
	return items
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

// TestSessionFocusStatus_BuildingWithPlanShowsProgressBadge verifies that a
// Building session backed by a plan file shows "▸ done/total · N active" on
// the badge, not the plain "N active, M idle" fallback.
func TestSessionFocusStatus_BuildingWithPlanShowsProgressBadge(t *testing.T) {
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

	badge := ansi.Strip(d.sessionFocusStatus(sess))
	if !strings.Contains(badge, "1/3") {
		t.Errorf("expected plan progress badge '1/3', got %q", badge)
	}
	if !strings.Contains(badge, "1 active") {
		t.Errorf("expected '1 active' in plan badge, got %q", badge)
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
