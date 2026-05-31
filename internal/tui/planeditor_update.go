package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// planEditorApproveMsg is emitted when the user approves the plan (`a`).
// The App spawns the real agent in response. The plan text itself isn't
// carried on the message — by the time approve fires, the editor has
// already written any pending textarea content to disk via Session.WritePlan,
// so the spawned agent reads .claude/plan.md directly.
type planEditorApproveMsg struct {
	sessionID string
	repoPath  string
}

// planEditorReviseMsg is emitted when the user submits a revise critique.
type planEditorReviseMsg struct {
	sessionID string
	repoPath  string
	critique  string
}

// planEditorRetryMsg is emitted when the user presses R in scroll mode while
// a draft error is set and the original prompt is available.
type planEditorRetryMsg struct {
	sessionID string
	repoPath  string
}

// planEditorAbandonMsg is emitted on `q` in scroll mode to abandon the
// planning session entirely.
type planEditorAbandonMsg struct {
	sessionID string
	repoPath  string
}

// planEditorCloseMsg is emitted on `esc` to close the editor and return to
// the dashboard without changing session state.
type planEditorCloseMsg struct {
	sessionID string
}

// planEditorSavedMsg is emitted when ctrl+s completes; the App typically
// just clears any pending error state.
type planEditorSavedMsg struct {
	sessionID string
}

// resolveQuestion sends answer to the planner, clears question state, and
// restores the prior mode. Safe to call only when a question is pending.
func (m *planEditorModel) resolveQuestion(answer string) {
	if m.questionAnswerCh == nil {
		return
	}
	// Non-blocking send: AnswerCh is buffered (cap 1) by the IPC server.
	select {
	case m.questionAnswerCh <- answer:
	default:
	}
	m.questionAnswerCh = nil
	m.questionText = ""
	m.questionInput.Blur()
	m.questionInput.SetValue("")
	m.mode = m.priorMode
	if m.mode == planEditorModeQuestion {
		m.mode = planEditorModeScroll
	}
}

// Update routes a key event. The caller should already have dispatched
// other tea.Msg types (resize, ticks).
func (m planEditorModel) Update(msg tea.Msg) (planEditorModel, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		// Forward non-key events to whichever component is active.
		if m.mode == planEditorModeEdit {
			var cmd tea.Cmd
			m.doc, cmd = m.doc.Update(msg)
			return m, cmd
		}
		if m.mode == planEditorModeReviseInput {
			var cmd tea.Cmd
			m.reviseInput, cmd = m.reviseInput.Update(msg)
			return m, cmd
		}
		if m.mode == planEditorModeQuestion {
			var cmd tea.Cmd
			m.questionInput, cmd = m.questionInput.Update(msg)
			return m, cmd
		}
		return m, nil
	}
	m.errMsg = ""

	var cmd tea.Cmd
	switch m.mode {
	case planEditorModeEdit:
		cmd = m.updateEdit(keyMsg)
	case planEditorModeReviseInput:
		cmd = m.updateReviseInput(keyMsg)
	case planEditorModeQuestion:
		cmd = m.updateQuestion(keyMsg)
	default:
		cmd = m.updateScroll(keyMsg)
	}
	return m, cmd
}

func (m *planEditorModel) updateScroll(msg tea.KeyPressMsg) tea.Cmd {
	if m.drafting {
		// Only esc/q work during drafting; everything else is a no-op so the
		// user can't approve a half-written plan.
		switch msg.String() {
		case "esc":
			return m.emitClose()
		case "q":
			return m.emitAbandon()
		}
		return nil
	}

	switch msg.String() {
	case "esc":
		return m.emitClose()
	case "q":
		return m.emitAbandon()
	case "j", "down":
		if len(m.sections) > 0 {
			m.sectionCursor++
			m.clampCursor()
			m.scrollToCursor()
		}
		return nil
	case "k", "up":
		if len(m.sections) > 0 {
			m.sectionCursor--
			m.clampCursor()
			m.scrollToCursor()
		}
		return nil
	case "ctrl+d", "pgdown":
		m.doc.scrollOff += m.doc.BodyHeight(5) / 2
		m.clampScroll()
		return nil
	case "ctrl+u", "pgup":
		m.doc.scrollOff -= m.doc.BodyHeight(5) / 2
		if m.doc.scrollOff < 0 {
			m.doc.scrollOff = 0
		}
		return nil
	case "g", "home":
		m.doc.scrollOff = 0
		return nil
	case "G", "end":
		m.doc.scrollOff = len(m.displayLines())
		m.clampScroll()
		return nil
	case "tab":
		idx := m.sectionCursor
		if idx >= 0 && idx < len(m.sections) {
			heading := m.sections[idx].heading
			m.folds[heading] = !m.folds[heading]
			m.invalidateDisplayCache()
			m.scrollToCursor()
		}
		return nil
	case "]":
		if len(m.sections) > 0 {
			m.sectionCursor++
			m.clampCursor()
			m.scrollToCursor()
		}
		return nil
	case "[":
		if len(m.sections) > 0 {
			m.sectionCursor--
			m.clampCursor()
			m.scrollToCursor()
		}
		return nil
	case "Z":
		// If any section is expanded, collapse all; otherwise expand all.
		anyExpanded := false
		for _, s := range m.sections {
			if !m.folds[s.heading] {
				anyExpanded = true
				break
			}
		}
		for _, s := range m.sections {
			m.folds[s.heading] = anyExpanded
		}
		m.invalidateDisplayCache()
		m.clampScroll()
		return nil
	case "i":
		if m.revising {
			return nil
		}
		m.mode = planEditorModeEdit
		return m.doc.Focus()
	case "r":
		if m.revising {
			return nil
		}
		m.mode = planEditorModeReviseInput
		m.reviseInput.SetValue("")
		return m.reviseInput.Focus()
	case "R":
		if m.drafting || m.revising {
			return nil
		}
		if m.sess == nil || m.sess.DraftError() == nil || m.sess.OriginalPrompt() == "" {
			return nil
		}
		sessID, repoPath := m.sess.ID, m.repoPath
		return func() tea.Msg { return planEditorRetryMsg{sessionID: sessID, repoPath: repoPath} }
	case "u":
		if m.revising || m.sess == nil {
			return nil
		}
		// Single-step undo is purely session-local (the editor holds sess),
		// so it runs inline rather than round-tripping a message through the
		// App. The HasPrevPlan guard keeps the no-op key press from looking
		// broken with a friendly inline message.
		if !m.sess.HasPrevPlan() {
			m.errMsg = "nothing to undo"
			return nil
		}
		m.restorePrevPlan()
		return nil
	case "a":
		if m.revising || m.drafting || m.sess == nil {
			return nil
		}
		// Persist any pending textarea edits before approving so the spawned
		// agent reads exactly what the user saw on screen. Approve is a
		// no-op on an empty plan; the editor surfaces an inline error and
		// stays put.
		val := m.doc.Value()
		if strings.TrimSpace(val) == "" {
			m.errMsg = "Plan is empty — edit or revise first."
			return nil
		}
		if m.dirty {
			if err := m.sess.WritePlan(val); err != nil {
				m.errMsg = "save plan: " + err.Error()
				return nil
			}
			m.plan = val
			m.dirty = false
		}
		return m.emitApprove()
	}
	return nil
}

func (m *planEditorModel) updateEdit(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		// Preserve any in-progress edits — esc only blurs the textarea so
		// the user can scroll and approve without losing typed content. The
		// dirty indicator stays visible until ctrl+s or `a` writes to disk.
		m.doc.Blur()
		m.rebuildSectionsPreservingFolds()
		m.mode = planEditorModeScroll
		return nil
	case "ctrl+s":
		if m.sess == nil {
			return nil
		}
		val := m.doc.Value()
		if err := m.sess.WritePlan(val); err != nil {
			m.errMsg = "save plan: " + err.Error()
			return nil
		}
		m.plan = val
		m.dirty = false
		m.saveNote = "saved"
		m.saveAt = time.Now()
		m.saveNoteVisible = true
		m.rebuildSectionsPreservingFolds()
		sessID := m.sess.ID
		return func() tea.Msg { return planEditorSavedMsg{sessionID: sessID} }
	}
	prev := m.doc.Value()
	var cmd tea.Cmd
	m.doc, cmd = m.doc.Update(msg)
	if m.doc.Value() != prev {
		m.dirty = true
	}
	return cmd
}

// updateQuestion handles input while the editor is parked on a planner
// ask_user question. Enter submits the typed answer; esc submits an empty
// answer (the agreed "skip / no answer" signal so the planner unblocks
// rather than deadlocking). Answering also restores the prior input mode
// so the user can continue scrolling/editing without re-entering it.
func (m *planEditorModel) updateQuestion(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.resolveQuestion("")
		return nil
	case "enter":
		answer := strings.TrimSpace(m.questionInput.Value())
		m.resolveQuestion(answer)
		return nil
	}
	var cmd tea.Cmd
	m.questionInput, cmd = m.questionInput.Update(msg)
	return cmd
}

func (m *planEditorModel) updateReviseInput(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.reviseInput.Blur()
		m.mode = planEditorModeScroll
		return nil
	case "enter":
		critique := strings.TrimSpace(m.reviseInput.Value())
		if critique == "" {
			m.errMsg = "Critique is empty — describe what should change."
			return nil
		}
		m.reviseInput.Blur()
		m.mode = planEditorModeScroll
		// Persist any unsaved textarea edits before revising so the drafter
		// sees what the user is actually looking at, not the last-saved
		// version. On write error, surface it inline and abort the revise —
		// otherwise the drafter would revise the wrong plan and overwrite the
		// user's edits with the result. The editor owns this save (it holds
		// sess), so App's revise handler is left as pure manager-routing.
		if m.dirty && m.sess != nil {
			val := m.doc.Value()
			if err := m.sess.WritePlan(val); err != nil {
				m.errMsg = "save plan: " + err.Error()
				return nil
			}
			m.plan = val
			m.dirty = false
		}
		sessID := m.sess.ID
		repoPath := m.repoPath
		return func() tea.Msg {
			return planEditorReviseMsg{sessionID: sessID, repoPath: repoPath, critique: critique}
		}
	}
	var cmd tea.Cmd
	m.reviseInput, cmd = m.reviseInput.Update(msg)
	return cmd
}

// restorePrevPlan performs a single-step undo: restore plan.prev.md → plan.md
// and reload the editor. Session-local, so the editor owns it directly instead
// of routing through the App. Callers guard HasPrevPlan first; the !restored
// branch is a race fallback (snapshot vanished between check and restore).
func (m *planEditorModel) restorePrevPlan() {
	if m.sess == nil {
		return
	}
	_, restored, err := m.sess.RestorePrevPlan()
	switch {
	case err != nil:
		m.errMsg = "undo: " + err.Error()
	case !restored:
		m.errMsg = "nothing to undo"
	default:
		m.Reload()
	}
}

func (m *planEditorModel) emitApprove() tea.Cmd {
	if m.sess == nil {
		return nil
	}
	sessID := m.sess.ID
	repoPath := m.repoPath
	return func() tea.Msg { return planEditorApproveMsg{sessionID: sessID, repoPath: repoPath} }
}

func (m *planEditorModel) emitAbandon() tea.Cmd {
	if m.sess == nil {
		return nil
	}
	sessID := m.sess.ID
	repoPath := m.repoPath
	return func() tea.Msg { return planEditorAbandonMsg{sessionID: sessID, repoPath: repoPath} }
}

func (m *planEditorModel) emitClose() tea.Cmd {
	if m.sess == nil {
		return nil
	}
	sessID := m.sess.ID
	return func() tea.Msg { return planEditorCloseMsg{sessionID: sessID} }
}
