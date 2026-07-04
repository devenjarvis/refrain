package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/diffmodel"
	"github.com/devenjarvis/refrain/internal/git"
)

// sessionListProps assembles the per-frame snapshot the session list renders
// from. Built fresh on every View()/Update so the list never holds a mirror of
// App state (CONVENTIONS.md §5/§6).
func (a *App) sessionListProps() sessionListProps {
	return sessionListProps{
		items:            a.listItems(),
		prCache:          a.prCache,
		closingSessions:  a.closingSessions,
		activeRepoPath:   a.activeRepo,
		prDraftSessionID: a.prDraftSessionID,
		prDraftRepoPath:  a.prDraftRepoPath,
	}
}

// agentTermCols and agentTermRows are the one terminal geometry every agent
// uses: the fullscreen launch-view viewport. With the preview pane gone,
// agents are created at and kept at this size, so opening the terminal never
// triggers a resize.
func (a *App) agentTermCols() int {
	return a.width
}

func (a *App) agentTermRows() int {
	return a.height - statusBarHeight - 2 // header row + tab bar row
}

// selectedSessionRow returns the session row under the list cursor.
// ok is false when the list is empty.
func (a *App) selectedSessionRow() (sessionRow, bool) {
	layout := buildSessionListLayout(a.listItems())
	if len(layout.rows) == 0 {
		return sessionRow{}, false
	}
	idx := a.sessionList.cursor
	if idx < 0 {
		idx = 0
	}
	if idx >= len(layout.rows) {
		idx = len(layout.rows) - 1
	}
	return layout.rows[idx], true
}

// selectSessionRow moves the list cursor to the row for (repoPath, sessionID).
// Used after session creation so the cursor lands on the new row.
func (a *App) selectSessionRow(repoPath, sessionID string) {
	layout := buildSessionListLayout(a.listItems())
	a.sessionList.selectSession(layout, repoPath, sessionID)
}

// activateSelectedRow opens the row under the cursor. Every session opens in
// the fullscreen terminal (the terminal is the product — rollback design
// §4.2); a session with no agents yet (plan-first flow) opens its plan editor
// instead, since there is no terminal to show.
func (a *App) activateSelectedRow() bool {
	row, ok := a.selectedSessionRow()
	if !ok {
		return false
	}
	if a.openSessionInFocusLaunch(row.session, row.repoPath) {
		return true
	}
	if row.session.IsDrafting() || row.session.HasPlan() {
		a.openPlanEditor(row.session, row.repoPath)
		return true
	}
	return false
}

// updateSessionList is the root-view router while a.view == ViewDashboard:
// it forwards to whichever overlay owns focus (launch terminal, config form,
// plan editor, review panel, PR panel) and otherwise dispatches session-list
// navigation and workflow keys.
func (a App) updateSessionList(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case configFormSaveMsg:
		return a.handleConfigFormSave()

	case configFormCancelMsg:
		return a.returnFromConfigForm()

	case configFormActionMsg:
		return a.handleConfigFormAction(msg)

	case repoChecksSaveMsg, repoChecksCancelMsg:
		return a.updateRepoChecks(msg)

	case tea.PasteMsg:
		return a.handleDashboardPaste(msg)

	case tea.KeyPressMsg:
		// PR compose modal consumes all keys while open.
		if a.prComposeModal.Active() {
			var cmd tea.Cmd
			a.prComposeModal, cmd = a.prComposeModal.Update(msg)
			return a, cmd
		}
		// Fullscreen agent terminal owns the keyboard while it's up.
		if a.modals.Is(focusLaunch) {
			return a.handleLaunchKeys(msg)
		}
		// Repo config form / validation-checks sub-editor own their keys.
		if form := a.modals.Config(); form != nil {
			a.confirmQuit = false
			var cmd tea.Cmd
			*form, cmd = form.Update(msg)
			return a, cmd
		}
		if editor := a.modals.RepoChecks(); editor != nil {
			a.confirmQuit = false
			var cmd tea.Cmd
			*editor, cmd = editor.Update(msg)
			return a, cmd
		}
		// Overlay panels: each helper is a no-op when its panel is inactive.
		if newA, cmd, handled := a.handleKeysPlanEditor(msg); handled {
			return newA, cmd
		}
		if newA, cmd, handled := a.handleKeysReviewPanel(msg); handled {
			return newA, cmd
		}
		if newA, cmd, handled := a.handleKeysShippingPanel(msg); handled {
			return newA, cmd
		}

		// List navigation.
		switch msg.String() {
		case "up", "k":
			a.confirmQuit = false
			a.sessionList.moveCursor(-1, buildSessionListLayout(a.listItems()))
			return a, nil
		case "down", "j":
			a.confirmQuit = false
			a.sessionList.moveCursor(1, buildSessionListLayout(a.listItems()))
			return a, nil
		case "space", "enter":
			a.confirmQuit = false
			a.activateSelectedRow()
			return a, nil
		}

		newA, cmd, handled := a.handleQuitKey(msg)
		if handled {
			return newA, cmd
		}
		a = newA // handleQuitKey clears confirmQuit on any non-quit key

		if newA, cmd, handled := a.handleSessionKeys(msg); handled {
			return newA, cmd
		}
		return a, nil
	}

	if msg, ok := msg.(tea.MouseClickMsg); ok {
		return a.handleListMouseClick(msg)
	}
	if msg, ok := msg.(tea.MouseMotionMsg); ok {
		if a.modals.Is(focusLaunch) {
			return a.handleLaunchMouseMotion(msg)
		}
		return a, nil
	}
	if _, ok := msg.(tea.MouseReleaseMsg); ok {
		if a.modals.Is(focusLaunch) {
			return a.handleLaunchMouseRelease()
		}
		return a, nil
	}
	if msg, ok := msg.(tea.MouseWheelMsg); ok {
		if a.modals.Is(focusLaunch) {
			return a.handleLaunchMouseWheel(msg)
		}
		return a, nil
	}

	return a, nil
}

// handleSessionKeys dispatches the workflow keys that act on the session
// under the list cursor (or on the active repo when the list is empty).
// Returns handled=false when the key matched no binding.
func (a App) handleSessionKeys(msg tea.KeyPressMsg) (App, tea.Cmd, bool) {
	switch msg.String() {
	case "n":
		// New session in the cursor-selected session's repo. With multiple
		// repos and no selection context the picker chooses the target.
		repoPath := a.activeRepo
		if row, ok := a.selectedSessionRow(); ok {
			repoPath = row.repoPath
		}
		if a.cfg != nil && len(a.cfg.Repos) > 1 {
			counts := make(map[string]int, len(a.cfg.Repos))
			for _, repo := range a.cfg.Repos {
				if mgr := a.managers[repo.Path]; mgr != nil {
					counts[repo.Path] = mgr.ActiveSessionCount()
				}
			}
			a.repoPicker = newRepoPickerModel()
			a.repoPicker.width = a.width
			a.repoPicker.height = a.height - statusBarHeight
			a.repoPicker.SetMode(repoPickerModeSession)
			a.repoPicker.setRepos(a.cfg.Repos, counts, a.activeRepo)
			a.view = ViewRepoPicker
			return a, nil, true
		}
		if repoPath == "" {
			return a, nil, true
		}
		a.activeRepo = repoPath
		if a.managers[repoPath] == nil {
			return a, nil, true
		}
		return a, a.openNewSession(ViewDashboard), true

	case "c":
		// Add an agent to the cursor-selected session.
		row, ok := a.selectedSessionRow()
		if !ok {
			a.setError("No session selected")
			return a, nil, true
		}
		mgr := a.managers[row.repoPath]
		if mgr == nil {
			return a, nil, true
		}
		if a.agentTermCols() <= 0 || a.agentTermRows() <= 0 {
			a.setError("Terminal size not yet known; try again")
			return a, nil, true
		}
		resolved := a.resolvedCache[row.repoPath]
		cfg := agent.Config{
			Rows:              a.agentTermRows(),
			Cols:              a.agentTermCols(),
			BypassPermissions: resolved.BypassPermissions,
			AgentProgram:      resolved.AgentProgram,
			AgentModel:        resolved.AgentModel,
			BuildSystemPrompt: resolved.BuildSystemPrompt,
		}
		sessionID := row.session.ID
		return a, func() tea.Msg {
			ag, err := mgr.AddAgent(sessionID, cfg)
			if err != nil {
				return createResultMsg{err: err}
			}
			return createResultMsg{sessionID: sessionID, agentID: ag.ID}
		}, true

	case "t":
		// Open or focus a shell terminal in the cursor-selected session.
		row, ok := a.selectedSessionRow()
		if !ok {
			a.setError("No session selected")
			return a, nil, true
		}
		sess := row.session
		if sess.HasShell() {
			for _, ag := range sess.Agents() {
				if ag.IsShell {
					a.openLaunchPanel(sess, ag, row.repoPath)
					a.launch.scrollOffset = 0
					ag.Resize(a.agentTermRows(), a.agentTermCols())
					break
				}
			}
			return a, nil, true
		}
		mgr := a.managers[row.repoPath]
		if mgr == nil {
			return a, nil, true
		}
		if a.agentTermCols() <= 0 || a.agentTermRows() <= 0 {
			a.setError("Terminal size not yet known; try again")
			return a, nil, true
		}
		cfg := agent.Config{Rows: a.agentTermRows(), Cols: a.agentTermCols()}
		sessionID := sess.ID
		return a, func() tea.Msg {
			ag, err := mgr.AddShell(sessionID, cfg)
			if err != nil {
				return createResultMsg{err: err}
			}
			return createResultMsg{sessionID: sessionID, agentID: ag.ID}
		}, true

	case "e":
		// Open the cursor-selected session's working directory in the IDE.
		row, ok := a.selectedSessionRow()
		if !ok {
			a.setError("No session selected")
			return a, nil, true
		}
		ideCmd := strings.TrimSpace(a.resolvedCache[row.repoPath].IDECommand)
		parts := splitIDECommand(ideCmd)
		if len(parts) == 0 {
			a.setError("No IDE configured (set 'IDE Command' in settings)")
			return a, nil, true
		}
		worktreePath := row.session.Worktree.Path
		return a, openIDECmd(parts[0], append(parts[1:], worktreePath), worktreePath), true

	case "o":
		// Branch picker: session on an existing branch/PR in the active repo.
		repoPath := a.activeRepo
		if row, ok := a.selectedSessionRow(); ok {
			repoPath = row.repoPath
		}
		if repoPath == "" {
			a.setError("No repo available")
			return a, nil, true
		}
		mgr := a.managers[repoPath]
		activeBranches := make(map[string]bool)
		if mgr != nil {
			for _, sess := range mgr.ListSessions() {
				activeBranches[sess.Branch()] = true
			}
		}
		a.branchPicker = newBranchPickerModel()
		a.branchPicker.width = a.width
		a.branchPicker.height = a.height - statusBarHeight
		a.activeRepo = repoPath
		a.view = ViewBranchPicker
		return a, loadBranchPickerData(repoPath, a.ghClient, activeBranches), true

	case "a":
		// File browser: add a repo.
		a.repoBrowser = newFileBrowserModel()
		a.repoBrowser.width = a.width
		a.repoBrowser.height = a.height - statusBarHeight
		a.view = ViewFileBrowser
		return a, nil, true

	case "R":
		// Repo picker in manage mode.
		counts := make(map[string]int, len(a.cfg.Repos))
		for _, repo := range a.cfg.Repos {
			if mgr := a.managers[repo.Path]; mgr != nil {
				counts[repo.Path] = mgr.ActiveSessionCount()
			}
		}
		a.repoPicker = newRepoPickerModel()
		a.repoPicker.width = a.width
		a.repoPicker.height = a.height - statusBarHeight
		a.repoPicker.SetMode(repoPickerModeManage)
		a.repoPicker.setRepos(a.cfg.Repos, counts, a.activeRepo)
		a.view = ViewRepoPicker
		return a, nil, true

	case "N":
		// Cycle the active repo.
		if a.cfg != nil && len(a.cfg.Repos) > 0 {
			currentIdx := -1
			for i, repo := range a.cfg.Repos {
				if repo.Path == a.activeRepo {
					currentIdx = i
					break
				}
			}
			a.activeRepo = a.cfg.Repos[(currentIdx+1)%len(a.cfg.Repos)].Path
		}
		return a, nil, true

	case "s":
		// Global settings overlay.
		a.globalConfig = newGlobalConfigModel(a.globalSettings, a.width, a.height)
		a.view = ViewGlobalConfig
		return a, nil, true

	case "d":
		// Diff the cursor-selected session's working tree.
		row, ok := a.selectedSessionRow()
		if !ok {
			return a, nil, true
		}
		rawDiff, err := git.Diff(row.repoPath, row.session.Worktree)
		if err != nil {
			a.setError(err.Error())
			return a, nil, true
		}
		m, err := diffmodel.Parse(rawDiff)
		if err != nil {
			a.setError(err.Error())
			return a, nil, true
		}
		a.view = ViewDiff
		a.diff = newDiffModel(row.session.GetDisplayName(), m, a.width, a.height-statusBarHeight)
		return a, nil, true

	case "r":
		// Review panel for the cursor-selected session. The flat list has no
		// review queue: any session's work can be reviewed from its row.
		row, ok := a.selectedSessionRow()
		if !ok {
			a.setError("No session selected")
			return a, nil, true
		}
		sess := row.session
		if sess.LifecyclePhase() == agent.LifecycleReadyForReview {
			sess.SetLifecyclePhase(agent.LifecycleInReview)
		}
		a.openReviewPanel(sess, row.repoPath)
		if _, cached := a.reviewDiffCache[cacheKey(row.repoPath, sess.ID)]; !cached {
			return a, tea.Batch(
				a.fetchReviewDiffCmd(sess, row.repoPath),
				a.startValidationChecksCmd(sess, row.repoPath),
			), true
		}
		if a.validationRuns[cacheKey(row.repoPath, sess.ID)] == nil {
			return a, a.startValidationChecksCmd(sess, row.repoPath), true
		}
		return a, nil, true

	case "p":
		// PR: open the PR panel when the poller knows a PR; otherwise push the
		// branch and draft one (once the session's agents have gone quiet).
		row, ok := a.selectedSessionRow()
		if !ok {
			return a, nil, true
		}
		sess := row.session
		if entry := a.prCache[cacheKey(row.repoPath, sess.ID)]; entry != nil && entry.pr != nil {
			a.openShippingPanel(sess, row.repoPath)
			return a, nil, true
		}
		if a.ghClient == nil {
			a.setError("GitHub auth not available")
			return a, nil, true
		}
		if sessionBusy(sess) {
			a.setError("agents are still running — wait for the session to go idle")
			return a, nil, true
		}
		if a.prDraftInFlight {
			return a, nil, true
		}
		a.prDraftInFlight = true
		a.prDraftSessionID = sess.ID
		a.prDraftRepoPath = row.repoPath
		return a, a.startPRDraftCmd(sess, row.repoPath, false), true

	case "x":
		// Kill the cursor-selected session's primary agent.
		row, ok := a.selectedSessionRow()
		if !ok {
			a.setError("No session selected")
			return a, nil, true
		}
		ag := row.session.PrimaryAgent()
		if ag == nil {
			a.setError("Session has no agents")
			return a, nil, true
		}
		mgr := a.managers[row.repoPath]
		if mgr == nil {
			return a, nil, true
		}
		repoPath := row.repoPath
		agentID := ag.ID
		sessionID := row.session.ID
		agentKey := agentCacheKey(repoPath, agentID)
		if a.closingAgents[agentKey] {
			return a, nil, true
		}
		a.closingAgents[agentKey] = true
		return a, func() tea.Msg {
			err := mgr.KillAgent(sessionID, agentID)
			return killResultMsg{
				scope:     killScopeAgent,
				repoPath:  repoPath,
				sessionID: sessionID,
				agentID:   agentID,
				err:       err,
			}
		}, true

	case "X":
		// Kill the cursor-selected session entirely.
		row, ok := a.selectedSessionRow()
		if !ok {
			return a, nil, true
		}
		mgr := a.managers[row.repoPath]
		if mgr == nil {
			return a, nil, true
		}
		repoPath := row.repoPath
		sessID := row.session.ID
		sessKey := cacheKey(repoPath, sessID)
		if a.closingSessions[sessKey] {
			return a, nil, true
		}
		var agentIDs []string
		for _, ag := range row.session.Agents() {
			agentIDs = append(agentIDs, ag.ID)
			a.closingAgents[agentCacheKey(repoPath, ag.ID)] = true
		}
		a.closingSessions[sessKey] = true
		return a, func() tea.Msg {
			err := mgr.KillSession(sessID)
			return killResultMsg{
				scope:     killScopeSession,
				repoPath:  repoPath,
				sessionID: sessID,
				agentIDs:  agentIDs,
				err:       err,
			}
		}, true
	}
	return a, nil, false
}

// sessionBusy reports whether any non-shell agent in sess is still doing (or
// blocked on) work. Used to gate the PR draft: pushing mid-turn would snapshot
// half-finished changes. A session with no agents is not busy — plan-only and
// resumed sessions can still open a PR.
func sessionBusy(sess *agent.Session) bool {
	for _, ag := range sess.Agents() {
		if ag.IsShell {
			continue
		}
		switch ag.Status() {
		case agent.StatusActive, agent.StatusWaiting, agent.StatusStarting:
			return true
		}
	}
	return false
}

// handleListMouseClick routes a left-click on the root view: the launch view
// delegates to its own handlers, review-panel clicks go to the panel, and
// otherwise the click lands on the session list — cursor move, double-click
// activation, or a PR-indicator hit that opens the PR in the browser.
func (a App) handleListMouseClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	topY := a.dashboardTopY()
	a.confirmQuit = false
	if msg.Button != tea.MouseLeft {
		return a, nil
	}

	if a.modals.Is(focusLaunch) {
		return a.handleLaunchMouseClick(msg)
	}

	if rp := a.modals.Review(); rp != nil {
		rp.SetDashboardTopY(topY)
		rp.handleClick(msg)
		return a, nil
	}
	if !a.modals.IsList() {
		return a, nil
	}

	layout := buildSessionListLayout(a.listItems())
	idx, firstLine, hit := a.sessionList.rowAt(layout, msg.Y-topY)
	if !hit {
		return a, nil
	}
	row := layout.rows[idx]

	// PR-indicator click on the card's first line opens the PR.
	if firstLine {
		if entry := a.prCache[cacheKey(row.repoPath, row.session.ID)]; entry != nil && entry.pr != nil && entry.pr.URL != "" {
			indicatorWidth := prIndicatorWidth(entry)
			if indicatorWidth > 0 && msg.X >= a.width-indicatorWidth-2 {
				if err := openURL(entry.pr.URL); err != nil {
					a.setError(err.Error())
				}
				// This click activated the PR, not the row — reset the
				// double-click bookkeeping so a fast follow-up click doesn't
				// read a stale timestamp and phantom-activate the row.
				a.lastListClick = time.Time{}
				return a, nil
			}
		}
	}

	now := time.Now()
	isDoubleClick := !a.lastListClick.IsZero() &&
		now.Sub(a.lastListClick) < PipelineDoubleClickWindow &&
		a.lastListClickIdx == idx
	a.lastListClick = now
	a.lastListClickIdx = idx

	a.sessionList.cursor = idx
	a.sessionList.ensureCursorVisible(layout)

	if isDoubleClick {
		a.activateSelectedRow()
	}
	return a, nil
}
