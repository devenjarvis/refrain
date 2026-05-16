package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/config"
	"github.com/devenjarvis/refrain/internal/github"
)

// PanelModel is the contract every overlay panel implements. Panels are owned
// by App as nil-when-inactive pointer fields. They consume keypresses,
// resizes, and their own messages, and they ask to close by returning a
// tea.Cmd that yields a panelClosedMsg.
//
// PanelServices is passed by value on every Update call so the panel can
// reach app-level state (managers, caches, error sink, navigation actions)
// without holding a back-pointer to App. App constructs a fresh services
// value each tick — the closures inside it close over &a, so they always
// see the latest App state.
type PanelModel interface {
	Update(msg tea.Msg, svc PanelServices) (PanelModel, tea.Cmd)
	View(svc PanelServices) string
	Resize(w, h int)
}

// panelClosedMsg is the canonical "I'm done; route back to the pipeline"
// signal. App's outer Update recognises it and clears the matching panel
// field + resets panelFocus to focusList.
type panelClosedMsg struct {
	kind panelFocus
}

// closePanelCmd returns a tea.Cmd that signals the given panel to close.
// Panels use this rather than mutating App state directly so the close
// ritual lives in one place.
func closePanelCmd(kind panelFocus) tea.Cmd {
	return func() tea.Msg { return panelClosedMsg{kind: kind} }
}

// PanelServices is the slice of App state and behavior that panels are
// allowed to reach. Keep this struct narrow: every field here is a coupling
// point between App and every panel that uses it.
//
// Cmd factories (the *Cmd fields) must produce messages only — they must
// never mutate App. Their return values flow through App.Update like any
// other tea.Cmd, which is where mutation is centralised.
type PanelServices struct {
	// Layout.
	Width         int
	Height        int
	DashboardTopY int

	// Lookups.
	ManagerFor  func(sessionID string) (mgr *agent.Manager, repoPath string)
	Resolved    func(repoPath string) config.ResolvedSettings
	GHClient    func() *github.Client
	PRCache     func(sessionID string) *prCacheEntry
	ReviewCache func(sessionID string) *reviewDiffEntry

	// Navigation / cross-panel actions.
	OpenInLaunch   func(sess *agent.Session) bool
	OpenPlanEditor func(sess *agent.Session, repoPath string)
	OpenURL        func(url string) error
	SetError       func(msg string)

	// Cmd factories. Each MUST be pure: build the tea.Cmd, but never
	// mutate App state; the resulting tea.Msg flows back through App.Update.
	MergePRCmd      func(sessionID string, force bool) tea.Cmd
	StartPRDraftCmd func(sess *agent.Session, repoPath string, transitionShipping bool) tea.Cmd
	KillSessionCmd  func(sess *agent.Session) tea.Cmd
	FetchReviewDiff func(sess *agent.Session) tea.Cmd
}
