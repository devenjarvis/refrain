package tui

import (
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/config"
)

// applyOverrideString returns override when non-empty, otherwise fallback.
func applyOverrideString(override, fallback string) string {
	if override != "" {
		return override
	}
	return fallback
}

// applyOverrideBool returns *override when non-nil, otherwise fallback.
func applyOverrideBool(override *bool, fallback bool) bool {
	if override != nil {
		return *override
	}
	return fallback
}

// planModelOpts returns a DraftOption slice containing WithPlanModel when the
// override is non-empty, or nil when no override was set.
func planModelOpts(over sessionOverrides) []agent.DraftOption {
	if over.PlanModel != "" {
		return []agent.DraftOption{agent.WithPlanModel(over.PlanModel)}
	}
	return nil
}

func (a App) handlePlannerQuestion(msg plannerQuestionMsg) (tea.Model, tea.Cmd) {
	// If the plan editor isn't open for this session, auto-open it — but
	// only when the editor panel is not already visible. If session A's
	// editor is focused (panelFocus == focusPlanEditor) and a question
	// arrives for session B, opening session B's editor would silently
	// discard session A's unsaved textarea edits. In that case fall through
	// to the skip path below.
	sessionID := msg.question.SessionID
	focusAtArrival := panelFocusName(a.modals.Current())
	pe := a.modals.PlanEditor()
	editorAlreadyOpen := pe != nil && pe.sess != nil && pe.sess.ID == sessionID

	// Compute the skip disposition eagerly during the auto-open attempt so
	// the skip path never needs a second ListSessions() call. The default
	// covers the focusPlanEditor case (a different session's editor is
	// active); the inner branches refine it for the manager-missing and
	// session-missing cases so log readers can tell them apart.
	skipDisp := "skipped-no-editor"
	if !editorAlreadyOpen && !a.modals.Is(focusPlanEditor) {
		mgr := a.managers[msg.repoPath]
		if mgr == nil {
			skipDisp = "skipped-no-manager"
		} else {
			skipDisp = "skipped-session-missing"
			for _, s := range mgr.ListSessions() {
				if s.ID == sessionID {
					a.openPlanEditor(s, msg.repoPath)
					pe = a.modals.PlanEditor()
					// Cleared because the happy-path block below logs
					// "auto-opened" instead. openPlanEditor always sets
					// modals.PlanEditor(), so the happy-path check is
					// guaranteed to fire — this branch is unreachable if
					// that guarantee ever breaks.
					skipDisp = ""
					break
				}
			}
		}
	}
	if pe != nil && pe.sess != nil && pe.sess.ID == sessionID {
		disp := "auto-opened"
		if editorAlreadyOpen {
			if a.modals.Is(focusPlanEditor) {
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
		cmd := pe.AskQuestion(msg.question.Question, msg.question.AnswerCh)
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
	if pe := a.modals.PlanEditor(); pe != nil && pe.HasPendingQuestion() {
		pe.resolveQuestion("")
	}
	a.closeModal()
	return a, nil
}

func (a App) handlePlanEditorAbandon(msg planEditorAbandonMsg) (tea.Model, tea.Cmd) {
	// Tear down the session entirely — the user explicitly chose to walk
	// away from this plan. Resolve any pending planner question first so
	// the in-flight draft drains cleanly while KillSession is in flight.
	pe := a.modals.PlanEditor()
	if pe != nil && pe.HasPendingQuestion() {
		pe.resolveQuestion("")
	}
	repoPath := msg.repoPath
	if repoPath == "" && pe != nil {
		repoPath = pe.repoPath
	}
	if repoPath == "" {
		repoPath = a.repoPathForSession(msg.sessionID)
	}
	delete(a.pendingOverrides, msg.sessionID)
	mgr := a.managers[repoPath]
	a.closeModal()
	if mgr == nil {
		return a, nil
	}
	sessID := msg.sessionID
	a.closingSessions[cacheKey(repoPath, sessID)] = true
	return a, func() tea.Msg {
		err := mgr.KillSession(sessID)
		return killResultMsg{
			scope:     killScopeSession,
			repoPath:  repoPath,
			sessionID: sessID,
			err:       err,
		}
	}
}

func (a App) handlePlanEditorRevise(msg planEditorReviseMsg) (tea.Model, tea.Cmd) {
	// Pure manager-routing: the editor has already persisted any unsaved
	// textarea edits before emitting planEditorReviseMsg (see updateReviseInput),
	// so this just resolves the repo and dispatches mgr.RevisePlan.
	repoPath := msg.repoPath
	if repoPath == "" && a.modals.PlanEditor() != nil {
		repoPath = a.modals.PlanEditor().repoPath
	}
	if repoPath == "" {
		repoPath = a.repoPathForSession(msg.sessionID)
	}
	if repoPath == "" {
		repoPath = a.activeRepo
	}
	mgr := a.managers[repoPath]
	if mgr == nil {
		if a.modals.PlanEditor() != nil {
			a.modals.PlanEditor().SetError("session manager not found")
		}
		return a, nil
	}
	if err := mgr.RevisePlan(msg.sessionID, msg.critique, planModelOpts(a.pendingOverrides[msg.sessionID])...); err != nil {
		if a.modals.PlanEditor() != nil {
			a.modals.PlanEditor().SetError("revise: " + err.Error())
		}
		return a, nil
	}
	// Reflect the revising state immediately; the EventStatusChanged
	// dispatch above will keep it in sync as the goroutine progresses.
	if a.modals.PlanEditor() != nil && a.modals.PlanEditor().sess != nil &&
		a.modals.PlanEditor().sess.ID == msg.sessionID {
		a.modals.PlanEditor().SetRevising(true)
	}
	return a, nil
}

func (a App) handlePlanEditorRetry(msg planEditorRetryMsg) (tea.Model, tea.Cmd) {
	// User pressed R in the plan editor after auto-retry was exhausted.
	// Re-issue StartDraft with the session's saved original prompt so the
	// drafter retries from scratch. Mirrors createSessionFromPrompt's call
	// to StartDraft at app.go:3859.
	repoPath := msg.repoPath
	if repoPath == "" && a.modals.PlanEditor() != nil {
		repoPath = a.modals.PlanEditor().repoPath
	}
	if repoPath == "" {
		repoPath = a.repoPathForSession(msg.sessionID)
	}
	if repoPath == "" {
		repoPath = a.activeRepo
	}
	mgr := a.managers[repoPath]
	if mgr == nil {
		if a.modals.PlanEditor() != nil {
			a.modals.PlanEditor().SetError("session manager not found")
		}
		return a, nil
	}
	sess := mgr.GetSession(msg.sessionID)
	if sess == nil {
		if a.modals.PlanEditor() != nil {
			a.modals.PlanEditor().SetError("session not found")
		}
		return a, nil
	}
	prompt := sess.OriginalPrompt()
	if err := mgr.StartDraft(msg.sessionID, prompt, planModelOpts(a.pendingOverrides[msg.sessionID])...); err != nil {
		if a.modals.PlanEditor() != nil {
			a.modals.PlanEditor().SetError("retry draft: " + err.Error())
		}
		return a, nil
	}
	// Reflect the drafting state immediately; EventStatusChanged will
	// keep the editor in sync as the goroutine progresses.
	if a.modals.PlanEditor() != nil && a.modals.PlanEditor().sess != nil &&
		a.modals.PlanEditor().sess.ID == msg.sessionID {
		a.modals.PlanEditor().SetDrafting(true)
	}
	return a, nil
}

func (a *App) submitPromptModal(msg promptModalSubmitMsg) (tea.Model, tea.Cmd) {
	prompt := strings.TrimSpace(msg.prompt)
	// The `n` handler / repo picker that opened this screen already resolved
	// the target repo and stored it in a.activeRepo. Trust that rather than
	// re-deriving from the list cursor, which may sit on an unrelated repo's
	// session and would pin the new session to the wrong repo.
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
	rows := a.agentTermRows()
	cols := a.agentTermCols()
	if rows <= 0 || cols <= 0 {
		a.setError("Terminal size not yet known; try again")
		return a, nil
	}

	if !msg.planFirst {
		// Raw session (the default `enter`): spawn claude immediately with
		// the prompt as its task. An empty prompt opens a blank REPL — the
		// everyday case for debugging and exploring.
		a.view = ViewDashboard
		cfg := agent.Config{
			Rows:              rows,
			Cols:              cols,
			BypassPermissions: applyOverrideBool(msg.overrides.BypassPermissions, resolved.BypassPermissions),
			AgentProgram:      resolved.AgentProgram,
			AgentModel:        applyOverrideString(msg.overrides.AgentModel, resolved.AgentModel),
			BuildSystemPrompt: resolved.BuildSystemPrompt,
			Task:              prompt,
		}
		if msg.context == contextCheckout {
			return a, func() tea.Msg {
				sess, ag, err := mgr.CreateSessionInDir(cfg)
				if err != nil {
					if errors.Is(err, agent.ErrCheckoutSessionExists) {
						// One checkout session per repo: two agent groups in
						// one working tree would fight over the same files.
						return createResultMsg{err: errors.New("a checkout session already exists — press c on it to add an agent")}
					}
					return createResultMsg{err: err}
				}
				return createResultMsg{sessionID: sess.ID, agentID: ag.ID, isNewSession: true}
			}
		}
		return a, func() tea.Msg {
			sess, ag, err := mgr.CreateSession(cfg)
			if err != nil {
				return createResultMsg{err: err}
			}
			return createResultMsg{sessionID: sess.ID, agentID: ag.ID, isNewSession: true}
		}
	}

	// Plan-first path (ctrl+p): create the session worktree with no real
	// agent yet, kick off the draft subprocess in the background, and land
	// back on the session list with a "drafting…" badge. The plan editor
	// opens from the row (or auto-opens on a planner question).
	if msg.context == contextCheckout {
		// CreateSessionNoAgent is worktree-only; a no-agent checkout session
		// has no manager primitive yet (rollback design §9.5 — later phase).
		a.view = ViewDashboard
		a.setError("plan-first isn't available for checkout sessions yet")
		return a, nil
	}
	cfg := agent.Config{
		Rows:              rows,
		Cols:              cols,
		BypassPermissions: applyOverrideBool(msg.overrides.BypassPermissions, resolved.BypassPermissions),
		AgentProgram:      resolved.AgentProgram,
		AgentModel:        applyOverrideString(msg.overrides.AgentModel, resolved.AgentModel),
	}
	sess, err := mgr.CreateSessionNoAgent(cfg)
	if err != nil {
		a.setError(err.Error())
		return a, nil
	}
	sess.SetOriginalPrompt(prompt)
	if err := mgr.StartDraft(sess.ID, prompt, planModelOpts(msg.overrides)...); err != nil {
		a.setError("start draft: " + err.Error())
		// No fallback to openPlanEditor on error — the session stays on the
		// list; the user can press enter to open the editor and edit or
		// abandon the plan by hand.
	}
	// Store overrides for the approve path to read.
	a.pendingOverrides[sess.ID] = msg.overrides
	// sessionsCreatedCount is intentionally NOT incremented here. The user
	// hasn't committed to this session yet — they could abandon it from the
	// editor (`q`) before any real agent spawns. We count the session only
	// when approvePlanAndSpawn fires (and the raw path counts via its own
	// createResultMsg with isNewSession=true), so each session is logged
	// exactly once.
	a.view = ViewDashboard
	a.clampCursor()
	a.selectSessionRow(repoPath, sess.ID)
	return a, nil
}

// handlePlanGoalSubmit dispatches the goal collected by the plan-goal modal
// (`P` on a plan-less session): kicks off StartDraft against the session's
// directory and opens the plan editor showing the drafting placeholder.
// Drafting mid-conversation is safe — DraftRequest.Cwd is the session's
// worktree, and the drafter runs read-only alongside any live agents.
func (a App) handlePlanGoalSubmit(msg planGoalSubmitMsg) (tea.Model, tea.Cmd) {
	mgr := a.managers[msg.repoPath]
	if mgr == nil {
		a.setError("session manager not found")
		return a, nil
	}
	sess := mgr.GetSession(msg.sessionID)
	if sess == nil {
		a.setError("session not found")
		return a, nil
	}
	if err := mgr.StartDraft(msg.sessionID, msg.goal); err != nil {
		a.setError("start draft: " + err.Error())
		return a, nil
	}
	a.openPlanEditor(sess, msg.repoPath)
	return a, nil
}

// openPlanEditor switches the dashboard into the plan-editor overlay for
// sess. Caller is responsible for marking the session as drafting if a
// background draft is in flight.
func (a *App) openPlanEditor(sess *agent.Session, repoPath string) {
	if sess == nil {
		return
	}
	editorH := a.height - statusBarHeight // status row reserved
	editor := newPlanEditor(sess, repoPath, a.width, editorH)
	if sess.IsDrafting() {
		editor.SetDrafting(true)
	}
	a.openPlanEditorPanel(&editor)
}

// approvePlanAndSpawn handles a planEditorApproveMsg: closes the editor and
// spawns the real agent with the configured BuildFromPlanPrompt — an action,
// not a transition (rollback design §4.5). The plan text is already on disk
// by the time this fires (the editor's `a` handler writes it).
func (a *App) approvePlanAndSpawn(msg planEditorApproveMsg) (tea.Model, tea.Cmd) {
	repoPath := msg.repoPath
	if repoPath == "" && a.modals.PlanEditor() != nil {
		repoPath = a.modals.PlanEditor().repoPath
	}
	if repoPath == "" {
		repoPath = a.repoPathForSession(msg.sessionID)
	}
	if repoPath == "" {
		repoPath = a.activeRepo
	}
	mgr := a.managers[repoPath]
	if mgr == nil {
		a.closeModal()
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
		a.closeModal()
		a.setError("session not found")
		return a, nil
	}

	resolved := a.resolvedCache[repoPath]
	prompt := strings.TrimSpace(resolved.BuildFromPlanPrompt)
	if prompt == "" {
		prompt = config.DefaultBuildFromPlanPrompt
	}

	rows := a.agentTermRows()
	cols := a.agentTermCols()
	if rows <= 0 || cols <= 0 {
		a.setError("Terminal size not yet known; try again")
		return a, nil
	}

	// Apply any per-session overrides stored at submit time.
	over := a.pendingOverrides[sess.ID]
	delete(a.pendingOverrides, sess.ID)

	cfg := agent.Config{
		Rows:              rows,
		Cols:              cols,
		BypassPermissions: applyOverrideBool(over.BypassPermissions, resolved.BypassPermissions),
		AgentProgram:      resolved.AgentProgram,
		AgentModel:        applyOverrideString(over.AgentModel, resolved.AgentModel),
		BuildSystemPrompt: resolved.BuildSystemPrompt,
		Task:              prompt,
	}

	a.closeModal()
	sessID := sess.ID
	return a, func() tea.Msg {
		ag, err := mgr.AddAgent(sessID, cfg)
		if err != nil {
			return createResultMsg{err: err}
		}
		// isNewSession=true: from the wellness counter's perspective, an
		// approved plan is when a session "starts" — submitPromptModal
		// deliberately doesn't increment on plan creation, since the user
		// could still abandon it before approving.
		return createResultMsg{sessionID: sessID, agentID: ag.ID, isNewSession: true, skipFocusLaunch: true}
	}
}
