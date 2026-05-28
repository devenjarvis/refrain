package tui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/audio"
	"github.com/devenjarvis/refrain/internal/config"
	"github.com/devenjarvis/refrain/internal/github"
	"github.com/devenjarvis/refrain/internal/state"
)

// handleWindowSize updates dimensions and resizes child models. Returns the
// mutated App without a command so the caller may continue to the view-router
// dispatch — the same fall-through behaviour the inline case relied on.
func (a App) handleWindowSize(msg tea.WindowSizeMsg) App {
	a.width = msg.Width
	a.height = msg.Height
	a.dashboard.width = msg.Width
	a.dashboard.height = msg.Height - 1 // room for statusbar
	a.diff, _ = a.diff.Update(tea.WindowSizeMsg{Width: msg.Width, Height: msg.Height - 1})
	a.repoBrowser.width = msg.Width
	a.repoBrowser.height = msg.Height - 1
	a.branchPicker.width = msg.Width
	a.branchPicker.height = msg.Height - 1
	a.repoPicker.width = msg.Width
	a.repoPicker.height = msg.Height - 1
	// A resize remaps the VT viewport — any in-flight selection is now
	// pointing at stale cells. Drop it.
	a.dashboard.clearSelection()

	// newSession always tracks terminal dimensions (it may be open in any view).
	a.newSession.SetSize(msg.Width, msg.Height-1)

	// Resize agent terminals to match their current display container.
	if a.view == ViewDashboard {
		a.resizeAllForDashboard()
		if ag := a.modals.LaunchAgent(); ag != nil {
			ag.Resize(a.focusLaunchTermHeight(), a.dashboard.width)
		}
		if pe := a.modals.PlanEditor(); pe != nil {
			pe.SetSize(msg.Width, msg.Height-1)
		}
		a.prComposeModal.SetSize(msg.Width, msg.Height-1)
		if sp := a.modals.Shipping(); sp != nil {
			sp.Resize(msg.Width, msg.Height-1)
		}
	}
	return a
}

func (a App) handleInit(msg initAppMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		a.setError(msg.err.Error())
		return a, nil
	}
	a.cfg = msg.cfg
	now := time.Now()
	a.wellness.appStart = now
	a.wellness.sessionStart = now

	// Load global settings and run one-time migration.
	globalSettings, err := config.LoadGlobalSettings()
	if err != nil {
		a.setError(err.Error())
	} else {
		a.globalSettings = globalSettings
		_ = config.MigrateBypassPermissions(a.cfg)
	}
	resolved := config.Resolve(a.globalSettings, nil)
	a.dashboard.sidebarWidth = resolved.SidebarWidth
	a.wellness.focusSessionMinutes = resolved.FocusSessionMinutes
	a.wellness.focusBreakMinutes = resolved.FocusBreakMinutes
	a.cursor.SetSection(focusSectionPlanning)
	// Default activeRepo to the first registered repo so the pipeline
	// header shows "repo: <name>" and workflow keys ('n', 'a', 'o') target
	// a known repo on a fresh dashboard.
	if a.activeRepo == "" && len(msg.cfg.Repos) > 0 {
		a.activeRepo = msg.cfg.Repos[0].Path
	}

	// Load per-repo settings and build resolved cache.
	for _, repo := range msg.cfg.Repos {
		rs, _ := config.LoadRepoSettings(repo.Path)
		a.repoSettings[repo.Path] = rs
		a.resolvedCache[repo.Path] = config.Resolve(a.globalSettings, rs)
	}

	// Initialize audio player (best-effort — nil on failure).
	if p, err := audio.NewPlayer(); err == nil {
		a.audioPlayer = p
	}
	// Initialize GitHub client (best-effort — nil on failure).
	if ghc, err := github.NewClient(); err == nil {
		a.ghClient = ghc
	}
	// Create a manager for every registered repo and start event listeners.
	var cmds []tea.Cmd
	for _, repo := range msg.cfg.Repos {
		if a.managers[repo.Path] == nil {
			mgr := agent.NewManager(repo.Path, a.resolvedCache[repo.Path])
			a.managers[repo.Path] = mgr
			ensureGitignore(repo.Path)
			cmds = append(cmds, listenEvents(mgr), listenPlannerQuestions(mgr))
		}
	}
	// Build resume work to run in the background so the TUI renders immediately.
	type resumeItem struct {
		repoPath  string
		mgr       SessionManager
		resumeCfg agent.Config
		sessions  []state.SessionState
	}
	resumeItems := make([]resumeItem, 0, len(msg.cfg.Repos))
	totalPruned := 0
	for _, repo := range msg.cfg.Repos {
		bs, err := state.Load(repo.Path)
		if err != nil || bs == nil {
			continue
		}
		mgr := a.managers[repo.Path]
		if mgr == nil {
			continue
		}
		// Prune sessions whose worktree directory no longer exists. Without
		// this, ResumeSession fails inside the goroutine, the error is
		// dropped, and the same broken entry is reloaded on the next
		// launch. Persist the pruned state so the user converges on a
		// clean slate after one quit-and-restart.
		valid := bs.Sessions[:0]
		pruned := 0
		for _, ss := range bs.Sessions {
			if _, statErr := os.Stat(ss.WorktreePath); statErr == nil {
				valid = append(valid, ss)
				continue
			}
			pruned++
		}
		if pruned > 0 {
			bs.Sessions = valid
			if saveErr := state.Save(repo.Path, bs); saveErr != nil {
				fmt.Fprintf(os.Stderr, "refrain: pruning stale state for %s: %v\n", repo.Path, saveErr)
			}
			totalPruned += pruned
		}
		if len(valid) == 0 {
			continue
		}
		resolved := a.resolvedCache[repo.Path]
		fixedW := a.dashboard.fixedTermWidth()
		fixedH := a.dashboard.fixedTermHeight()
		if fixedW <= 0 {
			fixedW = 80
		}
		if fixedH <= 0 {
			fixedH = 24
		}
		resumeItems = append(resumeItems, resumeItem{
			repoPath: repo.Path,
			mgr:      mgr,
			resumeCfg: agent.Config{
				Rows:              fixedH,
				Cols:              fixedW,
				BypassPermissions: resolved.BypassPermissions,
				AgentProgram:      resolved.AgentProgram,
				AgentModel:        resolved.AgentModel,
				BuildSystemPrompt: resolved.BuildSystemPrompt,
			},
			sessions: valid,
		})
	}
	if totalPruned > 0 {
		a.setError(fmt.Sprintf("dropped %d stale session(s) whose worktree no longer exists", totalPruned))
	}
	if len(resumeItems) > 0 {
		cmds = append(cmds, func() tea.Msg {
			var wg sync.WaitGroup
			for _, ri := range resumeItems {
				for _, ss := range ri.sessions {
					wg.Add(1)
					go func(mgr SessionManager, ss state.SessionState, cfg agent.Config) {
						defer wg.Done()
						_ = mgr.ResumeSession(ss, cfg)
					}(ri.mgr, ss, ri.resumeCfg)
				}
			}
			wg.Wait()
			repoPaths := make([]string, len(resumeItems))
			for i, ri := range resumeItems {
				repoPaths[i] = ri.repoPath
			}
			return resumeDoneMsg{repoPaths: repoPaths}
		})
	}
	// Always start on the dashboard — single repo or many.
	a.view = ViewDashboard
	a.refreshAgentList()
	a.updateDashboardPRCache()
	return a, tea.Batch(cmds...)
}

func (a App) handleTick(msg tickMsg) (tea.Model, tea.Cmd) {
	_ = msg
	// Break mode: advance animation and detect timer expiry. We DO NOT
	// auto-exit — once the configured break elapses we flip into a
	// "ready" state and wait for the user to explicitly resume. This
	// avoids dropping the user back into focus mode while they're still
	// away from the keyboard.
	if a.wellness.focusBreakMode {
		a.wellness.focusBreakAnimFrame++
		if a.wellness.focusBreakMinutes > 0 && !a.wellness.focusBreakTimerUp &&
			time.Since(a.wellness.focusBreakStart) >= time.Duration(a.wellness.focusBreakMinutes)*time.Minute {
			a.wellness.focusBreakTimerUp = true
			a.wellness.focusBreakShortWarning = false
			a.wellness.focusBreakAnimFrame = 0
			// One unmistakable cue when the break ends. Played even in
			// focus mode (the normal suppression path), since the whole
			// point is to grab attention.
			if a.audioPlayer != nil {
				a.audioPlayer.Play()
			}
		}
	} else if a.wellness.focusSessionMinutes > 0 &&
		a.modals.IsList() &&
		time.Since(a.wellness.sessionStart) >= time.Duration(a.wellness.focusSessionMinutes)*time.Minute {
		// Auto-enter break when the work block elapses. The asymmetry
		// with break-end (which waits for explicit `b`) is intentional:
		// end-of-block SHOULD interrupt the user — that's the whole
		// point of the timer — whereas end-of-break should not drag
		// them back from the keyboard.
		//
		// Deferred for ANY non-pipeline panel: focusLaunch (fullscreen
		// agent terminal), focusReview (review panel), focusShipping
		// (mid-merge / mid-feedback in the shipping panel), focusConfig
		// (editing settings), focusPlanEditor (editing plan.md). Firing
		// the overlay during a merge would hide the merge result behind
		// the break screen; deferring until the user is back on the
		// pipeline keeps interrupts at safe checkpoints.
		a.wellness.focusBreakMode = true
		a.wellness.focusBreakStart = time.Now().Round(0)
		a.wellness.focusBreakShortWarning = false
		a.wellness.focusBreakTimerUp = false
		a.wellness.focusBreakAnimFrame = 0
		// Bypass the dashboard chime suppression — same rationale as
		// the break-end branch above.
		if a.audioPlayer != nil {
			a.audioPlayer.Play()
		}
	}
	a.refreshAgentList()
	// Detect Active->Idle transitions for diff-stats refresh. Chime
	// notifications are fired in the EventStatusChanged handler below,
	// which reacts the instant Claude's Stop hook arrives.
	idleTransition := false
	for _, item := range a.dashboard.items {
		if item.kind != listItemAgent || item.agent == nil {
			continue
		}
		ag := item.agent
		currentStatus := ag.Status()
		key := agentCacheKey(item.repoPath, ag.ID)
		if prev, ok := a.lastKnownStatus[key]; ok {
			if prev == agent.StatusActive && currentStatus == agent.StatusIdle && !ag.IsShell {
				idleTransition = true
			}
		}
		a.lastKnownStatus[key] = currentStatus
	}
	// Detect alt-screen transitions and trigger a resize so Claude's TUI
	// redraws cleanly (replaces the old splashResizeMsg delayed timer).
	fixedW := a.dashboard.fixedTermWidth()
	fixedH := a.dashboard.fixedTermHeight()
	if fixedW > 0 && fixedH > 0 {
		selected := a.dashboard.selectedAgent()
		for _, item := range a.dashboard.items {
			if item.kind != listItemAgent || item.agent == nil {
				continue
			}
			if item.agent.AltScreenEntered() {
				// The focusLaunch agent renders fullscreen, so resize it
				// to the fullscreen dimensions instead of shrinking it
				// back to the preview size.
				if launchAgent := a.modals.LaunchAgent(); launchAgent != nil && item.agent.ID == launchAgent.ID {
					item.agent.Resize(a.focusLaunchTermHeight(), a.dashboard.width)
				} else {
					item.agent.Resize(fixedH, fixedW)
				}
				// VT history is cleared on alt-screen entry; any prior
				// scrollOffset now indexes into an empty buffer and would
				// leave the preview visually frozen until the user hits
				// home. Snap the currently focused agent back to live.
				if item.agent == selected {
					a.dashboard.scrollOffset = 0
				}
			}
		}
	}
	if a.errTicks > 0 {
		a.errTicks--
		if a.errTicks == 0 {
			a.err = ""
		}
	}
	// Clean stale diff cache entries periodically.
	a.cleanStaleCaches()
	// Refresh diff stats periodically or on idle transition.
	var diffCmd tea.Cmd
	if sess := a.dashboard.selectedSession(); sess != nil {
		repoPath := a.dashboard.selectedRepoPath()
		var entry *diffStatsEntry
		if repoPath != "" {
			entry = a.diffStatsCache[cacheKey(repoPath, sess.ID)]
		}
		stale := entry == nil || time.Since(entry.lastRefresh) > DiffStatsCacheTTL
		if (stale || idleTransition) && !a.diffRefreshInFlight {
			diffCmd = a.refreshDiffStatsCmd()
		}
	}
	// Advance sidebar marquee tickers for overflowing session names.
	a.dashboard.advanceTickers(time.Now())
	// Adaptive per-session PR polling.
	var prCmds []tea.Cmd
	if a.ghClient != nil {
		prCmds = a.pollAllSessions()
	}
	allCmds := append([]tea.Cmd{tickCmd(), diffCmd}, prCmds...)
	return a, tea.Batch(allCmds...)
}

func (a App) handleAgentEvent(msg agentEventMsg) (tea.Model, tea.Cmd) {
	var autoPromoteCmd tea.Cmd
	// Clean up stale lastKnownStatus entries when a session auto-closes.
	// lastKnownStatus is keyed by agentCacheKey(repoPath, agentID), so the
	// stale prefix has to include the repoPath to avoid wiping a colliding
	// agent ID in a different repo.
	if msg.event.Type == agent.EventSessionClosed && msg.event.SessionID != "" {
		prefix := msg.repoPath + "\x00" + msg.event.SessionID + "-agent-"
		for id := range a.lastKnownStatus {
			if strings.HasPrefix(id, prefix) {
				delete(a.lastKnownStatus, id)
			}
		}
	}
	// Chime when Claude's Stop hook arrives (EventStatusChanged with Idle).
	// Gated only by ChimedForTurn + HasReceivedInput: ChimedForTurn is reset
	// on Enter (Agent.SendKey), so once-per-turn semantics are enforced there
	// rather than by re-reading lastKnownStatus. This avoids a race with the
	// 100ms tickMsg: a tick landing between the manager mutating status and
	// the TUI dequeuing this event would otherwise clobber the cached "prev
	// was Active" signal and silently suppress the chime.
	if msg.event.Type == agent.EventStatusChanged {
		// Chime on both Idle (Claude finished its turn) and Waiting
		// (Claude needs user input). ChimedForTurn is the shared gate:
		// whichever fires first in a turn wins, and the other is
		// silently skipped. The flag resets on Enter or UserPromptSubmit.
		// StatusIdle chimes are suppressed — only StatusWaiting
		// (permission prompts) still fires. Suppressing the idle chime
		// is the wellness-first default: every block ends in idle, and
		// chiming on every turn trains the user to ignore it.
		if msg.event.Status == agent.StatusIdle || msg.event.Status == agent.StatusWaiting {
			if mgr := a.managers[msg.repoPath]; mgr != nil {
				if ag := mgr.Get(msg.event.AgentID); ag != nil && !ag.IsShell {
					if ag.HasReceivedInput() && !ag.ChimedForTurn() {
						resolved := a.resolvedCache[msg.repoPath]
						chimeAllowed := msg.event.Status == agent.StatusWaiting
						if resolved.AudioEnabled && a.audioPlayer != nil && chimeAllowed {
							a.audioPlayer.Play()
							ag.MarkChimedForTurn()
						}
					}
				}
			}
		}
		a.lastKnownStatus[agentCacheKey(msg.repoPath, msg.event.AgentID)] = msg.event.Status
		// Auto-promote and MarkDone share a single FindAgentAndSession
		// call, gated on the statuses that can trigger either transition.
		if msg.event.Status == agent.StatusIdle ||
			msg.event.Status == agent.StatusDone ||
			msg.event.Status == agent.StatusError {
			if mgr := a.managers[msg.repoPath]; mgr != nil {
				if _, sess := mgr.FindAgentAndSession(msg.event.AgentID); sess != nil {
					// Auto-promote InProgress → ReadyForReview on first
					// idle/done signal. Only fires once per session (the
					// phase gate makes it idempotent on subsequent events).
					// Suppressed when the plan has uncompleted tasks so the
					// session stays in BUILDING with a visible progress bar.
					if sess.LifecyclePhase() == agent.LifecycleInProgress && sess.IsReviewable() {
						promote := true
						if plan, present := sess.CachedPlan(); present {
							pTotal, pDone := planTaskCounts(plan)
							cDone, cMax := sess.CommitTaskCount()
							effectiveTotal := max(pTotal, cMax)
							effectiveDone := max(pDone, cDone)
							if effectiveTotal > 0 && effectiveDone < effectiveTotal {
								promote = false
							}
						}
						if promote {
							sess.SetLifecyclePhase(agent.LifecycleReadyForReview)
							autoPromoteCmd = tea.Batch(
								a.fetchReviewDiffCmd(sess, msg.repoPath),
								a.startValidationChecksCmd(sess, msg.repoPath),
							)
						}
					}
					// Mark session done when all non-shell agents have exited.
					if msg.event.Status == agent.StatusDone || msg.event.Status == agent.StatusError {
						allDone := true
						for _, ag := range sess.Agents() {
							if !ag.IsShell && ag.Status() != agent.StatusDone && ag.Status() != agent.StatusError {
								allDone = false
								break
							}
						}
						if allDone {
							sess.MarkDone()
						}
					}
				}
			}
		}
	}
	// Branch rename invalidates any PR-by-branch lookup. Schedule a burst of
	// short-interval polls so the SHA-based lookup can rediscover the PR
	// quickly — do NOT clear the cache here; that happens only when the
	// next poll confirms the PR is gone (handled in prPollMsg).
	if msg.event.Type == agent.EventBranchRenamed && msg.event.SessionID != "" {
		key := cacheKey(msg.repoPath, msg.event.SessionID)
		ps := a.prPollStates[key]
		if ps == nil {
			ps = &prSessionState{}
			a.prPollStates[key] = ps
		}
		ps.burstUntil = time.Now().Add(PRPollBurstAfterCreate)
		ps.lastPoll = time.Time{}
		// Clearing lastRemoteSHA forces getRemoteSHA to re-check against
		// the new branch name on the next tick instead of comparing against
		// the SHA it read under the old branch.
		ps.lastRemoteSHA = ""
		ps.lastSHACheck = time.Time{}
	}
	// If the editor is open on the session whose drafting/revising just
	// landed, refresh its content + state so the placeholder swaps to
	// the rendered plan without requiring a re-open.
	if pe := a.modals.PlanEditor(); msg.event.Type == agent.EventStatusChanged && pe != nil &&
		pe.sess != nil && pe.sess.ID == msg.event.SessionID {
		// Read both flags up-front so the Reload decision sees a coherent
		// snapshot. RevisePlan emits EventStatusChanged synchronously with
		// IsRevising=true (before runRevise spawns), so a naive
		// "Reload when !drafting" path would reset scrollOff at the moment
		// the revise banner appears. Only Reload when neither subprocess
		// is in flight — that's the single state where plan.md is stable
		// and the editor view should reflect disk.
		drafting := pe.sess.IsDrafting()
		revising := pe.sess.IsRevising()
		pe.SetDrafting(drafting)
		pe.SetRevising(revising)
		if !drafting && !revising {
			pe.Reload()
			if derr := pe.sess.DraftError(); derr != nil {
				pe.SetError("draft failed: " + derr.Error())
			} else if rerr := pe.sess.ReviseError(); rerr != nil {
				pe.SetError("revise failed: " + rerr.Error())
			}
		}
	}

	// Refresh list on any agent event — all repos are visible in the dashboard.
	a.refreshAgentList()
	if mgr := a.managers[msg.repoPath]; mgr != nil {
		if autoPromoteCmd != nil {
			return a, tea.Batch(autoPromoteCmd, listenEvents(mgr))
		}
		return a, listenEvents(mgr)
	}
	if autoPromoteCmd != nil {
		return a, autoPromoteCmd
	}
	return a, nil
}

func (a App) handleCreateResult(msg createResultMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		a.setError(msg.err.Error())
		return a, nil
	}
	a.wellness.agentsCreatedCount++
	if msg.isNewSession {
		a.wellness.sessionsCreatedCount++
	}
	a.refreshAgentList()
	// Find the new agent by ID. Cursor always moves to the new session's
	// row; focusLaunch is only entered when skipFocusLaunch is false.
	if msg.agentID != "" {
		for i, item := range a.dashboard.items {
			if item.kind == listItemAgent && item.agent != nil && item.agent.ID == msg.agentID {
				a.dashboard.selected = i
				if !msg.skipFocusLaunch {
					a.openLaunchPanel(item.session, item.agent, item.repoPath)
					a.dashboard.scrollOffset = 0
					item.agent.Resize(a.focusLaunchTermHeight(), a.dashboard.width)
				}
				// Move the pipeline cursor to the new session so when the
				// user esc's back to focusList their cursor is on the row
				// they just spawned. Walk every section because newSession
				// defaults to LifecyclePlanning and AddAgent/Restore paths
				// can land in any phase.
				if item.session != nil {
				Sections:
					for _, section := range focusSectionsInOrder() {
						for idx, s := range a.dashboard.sectionItems(section) {
							if s.session == item.session {
								a.cursor.JumpTo(section, idx)
								a.syncFocusCursorToDashboard()
								break Sections
							}
						}
					}
				}
				break
			}
		}
	}
	return a, nil
}

func (a App) handleKillResult(msg killResultMsg) (tea.Model, tea.Cmd) {
	// Clean up closing-set entries regardless of error so the UI never
	// gets stuck rendering "closing…" on a row whose kill failed.
	if msg.repoPath == "" {
		// Defensive: every producer of killResultMsg should populate
		// repoPath. If one ever doesn't, drop with a visible error so the
		// missing field is fixed at the source rather than corrupting
		// closing-set cleanup across repos.
		a.setError("internal: killResultMsg missing repoPath; closing-set may be stale")
		return a, nil
	}
	switch msg.scope {
	case killScopeAgent:
		agentKey := agentCacheKey(msg.repoPath, msg.agentID)
		delete(a.closingAgents, agentKey)
		delete(a.lastKnownStatus, agentKey)
		// Exit focusLaunch if the killed agent is the one being viewed.
		if ag := a.modals.LaunchAgent(); ag != nil && ag.ID == msg.agentID {
			a.closeModal()
			a.dashboard.scrollOffset = 0
		}
	case killScopeSession:
		sessKey := cacheKey(msg.repoPath, msg.sessionID)
		delete(a.closingSessions, sessKey)
		for _, id := range msg.agentIDs {
			agentKey := agentCacheKey(msg.repoPath, id)
			delete(a.closingAgents, agentKey)
			delete(a.lastKnownStatus, agentKey)
			if ag := a.modals.LaunchAgent(); ag != nil && ag.ID == id {
				a.closeModal()
				a.dashboard.scrollOffset = 0
			}
		}
		delete(a.diffStatsCache, sessKey)
	}
	if msg.err != nil {
		a.setError(msg.err.Error())
	}
	a.refreshAgentList()
	a.updateDashboardDiffStats()
	return a, nil
}

func (a App) handleDiffStats(msg diffStatsMsg) (tea.Model, tea.Cmd) {
	a.diffRefreshInFlight = false
	if msg.repoPath == "" {
		// Without a repoPath we can't key the cache safely; drop the result.
		// All callers populate repoPath; this guard catches future regressions.
		return a, nil
	}
	// Always update cache timestamp to prevent tight retry loops on persistent errors.
	a.diffStatsCache[cacheKey(msg.repoPath, msg.sessionID)] = &diffStatsEntry{
		stats:       msg.stats,
		lastRefresh: time.Now(),
	}
	// Update dashboard with current session's stats.
	if sess := a.dashboard.selectedSession(); sess != nil && sess.ID == msg.sessionID {
		a.dashboard.diffStats = msg.stats
	}
	return a, nil
}

func (a App) handleResumeDone(msg resumeDoneMsg) (tea.Model, tea.Cmd) {
	for _, repoPath := range msg.repoPaths {
		_ = state.Remove(repoPath)
	}
	a.refreshAgentList()
	return a, nil
}
