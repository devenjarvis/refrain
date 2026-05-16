package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/devenjarvis/refrain/internal/agent"
)

// panel-level test pattern: instantiate the panel directly, supply a stub
// PanelServices, drive it with messages, assert state. No full App spin-up,
// no goroutines, no claude binary.

func newTestSvc() (PanelServices, *testServiceState) {
	state := &testServiceState{}
	svc := PanelServices{
		Width:  120,
		Height: 40,
		ManagerFor: func(string) (*agent.Manager, string) {
			return nil, ""
		},
		ReviewCache: func(string) *reviewDiffEntry { return nil },
		ClosePanel: func() {
			state.closed = true
		},
		OpenInLaunch: func(*agent.Session) bool {
			state.openInLaunchCalled = true
			return state.openInLaunchResult
		},
		SetError: func(msg string) {
			state.errMsg = msg
		},
		KillSessionCmd: func(*agent.Session) tea.Cmd {
			state.killSessionCalled = true
			return func() tea.Msg { return nil }
		},
		prDraftInFlightFor: func(string) bool { return false },
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

// TestReviewPanelModel_EscCloses verifies that pressing esc invokes
// svc.ClosePanel() and otherwise leaves session lifecycle untouched.
func TestReviewPanelModel_EscCloses(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	sess.SetLifecyclePhase(agent.LifecycleInReview)
	panel := newReviewPanel(sess, 120, 40)
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
	panel := newReviewPanel(sess, 120, 40)
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
	panel := newReviewPanel(sess, 120, 40)
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
	panel := newReviewPanel(sess, 120, 40)
	svc, state := newTestSvc()
	// Wire ManagerFor to a non-nil-looking entry so the panel reaches the
	// kill path. The actual manager pointer is not exercised here because
	// KillSessionCmd is stubbed.
	svc.ManagerFor = func(string) (*agent.Manager, string) {
		return &agent.Manager{}, "/repo"
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
	panel := newReviewPanel(sess, 120, 40)
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{
			{Index: 1, Text: "task one"},
			{Index: 2, Text: "task two"},
			{Index: 3, Text: "task three"},
		},
	}
	svc, _ := newTestSvc()
	svc.ReviewCache = func(string) *reviewDiffEntry { return entry }

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
