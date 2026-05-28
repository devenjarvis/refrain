package tui

import (
	"fmt"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/devenjarvis/refrain/internal/agent"
)

const (
	reviewTabTasks  = 0
	reviewTabDiff   = 1
	reviewTabChecks = 2
)

// reviewPanelModel owns the keyboard, mouse, and view dispatch for the review
// panel. Per-panel state (cursor, active tab, spec overlay toggle) lives here;
// cross-panel state (the reviewDiffCache keyed by session ID) stays on App
// because its lifetime exceeds a single panel session.
type reviewPanelModel struct {
	session           *agent.Session
	repoPath          string
	taskCursor        int
	activeTab         int
	specOverlay       bool
	specOverlayScroll int

	width, height int
}

// newReviewPanel constructs a review panel for sess at the given size.
// repoPath pins which repo's manager is used for all key handlers — it must
// match the repo the session belongs to so multi-repo ID collisions are safe.
func newReviewPanel(sess *agent.Session, repoPath string, width, height int) *reviewPanelModel {
	return &reviewPanelModel{
		session:  sess,
		repoPath: repoPath,
		width:    width,
		height:   height,
	}
}

// SessionID returns the ID of the session this panel is open for, or "" when
// the panel is unbound. Used by cross-panel transitions (PR auto-promote,
// PR-closed cleanup) to decide whether to close this panel.
func (m *reviewPanelModel) SessionID() string {
	if m == nil || m.session == nil {
		return ""
	}
	return m.session.ID
}

// Session returns the bound session, or nil. Read-only access for App.
func (m *reviewPanelModel) Session() *agent.Session {
	if m == nil {
		return nil
	}
	return m.session
}

// TaskCursor returns the current task-cursor row. Surfaced so App's
// fetchReviewDiffCmd / refreshAgentList can read it.
func (m *reviewPanelModel) TaskCursor() int {
	if m == nil {
		return 0
	}
	return m.taskCursor
}

// Resize updates cached layout dimensions.
func (m *reviewPanelModel) Resize(w, h int) {
	if m == nil {
		return
	}
	m.width = w
	m.height = h
}

// closeReviewPanel invokes svc.ClosePanel(), which clears App's reviewPanel
// pointer and routes focus back to the pipeline. Returns nil so callers can
// `return m, closeReviewPanel(svc)` inline.
func closeReviewPanel(svc PanelServices) tea.Cmd {
	if svc.ClosePanel != nil {
		svc.ClosePanel()
	}
	return nil
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
// missing, errors via svc.SetError when the IDE command isn't set.
func reviewOpenIDECmd(sess *agent.Session, repoPath string, svc PanelServices) {
	if sess == nil || sess.Worktree == nil {
		return
	}
	if repoPath == "" {
		return
	}
	ideCmd := strings.TrimSpace(svc.Resolved(repoPath).IDECommand)
	if ideCmd == "" {
		svc.SetError("No IDE configured (set 'IDE Command' in settings)")
		return
	}
	parts := splitIDECommand(ideCmd)
	if len(parts) == 0 {
		svc.SetError("No IDE configured (set 'IDE Command' in settings)")
		return
	}
	worktreePath := sess.Worktree.Path
	exe := parts[0]
	args := append(parts[1:], worktreePath)
	go func() {
		cmd := exec.Command(exe, args...)
		cmd.Dir = worktreePath
		_ = cmd.Start()
	}()
}

// Update dispatches the review panel's key handling. Returns the (possibly
// updated) panel and a tea.Cmd. To close, the panel returns a
// closeReviewPanel(svc); App's outer Update routes that back to
// focusList and clears a.reviewPanel.
func (m *reviewPanelModel) Update(msg tea.Msg, svc PanelServices) (PanelModel, tea.Cmd) {
	if m == nil || m.session == nil {
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.Resize(msg.Width, msg.Height-1)
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg, svc)
	case tea.MouseClickMsg:
		m.handleClick(msg, svc)
		return m, nil
	}
	return m, nil
}

// handleKey is the per-key dispatch extracted from app.go's monolithic
// Update. Spec overlay intercepts all keys while active.
func (m *reviewPanelModel) handleKey(msg tea.KeyPressMsg, svc PanelServices) (PanelModel, tea.Cmd) {
	// Tab-switching keys work even while spec overlay is open.
	switch msg.String() {
	case "1":
		m.activeTab = reviewTabTasks
		return m, nil
	case "2":
		m.activeTab = reviewTabDiff
		return m, nil
	case "3":
		m.activeTab = reviewTabChecks
		return m, nil
	case "tab":
		m.activeTab = (m.activeTab + 1) % 3
		return m, nil
	case "shift+tab":
		m.activeTab = (m.activeTab + 2) % 3
		return m, nil
	}

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
		return m, closeReviewPanel(svc)
	case "d":
		m.session.SetLifecyclePhase(agent.LifecycleReadyForReview)
		return m, closeReviewPanel(svc)
	case "p":
		sess := m.session
		entry := svc.PRCache(m.repoPath, sess.ID)
		if entry != nil && entry.pr != nil && entry.pr.URL != "" {
			if err := svc.OpenURL(entry.pr.URL); err != nil {
				svc.SetError(err.Error())
				return m, nil
			}
			sess.SetLifecyclePhase(agent.LifecycleShipping)
			return m, closeReviewPanel(svc)
		}
		gh := svc.GHClient()
		if gh == nil {
			svc.SetError("GitHub auth not available")
			return m, nil
		}
		return m, svc.StartPRDraftCmd(sess, m.repoPath, true)
	case "t":
		sess := m.session
		repoPath := m.repoPath
		// Close first regardless of outcome: pressing 't' is an exit-the-panel
		// intent. If the open fails, the error surfaces with the panel already
		// closed, matching the pre-refactor behaviour.
		closeReviewPanel(svc)
		if !svc.OpenInLaunch(sess, repoPath) {
			svc.SetError("session has no agents to open")
		}
		return m, nil
	case "c":
		sess := m.session
		sess.SetLifecyclePhase(agent.LifecycleComplete)
		mgr := svc.Manager(m.repoPath)
		if mgr == nil {
			return m, closeReviewPanel(svc)
		}
		return m, tea.Batch(closeReviewPanel(svc), svc.KillSessionCmd(sess, m.repoPath))
	case "e":
		reviewOpenIDECmd(m.session, m.repoPath, svc)
		return m, nil
	case "j", "down":
		if m.activeTab == reviewTabTasks {
			if entry := svc.ReviewCache(m.repoPath, m.session.ID); entry != nil {
				maxIdx := reviewTaskCount(entry) - 1
				if m.taskCursor < maxIdx {
					m.taskCursor++
				}
			}
		}
		return m, nil
	case "k", "up":
		if m.activeTab == reviewTabTasks && m.taskCursor > 0 {
			m.taskCursor--
		}
		return m, nil
	case "f":
		entry := svc.ReviewCache(m.repoPath, m.session.ID)
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
		entry := svc.ReviewCache(m.repoPath, m.session.ID)
		prompt := buildReviewReworkPrompt(entry)
		if prompt == "" {
			svc.SetError("no tasks flagged or marked concerns/fail")
			return m, nil
		}
		sessID := m.session.ID
		repoPath := m.repoPath
		return m, func() tea.Msg {
			return reviewReworkRequestMsg{sessionID: sessID, repoPath: repoPath, prompt: prompt}
		}
	case "enter":
		if m.activeTab == reviewTabTasks {
			entry := svc.ReviewCache(m.repoPath, m.session.ID)
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
		}
		return m, nil
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
// move. Mirrors the offset math in renderTaskListPane so the visual row
// under the click becomes the new cursor.
func (m *reviewPanelModel) handleClick(msg tea.MouseClickMsg, svc PanelServices) {
	if m == nil || m.session == nil {
		return
	}
	entry := svc.ReviewCache(m.repoPath, m.session.ID)
	if entry == nil {
		return
	}
	headerH := len(renderReviewHeader(m.session, m.width))
	const tabBarH = 2
	paneTop := svc.DashboardTopY + headerH + tabBarH
	rowIdx := reviewListPaneRowAt(entry, msg.X, msg.Y, paneTop, 0, m.width-2)
	if rowIdx < 0 {
		return
	}
	footerLines := 3
	bodyH := m.height - svc.DashboardTopY - headerH - tabBarH - footerLines
	if bodyH < 4 {
		bodyH = 4
	}
	const listHeaderLines = 2
	rowsH := bodyH - listHeaderLines
	if rowsH < 1 {
		rowsH = 1
	}
	nRows := reviewTaskCount(entry)
	offset := m.taskCursor - rowsH/2
	if offset < 0 {
		offset = 0
	}
	if offset+rowsH > nRows {
		offset = nRows - rowsH
		if offset < 0 {
			offset = 0
		}
	}
	m.taskCursor = offset + rowIdx
}

// View renders the review panel — either the spec overlay or the main panel.
func (m *reviewPanelModel) View(svc PanelServices) string {
	if m == nil || m.session == nil {
		return ""
	}
	if m.specOverlay {
		plan, _ := m.session.CachedPlan()
		return renderReviewSpecOverlay(m.session, plan, m.specOverlayScroll, m.width, m.height)
	}
	entry := svc.ReviewCache(m.repoPath, m.session.ID)
	prDraftInFlight := svc.prDraftInFlightFor(m.session.ID, m.repoPath)
	return renderReviewPanel(m.session, entry, m.width, m.height, m.taskCursor, prDraftInFlight, m.activeTab)
}
