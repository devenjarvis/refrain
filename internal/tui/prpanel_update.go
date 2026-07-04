package tui

import (
	tea "charm.land/bubbletea/v2"
)

// prFeedbackRequestMsg is emitted by the panel when the user presses
// 'r' to address feedback. App handles the cross-cutting effects.
type prFeedbackRequestMsg struct {
	sessionID string
	repoPath  string
}

// feedbackNoteSubmitMsg is emitted by the embedded feedbackNoteModal when the
// user saves a note (enter). It is owned and handled here (§4): the PR
// panel persists the note via deps.SetFeedbackNote one Update cycle after the
// modal closes.
type feedbackNoteSubmitMsg struct {
	itemKey string
	note    string
}

// closePRPanel returns a tea.Cmd yielding panelCloseMsg. Returns the cmd
// for inline `return m, closePRPanel()`.
func closePRPanel() tea.Cmd {
	return func() tea.Msg { return panelCloseMsg{} }
}

// Update dispatches the PR panel's key handling.
func (m *prPanelModel) Update(msg tea.Msg) (PanelModel, tea.Cmd) {
	if m == nil || m.session == nil {
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height-1)
		return m, nil
	case feedbackNoteSubmitMsg:
		if m.deps.SetFeedbackNote != nil {
			m.deps.SetFeedbackNote(m.repoPath, m.session.ID, msg.itemKey, msg.note)
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// handleKey is the per-key dispatch. The nested feedback-note modal
// intercepts all keys when active.
func (m *prPanelModel) handleKey(msg tea.KeyPressMsg) (PanelModel, tea.Cmd) {
	if m.feedbackNote.Active() {
		var cmd tea.Cmd
		m.feedbackNote, cmd = m.feedbackNote.Update(msg)
		return m, cmd
	}

	entry := m.deps.PRCache(m.repoPath, m.session.ID)
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
		if len(items) > 0 && m.feedbackCursor < len(items) && m.deps.SetFeedbackVerdict != nil {
			key := feedbackItemKey(items[m.feedbackCursor])
			m.deps.SetFeedbackVerdict(m.repoPath, m.session.ID, key, feedbackApproved)
		}
	case "x":
		if len(items) > 0 && m.feedbackCursor < len(items) && m.deps.SetFeedbackVerdict != nil {
			key := feedbackItemKey(items[m.feedbackCursor])
			m.deps.SetFeedbackVerdict(m.repoPath, m.session.ID, key, feedbackDisagreed)
		}
	case "u":
		if len(items) > 0 && m.feedbackCursor < len(items) && m.deps.SetFeedbackVerdict != nil {
			key := feedbackItemKey(items[m.feedbackCursor])
			m.deps.SetFeedbackVerdict(m.repoPath, m.session.ID, key, feedbackNeutral)
		}
	case "n":
		if len(items) > 0 && m.feedbackCursor < len(items) {
			item := items[m.feedbackCursor]
			key := feedbackItemKey(item)
			existing := ""
			if triage := m.deps.FeedbackTriage(m.repoPath, m.session.ID); triage != nil {
				if e := triage[key]; e != nil {
					existing = e.Note
				}
			}
			return m, m.feedbackNote.Open(key, existing)
		}
	case "esc":
		return m, closePRPanel()
	case "t":
		sess := m.session
		repoPath := m.repoPath
		return m, tea.Batch(
			closePRPanel(),
			func() tea.Msg {
				return openAgentTerminalRequestMsg{
					session:       sess,
					repoPath:      repoPath,
					fallbackError: "session has no agents to open",
				}
			},
		)
	case "p":
		if entry != nil && entry.pr != nil && entry.pr.URL != "" {
			return m, openURLRequestCmd(entry.pr.URL)
		}
		return m, setErrorCmd("no PR URL available")
	case "m":
		if !isMergeReady(entry) {
			return m, setErrorCmd("not ready to merge — use M to force")
		}
		return m, m.deps.MergePRCmd(m.session.ID, m.repoPath, false)
	case "M":
		if entry == nil || entry.pr == nil {
			return m, setErrorCmd("no PR found")
		}
		return m, m.deps.MergePRCmd(m.session.ID, m.repoPath, true)
	case "r":
		sessID := m.session.ID
		repoPath := m.repoPath
		return m, func() tea.Msg {
			return prFeedbackRequestMsg{sessionID: sessID, repoPath: repoPath}
		}
	}
	return m, nil
}
