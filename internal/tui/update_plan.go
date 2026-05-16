package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/config"
)

func (a App) handlePlannerQuestion(msg plannerQuestionMsg) (tea.Model, tea.Cmd) {
	// If the plan editor isn't open for this session, auto-open it — but
	// only when the editor panel is not already visible. If session A's
	// editor is focused (panelFocus == focusPlanEditor) and a question
	// arrives for session B, opening session B's editor would silently
	// discard session A's unsaved textarea edits. In that case fall through
	// to the skip path below.
	sessionID := msg.question.SessionID
	focusAtArrival := panelFocusName(a.dashboard.panelFocus)
	editorAlreadyOpen := a.planEditor != nil && a.planEditor.sess != nil &&
		a.planEditor.sess.ID == sessionID

	// Compute the skip disposition eagerly during the auto-open attempt so
	// the skip path never needs a second ListSessions() call. The default
	// covers the focusPlanEditor case (a different session's editor is
	// active); the inner branches refine it for the manager-missing and
	// session-missing cases so log readers can tell them apart.
	skipDisp := "skipped-no-editor"
	if !editorAlreadyOpen && a.dashboard.panelFocus != focusPlanEditor {
		mgr := a.managers[msg.repoPath]
		if mgr == nil {
			skipDisp = "skipped-no-manager"
		} else {
			skipDisp = "skipped-session-missing"
			for _, s := range mgr.ListSessions() {
				if s.ID == sessionID {
					a.openPlanEditor(s, msg.repoPath)
					// Cleared because the happy-path block below logs
					// "auto-opened" instead. openPlanEditor always sets
					// a.planEditor, so the happy-path check is guaranteed
					// to fire — this branch is unreachable if that
					// guarantee ever breaks.
					skipDisp = ""
					break
				}
			}
		}
	}
	if a.planEditor != nil && a.planEditor.sess != nil &&
		a.planEditor.sess.ID == sessionID {
		disp := "auto-opened"
		if editorAlreadyOpen {
			if a.dashboard.panelFocus == focusPlanEditor {
				disp = "routed-to-existing"
			} else {
				disp = "routed-to-background-editor"
			}
		}
		a.writePlannerLog(msg.repoPath, plannerLogEntry{
			Time:        time.Now().UTC().Format(time.RFC3339),
			SessionID:   sessionID,
			Disposition: disp,
			PanelFocus:  focusAtArrival,
		})
		// When the editor for this session exists but isn't the active
		// panel (user navigated to focusLaunch/focusReview), the question
		// is queued silently. Surface it in the status bar so the user
		// knows to switch back.
		if disp == "routed-to-background-editor" {
			a.setError("planner question waiting — open the plan editor to answer")
		}
		cmd := a.planEditor.AskQuestion(msg.question.Question, msg.question.AnswerCh)
		if mgr := a.managers[msg.repoPath]; mgr != nil {
			return a, tea.Batch(cmd, listenPlannerQuestions(mgr))
		}
		return a, cmd
	}
	// Skip path: either a different editor is focused (can't discard its
	// edits), the manager isn't registered, or the session isn't found.
	// Log and surface a status-bar error so the skip is never silent.
	a.writePlannerLog(msg.repoPath, plannerLogEntry{
		Time:        time.Now().UTC().Format(time.RFC3339),
		SessionID:   sessionID,
		Disposition: skipDisp,
		PanelFocus:  focusAtArrival,
	})
	a.setError("planner question skipped — plan editor could not open for session " + sessionID)
	select {
	case msg.question.AnswerCh <- "":
	default:
	}
	if mgr := a.managers[msg.repoPath]; mgr != nil {
		return a, listenPlannerQuestions(mgr)
	}
	return a, nil
}

func (a App) handlePlanEditorClose(msg planEditorCloseMsg) (tea.Model, tea.Cmd) {
	_ = msg
	// If the editor was parked on a planner question, answer it with the
	// skip-signal before tearing the editor down so the planner subprocess
	// unblocks promptly instead of waiting for its server to close.
	if a.planEditor != nil && a.planEditor.HasPendingQuestion() {
		a.planEditor.resolveQuestion("")
	}
	a.dashboard.panelFocus = focusList
	a.planEditor = nil
	return a, nil
}

func (a App) handlePlanEditorAbandon(msg planEditorAbandonMsg) (tea.Model, tea.Cmd) {
	// Tear down the session entirely — the user explicitly chose to walk
	// away from this plan. Resolve any pending planner question first so
	// the in-flight draft drains cleanly while KillSession is in flight.
	if a.planEditor != nil && a.planEditor.HasPendingQuestion() {
		a.planEditor.resolveQuestion("")
	}
	repoPath := msg.repoPath
	if repoPath == "" && a.planEditor != nil {
		repoPath = a.planEditor.repoPath
	}
	if repoPath == "" {
		repoPath = a.repoPathForSession(msg.sessionID)
	}
	mgr := a.managers[repoPath]
	a.dashboard.panelFocus = focusList
	a.planEditor = nil
	if mgr == nil {
		return a, nil
	}
	sessID := msg.sessionID
	a.closingSessions[sessID] = true
	return a, func() tea.Msg {
		err := mgr.KillSession(sessID)
		return killResultMsg{
			scope:     killScopeSession,
			sessionID: sessID,
			err:       err,
		}
	}
}

func (a App) handlePlanEditorRevise(msg planEditorReviseMsg) (tea.Model, tea.Cmd) {
	// Persist any unsaved textarea edits before revising so the drafter
	// sees what the user is actually looking at, not the last-saved
	// version. WritePlan errors flow up as an inline editor error and
	// we abort the revise — otherwise the model would revise the wrong
	// plan and overwrite the user's edits with the result.
	if a.planEditor != nil && a.planEditor.dirty && a.planEditor.sess != nil {
		val := a.planEditor.textarea.Value()
		if err := a.planEditor.sess.WritePlan(val); err != nil {
			a.planEditor.SetError("save plan: " + err.Error())
			return a, nil
		}
		a.planEditor.plan = val
		a.planEditor.dirty = false
	}
	repoPath := msg.repoPath
	if repoPath == "" && a.planEditor != nil {
		repoPath = a.planEditor.repoPath
	}
	if repoPath == "" {
		repoPath = a.repoPathForSession(msg.sessionID)
	}
	if repoPath == "" {
		repoPath = a.activeRepo
	}
	mgr := a.managers[repoPath]
	if mgr == nil {
		if a.planEditor != nil {
			a.planEditor.SetError("session manager not found")
		}
		return a, nil
	}
	if err := mgr.RevisePlan(msg.sessionID, msg.critique); err != nil {
		if a.planEditor != nil {
			a.planEditor.SetError("revise: " + err.Error())
		}
		return a, nil
	}
	// Reflect the revising state immediately; the EventStatusChanged
	// dispatch above will keep it in sync as the goroutine progresses.
	if a.planEditor != nil && a.planEditor.sess != nil &&
		a.planEditor.sess.ID == msg.sessionID {
		a.planEditor.SetRevising(true)
	}
	return a, nil
}

func (a App) handlePlanEditorRetry(msg planEditorRetryMsg) (tea.Model, tea.Cmd) {
	// User pressed R in the plan editor after auto-retry was exhausted.
	// Re-issue StartDraft with the session's saved original prompt so the
	// drafter retries from scratch. Mirrors createSessionFromPrompt's call
	// to StartDraft at app.go:3859.
	repoPath := msg.repoPath
	if repoPath == "" && a.planEditor != nil {
		repoPath = a.planEditor.repoPath
	}
	if repoPath == "" {
		repoPath = a.repoPathForSession(msg.sessionID)
	}
	if repoPath == "" {
		repoPath = a.activeRepo
	}
	mgr := a.managers[repoPath]
	if mgr == nil {
		if a.planEditor != nil {
			a.planEditor.SetError("session manager not found")
		}
		return a, nil
	}
	sess := mgr.GetSession(msg.sessionID)
	if sess == nil {
		if a.planEditor != nil {
			a.planEditor.SetError("session not found")
		}
		return a, nil
	}
	prompt := sess.OriginalPrompt()
	if err := mgr.StartDraft(msg.sessionID, prompt); err != nil {
		if a.planEditor != nil {
			a.planEditor.SetError("retry draft: " + err.Error())
		}
		return a, nil
	}
	// Reflect the drafting state immediately; EventStatusChanged will
	// keep the editor in sync as the goroutine progresses.
	if a.planEditor != nil && a.planEditor.sess != nil &&
		a.planEditor.sess.ID == msg.sessionID {
		a.planEditor.SetDrafting(true)
	}
	return a, nil
}

func (a App) handlePlanEditorRestore(msg planEditorRestoreMsg) (tea.Model, tea.Cmd) {
	// Single-step undo: restore plan.prev.md → plan.md and reload the
	// editor. No-op when no snapshot exists.
	if a.planEditor == nil || a.planEditor.sess == nil {
		return a, nil
	}
	sess := a.planEditor.sess
	if sess.ID != msg.sessionID {
		return a, nil
	}
	_, restored, err := sess.RestorePrevPlan()
	switch {
	case err != nil:
		a.planEditor.SetError("undo: " + err.Error())
	case !restored:
		a.planEditor.SetError("nothing to undo")
	default:
		a.planEditor.Reload()
	}
	return a, nil
}
func (a *App) submitPromptModal(msg promptModalSubmitMsg) (tea.Model, tea.Cmd) {
	prompt := strings.TrimSpace(msg.prompt)
	if prompt == "" {
		return a, nil
	}
	// The `n` handler / repo picker that opened this modal already resolved
	// the target repo and stored it in a.activeRepo. Re-resolving via
	// dashboard.selectedRepoPath here would override that with the legacy
	// d.selected lookup — which clamps to the first repo's header and never
	// follows the pipeline cursor or the picker's selection — pinning new
	// sessions to the first registered repo regardless of what the user
	// picked.
	repoPath := a.activeRepo
	if repoPath == "" {
		a.setError("no repo selected")
		return a, nil
	}
	mgr := a.managers[repoPath]
	if mgr == nil {
		a.setError("session manager not found")
		return a, nil
	}

	resolved := a.resolvedCache[repoPath]
	fixedW := a.dashboard.fixedTermWidth()
	fixedH := a.dashboard.fixedTermHeight()
	if fixedW <= 0 || fixedH <= 0 {
		a.setError("Terminal size not yet known; try again")
		return a, nil
	}

	if msg.skipPlanning {
		// Skip path: identical to today's `n` flow — create the session and
		// spawn the real agent immediately with the user's prompt as the
		// initial Task. Lifecycle starts at LifecycleInProgress so the row
		// shows up in BUILDING, not Planning.
		cfg := agent.Config{
			Rows:              fixedH,
			Cols:              fixedW,
			BypassPermissions: resolved.BypassPermissions,
			AgentProgram:      resolved.AgentProgram,
			AgentModel:        resolved.AgentModel,
			BuildSystemPrompt: resolved.BuildSystemPrompt,
			Task:              prompt,
		}
		return a, func() tea.Msg {
			sess, ag, err := mgr.CreateSession(cfg)
			if err != nil {
				return createResultMsg{err: err}
			}
			sess.SetLifecyclePhase(agent.LifecycleInProgress)
			return createResultMsg{sessionID: sess.ID, agentID: ag.ID, isNewSession: true}
		}
	}

	// Planning path: create the session worktree with no real agent yet,
	// kick off the draft subprocess in the background, and open the editor
	// with a "Drafting…" placeholder. The editor reloads itself when the
	// draft lands (via the EventStatusChanged emitted by runDraft).
	cfg := agent.Config{
		Rows:              fixedH,
		Cols:              fixedW,
		BypassPermissions: resolved.BypassPermissions,
		AgentProgram:      resolved.AgentProgram,
		AgentModel:        resolved.AgentModel,
	}
	sess, err := mgr.CreateSessionForPlanning(cfg)
	if err != nil {
		a.setError(err.Error())
		return a, nil
	}
	sess.SetOriginalPrompt(prompt)
	if err := mgr.StartDraft(sess.ID, prompt); err != nil {
		a.setError("start draft: " + err.Error())
		// No fallback to openPlanEditor on error — the session stays on the
		// dashboard in the planning card; the user can press enter to open the
		// editor and edit or abandon the plan by hand.
	}
	// sessionsCreatedCount is intentionally NOT incremented here. The user
	// hasn't committed to this session yet — they could abandon it from the
	// editor (`q`) before any real agent spawns. We count the session only
	// when approvePlanAndSpawn fires (and the skip path counts via its own
	// createResultMsg with isNewSession=true), so each approved/skipped
	// session is logged exactly once.
	a.refreshAgentList()
	// Stay on the dashboard while the draft runs in the background — the
	// planning card shows the "drafting…" badge. The user can press
	// enter/space on the card to open the editor when ready, or the editor
	// auto-opens if the planner raises a clarifying question.
	for idx, item := range a.dashboard.planningSessions() {
		if item.session != nil && item.session.ID == sess.ID {
			a.cursor.JumpTo(focusSectionPlanning, idx)
			a.syncFocusCursorToDashboard()
			break
		}
	}
	return a, nil
}

// openPlanEditor switches the dashboard into the plan-editor overlay for
// sess. Caller is responsible for marking the session as drafting if a
// background draft is in flight.
func (a *App) openPlanEditor(sess *agent.Session, repoPath string) {
	if sess == nil {
		return
	}
	editorH := a.height - 1 // status row reserved
	editor := newPlanEditor(sess, repoPath, a.width, editorH)
	if sess.IsDrafting() {
		editor.SetDrafting(true)
	}
	a.planEditor = &editor
	a.dashboard.panelFocus = focusPlanEditor
	a.dashboard.scrollOffset = 0
}

// approvePlanAndSpawn handles a planEditorApproveMsg: closes the editor,
// transitions the session to LifecycleInProgress, and spawns the real
// agent with the configured BuildFromPlanPrompt. The plan text is already
// on disk by the time this fires (the editor's `a` handler writes it).
func (a *App) approvePlanAndSpawn(msg planEditorApproveMsg) (tea.Model, tea.Cmd) {
	repoPath := msg.repoPath
	if repoPath == "" && a.planEditor != nil {
		repoPath = a.planEditor.repoPath
	}
	if repoPath == "" {
		repoPath = a.repoPathForSession(msg.sessionID)
	}
	if repoPath == "" {
		repoPath = a.activeRepo
	}
	mgr := a.managers[repoPath]
	if mgr == nil {
		a.dashboard.panelFocus = focusList
		a.planEditor = nil
		a.setError("session manager not found")
		return a, nil
	}

	var sess *agent.Session
	for _, s := range mgr.ListSessions() {
		if s.ID == msg.sessionID {
			sess = s
			break
		}
	}
	if sess == nil {
		a.dashboard.panelFocus = focusList
		a.planEditor = nil
		a.setError("session not found")
		return a, nil
	}

	resolved := a.resolvedCache[repoPath]
	prompt := strings.TrimSpace(resolved.BuildFromPlanPrompt)
	if prompt == "" {
		prompt = config.DefaultBuildFromPlanPrompt
	}

	fixedW := a.dashboard.fixedTermWidth()
	fixedH := a.dashboard.fixedTermHeight()
	if fixedW <= 0 || fixedH <= 0 {
		a.setError("Terminal size not yet known; try again")
		return a, nil
	}

	cfg := agent.Config{
		Rows:              fixedH,
		Cols:              fixedW,
		BypassPermissions: resolved.BypassPermissions,
		AgentProgram:      resolved.AgentProgram,
		AgentModel:        resolved.AgentModel,
		BuildSystemPrompt: resolved.BuildSystemPrompt,
		Task:              prompt,
	}

	a.dashboard.panelFocus = focusList
	a.planEditor = nil
	sessID := sess.ID
	// Phase transition is intentionally inside the closure: if AddAgent
	// fails, the session stays in LifecyclePlanning so the user can retry
	// from the plan editor instead of seeing an orphan row in BUILDING.
	return a, func() tea.Msg {
		ag, err := mgr.AddAgent(sessID, cfg)
		if err != nil {
			return createResultMsg{err: err}
		}
		sess.SetLifecyclePhase(agent.LifecycleInProgress)
		// isNewSession=true: from the wellness counter's perspective, an
		// approved plan is when a session "starts" — submitPromptModal
		// deliberately doesn't increment on plan creation, since the user
		// could still abandon it before approving.
		return createResultMsg{sessionID: sessID, agentID: ag.ID, isNewSession: true, skipFocusLaunch: true}
	}
}
