package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/devenjarvis/baton/internal/agent"
	"github.com/muesli/termenv"
)

// TestRenderFocusActiveCursor verifies that the selected session card in focus
// mode is visually distinct: the leading stripe glyph is rendered in
// ColorSecondary (cyan) for the selected session and a different color for
// unselected sessions. Selection is no longer signalled by a "> " chevron.
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
	d.focusCursorSection = focusSectionActive
	d.focusActiveIdx = 1
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
