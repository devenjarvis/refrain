package tui

import (
	"time"

	"github.com/devenjarvis/refrain/internal/agent"
)

// dashboardProps carries every value the dashboard renders from but does not
// own. App.dashboardProps() rebuilds it fresh each frame and threads it into
// dashboardModel.View/Update (and their render helpers), so the dashboard reads
// live App state without mirroring it onto its own struct — the CONVENTIONS.md
// §5/§6 replacement for the old syncModalsToDashboard / refreshAgentList sync
// path. It is the dashboard's analogue of PanelServices.
//
// Purity rule (§5): every time-derived field here is computed from the
// tick-refreshed render clock (dashboardModel.now), never time.Now(), because
// dashboardProps() is invoked inside View().
type dashboardProps struct {
	// Modal state, owned by App.modals.
	panelFocus         panelFocus
	repoConfigForm     *configForm
	configRepoPath     string
	repoChecksEditor   *repoChecksModel
	repoChecksRepoPath string
	focusLaunchAgent   *agent.Agent
	focusLaunchSession *agent.Session

	// Pipeline cursor, owned by App.cursor.
	cursor FocusedCursor

	// Wellness snapshot, owned by App.wellness. Elapsed values are computed
	// against the render clock (see purity rule above).
	sessionElapsed         time.Duration
	focusSessionMinutes    int
	focusBreakMode         bool
	focusBreakElapsed      time.Duration
	focusBlockCount        int
	focusBreakMinutes      int
	focusBreakAnimFrame    int
	focusBreakShortWarning bool
	focusBreakTimerUp      bool

	// PR draft-in-flight indicator, owned by App.
	prDraftSessionID string
	prDraftRepoPath  string

	// Active repo, owned by App.
	activeRepoName string
	activeRepoPath string

	// Per-session caches, owned by App. Only the entries the dashboard renders
	// are carried: the PR badge cache and the session closing-set.
	prCache         map[string]*prCacheEntry
	closingSessions map[string]bool

	// items is the hierarchical repo/session/agent row list, rebuilt from the
	// managers each frame by App.listItems().
	items listItems
}
