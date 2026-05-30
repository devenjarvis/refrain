package tui

import (
	"time"

	"github.com/devenjarvis/refrain/internal/agent"
)

const (
	reviewTabTasks  = 0
	reviewTabDiff   = 1
	reviewTabChecks = 2
)

// reviewPanelModel owns the keyboard, mouse, and view dispatch for the review
// panel. Per-panel state (cursor, active tab, spec overlay toggle) lives here;
// cross-panel state (the reviewDiffCache keyed by session ID) stays on App
// because its lifetime exceeds a single panel session.
type reviewPanelModel struct {
	session           *agent.Session
	repoPath          string
	taskCursor        int
	activeTab         int
	specOverlay       bool
	specOverlayScroll int
	checksCursor      int // cursor position in the Checks tab list
	checksScroll      int // scroll offset for the Checks tab output pane

	// now is the render clock, refreshed from the app tick via SetNow. View
	// derives elapsed/age strings and the verdict spinner from it so rendering
	// stays pure (§5: no clock read at render time).
	now time.Time

	width, height int
}

// newReviewPanel constructs a review panel for sess at the given size.
// repoPath pins which repo's manager is used for all key handlers — it must
// match the repo the session belongs to so multi-repo ID collisions are safe.
func newReviewPanel(sess *agent.Session, repoPath string, width, height int) *reviewPanelModel {
	return &reviewPanelModel{
		session:  sess,
		repoPath: repoPath,
		width:    width,
		height:   height,
		now:      time.Now(),
	}
}

// SetNow updates the render clock. Called from the app tick so View can derive
// time-based display values without reading the clock itself (§5).
func (m *reviewPanelModel) SetNow(now time.Time) {
	if m == nil {
		return
	}
	m.now = now
}

// SessionID returns the ID of the session this panel is open for, or "" when
// the panel is unbound. Used by cross-panel transitions (PR auto-promote,
// PR-closed cleanup) to decide whether to close this panel.
func (m *reviewPanelModel) SessionID() string {
	if m == nil || m.session == nil {
		return ""
	}
	return m.session.ID
}

// Session returns the bound session, or nil. Read-only access for App.
func (m *reviewPanelModel) Session() *agent.Session {
	if m == nil {
		return nil
	}
	return m.session
}

// TaskCursor returns the current task-cursor row. Surfaced so App's
// click-handling and fetchReviewDiffCmd paths can read it.
func (m *reviewPanelModel) TaskCursor() int {
	if m == nil {
		return 0
	}
	return m.taskCursor
}

// SetSize updates cached layout dimensions.
func (m *reviewPanelModel) SetSize(w, h int) {
	if m == nil {
		return
	}
	m.width = w
	m.height = h
}
