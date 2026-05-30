package tui

import (
	tea "charm.land/bubbletea/v2"
)

// shippingFeedbackRequestMsg is emitted by the panel when the user presses
// 'r' to address feedback. App handles the cross-cutting effects.
type shippingFeedbackRequestMsg struct {
	sessionID string
	repoPath  string
}

// feedbackNoteSubmitMsg is emitted by the embedded feedbackNoteModal when the
// user saves a note (enter). It is owned and handled here (§4): the shipping
// panel persists the note via svc.SetFeedbackNote one Update cycle after the
// modal closes.
type feedbackNoteSubmitMsg struct {
	itemKey string
	note    string
}

// closeShippingPanel invokes svc.ClosePanel(). Returns nil for inline
// `return m, closeShippingPanel(svc)`.
func closeShippingPanel(svc PanelServices) tea.Cmd {
	if svc.ClosePanel != nil {
		svc.ClosePanel()
	}
	return nil
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
