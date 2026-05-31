package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/config"
)

// closeReviewPanel returns a tea.Cmd yielding panelCloseMsg, which App.Update
// handles by clearing the active panel and routing focus back to the pipeline.
// Returns the cmd so callers can `return m, closeReviewPanel()` inline.
func closeReviewPanel() tea.Cmd {
	return func() tea.Msg { return panelCloseMsg{} }
}

// reviewReworkRequestMsg is emitted by the panel when the user presses 'b'
// (back to build). App's outer Update handles the cross-cutting effects
// (drop the diff cache, spawn the new agent, transition the session) so the
// panel only signals intent.
type reviewReworkRequestMsg struct {
	sessionID string
	repoPath  string
	prompt    string
}

// reviewOpenIDECmd opens the configured IDE on the session's worktree.
// Mirrors the inline 'i' / 'e' key handler shape: silent if the worktree is
// missing, emits a setErrorMsg when the IDE command isn't set. Returns the
// launch tea.Cmd (or a setErrorMsg cmd, or nil); the launch result surfaces as
// ideOpenedMsg.
func reviewOpenIDECmd(sess *agent.Session, repoPath string, resolved func(repoPath string) config.ResolvedSettings) tea.Cmd {
	if sess == nil || sess.Worktree == nil {
		return nil
	}
	if repoPath == "" {
		return nil
	}
	ideCmd := strings.TrimSpace(resolved(repoPath).IDECommand)
	if ideCmd == "" {
		return setErrorCmd("No IDE configured (set 'IDE Command' in settings)")
	}
	parts := splitIDECommand(ideCmd)
	if len(parts) == 0 {
		return setErrorCmd("No IDE configured (set 'IDE Command' in settings)")
	}
	worktreePath := sess.Worktree.Path
	exe := parts[0]
	args := append(parts[1:], worktreePath)
	return openIDECmd(exe, args, worktreePath)
}

// setErrorCmd returns a tea.Cmd yielding a setErrorMsg, so panels can surface a
// transient error without reaching App directly.
func setErrorCmd(text string) tea.Cmd {
	return func() tea.Msg { return setErrorMsg{text: text} }
}

// openURLRequestCmd returns a pure tea.Cmd that opens url in the browser and
// reports the result via openURLResultMsg. Panels return this instead of
// calling openURL synchronously so the side effect flows through App.Update,
// where any failure is surfaced transiently.
func openURLRequestCmd(url string) tea.Cmd {
	return func() tea.Msg { return openURLResultMsg{err: openURL(url)} }
}

// Update dispatches the review panel's key handling. Returns the (possibly
// updated) panel and a tea.Cmd. To close, the panel returns closeReviewPanel();
// App's outer Update handles the panelCloseMsg.
func (m *reviewPanelModel) Update(msg tea.Msg) (PanelModel, tea.Cmd) {
	if m == nil || m.session == nil {
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height-1)
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.MouseClickMsg:
		m.handleClick(msg)
		return m, nil
	}
	return m, nil
}

// handleKey is the per-key dispatch extracted from app.go's monolithic
// Update. Spec overlay intercepts all keys while active.
func (m *reviewPanelModel) handleKey(msg tea.KeyPressMsg) (PanelModel, tea.Cmd) {

	if m.specOverlay {
		switch msg.String() {
		case "esc":
			m.specOverlay = false
		case "pgdown":
			m.specOverlayScroll += m.height - 4
		case "pgup":
			m.specOverlayScroll -= m.height - 4
			if m.specOverlayScroll < 0 {
				m.specOverlayScroll = 0
			}
		case "g":
			m.specOverlayScroll = 0
		case "G":
			m.specOverlayScroll = 9999
		}
		return m, nil
	}

	switch msg.String() {
	case "esc":
		return m, closeReviewPanel()
	case "d":
		m.session.SetLifecyclePhase(agent.LifecycleReadyForReview)
		return m, closeReviewPanel()
	case "p":
		sess := m.session
		entry := m.deps.PRCache(m.repoPath, sess.ID)
		if entry != nil && entry.pr != nil && entry.pr.URL != "" {
			sess.SetLifecyclePhase(agent.LifecycleShipping)
			return m, tea.Batch(openURLRequestCmd(entry.pr.URL), closeReviewPanel())
		}
		gh := m.deps.GHClient()
		if gh == nil {
			return m, setErrorCmd("GitHub auth not available")
		}
		repoPath := m.repoPath
		return m, func() tea.Msg {
			return startPRDraftRequestMsg{session: sess, repoPath: repoPath, transitionShipping: true}
		}
	case "t":
		sess := m.session
		repoPath := m.repoPath
		// Pressing 't' is an exit-the-panel intent: close, then ask App to open
		// the agent terminal. If no agent is available, App surfaces the error
		// with the panel already closed, matching the pre-refactor behaviour.
		return m, tea.Batch(
			closeReviewPanel(),
			func() tea.Msg {
				return openAgentTerminalRequestMsg{
					session:       sess,
					repoPath:      repoPath,
					fallbackError: "session has no agents to open",
				}
			},
		)
	case "m", "c":
		// m is the advertised key ("m approve"); c kept for backwards compatibility.
		sess := m.session
		sess.SetLifecyclePhase(agent.LifecycleComplete)
		mgr := m.deps.Manager(m.repoPath)
		if mgr == nil {
			return m, closeReviewPanel()
		}
		return m, tea.Batch(closeReviewPanel(), m.deps.KillSessionCmd(sess, m.repoPath))
	case "e":
		return m, reviewOpenIDECmd(m.session, m.repoPath, m.deps.Resolved)
	case "j", "down":
		if entry := m.deps.ReviewCache(m.repoPath, m.session.ID); entry != nil {
			maxIdx := reviewTaskCount(entry) - 1
			if m.taskCursor < maxIdx {
				m.taskCursor++
			}
		}
		return m, nil
	case "k", "up":
		if m.taskCursor > 0 {
			m.taskCursor--
		}
		return m, nil
	case "r":
		if m.deps.TriggerValidationRerun != nil {
			var run *validationRunState
			if m.deps.ValidationRuns != nil {
				run = m.deps.ValidationRuns(m.repoPath, m.session.ID)
			}
			var checks []config.ValidationCheck
			if run != nil {
				checks = run.checks
			} else if m.deps.Resolved != nil {
				checks = m.deps.Resolved(m.repoPath).ValidationChecks
			}
			if len(checks) == 0 {
				return m, nil
			}
			var worktreePath string
			if m.session.Worktree != nil {
				worktreePath = m.session.Worktree.Path
			}
			return m, m.deps.TriggerValidationRerun(m.session.ID, m.repoPath, worktreePath, checks)
		}
		return m, nil
	case "pgdown":
		m.vp.PageDown()
		return m, nil
	case "pgup":
		m.vp.PageUp()
		return m, nil
	case "ctrl+d":
		m.vp.HalfPageDown()
		return m, nil
	case "ctrl+u":
		m.vp.HalfPageUp()
		return m, nil
	case "g":
		m.vp.GotoTop()
		return m, nil
	case "G":
		m.vp.GotoBottom()
		return m, nil
	case "s":
		m.sideBySide = !m.sideBySide
		// Invalidate the renderer cache so the diff is re-rendered at the next View().
		m.renderersByPath = nil
		return m, nil
	case "[":
		entry := m.deps.ReviewCache(m.repoPath, m.session.ID)
		if dm, _, ok := m.focusedDiffModel(entry); ok && dm != nil {
			if m.vpFileIdx > 0 {
				m.vpFileIdx--
				m.vp.GotoTop()
				m.renderersByPath = nil
			}
		}
		return m, nil
	case "]":
		entry := m.deps.ReviewCache(m.repoPath, m.session.ID)
		if dm, _, ok := m.focusedDiffModel(entry); ok && dm != nil {
			if m.vpFileIdx < len(dm.Files)-1 {
				m.vpFileIdx++
				m.vp.GotoTop()
				m.renderersByPath = nil
			}
		}
		return m, nil
	case "f":
		entry := m.deps.ReviewCache(m.repoPath, m.session.ID)
		if entry == nil {
			return m, nil
		}
		idx, ok := reviewTaskIndexAtCursor(entry, m.taskCursor)
		if !ok {
			return m, nil
		}
		if entry.verdicts == nil {
			entry.verdicts = make(map[int]*taskVerdictRecord)
		}
		rec := entry.verdicts[idx]
		if rec == nil {
			rec = &taskVerdictRecord{state: verdictPending}
			entry.verdicts[idx] = rec
		}
		rec.userFlagged = !rec.userFlagged
		return m, nil
	case "b":
		entry := m.deps.ReviewCache(m.repoPath, m.session.ID)
		prompt := buildReviewReworkPrompt(entry)
		if prompt == "" {
			return m, setErrorCmd("no tasks flagged or marked concerns/fail")
		}
		sessID := m.session.ID
		repoPath := m.repoPath
		return m, func() tea.Msg {
			return reviewReworkRequestMsg{sessionID: sessID, repoPath: repoPath, prompt: prompt}
		}
	case "enter":
		entry := m.deps.ReviewCache(m.repoPath, m.session.ID)
		group := reviewTaskGroupAtCursor(entry, m.taskCursor)
		if group == nil || group.rawDiff == "" {
			return m, nil
		}
		// Build "[N] task text" label using same row order as the list pane.
		label := "Other changes"
		if entry != nil {
			row := 0
			for _, t := range entry.tasks {
				if row == m.taskCursor {
					label = fmt.Sprintf("[%d] %s", t.Index, t.Text)
					break
				}
				row++
			}
		}
		rawDiff := group.rawDiff
		return m, func() tea.Msg {
			return reviewOpenTaskDiffMsg{rawDiff: rawDiff, taskLabel: label}
		}
	case "space":
		return m, nil
	case "?":
		if m.session.HasPlan() {
			m.specOverlay = true
			m.specOverlayScroll = 0
		}
	}
	return m, nil
}

// handleClick maps a mouse-click on the review task list pane to a cursor
// move. Mirrors the layout math in renderTaskListPane and View() so the card
// under the click becomes the new cursor position.
func (m *reviewPanelModel) handleClick(msg tea.MouseClickMsg) {
	if m == nil || m.session == nil {
		return
	}
	entry := m.deps.ReviewCache(m.repoPath, m.session.ID)
	if entry == nil {
		return
	}

	// Compute left-pane width the same way as View() and the renderer.
	leftPaneW := reviewLeftPaneWidth(m.width)
	if leftPaneW == 0 {
		// Narrow stacked mode: full width is the task ledger.
		leftPaneW = m.width - 2
	} else if msg.X >= leftPaneW+1 {
		// Click is in the right (diff) pane — ignore.
		return
	}

	headerH := len(renderReviewHeader(m.session, m.width, m.now))
	checksH := len(renderChecksStrip(nil, m.width, m.now)) // conservative: nil if no checks
	paneTop := m.dashboardTopY + headerH + checksH
	rowIdx := reviewListPaneRowAt(entry, msg.X, msg.Y, paneTop, 0, leftPaneW)
	if rowIdx < 0 {
		return
	}

	footerLines := 3
	bodyH := m.height - m.dashboardTopY - headerH - checksH - footerLines
	if bodyH < 4 {
		bodyH = 4
	}
	const listHeaderLines = 2
	const cardH = 4
	cardsH := (bodyH - listHeaderLines) / cardH
	if cardsH < 1 {
		cardsH = 1
	}
	nRows := reviewTaskCount(entry)
	offset := m.taskCursor - cardsH/2
	if offset < 0 {
		offset = 0
	}
	if offset+cardsH > nRows {
		offset = nRows - cardsH
		if offset < 0 {
			offset = 0
		}
	}
	m.taskCursor = offset + rowIdx
}
