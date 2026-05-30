package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/devenjarvis/refrain/internal/agent"
)

// shippingPanelModel owns key/view dispatch for the shipping panel (PR
// status, CI failures, review threads, merge gate). Per-panel state
// (cursor, scroll, feedback-note modal) lives here; feedbackTriage stays on
// App because it survives panel close/reopen.
type shippingPanelModel struct {
	session        *agent.Session
	repoPath       string
	feedbackCursor int
	detailScroll   int
	feedbackNote   feedbackNoteModal

	width, height int
}

// newShippingPanel constructs a shipping panel for sess. repoPath pins which
// repo's manager is used for merge and feedback key handlers, preventing
// multi-repo session-ID collisions from routing operations to the wrong repo.
// The nested feedbackNote modal is initialised but inactive until the user
// presses 'n'.
func newShippingPanel(sess *agent.Session, repoPath string, width, height int) *shippingPanelModel {
	note := newFeedbackNoteModal()
	note.SetSize(width, height+1)
	return &shippingPanelModel{
		session:      sess,
		repoPath:     repoPath,
		feedbackNote: note,
		width:        width,
		height:       height,
	}
}

// SessionID returns the bound session's ID or "" when unbound.
func (m *shippingPanelModel) SessionID() string {
	if m == nil || m.session == nil {
		return ""
	}
	return m.session.ID
}

// Session returns the bound session, or nil.
func (m *shippingPanelModel) Session() *agent.Session {
	if m == nil {
		return nil
	}
	return m.session
}

// FeedbackCursor exposes the cursor row for tests and View rendering.
func (m *shippingPanelModel) FeedbackCursor() int {
	if m == nil {
		return 0
	}
	return m.feedbackCursor
}

// DetailScroll exposes the detail-pane scroll for tests and View rendering.
func (m *shippingPanelModel) DetailScroll() int {
	if m == nil {
		return 0
	}
	return m.detailScroll
}

// SetSize updates layout dimensions and forwards to the nested modal.
func (m *shippingPanelModel) SetSize(w, h int) {
	if m == nil {
		return
	}
	m.width = w
	m.height = h
	m.feedbackNote.SetSize(w, h+1)
}

// shippingFeedbackRequestMsg is emitted by the panel when the user presses
// 'r' to address feedback. App handles the cross-cutting effects.
type shippingFeedbackRequestMsg struct {
	sessionID string
	repoPath  string
}

// closeShippingPanel invokes svc.ClosePanel(). Returns nil for inline
// `return m, closeShippingPanel(svc)`.
func closeShippingPanel(svc PanelServices) tea.Cmd {
	if svc.ClosePanel != nil {
		svc.ClosePanel()
	}
	return nil
}

// feedbackNoteSubmitMsg is emitted by the embedded feedbackNoteModal when the
// user saves a note (enter). It is owned and handled here (§4): the shipping
// panel persists the note via svc.SetFeedbackNote one Update cycle after the
// modal closes.
type feedbackNoteSubmitMsg struct {
	itemKey string
	note    string
}

// Update dispatches the shipping panel's key handling.
func (m *shippingPanelModel) Update(msg tea.Msg, svc PanelServices) (PanelModel, tea.Cmd) {
	if m == nil || m.session == nil {
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height-1)
		return m, nil
	case feedbackNoteSubmitMsg:
		if svc.SetFeedbackNote != nil {
			svc.SetFeedbackNote(m.repoPath, m.session.ID, msg.itemKey, msg.note)
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg, svc)
	}
	return m, nil
}

// handleKey is the per-key dispatch. The nested feedback-note modal
// intercepts all keys when active.
func (m *shippingPanelModel) handleKey(msg tea.KeyPressMsg, svc PanelServices) (PanelModel, tea.Cmd) {
	if m.feedbackNote.Active() {
		var cmd tea.Cmd
		m.feedbackNote, cmd = m.feedbackNote.Update(msg)
		return m, cmd
	}

	entry := svc.PRCache(m.repoPath, m.session.ID)
	items := feedbackItems(entryThreads(entry))
	halfPane := m.height / 4
	if halfPane < 1 {
		halfPane = 1
	}

	switch msg.String() {
	case "j", "down":
		max := len(items) - 1
		if max < 0 {
			max = 0
		}
		if m.feedbackCursor < max {
			m.feedbackCursor++
		}
		m.detailScroll = 0
	case "k", "up":
		if m.feedbackCursor > 0 {
			m.feedbackCursor--
		}
		m.detailScroll = 0
	case "pgdown", "ctrl+d":
		m.detailScroll += halfPane
		if m.detailScroll < 0 {
			m.detailScroll = 0
		}
	case "pgup", "ctrl+u":
		m.detailScroll -= halfPane
		if m.detailScroll < 0 {
			m.detailScroll = 0
		}
	case "a":
		if len(items) > 0 && m.feedbackCursor < len(items) && svc.SetFeedbackVerdict != nil {
			key := feedbackItemKey(items[m.feedbackCursor])
			svc.SetFeedbackVerdict(m.repoPath, m.session.ID, key, feedbackApproved)
		}
	case "x":
		if len(items) > 0 && m.feedbackCursor < len(items) && svc.SetFeedbackVerdict != nil {
			key := feedbackItemKey(items[m.feedbackCursor])
			svc.SetFeedbackVerdict(m.repoPath, m.session.ID, key, feedbackDisagreed)
		}
	case "u":
		if len(items) > 0 && m.feedbackCursor < len(items) && svc.SetFeedbackVerdict != nil {
			key := feedbackItemKey(items[m.feedbackCursor])
			svc.SetFeedbackVerdict(m.repoPath, m.session.ID, key, feedbackNeutral)
		}
	case "n":
		if len(items) > 0 && m.feedbackCursor < len(items) {
			item := items[m.feedbackCursor]
			key := feedbackItemKey(item)
			existing := ""
			if triage := svc.FeedbackTriage(m.repoPath, m.session.ID); triage != nil {
				if e := triage[key]; e != nil {
					existing = e.Note
				}
			}
			return m, m.feedbackNote.Open(key, existing)
		}
	case "esc":
		return m, closeShippingPanel(svc)
	case "t":
		sess := m.session
		repoPath := m.repoPath
		closeShippingPanel(svc)
		if !svc.OpenInLaunch(sess, repoPath) {
			svc.SetError("session has no agents to open")
		}
		return m, nil
	case "p":
		if entry != nil && entry.pr != nil && entry.pr.URL != "" {
			if err := svc.OpenURL(entry.pr.URL); err != nil {
				svc.SetError(err.Error())
			}
		} else {
			svc.SetError("no PR URL available")
		}
	case "m":
		if !isMergeReady(entry) {
			svc.SetError("not ready to merge — use M to force")
			return m, nil
		}
		return m, svc.MergePRCmd(m.session.ID, m.repoPath, false)
	case "M":
		if entry == nil || entry.pr == nil {
			svc.SetError("no PR found")
			return m, nil
		}
		return m, svc.MergePRCmd(m.session.ID, m.repoPath, true)
	case "r":
		sessID := m.session.ID
		repoPath := m.repoPath
		return m, func() tea.Msg {
			return shippingFeedbackRequestMsg{sessionID: sessID, repoPath: repoPath}
		}
	}
	return m, nil
}

// View renders the shipping panel. Modal precedence: if the feedback-note
// modal is active, render it overlaid; otherwise render the panel proper.
func (m *shippingPanelModel) View(svc PanelServices) string {
	if m == nil || m.session == nil {
		return ""
	}
	entry := svc.PRCache(m.repoPath, m.session.ID)
	triage := svc.FeedbackTriage(m.repoPath, m.session.ID)
	return renderShippingPanel(m.session, entry, m.width, m.height, m.feedbackCursor, m.detailScroll, triage)
}

// NoteActive reports whether the feedback-note modal is currently active.
// App's View uses this to overlay the modal above the panel.
func (m *shippingPanelModel) NoteActive() bool {
	return m != nil && m.feedbackNote.Active()
}

// NoteView returns the rendered feedback-note modal for overlaying.
func (m *shippingPanelModel) NoteView() string {
	if m == nil {
		return ""
	}
	return m.feedbackNote.View()
}
