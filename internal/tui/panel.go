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

// Panels close synchronously by calling svc.ClosePanel(). The callback is
// built each Update over &a (App's local copy), so the close ritual is a
// direct field mutation rather than an async tea.Msg round-trip — this keeps
// the test pattern simple (assert state immediately after panel.Update).

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
	Manager     func(repoPath string) SessionManager
	Resolved    func(repoPath string) config.ResolvedSettings
	GHClient    func() *github.Client
	PRCache     func(sessionID string) *prCacheEntry
	ReviewCache func(sessionID string) *reviewDiffEntry

	// Navigation / cross-panel actions.
	ClosePanel     func()
	OpenInLaunch   func(sess *agent.Session) bool
	OpenPlanEditor func(sess *agent.Session, repoPath string)
	OpenURL        func(url string) error
	SetError       func(msg string)

	// Cmd factories. Each MUST be pure: build the tea.Cmd, but never
	// mutate App state; the resulting tea.Msg flows back through App.Update.
	MergePRCmd      func(sessionID string, force bool) tea.Cmd
	StartPRDraftCmd func(sess *agent.Session, repoPath string, transitionShipping bool) tea.Cmd
	KillSessionCmd  func(sess *agent.Session) tea.Cmd
	FetchReviewDiff func(sess *agent.Session, repoPath string) tea.Cmd

	// prDraftInFlightFor reports whether a PR draft request is currently in
	// flight for the given session ID. The review panel uses this to render
	// the "Drafting PR…" footer hint.
	prDraftInFlightFor func(sessionID string) bool

	// Shipping-panel feedback triage state. Reads return the live map (or
	// nil); the setters lazily allocate and apply the same cleanup rules as
	// the original App methods (neutral+empty -> delete entry).
	FeedbackTriage     func(sessionID string) map[string]*feedbackTriageEntry
	SetFeedbackVerdict func(sessionID, itemKey string, v feedbackVerdict)
	SetFeedbackNote    func(sessionID, itemKey, note string)
}
