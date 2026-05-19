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

	review      *reviewPanelModel
	shipping    *shippingPanelModel
	planEditor  *planEditorModel
	config      *configForm
	configRepo  string
	launchAgent *agent.Agent
	launchSess  *agent.Session
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
	m.shipping = nil
	m.planEditor = nil
	m.config = nil
	m.configRepo = ""
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

// OpenShipping opens the shipping panel.
func (m *Modals) OpenShipping(p *shippingPanelModel) {
	m.Close()
	m.current = focusShipping
	m.shipping = p
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

// Shipping returns the shipping panel model if focusShipping is current.
func (m *Modals) Shipping() *shippingPanelModel {
	if m.current == focusShipping {
		return m.shipping
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

// CompareAndSetShipping is the shipping panel's snapshot-and-swap variant.
func (m *Modals) CompareAndSetShipping(old, fresh *shippingPanelModel) bool {
	if m.current != focusShipping || m.shipping != old {
		return false
	}
	m.shipping = fresh
	return true
}
