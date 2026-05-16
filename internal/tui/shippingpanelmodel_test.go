package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/github"
)

// newTestShippingSvc returns a stub PanelServices for shipping-panel tests
// plus a state value the test can inspect after Update.
func newTestShippingSvc() (PanelServices, *testShippingSvcState) {
	state := &testShippingSvcState{
		triage: map[string]*feedbackTriageEntry{},
	}
	svc := PanelServices{
		Width:  120,
		Height: 40,
		ManagerFor: func(string) (*agent.Manager, string) {
			return nil, ""
		},
		PRCache: func(string) *prCacheEntry { return state.pr },
		ClosePanel: func() {
			state.closed = true
		},
		OpenInLaunch: func(*agent.Session) bool {
			return state.openInLaunchResult
		},
		SetError: func(msg string) {
			state.errMsg = msg
		},
		OpenURL: func(string) error {
			state.openedURL = true
			return nil
		},
		MergePRCmd: func(sessionID string, force bool) tea.Cmd {
			state.mergeRequested = true
			state.mergeForce = force
			return func() tea.Msg { return nil }
		},
		FeedbackTriage: func(string) map[string]*feedbackTriageEntry {
			return state.triage
		},
		SetFeedbackVerdict: func(_, key string, v feedbackVerdict) {
			if state.triage[key] == nil {
				state.triage[key] = &feedbackTriageEntry{}
			}
			state.triage[key].Verdict = v
		},
		SetFeedbackNote: func(_, key, note string) {
			if state.triage[key] == nil {
				state.triage[key] = &feedbackTriageEntry{}
			}
			state.triage[key].Note = note
		},
	}
	return svc, state
}

type testShippingSvcState struct {
	pr                 *prCacheEntry
	triage             map[string]*feedbackTriageEntry
	closed             bool
	errMsg             string
	openedURL          bool
	openInLaunchResult bool
	mergeRequested     bool
	mergeForce         bool
}

// TestShippingPanelModel_EscCloses verifies esc closes the panel.
func TestShippingPanelModel_EscCloses(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship")
	panel := newShippingPanel(sess, 120, 40)
	svc, state := newTestShippingSvc()

	_, _ = panel.Update(tea.KeyPressMsg{Code: tea.KeyEscape}, svc)
	if !state.closed {
		t.Fatal("expected ClosePanel on esc")
	}
}

// TestShippingPanelModel_MergeBlockedWhenNotReady verifies 'm' surfaces an
// error and does not call MergePRCmd when the PR isn't merge-ready.
func TestShippingPanelModel_MergeBlockedWhenNotReady(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship")
	panel := newShippingPanel(sess, 120, 40)
	svc, state := newTestShippingSvc()
	// No PR entry => not merge-ready.

	_, _ = panel.Update(tea.KeyPressMsg{Code: 'm', Text: "m"}, svc)
	if state.mergeRequested {
		t.Error("expected merge to be blocked when PR not ready")
	}
	if state.errMsg == "" {
		t.Error("expected error to be surfaced when not ready")
	}
}

// TestShippingPanelModel_ForceMergeBypasses verifies 'M' bypasses the ready
// gate and calls MergePRCmd with force=true.
func TestShippingPanelModel_ForceMergeBypasses(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship")
	panel := newShippingPanel(sess, 120, 40)
	svc, state := newTestShippingSvc()
	state.pr = &prCacheEntry{pr: &github.PRState{Number: 1}}

	_, _ = panel.Update(tea.KeyPressMsg{Code: 'M', Text: "M"}, svc)
	if !state.mergeRequested {
		t.Fatal("expected force-merge to be requested")
	}
	if !state.mergeForce {
		t.Error("expected force=true on force-merge")
	}
}

// TestShippingPanelModel_PKeyOpensPRURL verifies 'p' calls svc.OpenURL when
// a URL is cached.
func TestShippingPanelModel_PKeyOpensPRURL(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship")
	panel := newShippingPanel(sess, 120, 40)
	svc, state := newTestShippingSvc()
	state.pr = &prCacheEntry{pr: &github.PRState{URL: "https://example/pr/1"}}

	_, _ = panel.Update(tea.KeyPressMsg{Code: 'p', Text: "p"}, svc)
	if !state.openedURL {
		t.Error("expected OpenURL invoked when PR URL is cached")
	}
}
