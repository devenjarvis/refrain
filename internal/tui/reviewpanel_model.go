package tui

import (
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/config"
	"github.com/devenjarvis/refrain/internal/diffmodel"
	"github.com/devenjarvis/refrain/internal/github"
	tuidiff "github.com/devenjarvis/refrain/internal/tui/diff"
)

// reviewPanelModel owns the keyboard, mouse, and view dispatch for the review
// panel. Per-panel state (cursor, spec overlay toggle) lives here; cross-panel
// state (the reviewDiffCache keyed by session ID) stays on App because its
// lifetime exceeds a single panel session.
type reviewPanelModel struct {
	session           *agent.Session
	repoPath          string
	taskCursor        int
	specOverlay       bool
	specOverlayScroll int
	checksCursor      int // cursor within the inline checks strip
	checksScroll      int // scroll offset for checks strip output

	// parsedDiffs is a lazy cache of parsed diff models keyed by taskIndex.
	// Populated by focusedDiffModel on first access; lives with the panel.
	parsedDiffs map[int]*diffmodel.Model

	// vp is the embedded viewport for the right (diff) pane.
	vp viewport.Model
	// vpFileIdx is the index of the currently shown file within the focused
	// task's diff model. Cycled with [ and ].
	vpFileIdx int
	// sideBySide toggles unified vs side-by-side diff rendering.
	sideBySide bool
	// renderersByPath caches diff.Renderer instances keyed by file path so
	// re-renders are cheap for revisited files.
	renderersByPath map[string]*tuidiff.Renderer

	// dashboardTopY is the screen Y offset where dashboard content begins.
	// App pushes this in via SetDashboardTopY; handleClick reads it instead of
	// reaching through a services value (§3 fold).
	dashboardTopY int

	// drafting reflects whether a PR draft is in flight for THIS session. App
	// pushes it via SetDrafting whenever the draft flags change; View renders
	// from it instead of querying App.
	drafting bool

	// now is the render clock, refreshed from the app tick via SetNow. View
	// derives elapsed/age strings and the verdict spinner from it so rendering
	// stays pure (§5: no clock read at render time).
	now time.Time

	// deps carries the App-level reference handles the panel reaches through
	// for lookups and cmd factories. Bound once at construction to maps and
	// pointers that outlive App value-copies (§3 fold).
	deps reviewDeps

	width, height int
}

// reviewDeps holds the reference-typed App handles the review panel needs.
// Bound at construction to the App's maps/pointers (never to App itself) so the
// closures stay live across App value-copies (App.Update is a value receiver).
type reviewDeps struct {
	Manager     func(repoPath string) SessionManager
	Resolved    func(repoPath string) config.ResolvedSettings
	GHClient    func() *github.Client
	PRCache     func(repoPath, sessionID string) *prCacheEntry
	ReviewCache func(repoPath, sessionID string) *reviewDiffEntry

	ValidationRuns         func(repoPath, sessID string) *validationRunState
	TriggerValidationRerun func(sessID, repoPath, worktreePath string, checks []config.ValidationCheck) tea.Cmd
	KillSessionCmd         func(sess *agent.Session, repoPath string) tea.Cmd
}

// newReviewPanel constructs a review panel for sess at the given size.
// repoPath pins which repo's manager is used for all key handlers — it must
// match the repo the session belongs to so multi-repo ID collisions are safe.
func newReviewPanel(sess *agent.Session, repoPath string, width, height int, deps reviewDeps) *reviewPanelModel {
	return &reviewPanelModel{
		session:  sess,
		repoPath: repoPath,
		width:    width,
		height:   height,
		now:      time.Now(),
		deps:     deps,
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

// SetDashboardTopY records the screen Y offset where dashboard content begins.
// App pushes this whenever it sets the panel size or the offset changes; the
// click-handler reads it instead of reaching through a services struct.
func (m *reviewPanelModel) SetDashboardTopY(y int) {
	if m == nil {
		return
	}
	m.dashboardTopY = y
}

// SetDrafting records whether a PR draft is in flight for this session. App
// pushes the updated value whenever its draft flags change; View renders the
// "Drafting PR…" footer from it.
func (m *reviewPanelModel) SetDrafting(v bool) {
	if m == nil {
		return
	}
	m.drafting = v
}
