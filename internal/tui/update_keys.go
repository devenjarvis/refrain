package tui

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	xvt "github.com/charmbracelet/x/vt"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/config"
	"github.com/devenjarvis/refrain/internal/diffmodel"
	"github.com/devenjarvis/refrain/internal/git"
	"github.com/devenjarvis/refrain/internal/state"
	"github.com/devenjarvis/refrain/internal/vt"
)

func (a App) updateDashboard(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case configFormSaveMsg:
		// Repo config form saved.
		if form := a.modals.Config(); form != nil && a.modals.ConfigRepoPath() != "" {
			repoPath := a.modals.ConfigRepoPath()
			alias := strings.TrimSpace(form.textValue("Alias"))
			settings := a.extractRepoSettings()
			if err := config.SaveRepoSettings(repoPath, settings); err != nil {
				a.setError(err.Error())
			} else {
				a.repoSettings[repoPath] = settings
				a.resolvedCache[repoPath] = config.Resolve(a.globalSettings, settings)
				if mgr := a.managers[repoPath]; mgr != nil {
					mgr.UpdateSettings(a.resolvedCache[repoPath])
				}
			}
			for i, r := range a.cfg.Repos {
				if r.Path == repoPath && r.Alias != alias {
					a.cfg.Repos[i].Alias = alias
					if err := config.Save(a.cfg); err != nil {
						a.setError(err.Error())
					}
					a.refreshAgentList()
					break
				}
			}
		}
		return a.returnFromConfigForm()

	case configFormCancelMsg:
		// Repo config form cancelled.
		return a.returnFromConfigForm()

	case tea.PasteMsg:
		// Prompt modal consumes paste while open (same precedence as the
		// KeyPressMsg branch below) so cmd+v / bracketed paste inserts into
		// the textarea instead of being swallowed by the focusLaunch path.
		if a.promptModal.Active() {
			cmd := a.promptModal.Update(msg)
			return a, cmd
		}
		if a.prComposeModal.Active() {
			cmd := a.prComposeModal.Update(msg)
			return a, cmd
		}
		if ag := a.modals.LaunchAgent(); ag != nil {
			ag.Paste(msg.Content)
			return a, nil
		}

	case tea.KeyPressMsg:
		// Prompt modal consumes all keys while open (it's a modal overlay).
		// Submit/cancel emit dedicated messages handled below.
		if a.promptModal.Active() {
			cmd := a.promptModal.Update(msg)
			return a, cmd
		}
		// PR compose modal consumes all keys while open.
		if a.prComposeModal.Active() {
			cmd := a.prComposeModal.Update(msg)
			return a, cmd
		}
		// focusLaunch: forward all keys to the launch agent; esc/ctrl+e returns to focus pipeline.
		if a.modals.Is(focusLaunch) {
			return a.updateFocusLaunchKeys(msg)
		}

		// When the config panel has focus, skip all app-level bindings.
		if a.modals.Is(focusConfig) {
			a.confirmQuit = false
			break
		}

		// Agent-limit modal: any key other than 'n' dismisses without navigating.
		// 'n' falls through to the normal handler, which sees the flag still set
		// and proceeds with spawn (the existing two-press guard logic below).
		if a.agentLimitModalActive && msg.String() != "n" {
			a.agentLimitModalActive = false
			return a, nil
		}

		// Clear the backlog warning flag on any key that isn't n.
		// Done here (before focus-mode early returns) so navigation keys clear it too.
		if a.wellness.focusBacklogWarning && msg.String() != "n" {
			a.wellness.focusBacklogWarning = false
		}

		// Clear the break short-warning on any key that isn't b.
		if a.wellness.focusBreakShortWarning && msg.String() != "b" {
			a.wellness.focusBreakShortWarning = false
		}

		// Forward to whichever overlay panel owns focus. Each helper is a
		// no-op when its panel is inactive, so the dispatch order only
		// matters when multiple panels could theoretically be active —
		// which is forbidden by the panelFocus state machine.
		if newA, cmd, handled := a.handleKeysPlanEditor(msg); handled {
			return newA, cmd
		}
		if newA, cmd, handled := a.handleKeysReviewPanel(msg); handled {
			return newA, cmd
		}
		if newA, cmd, handled := a.handleKeysShippingPanel(msg); handled {
			return newA, cmd
		}

		// Pipeline view key handling (the only dashboard mode).
		if !a.modals.Is(focusReview) && !a.modals.Is(focusShipping) {
			switch msg.String() {
			case "up", "k":
				a.cursor.MoveUp(a.dashboard.sectionCounts())
				a.syncFocusCursorToDashboard()
				return a, nil
			case "down", "j":
				a.cursor.MoveDown(a.dashboard.sectionCounts())
				a.syncFocusCursorToDashboard()
				return a, nil
			case "space", "enter":
				if cmd, ok := a.activateFocusCursor(); ok {
					return a, cmd
				}
				// Cursor section had no actionable row: fall through to normal enter handling.
			case "N":
				if a.cfg != nil && len(a.cfg.Repos) > 0 {
					currentIdx := -1
					for i, repo := range a.cfg.Repos {
						if repo.Path == a.activeRepo {
							currentIdx = i
							break
						}
					}
					nextIdx := (currentIdx + 1) % len(a.cfg.Repos)
					a.activeRepo = a.cfg.Repos[nextIdx].Path
					a.refreshAgentList()
				}
				return a, nil
			case "b":
				// Context-sensitive: when the cursor is on a Planning row,
				// 'b' advances it to Building. Otherwise we leave the case
				// without returning so the global "take a break" handler in
				// the switch below catches the press. Picking a Planning row
				// to advance is a deliberate action, while taking a break is
				// the catch-all everywhere else — so the cursor location is
				// the disambiguator the user already has at hand.
				if !a.wellness.focusBreakMode && a.cursor.Section() == focusSectionPlanning {
					planning := a.dashboard.planningSessions()
					if len(planning) > 0 {
						idx := a.cursor.Index(focusSectionPlanning)
						if idx >= len(planning) {
							idx = len(planning) - 1
						}
						if sess := planning[idx].session; sess != nil {
							sess.SetLifecyclePhase(agent.LifecycleInProgress)
							a.cursor.Clamp(a.dashboard.sectionCounts())
							a.syncFocusCursorToDashboard()
						}
					}
					// Cursor is on Planning — even if the section was empty in
					// a fast-tick race window, swallow the press here so we
					// don't accidentally trigger the wellness break.
					return a, nil
				}
				// Fall through to the global break handler below.
			case "m":
				// Mark the cursor-selected Planning or Building session as
				// ReadyForReview. We accept Planning too so the natural flow
				// works when Claude finishes the work in one shot — the
				// idle-reviewable cue ("press m to review") is rendered for
				// any reviewable session regardless of phase, and pressing m
				// shouldn't surprise the user with an error in that case.
				var sess *agent.Session
				switch a.cursor.Section() {
				case focusSectionPlanning:
					planning := a.dashboard.planningSessions()
					if pi := a.cursor.Index(focusSectionPlanning); pi < len(planning) {
						sess = planning[pi].session
					}
				case focusSectionBuilding:
					building := a.dashboard.buildingSessions()
					if bi := a.cursor.Index(focusSectionBuilding); bi < len(building) {
						sess = building[bi].session
					}
				default:
					a.setError("nothing to mark — cursor isn't on a Planning or Building session")
					return a, nil
				}
				if sess == nil {
					a.setError("no session under cursor")
					return a, nil
				}
				if !sess.IsReviewable() {
					switch sess.Status() {
					case agent.StatusActive:
						a.setError("session is still running — wait for Claude to finish its turn")
					case agent.StatusWaiting:
						a.setError("session is waiting for input — resolve the prompt first")
					default:
						a.setError("session is still running — wait for Claude to finish its turn")
					}
					return a, nil
				}
				sess.SetLifecyclePhase(agent.LifecycleReadyForReview)
				a.cursor.SetIndex(focusSectionReview, 0)
				return a, a.fetchReviewDiffCmd(sess)
			case "r":
				reviewItems := a.dashboard.reviewQueueSessions()
				if len(reviewItems) == 0 {
					a.setError("review queue is empty — press m on a finished session first")
					return a, nil
				}
				idx := a.cursor.Index(focusSectionReview)
				if idx >= len(reviewItems) {
					idx = len(reviewItems) - 1
				}
				sess := reviewItems[idx].session
				sess.SetLifecyclePhase(agent.LifecycleInReview)
				a.openReview(newReviewPanel(sess, a.width, a.height))
				if _, ok := a.reviewDiffCache[sess.ID]; !ok {
					return a, a.fetchReviewDiffCmd(sess)
				}
				a.modals.Review().RefreshDiffViewport(a.panelServices())
				return a, nil
			case "n":
				if a.cfg != nil && len(a.cfg.Repos) > 1 {
					// Apply the same soft agent-count guard as the single-repo
					// path before opening the picker.
					resolved := a.resolvedCache[a.activeRepo]
					if resolved.MaxConcurrentAgents > 0 && a.activeAgentCount() >= resolved.MaxConcurrentAgents {
						if !a.agentLimitModalActive {
							a.agentLimitModalActive = true
							return a, nil
						}
						a.agentLimitModalActive = false
					}
					counts := make(map[string]int, len(a.cfg.Repos))
					for _, repo := range a.cfg.Repos {
						if mgr := a.managers[repo.Path]; mgr != nil {
							counts[repo.Path] = mgr.AgentCount()
						}
					}
					a.repoPicker = newRepoPickerModel()
					a.repoPicker.width = a.width
					a.repoPicker.height = a.height - 1
					a.repoPicker.SetMode(repoPickerModeSession)
					a.repoPicker.setRepos(a.cfg.Repos, counts, a.activeRepo)
					a.view = ViewRepoPicker
					return a, nil
				}
				// Single repo: exits this case without returning, so control
				// falls through to the general "n" handler below, which contains
				// the agent-count and backlog soft-limit checks.
			}
		}

		// Enter/right on a repo header: open repo config in right panel.
		if (msg.String() == "enter" || msg.String() == "right") && a.modals.IsList() {
			item := a.dashboard.selectedItem()
			if item != nil && item.kind == listItemRepo {
				a.initRepoConfigForm(item.repoPath)
				return a, nil
			}
		}

		switch msg.String() {
		case "q", "ctrl+c":
			// Detach path: save state and exit, preserving worktrees.
			hasRunning := false
			for _, mgr := range a.managers {
				if mgr.AgentCount() > 0 {
					hasRunning = true
					break
				}
			}
			if hasRunning && !a.confirmQuit {
				a.confirmQuit = true
				return a, nil
			}
			// Detach all managers and save state.
			for repoPath, mgr := range a.managers {
				bs := mgr.Detach()
				if bs != nil {
					_ = state.Save(repoPath, bs)
				} else {
					_ = state.Remove(repoPath)
				}
			}
			if a.audioPlayer != nil {
				a.audioPlayer.Close()
			}
			a.writeWellnessLog()
			return a, tea.Quit
		default:
			a.confirmQuit = false
		}

		if newA, cmd, handled := a.handleKeysWorkflow(msg); handled {
			return newA, cmd
		}
	}

	if msg, ok := msg.(tea.MouseClickMsg); ok {
		// Compute offset before clearing confirmQuit — reflects what was on screen.
		dashboardTopY := 0
		if a.err != "" {
			dashboardTopY++
		}
		if a.confirmQuit {
			dashboardTopY++
		}
		a.confirmQuit = false
		if msg.Button != tea.MouseLeft {
			return a, nil
		}

		// focusLaunch: tab bar click switches the active agent; clicks inside
		// the agent terminal seed a text selection.
		if sess := a.modals.LaunchSession(); sess != nil {
			tabBarY := dashboardTopY + 1
			if msg.Y == tabBarY {
				if idx := a.focusLaunchTabIndexAt(msg.X); idx >= 0 {
					agents := sess.Agents()
					a.modals.SetLaunchAgent(agents[idx])
					a.syncModalsToDashboard()
					a.dashboard.scrollOffset = 0
					agents[idx].Resize(a.focusLaunchTermHeight(), a.dashboard.width)
				}
				return a, nil
			}
			if ag := a.modals.LaunchAgent(); ag != nil {
				if termX, termY, inVP := a.screenToTermCellFocusLaunch(msg.X, msg.Y); inVP {
					a.dashboard.selection = selection{
						anchorX: termX,
						anchorY: termY,
						cursorX: termX,
						cursorY: termY,
						active:  true,
						agentID: ag.ID,
					}
				} else {
					a.dashboard.clearSelection()
				}
			}
			return a, nil
		}

		// focusReview: delegate left-pane row clicks to the panel.
		if rp := a.modals.Review(); rp != nil {
			before := rp.TaskCursor()
			rp.handleClick(msg, a.panelServices())
			if rp.TaskCursor() != before {
				return a, nil
			}
		}

		// Pipeline view: click on a session card moves the cursor; double-click
		// activates (focusLaunch for active sessions, review panel for queue).
		// PR-indicator click on a queue row opens the PR in the browser.
		section, idx, hit := a.pipelineHitTest(msg.Y - dashboardTopY)
		if !hit {
			return a, nil
		}
		// Detect a PR-indicator click on review/shipping rows: the prIndicator
		// is right-aligned on the card; the X-column check below narrows the
		// hit region without needing per-row Y granularity from pipelineHitTest.
		if section == focusSectionReview || section == focusSectionShipping {
			items := a.dashboard.sectionItems(section)
			if idx < len(items) {
				sess := items[idx].session
				if entry := a.prCache[sess.ID]; entry != nil && entry.pr != nil && entry.pr.URL != "" {
					indicatorWidth := prIndicatorWidth(entry)
					if indicatorWidth > 0 && msg.X >= a.width-indicatorWidth-2 {
						if err := openURL(entry.pr.URL); err != nil {
							a.setError(err.Error())
						}
						// Reset double-click bookkeeping: this click was a PR
						// activation, not a card selection. Without this, a
						// quick follow-up card click on the same section/idx
						// could read a stale lastPipelineClick and fire a
						// phantom double-click into the review panel.
						a.lastPipelineClick = time.Time{}
						return a, nil
					}
				}
			}
		}
		// Move the cursor to the clicked session.
		now := time.Now()
		isDoubleClick := !a.lastPipelineClick.IsZero() &&
			now.Sub(a.lastPipelineClick) < 500*time.Millisecond &&
			a.lastPipelineClickSec == section &&
			a.lastPipelineClickIdx == idx
		a.lastPipelineClick = now
		a.lastPipelineClickSec = section
		a.lastPipelineClickIdx = idx

		a.cursor.JumpTo(section, idx)
		a.syncFocusCursorToDashboard()

		if isDoubleClick {
			if cmd, ok := a.activateFocusCursor(); ok {
				return a, cmd
			}
		}
		return a, nil
	}

	if msg, ok := msg.(tea.MouseMotionMsg); ok {
		// Drag updates the cursor end of an in-flight selection. Selections
		// only seed in focusLaunch (see MouseClickMsg), so any motion outside
		// focusLaunch can be ignored here.
		if a.dashboard.selection.active && msg.Button == tea.MouseLeft &&
			a.modals.LaunchAgent() != nil {
			tx, ty, inVP := a.screenToTermCellFocusLaunch(msg.X, msg.Y)
			if inVP {
				a.dashboard.selection.cursorX = tx
				a.dashboard.selection.cursorY = ty
				a.dashboard.selection.dragSeen = true
			}
		}
		return a, nil
	}

	if _, ok := msg.(tea.MouseReleaseMsg); ok {
		if a.dashboard.selection.active {
			if a.dashboard.selection.dragSeen {
				// Real drag — copy the highlighted region. Selections only seed
				// in focusLaunch (see MouseClickMsg), so this is the only path.
				if ag := a.modals.LaunchAgent(); ag != nil && ag.ID == a.dashboard.selection.agentID {
					if sx, sy, ex, ey, ok := a.dashboard.selectionRect(); ok {
						rect := vt.SelectionRect{
							StartX: sx, StartY: sy, EndX: ex, EndY: ey, Active: true,
						}
						var text string
						if a.dashboard.scrollOffset > 0 {
							vpWidth := a.dashboard.width
							vpHeight := a.focusLaunchTermHeight()
							text = ag.ExtractTextFromSnapshot(vpWidth, vpHeight, a.dashboard.scrollOffset, rect)
						} else {
							text = ag.ExtractText(rect)
						}
						if text != "" {
							return a, tea.SetClipboard(text)
						}
					}
				}
			} else {
				// Plain click — drop the seeded selection. Focus already moved
				// in the click handler.
				a.dashboard.clearSelection()
			}
		}
		return a, nil
	}

	if msg, ok := msg.(tea.MouseWheelMsg); ok {
		if ag := a.modals.LaunchAgent(); ag != nil {
			if ag.IsAltScreen() {
				termX, termY, _ := a.screenToTermCellFocusLaunch(msg.X, msg.Y)
				if termX < 0 {
					termX = 0
				}
				if termX >= a.dashboard.width {
					termX = a.dashboard.width - 1
				}
				if termY < 0 {
					termY = 0
				}
				if termY >= a.focusLaunchTermHeight() {
					termY = a.focusLaunchTermHeight() - 1
				}
				ag.SendMouse(xvt.MouseWheel{
					X:      termX,
					Y:      termY,
					Button: xvt.MouseButton(msg.Button),
					Mod:    xvt.KeyMod(msg.Mod),
				})
				return a, nil
			}
			if msg.Button == tea.MouseWheelUp {
				sbLines := len(ag.ScrollbackLines())
				max := sbLines
				a.dashboard.scrollOffset += 3
				if a.dashboard.scrollOffset > max {
					a.dashboard.scrollOffset = max
				}
			} else {
				a.dashboard.scrollOffset -= 3
				if a.dashboard.scrollOffset < 0 {
					a.dashboard.scrollOffset = 0
				}
			}
		}
		return a, nil
	}

	prevSelected := a.dashboard.selected
	var cmd tea.Cmd
	a.dashboard, cmd = a.dashboard.Update(msg)
	// On selection change, update diff stats from cache (or trigger refresh).
	if a.dashboard.selected != prevSelected {
		a.updateDashboardDiffStats()
		if sess := a.dashboard.selectedSession(); sess != nil {
			entry := a.diffStatsCache[sess.ID]
			if (entry == nil || time.Since(entry.lastRefresh) > 5*time.Second) && !a.diffRefreshInFlight {
				diffCmd := a.refreshDiffStatsCmd()
				return a, tea.Batch(cmd, diffCmd)
			}
		}
	}
	// Maintain the invariant: a text selection only persists while the user
	// remains in focusLaunch viewing the same agent. Any focus or agent
	// change (esc back to pipeline, tab switch, etc.) drops it.
	if a.dashboard.selection.active {
		ag := a.modals.LaunchAgent()
		if ag == nil || ag.ID != a.dashboard.selection.agentID {
			a.dashboard.clearSelection()
		}
	}
	return a, cmd
}

// updateFocusLaunchKeys handles all keypresses while panelFocus == focusLaunch.
// The fullscreen agent terminal owns the keyboard while it's up: most keys are
// forwarded to the underlying PTY, and a small set of escape hatches (esc /
// ctrl+e to return, alt+[ / alt+] to cycle agents, ctrl+t / ctrl+n to add a
// shell / agent, ctrl+w to close the current agent) are handled here. Lives
// off updateDashboard so the latter reads as a router, not a god-method.
func (a App) updateFocusLaunchKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	a.confirmQuit = false
	ag := a.modals.LaunchAgent()
	sess := a.modals.LaunchSession()
	if ag == nil {
		a.closeModal()
		a.dashboard.scrollOffset = 0
		return a, nil
	}
	switch msg.String() {
	case "esc", "ctrl+e":
		a.resizeAgentForDashboard(ag)
		a.closeModal()
		a.dashboard.scrollOffset = 0
	case "shift+esc":
		ag.SendKey(xvt.KeyPressEvent{Code: tea.KeyEscape})
	case "pgup":
		sbLines := len(ag.ScrollbackLines())
		maxScroll := sbLines
		a.dashboard.scrollOffset += a.dashboard.height / 2
		if a.dashboard.scrollOffset > maxScroll {
			a.dashboard.scrollOffset = maxScroll
		}
	case "pgdn":
		a.dashboard.scrollOffset -= a.dashboard.height / 2
		if a.dashboard.scrollOffset < 0 {
			a.dashboard.scrollOffset = 0
		}
	case "home":
		a.dashboard.scrollOffset = 0
	case "alt+]", "alt+[":
		if sess != nil {
			agents := sess.Agents()
			idx := 0
			for i, candidate := range agents {
				if candidate.ID == ag.ID {
					idx = i
					break
				}
			}
			if msg.String() == "alt+]" {
				idx = (idx + 1) % len(agents)
			} else {
				idx = (idx - 1 + len(agents)) % len(agents)
			}
			a.modals.SetLaunchAgent(agents[idx])
			a.syncModalsToDashboard()
			a.dashboard.scrollOffset = 0
			agents[idx].Resize(a.focusLaunchTermHeight(), a.dashboard.width)
		}
	case "ctrl+t":
		if sess != nil {
			repoPath := a.repoPathForSession(sess.ID)
			mgr := a.managers[repoPath]
			if mgr != nil {
				resolved := a.resolvedCache[repoPath]
				cfg := agent.Config{
					Rows:              a.focusLaunchTermHeight(),
					Cols:              a.dashboard.width,
					BypassPermissions: resolved.BypassPermissions,
				}
				if newAg, err := mgr.AddShell(sess.ID, cfg); err == nil {
					a.modals.SetLaunchAgent(newAg)
					a.syncModalsToDashboard()
					a.dashboard.scrollOffset = 0
				} else {
					a.setError(err.Error())
				}
			}
		}
	case "ctrl+n":
		if sess != nil {
			repoPath := a.repoPathForSession(sess.ID)
			mgr := a.managers[repoPath]
			if mgr != nil {
				resolved := a.resolvedCache[repoPath]
				cfg := agent.Config{
					Rows:              a.focusLaunchTermHeight(),
					Cols:              a.dashboard.width,
					BypassPermissions: resolved.BypassPermissions,
					AgentProgram:      resolved.AgentProgram,
					AgentModel:        resolved.AgentModel,
					BuildSystemPrompt: resolved.BuildSystemPrompt,
				}
				if newAg, err := mgr.AddAgent(sess.ID, cfg); err == nil {
					a.modals.SetLaunchAgent(newAg)
					a.syncModalsToDashboard()
					a.dashboard.scrollOffset = 0
				} else {
					a.setError(err.Error())
				}
			}
		}
	case "ctrl+w":
		return a.closeFocusLaunchAgent()
	default:
		if msg.Text != "" {
			ag.SendText(msg.Text)
		} else {
			ag.SendKey(xvt.KeyPressEvent(msg))
		}
	}
	return a, nil
}

// closeFocusLaunchAgent kills the currently-focused agent inside the fullscreen
// launch view. If it's the last agent in its session, the view collapses back
// to the pipeline; otherwise focus moves to the neighbor and the kill runs
// asynchronously. Split from updateFocusLaunchKeys so the ctrl+w switch case
// reads as one line.
func (a App) closeFocusLaunchAgent() (tea.Model, tea.Cmd) {
	sess := a.modals.LaunchSession()
	ag := a.modals.LaunchAgent()
	if sess == nil || ag == nil {
		return a, nil
	}
	agents := sess.Agents()
	if len(agents) == 0 {
		a.closeModal()
		a.dashboard.scrollOffset = 0
		return a, nil
	}
	oldID := ag.ID
	sessionID := sess.ID
	currentIdx := 0
	for i, candidate := range agents {
		if candidate.ID == oldID {
			currentIdx = i
			break
		}
	}
	lastAgent := len(agents) == 1
	if lastAgent {
		a.closeModal()
		a.dashboard.scrollOffset = 0
	} else {
		nextIdx := currentIdx + 1
		if currentIdx == len(agents)-1 {
			nextIdx = currentIdx - 1
		}
		a.modals.SetLaunchAgent(agents[nextIdx])
		a.syncModalsToDashboard()
		agents[nextIdx].Resize(a.focusLaunchTermHeight(), a.dashboard.width)
		a.dashboard.scrollOffset = 0
	}
	if a.closingAgents[oldID] {
		return a, nil
	}
	repoPath := a.repoPathForSession(sessionID)
	mgr := a.managers[repoPath]
	if mgr == nil {
		return a, nil
	}
	a.closingAgents[oldID] = true
	return a, func() tea.Msg {
		err := mgr.KillAgent(sessionID, oldID)
		return killResultMsg{
			scope:     killScopeAgent,
			sessionID: sessionID,
			agentID:   oldID,
			err:       err,
		}
	}
}

// handleKeysPlanEditor forwards a keypress to the open plan editor. handled is
// true exactly when the editor owns focus, signalling to the caller that no
// further dispatch is needed.
func (a App) handleKeysPlanEditor(msg tea.KeyPressMsg) (App, tea.Cmd, bool) {
	pe := a.modals.PlanEditor()
	if pe == nil {
		return a, nil, false
	}
	cmd := pe.Update(msg)
	return a, cmd, true
}

// handleKeysReviewPanel forwards a keypress to the review panel. handled is true
// exactly when the review panel owns focus. The snapshot dance preserves the
// invariant that svc.ClosePanel wins over a stale restore — if the panel was
// closed mid-Update, CompareAndSetReview returns false and the modals state
// stays nil.
func (a App) handleKeysReviewPanel(msg tea.KeyPressMsg) (App, tea.Cmd, bool) {
	snapshot := a.modals.Review()
	if snapshot == nil {
		return a, nil, false
	}
	updated, cmd := snapshot.Update(msg, a.panelServices())
	if rp, ok := updated.(*reviewPanelModel); ok {
		a.modals.CompareAndSetReview(snapshot, rp)
		a.syncModalsToDashboard()
	}
	return a, cmd, true
}

// handleKeysShippingPanel forwards a keypress to the shipping panel. Mirrors the
// review-panel snapshot-and-restore pattern.
func (a App) handleKeysShippingPanel(msg tea.KeyPressMsg) (App, tea.Cmd, bool) {
	snapshot := a.modals.Shipping()
	if snapshot == nil {
		return a, nil, false
	}
	updated, cmd := snapshot.Update(msg, a.panelServices())
	if sp, ok := updated.(*shippingPanelModel); ok {
		a.modals.CompareAndSetShipping(snapshot, sp)
		a.syncModalsToDashboard()
	}
	return a, cmd, true
}

// handleKeysWorkflow handles the dashboard-wide workflow keypresses (new
// session, add agent, advance phase, open review, open shell, etc.). Returns
// handled=false when the key did not match any binding, letting the caller
// fall through to default dashboard navigation (e.g. dashboard.Update for
// scrolling).
func (a App) handleKeysWorkflow(msg tea.KeyPressMsg) (App, tea.Cmd, bool) {
	switch msg.String() {
	case "b":
		switch {
		case !a.wellness.focusBreakMode:
			// Enter break. Round(0) strips the monotonic reading so
			// time.Since uses wall-clock arithmetic, which keeps the
			// timer honest across suspend/resume.
			a.wellness.focusBreakMode = true
			a.wellness.focusBreakStart = time.Now().Round(0)
			a.wellness.focusBreakShortWarning = false
			a.wellness.focusBreakTimerUp = false
			a.wellness.focusBreakAnimFrame = 0
		case a.wellness.focusBreakTimerUp:
			// Break is fully elapsed; user is opting back in. Exit
			// without any "are you sure" friction.
			a.wellness.sessionStart = time.Now()
			a.wellness.focusBlockCount++
			a.wellness.focusBreakMode = false
			a.wellness.focusBreakShortWarning = false
			a.wellness.focusBreakTimerUp = false
			a.wellness.focusBreakAnimFrame = 0
		case !a.wellness.focusBreakShortWarning:
			a.wellness.focusBreakShortWarning = true
		default:
			// Third b press while still inside the break window:
			// override the short-break guard and end early.
			a.wellness.sessionStart = time.Now()
			a.wellness.focusBlockCount++
			a.wellness.focusBreakMode = false
			a.wellness.focusBreakShortWarning = false
			a.wellness.focusBreakTimerUp = false
			a.wellness.focusBreakAnimFrame = 0
		}
		return a, nil, true

	case "n":
		// Create a new session in the repo of the currently selected item.
		repoPath := a.dashboard.selectedRepoPath()
		if repoPath == "" {
			repoPath = a.activeRepo
		}
		if repoPath == "" {
			return a, nil, true
		}
		a.activeRepo = repoPath

		// Soft agent-count guidance.
		resolved := a.resolvedCache[repoPath]
		if resolved.MaxConcurrentAgents > 0 && a.activeAgentCount() >= resolved.MaxConcurrentAgents {
			if !a.agentLimitModalActive {
				a.agentLimitModalActive = true
				return a, nil, true
			}
			// Second press: proceed, clear modal flag.
			a.agentLimitModalActive = false
		}

		// Soft review-backlog limit.
		resolvedForBacklog := a.resolvedCache[a.activeRepo]
		if resolvedForBacklog.MaxReviewBacklog > 0 {
			var backlogCount int
			if mgr := a.managers[a.activeRepo]; mgr != nil {
				for _, sess := range mgr.ListSessions() {
					if sess.LifecyclePhase() == agent.LifecycleReadyForReview {
						backlogCount++
					}
				}
			}
			if backlogCount >= resolvedForBacklog.MaxReviewBacklog {
				if !a.wellness.focusBacklogWarning {
					a.wellness.focusBacklogWarning = true
					a.setError(fmt.Sprintf("n again to override — %d sessions awaiting review", backlogCount))
					return a, nil, true
				}
				a.wellness.focusBacklogWarning = false // second n: proceed
			} else {
				a.wellness.focusBacklogWarning = false
			}
		}

		fixedW := a.dashboard.fixedTermWidth()
		fixedH := a.dashboard.fixedTermHeight()
		if fixedW <= 0 || fixedH <= 0 {
			a.setError("Terminal size not yet known; try again")
			return a, nil, true
		}
		mgr := a.managers[repoPath]
		if mgr == nil {
			return a, nil, true
		}

		// Plan-first flow: open the prompt modal so the user can describe
		// the task before any subprocess spawns. The modal's submit
		// message routes through submitPromptModal which decides between
		// the planning path (StartDraft + editor) and the skip path
		// (today's flow).
		if resolved.PlanFirstEnabled {
			return a, a.promptModal.Open(), true
		}

		cfg := agent.Config{
			Rows:              fixedH,
			Cols:              fixedW,
			BypassPermissions: resolved.BypassPermissions,
			AgentProgram:      resolved.AgentProgram,
			AgentModel:        resolved.AgentModel,
			BuildSystemPrompt: resolved.BuildSystemPrompt,
		}
		return a, func() tea.Msg {
			sess, ag, err := mgr.CreateSession(cfg)
			if err != nil {
				return createResultMsg{err: err}
			}
			// Legacy n (PlanFirstEnabled=false) spawns the agent
			// immediately, so the session belongs in BUILDING from the
			// start. Without this transition the row would land in
			// PLANNING, where the dashboard renders plan-status badges
			// rather than agent activity. The skip path in
			// submitPromptModal does the same — keeping both call sites
			// consistent.
			sess.SetLifecyclePhase(agent.LifecycleInProgress)
			return createResultMsg{sessionID: sess.ID, agentID: ag.ID, isNewSession: true}
		}, true

	case "c":
		// Add an agent to the cursor-selected session.
		sess := a.cursorSelectedSession()
		if sess == nil {
			a.setError("No session selected")
			return a, nil, true
		}
		repoPath := a.cursorSelectedRepoPath()
		mgr := a.managers[repoPath]
		if mgr == nil {
			return a, nil, true
		}
		fixedW := a.dashboard.fixedTermWidth()
		fixedH := a.dashboard.fixedTermHeight()
		if fixedW <= 0 || fixedH <= 0 {
			a.setError("Terminal size not yet known; try again")
			return a, nil, true
		}
		resolved := a.resolvedCache[repoPath]
		cfg := agent.Config{
			Rows:              fixedH,
			Cols:              fixedW,
			BypassPermissions: resolved.BypassPermissions,
			AgentProgram:      resolved.AgentProgram,
			AgentModel:        resolved.AgentModel,
			BuildSystemPrompt: resolved.BuildSystemPrompt,
		}
		sessionID := sess.ID
		return a, func() tea.Msg {
			ag, err := mgr.AddAgent(sessionID, cfg)
			if err != nil {
				return createResultMsg{err: err}
			}
			return createResultMsg{sessionID: sessionID, agentID: ag.ID}
		}, true

	case "e":
		// Open the cursor-selected session's worktree in the configured IDE.
		sess := a.cursorSelectedSession()
		if sess == nil {
			a.setError("No session selected")
			return a, nil, true
		}
		repoPath := a.cursorSelectedRepoPath()
		ideCmd := strings.TrimSpace(a.resolvedCache[repoPath].IDECommand)
		if ideCmd == "" {
			a.setError("No IDE configured (set 'IDE Command' in settings)")
			return a, nil, true
		}
		parts := splitIDECommand(ideCmd)
		if len(parts) == 0 {
			a.setError("No IDE configured (set 'IDE Command' in settings)")
			return a, nil, true
		}
		worktreePath := sess.Worktree.Path
		exe := parts[0]
		args := append(parts[1:], worktreePath)
		go func() {
			cmd := exec.Command(exe, args...)
			cmd.Dir = worktreePath
			_ = cmd.Start()
		}()
		return a, nil, true

	case "a":
		// Open file browser to add a new repo.
		a.repoBrowser = newFileBrowserModel()
		a.repoBrowser.width = a.width
		a.repoBrowser.height = a.height - 1
		a.view = ViewFileBrowser
		return a, nil, true

	case "o":
		// Open branch picker to create session on existing branch/PR.
		// `o` is not session-scoped; the picker always targets the active repo.
		repoPath := a.cursorSelectedRepoPath()
		if repoPath == "" {
			a.setError("No repo available")
			return a, nil, true
		}
		// Build set of branches that already have active sessions.
		mgr := a.managers[repoPath]
		activeBranches := make(map[string]bool)
		if mgr != nil {
			for _, sess := range mgr.ListSessions() {
				activeBranches[sess.Branch()] = true
			}
		}
		a.branchPicker = newBranchPickerModel()
		a.branchPicker.width = a.width
		a.branchPicker.height = a.height - 1
		a.activeRepo = repoPath
		a.view = ViewBranchPicker
		return a, loadBranchPickerData(repoPath, a.ghClient, activeBranches), true

	case "t":
		// Open or focus a shell terminal in the cursor-selected session.
		sess := a.cursorSelectedSession()
		if sess == nil {
			a.setError("No session selected")
			return a, nil, true
		}
		if sess.HasShell() {
			// Shell exists — open it in focusLaunch directly.
			for _, ag := range sess.Agents() {
				if ag.IsShell {
					a.openLaunchPanel(sess, ag)
					a.dashboard.scrollOffset = 0
					ag.Resize(a.focusLaunchTermHeight(), a.dashboard.width)
					break
				}
			}
			return a, nil, true
		}
		repoPath := a.cursorSelectedRepoPath()
		mgr := a.managers[repoPath]
		if mgr == nil {
			return a, nil, true
		}
		fixedW := a.dashboard.fixedTermWidth()
		fixedH := a.dashboard.fixedTermHeight()
		if fixedW <= 0 || fixedH <= 0 {
			a.setError("Terminal size not yet known; try again")
			return a, nil, true
		}
		cfg := agent.Config{
			Rows: fixedH,
			Cols: fixedW,
		}
		sessionID := sess.ID
		return a, func() tea.Msg {
			ag, err := mgr.AddShell(sessionID, cfg)
			if err != nil {
				return createResultMsg{err: err}
			}
			return createResultMsg{sessionID: sessionID, agentID: ag.ID}
		}, true

	case "p":
		// Open the cursor-selected session's PR in the browser, or push
		// and draft a new one if no open PR exists yet.
		sess := a.cursorSelectedSession()
		if sess != nil {
			if entry := a.prCache[sess.ID]; entry != nil && entry.pr != nil && entry.pr.URL != "" {
				if err := openURL(entry.pr.URL); err != nil {
					a.setError(err.Error())
				}
			} else {
				if a.ghClient == nil {
					a.setError("GitHub auth not available")
					return a, nil, true
				}
				phase := sess.LifecyclePhase()
				if phase != agent.LifecycleReadyForReview && phase != agent.LifecycleInReview {
					return a, nil, true
				}
				if a.prDraftInFlight {
					return a, nil, true
				}
				a.prDraftInFlight = true
				a.prDraftSessionID = sess.ID
				repoPath := a.cursorSelectedRepoPath()
				return a, a.startPRDraftCmd(sess, repoPath, false), true
			}
		}
		return a, nil, true

	case "R":
		// Open repo picker in manage mode (switch active repo, edit settings, remove).
		counts := make(map[string]int, len(a.cfg.Repos))
		for _, repo := range a.cfg.Repos {
			if mgr := a.managers[repo.Path]; mgr != nil {
				counts[repo.Path] = mgr.AgentCount()
			}
		}
		a.repoPicker = newRepoPickerModel()
		a.repoPicker.width = a.width
		a.repoPicker.height = a.height - 1
		a.repoPicker.SetMode(repoPickerModeManage)
		a.repoPicker.setRepos(a.cfg.Repos, counts, a.activeRepo)
		a.view = ViewRepoPicker
		return a, nil, true

	case "s":
		// Open global settings overlay.
		a.globalConfig = newGlobalConfigModel(a.globalSettings, a.width, a.height)
		a.view = ViewGlobalConfig
		return a, nil, true

	case "d":
		// Diff the cursor-selected session's worktree.
		sess := a.cursorSelectedSession()
		if sess == nil {
			return a, nil, true
		}
		repoPath := a.cursorSelectedRepoPath()
		rawDiff, err := git.Diff(repoPath, sess.Worktree)
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
		a.diff = newDiffModel(sess.GetDisplayName(), m, a.width, a.height-1)
		return a, nil, true

	case "x":
		// Kill the cursor-selected session's primary agent asynchronously.
		sess := a.cursorSelectedSession()
		if sess == nil {
			a.setError("No session selected")
			return a, nil, true
		}
		ag := sess.PrimaryAgent()
		if ag == nil {
			a.setError("Session has no agents")
			return a, nil, true
		}
		repoPath := a.cursorSelectedRepoPath()
		mgr := a.managers[repoPath]
		if mgr == nil {
			return a, nil, true
		}
		agentID := ag.ID
		sessionID := sess.ID
		// Already dispatched — no-op to avoid double-kills.
		if a.closingAgents[agentID] {
			return a, nil, true
		}
		a.closingAgents[agentID] = true
		a.refreshAgentList()
		return a, func() tea.Msg {
			err := mgr.KillAgent(sessionID, agentID)
			return killResultMsg{
				scope:     killScopeAgent,
				sessionID: sessionID,
				agentID:   agentID,
				err:       err,
			}
		}, true

	case "X":
		// Kill the cursor-selected session entirely.
		sess := a.cursorSelectedSession()
		if sess == nil {
			return a, nil, true
		}
		repoPath := a.cursorSelectedRepoPath()
		mgr := a.managers[repoPath]
		if mgr == nil {
			return a, nil, true
		}
		sessID := sess.ID
		// Already dispatched — no-op.
		if a.closingSessions[sessID] {
			return a, nil, true
		}
		var agentIDs []string
		for _, ag := range sess.Agents() {
			agentIDs = append(agentIDs, ag.ID)
			a.closingAgents[ag.ID] = true
		}
		a.closingSessions[sessID] = true
		a.refreshAgentList()
		return a, func() tea.Msg {
			err := mgr.KillSession(sessID)
			return killResultMsg{
				scope:     killScopeSession,
				sessionID: sessID,
				agentIDs:  agentIDs,
				err:       err,
			}
		}, true

	}
	return a, nil, false
}
