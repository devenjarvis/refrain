package tui

import "github.com/devenjarvis/refrain/internal/agent"

// Modals owns the dashboard's panel focus and the lifetime of the modal
// models that focus governs. The invariant -- "the model pointer for
// panelFocus X is non-nil iff Current() == X" -- is enforced here: every
// Open* call atomically sets focus + model, every Close call sets focus
// to focusList and nils every owned pointer.
//
// Read accessors return non-nil only when their focus is current. This
// collapses the previous "if panelFocus == X && app.X != nil" guard pairs
// into a single typed lookup.
type Modals struct {
	current panelFocus

	review         *reviewPanelModel
	prPanel        *prPanelModel
	planEditor     *planEditorModel
	config         *configForm
	configRepo     string
	repoChecks     *repoChecksModel
	repoChecksRepo string
	launchAgent    *agent.Agent
	launchSess     *agent.Session
	// launchRepoPath is the repo that owns launchSess. Stored so the
	// focusLaunch key handlers (ctrl+t shell, ctrl+n agent, ctrl+w kill)
	// route to the right manager without a first-match session lookup, which
	// is wrong when session IDs collide across repos.
	launchRepoPath string
}

// Current returns the active panel focus.
func (m *Modals) Current() panelFocus { return m.current }

// Is reports whether the active focus is f.
func (m *Modals) Is(f panelFocus) bool { return m.current == f }

// IsList reports whether the pipeline list has focus (no overlay active).
func (m *Modals) IsList() bool { return m.current == focusList }

// Close returns focus to the pipeline list and nils every owned model.
// Calling Close on an already-closed Modals is a no-op.
func (m *Modals) Close() {
	m.current = focusList
	m.review = nil
	m.prPanel = nil
	m.planEditor = nil
	m.config = nil
	m.configRepo = ""
	m.repoChecks = nil
	m.repoChecksRepo = ""
	m.launchAgent = nil
	m.launchSess = nil
	m.launchRepoPath = ""
}

// OpenReview opens the review panel. Closes any prior modal first.
func (m *Modals) OpenReview(p *reviewPanelModel) {
	m.Close()
	m.current = focusReview
	m.review = p
}

// OpenPRPanel opens the PR panel.
func (m *Modals) OpenPRPanel(p *prPanelModel) {
	m.Close()
	m.current = focusPRPanel
	m.prPanel = p
}

// OpenPlanEditor opens the full-page plan editor.
func (m *Modals) OpenPlanEditor(p *planEditorModel) {
	m.Close()
	m.current = focusPlanEditor
	m.planEditor = p
}

// OpenConfig opens the per-repo config form for the given repo path.
func (m *Modals) OpenConfig(form *configForm, repoPath string) {
	m.Close()
	m.current = focusConfig
	m.config = form
	m.configRepo = repoPath
}

// OpenRepoChecks opens the validation-checks sub-editor without disturbing
// the parent config form — the config form is preserved so the user returns
// to it on save/cancel. The caller must hold focusConfig as a precondition,
// otherwise the call is a no-op.
func (m *Modals) OpenRepoChecks(editor *repoChecksModel, repoPath string) {
	if m.current != focusConfig {
		return
	}
	m.current = focusRepoChecks
	m.repoChecks = editor
	m.repoChecksRepo = repoPath
}

// CloseRepoChecks pops the checks editor and returns focus to the underlying
// repo config form. The parent form pointer was retained across the open, so
// it's still valid here.
func (m *Modals) CloseRepoChecks() {
	if m.current != focusRepoChecks {
		return
	}
	m.repoChecks = nil
	m.repoChecksRepo = ""
	m.current = focusConfig
}

// OpenLaunch opens the fullscreen agent terminal focused on ag (owned by sess
// in the repo at repoPath). repoPath is required so the focusLaunch key
// handlers (ctrl+t / ctrl+n / ctrl+w) can route to the right manager when
// session IDs collide across repos.
func (m *Modals) OpenLaunch(sess *agent.Session, ag *agent.Agent, repoPath string) {
	m.Close()
	m.current = focusLaunch
	m.launchSess = sess
	m.launchAgent = ag
	m.launchRepoPath = repoPath
}

// Review returns the review panel model if focusReview is current; otherwise nil.
func (m *Modals) Review() *reviewPanelModel {
	if m.current == focusReview {
		return m.review
	}
	return nil
}

// PRPanel returns the PR panel model if focusPRPanel is current.
func (m *Modals) PRPanel() *prPanelModel {
	if m.current == focusPRPanel {
		return m.prPanel
	}
	return nil
}

// PlanEditor returns the plan editor model if focusPlanEditor is current.
func (m *Modals) PlanEditor() *planEditorModel {
	if m.current == focusPlanEditor {
		return m.planEditor
	}
	return nil
}

// Config returns the config form if focusConfig is current.
func (m *Modals) Config() *configForm {
	if m.current == focusConfig {
		return m.config
	}
	return nil
}

// ConfigRepoPath returns the repo path the config form is editing, or "" if
// focusConfig is not current.
func (m *Modals) ConfigRepoPath() string {
	if m.current == focusConfig {
		return m.configRepo
	}
	return ""
}

// RepoChecks returns the validation-checks editor if focusRepoChecks is
// current; otherwise nil.
func (m *Modals) RepoChecks() *repoChecksModel {
	if m.current == focusRepoChecks {
		return m.repoChecks
	}
	return nil
}

// RepoChecksRepoPath returns the repo path whose checks are being edited, or
// "" when focusRepoChecks is not current.
func (m *Modals) RepoChecksRepoPath() string {
	if m.current == focusRepoChecks {
		return m.repoChecksRepo
	}
	return ""
}

// LaunchAgent returns the focused agent if focusLaunch is current.
func (m *Modals) LaunchAgent() *agent.Agent {
	if m.current == focusLaunch {
		return m.launchAgent
	}
	return nil
}

// LaunchSession returns the focused session if focusLaunch is current.
func (m *Modals) LaunchSession() *agent.Session {
	if m.current == focusLaunch {
		return m.launchSess
	}
	return nil
}

// LaunchRepoPath returns the repo path owning the focused session, or "" when
// focusLaunch is not current. Use this instead of repoPathForSession in
// focusLaunch handlers — repoPathForSession is ambiguous when two repos share
// a session ID, and falls back to "".
func (m *Modals) LaunchRepoPath() string {
	if m.current == focusLaunch {
		return m.launchRepoPath
	}
	return ""
}

// SetLaunchAgent updates the launch agent without changing focus or the
// session. Used when retargeting between sibling agents on the same session.
// A no-op when focusLaunch is not current.
func (m *Modals) SetLaunchAgent(ag *agent.Agent) {
	if m.current == focusLaunch {
		m.launchAgent = ag
	}
}

// CompareAndSetReview replaces the review panel only if the currently held
// pointer equals old and focusReview is current. Returns true on success.
// Mirrors the snapshot-and-restore pattern used after a panel's Update may
// have invoked svc.ClosePanel -- if the panel was closed during Update, the
// stored pointer differs from old and the swap is skipped.
func (m *Modals) CompareAndSetReview(old, fresh *reviewPanelModel) bool {
	if m.current != focusReview || m.review != old {
		return false
	}
	m.review = fresh
	return true
}

// CompareAndSetPRPanel is the PR panel's snapshot-and-swap variant.
func (m *Modals) CompareAndSetPRPanel(old, fresh *prPanelModel) bool {
	if m.current != focusPRPanel || m.prPanel != old {
		return false
	}
	m.prPanel = fresh
	return true
}
