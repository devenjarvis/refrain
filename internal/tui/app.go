package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	xvt "github.com/charmbracelet/x/vt"
	"github.com/devenjarvis/baton/internal/agent"
	"github.com/devenjarvis/baton/internal/audio"
	"github.com/devenjarvis/baton/internal/config"
	"github.com/devenjarvis/baton/internal/diffmodel"
	"github.com/devenjarvis/baton/internal/git"
	"github.com/devenjarvis/baton/internal/github"
	"github.com/devenjarvis/baton/internal/state"
	"github.com/devenjarvis/baton/internal/vt"
)

// tickMsg triggers periodic re-renders.
type tickMsg time.Time

// agentEventMsg wraps an agent manager event for the TUI.
type agentEventMsg struct {
	event    agent.Event
	repoPath string
}

// createResultMsg carries the result of async agent creation.
type createResultMsg struct {
	sessionID string
	agentID   string
	err       error
}

// killScope distinguishes an agent-level kill from a session-level kill so the
// result handler knows which closing set to clean up.
type killScope int

const (
	killScopeAgent killScope = iota
	killScopeSession
)

// killResultMsg carries the result of an async KillAgent/KillSession call.
// agentID is empty for session-scoped kills; the closing-set cleanup iterates
// the manager to find stale IDs instead.
type killResultMsg struct {
	scope     killScope
	sessionID string
	agentID   string
	agentIDs  []string // for session scope: all agent IDs that were in the session
	err       error
}

// diffStatsMsg carries the result of an async diff stats refresh.
type diffStatsMsg struct {
	sessionID string
	stats     *diffSummaryData
}

// initAppMsg carries the result of app initialization.
type initAppMsg struct {
	cfg *config.Config
	err error
}

// resumeDoneMsg signals that background session resume has completed.
type resumeDoneMsg struct {
	repoPaths []string // repos whose state files should be cleaned up
}

// diffStatsEntry holds cached diff stats for a single session.
type diffStatsEntry struct {
	stats       *diffSummaryData
	lastRefresh time.Time
}

// App is the root Bubble Tea model.
type App struct {
	managers     map[string]*agent.Manager
	activeRepo   string
	cfg          *config.Config
	repoBrowser  fileBrowserModel
	branchPicker branchPickerModel
	repoPicker   repoPickerModel

	// repoPickerPending is set when the file browser was opened from the repo
	// picker. After the browser emits a select or cancel, control returns to
	// ViewRepoPicker rather than the dashboard.
	repoPickerPending bool

	// Settings
	globalSettings *config.GlobalSettings
	repoSettings   map[string]*config.RepoSettings    // keyed by repo path
	resolvedCache  map[string]config.ResolvedSettings // keyed by repo path

	view         ViewMode
	dashboard    dashboardModel
	diff         diffModel
	globalConfig globalConfigModel

	width       int
	height      int
	err         string
	errTicks    int // ticks remaining to show error
	confirmQuit bool

	lastKnownStatus map[string]agent.Status
	audioPlayer     *audio.Player

	// Wellness / focus mode state.
	focusModeActive        bool
	appStart               time.Time // set once at init; never reset; used for total session duration in wellness log
	sessionStart           time.Time // per-block work timer; reset on each break completion
	lastReviewAt           time.Time
	agentLimitModalActive  bool
	focusSessionMinutes    int          // cached from resolved global settings
	focusBreakMinutes      int          // cached from resolved global settings
	focusActiveIdx         int          // index into allInProgressSessions()
	focusQueueIndex        int          // index into reviewQueueSessions
	focusCursorSection     focusSection // which section the focus-mode cursor is on
	focusLaunchAgent       *agent.Agent
	focusLaunchSession     *agent.Session
	focusBacklogWarning    bool // first n at backlog limit shows warning; second proceeds
	focusBreakMode         bool
	focusBreakStart        time.Time // wall-clock; monotonic stripped so suspend counts toward elapsed
	focusBlockCount        int
	focusBreakShortWarning bool
	focusBreakTimerUp      bool // break duration elapsed; waiting on user to resume
	focusBreakAnimFrame    int
	reviewDiffCache        map[string]*reviewDiffEntry // keyed by session ID
	reviewSession          *agent.Session              // session currently open in review panel

	// Wellness counters (written to log on quit).
	agentsCreatedCount   int
	sessionsCreatedCount int
	focusModeSwitches    int

	// closingAgents and closingSessions track in-flight kill requests so the
	// dashboard can render a "closing…" indicator while the async teardown runs.
	// Lives in the TUI because it's purely a UI concern.
	closingAgents   map[string]bool
	closingSessions map[string]bool

	diffStatsCache      map[string]*diffStatsEntry // keyed by session ID
	diffRefreshInFlight bool

	ghClient        *github.Client
	prCache         map[string]*prCacheEntry   // keyed by session ID
	prPollStates    map[string]*prSessionState // keyed by session ID
	prPollsInFlight int                        // count of concurrent in-flight polls
}

func NewApp() App {
	return App{
		view:            ViewDashboard,
		dashboard:       newDashboardModel(),
		managers:        make(map[string]*agent.Manager),
		repoSettings:    make(map[string]*config.RepoSettings),
		resolvedCache:   make(map[string]config.ResolvedSettings),
		lastKnownStatus: make(map[string]agent.Status),
		diffStatsCache:  make(map[string]*diffStatsEntry),
		reviewDiffCache: make(map[string]*reviewDiffEntry),
		prCache:         make(map[string]*prCacheEntry),
		prPollStates:    make(map[string]*prSessionState),
		closingAgents:   make(map[string]bool),
		closingSessions: make(map[string]bool),
	}
}

func (a App) Init() tea.Cmd {
	return tea.Batch(tickCmd(), initAppCmd())
}

func initAppCmd() tea.Cmd {
	return func() tea.Msg {
		cfg, err := config.Load()
		if err != nil {
			return initAppMsg{err: err}
		}

		if len(cfg.Repos) == 0 {
			// Auto-register the current working directory on first run.
			if err := config.AddRepo(cfg, "."); err != nil {
				return initAppMsg{err: err}
			}
			if err := config.Save(cfg); err != nil {
				return initAppMsg{err: err}
			}
		}

		return initAppMsg{cfg: cfg}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func listenEvents(mgr *agent.Manager) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-mgr.Events()
		if !ok {
			return nil // channel closed (shutdown)
		}
		return agentEventMsg{event: e, repoPath: mgr.RepoPath()}
	}
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
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
		a.recomputePRSectionY()
		// A resize remaps the VT viewport — any in-flight selection is now
		// pointing at stale cells. Drop it.
		a.dashboard.clearSelection()

		// Resize agent terminals to match their current display container.
		if a.view == ViewDashboard {
			a.resizeAllForDashboard()
			if a.dashboard.panelFocus == focusLaunch && a.focusLaunchAgent != nil {
				a.focusLaunchAgent.Resize(a.focusLaunchTermHeight(), a.dashboard.width)
			}
		}

	case initAppMsg:
		if msg.err != nil {
			a.setError(msg.err.Error())
			return a, nil
		}
		a.cfg = msg.cfg
		now := time.Now()
		a.appStart = now
		a.sessionStart = now

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
		a.focusModeActive = resolved.FocusModeEnabled
		a.focusSessionMinutes = resolved.FocusSessionMinutes
		a.focusBreakMinutes = resolved.FocusBreakMinutes

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
				cmds = append(cmds, listenEvents(mgr))
			}
		}
		// Build resume work to run in the background so the TUI renders immediately.
		type resumeItem struct {
			repoPath  string
			mgr       *agent.Manager
			resumeCfg agent.Config
			sessions  []state.SessionState
		}
		var resumeItems []resumeItem
		for _, repo := range msg.cfg.Repos {
			bs, err := state.Load(repo.Path)
			if err != nil || bs == nil {
				continue
			}
			mgr := a.managers[repo.Path]
			if mgr == nil {
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
				},
				sessions: bs.Sessions,
			})
		}
		if len(resumeItems) > 0 {
			cmds = append(cmds, func() tea.Msg {
				var wg sync.WaitGroup
				for _, ri := range resumeItems {
					for _, ss := range ri.sessions {
						wg.Add(1)
						go func(mgr *agent.Manager, ss state.SessionState, cfg agent.Config) {
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

	case tickMsg:
		// Break mode: advance animation and detect timer expiry. We DO NOT
		// auto-exit — once the configured break elapses we flip into a
		// "ready" state and wait for the user to explicitly resume. This
		// avoids dropping the user back into focus mode while they're still
		// away from the keyboard.
		if a.focusBreakMode {
			a.focusBreakAnimFrame++
			if a.focusBreakMinutes > 0 && !a.focusBreakTimerUp &&
				time.Since(a.focusBreakStart) >= time.Duration(a.focusBreakMinutes)*time.Minute {
				a.focusBreakTimerUp = true
				a.focusBreakShortWarning = false
				a.focusBreakAnimFrame = 0
				// One unmistakable cue when the break ends. Played even in
				// focus mode (the normal suppression path), since the whole
				// point is to grab attention.
				if a.audioPlayer != nil {
					a.audioPlayer.Play()
				}
			}
		} else if a.focusModeActive && a.focusSessionMinutes > 0 &&
			a.dashboard.panelFocus != focusLaunch &&
			time.Since(a.sessionStart) >= time.Duration(a.focusSessionMinutes)*time.Minute {
			// Auto-enter break when the work block elapses. The asymmetry
			// with break-end (which waits for explicit `b`) is intentional:
			// end-of-block SHOULD interrupt the user — that's the whole
			// point of the timer — whereas end-of-break should not drag
			// them back from the keyboard. Deferred while in focusLaunch
			// (fullscreen agent terminal); fires the moment they pop back.
			a.focusBreakMode = true
			a.focusBreakStart = time.Now().Round(0)
			a.focusBreakShortWarning = false
			a.focusBreakTimerUp = false
			a.focusBreakAnimFrame = 0
			// Bypass focus-mode chime suppression — same rationale as
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
			if prev, ok := a.lastKnownStatus[ag.ID]; ok {
				if prev == agent.StatusActive && currentStatus == agent.StatusIdle && !ag.IsShell {
					idleTransition = true
				}
			}
			a.lastKnownStatus[ag.ID] = currentStatus
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
					if a.focusLaunchAgent != nil && item.agent.ID == a.focusLaunchAgent.ID {
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
			entry := a.diffStatsCache[sess.ID]
			stale := entry == nil || time.Since(entry.lastRefresh) > 5*time.Second
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

	case agentEventMsg:
		// Clean up stale lastKnownStatus entries when a session auto-closes.
		if msg.event.Type == agent.EventSessionClosed && msg.event.SessionID != "" {
			prefix := msg.event.SessionID + "-agent-"
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
			// In focus mode, StatusIdle chimes are suppressed — only
			// StatusWaiting (permission prompts) still fires.
			if msg.event.Status == agent.StatusIdle || msg.event.Status == agent.StatusWaiting {
				if mgr := a.managers[msg.repoPath]; mgr != nil {
					if ag := mgr.Get(msg.event.AgentID); ag != nil && !ag.IsShell {
						if ag.HasReceivedInput() && !ag.ChimedForTurn() {
							resolved := a.resolvedCache[msg.repoPath]
							chimeAllowed := !a.focusModeActive || msg.event.Status == agent.StatusWaiting
							if resolved.AudioEnabled && a.audioPlayer != nil && chimeAllowed {
								a.audioPlayer.Play()
								ag.MarkChimedForTurn()
							}
						}
					}
				}
			}
			a.lastKnownStatus[msg.event.AgentID] = msg.event.Status
			if msg.event.Status == agent.StatusDone || msg.event.Status == agent.StatusError {
				if mgr := a.managers[msg.repoPath]; mgr != nil {
					if _, sess := mgr.FindAgentAndSession(msg.event.AgentID); sess != nil {
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
		// Branch rename invalidates any PR-by-branch lookup. Schedule a burst of
		// short-interval polls so the SHA-based lookup can rediscover the PR
		// quickly — do NOT clear the cache here; that happens only when the
		// next poll confirms the PR is gone (handled in prPollMsg).
		if msg.event.Type == agent.EventBranchRenamed && msg.event.SessionID != "" {
			ps := a.prPollStates[msg.event.SessionID]
			if ps == nil {
				ps = &prSessionState{}
				a.prPollStates[msg.event.SessionID] = ps
			}
			ps.burstUntil = time.Now().Add(60 * time.Second)
			ps.lastPoll = time.Time{}
			// Clearing lastRemoteSHA forces getRemoteSHA to re-check against
			// the new branch name on the next tick instead of comparing against
			// the SHA it read under the old branch.
			ps.lastRemoteSHA = ""
			ps.lastSHACheck = time.Time{}
		}
		// Refresh list on any agent event — all repos are visible in the dashboard.
		a.refreshAgentList()
		if mgr := a.managers[msg.repoPath]; mgr != nil {
			return a, listenEvents(mgr)
		}
		return a, nil

	case createResultMsg:
		if msg.err != nil {
			a.setError(msg.err.Error())
			return a, nil
		}
		a.agentsCreatedCount++
		if msg.sessionID != "" && msg.agentID != "" {
			// Count new sessions (agentID present means a session was also created).
			// Distinguish new session (sessionID returned fresh from CreateSession)
			// vs AddAgent (sessionID pre-existing) by checking if agentID is the
			// first agent in the session.
			if mgr := a.managers[a.activeRepo]; mgr != nil {
				for _, sess := range mgr.ListSessions() {
					if sess.ID == msg.sessionID && sess.AgentCount() == 1 {
						a.sessionsCreatedCount++
						break
					}
				}
			}
		}
		a.refreshAgentList()
		// Find the new agent by ID, select it, and auto-focus its terminal.
		if msg.agentID != "" {
			for i, item := range a.dashboard.items {
				if item.kind == listItemAgent && item.agent != nil && item.agent.ID == msg.agentID {
					a.dashboard.selected = i
					if a.focusModeActive {
						a.focusLaunchAgent = item.agent
						a.focusLaunchSession = item.session
						a.dashboard.panelFocus = focusLaunch
						a.dashboard.scrollOffset = 0
						item.agent.Resize(a.focusLaunchTermHeight(), a.dashboard.width)
					} else {
						a.dashboard.panelFocus = focusTerminal
					}
					break
				}
			}
		}
		// Set initial dimensions before any output arrives. The alt-screen
		// transition detector in the tick handler will fire a follow-up resize
		// once Claude's TUI enters alternate screen mode.
		// Skip the bulk resize when focusLaunch is active — the agent was already
		// resized to fullscreen above and resizeSelectedForDashboard would shrink it.
		if a.dashboard.panelFocus != focusLaunch {
			a.resizeSelectedForDashboard()
		}
		return a, nil

	case killResultMsg:
		// Clean up closing-set entries regardless of error so the UI never
		// gets stuck rendering "closing…" on a row whose kill failed.
		switch msg.scope {
		case killScopeAgent:
			delete(a.closingAgents, msg.agentID)
			delete(a.lastKnownStatus, msg.agentID)
			// Exit focusLaunch if the killed agent is the one being viewed.
			if a.focusLaunchAgent != nil && a.focusLaunchAgent.ID == msg.agentID {
				a.focusLaunchAgent = nil
				a.focusLaunchSession = nil
				a.dashboard.panelFocus = focusList
				a.dashboard.scrollOffset = 0
			}
		case killScopeSession:
			delete(a.closingSessions, msg.sessionID)
			for _, id := range msg.agentIDs {
				delete(a.closingAgents, id)
				delete(a.lastKnownStatus, id)
				if a.focusLaunchAgent != nil && a.focusLaunchAgent.ID == id {
					a.focusLaunchAgent = nil
					a.focusLaunchSession = nil
					a.dashboard.panelFocus = focusList
					a.dashboard.scrollOffset = 0
				}
			}
			delete(a.diffStatsCache, msg.sessionID)
		}
		if msg.err != nil {
			a.setError(msg.err.Error())
		}
		a.refreshAgentList()
		a.updateDashboardDiffStats()
		return a, nil

	case diffStatsMsg:
		a.diffRefreshInFlight = false
		// Always update cache timestamp to prevent tight retry loops on persistent errors.
		a.diffStatsCache[msg.sessionID] = &diffStatsEntry{
			stats:       msg.stats,
			lastRefresh: time.Now(),
		}
		// Update dashboard with current session's stats.
		if sess := a.dashboard.selectedSession(); sess != nil && sess.ID == msg.sessionID {
			a.dashboard.diffStats = msg.stats
		}
		return a, nil

	case prPollMsg:
		a.prPollsInFlight--
		if a.prPollsInFlight < 0 {
			a.prPollsInFlight = 0
		}
		ps := a.prPollStates[msg.sessionID]
		if ps != nil {
			ps.inFlight = false
		}
		// Fetch failed: preserve cache so a transient error doesn't blank the UI.
		if msg.err != nil {
			return a, nil
		}
		// Lookup succeeded with no PR. Apply a 2-consecutive-nil grace period
		// before evicting the cache: a single nil is common during the rename
		// gap (branch pushed under old name, remote SHA not yet updated) or a
		// rapid force-push window. Two in a row means the PR is genuinely gone.
		if msg.pr == nil {
			if _, had := a.prCache[msg.sessionID]; had {
				// ps is always non-nil here: pollAllSessions initialises it before
				// dispatching a poll, and prPollMsg can only arrive after dispatch.
				// The nil guard is defensive; if ps were nil we skip the grace period
				// and evict immediately rather than dereference.
				if ps != nil {
					ps.consecutiveNilPolls++
					if ps.consecutiveNilPolls < 2 {
						return a, nil
					}
				}
				delete(a.prCache, msg.sessionID)
				if ps != nil {
					ps.lastCheckState = ""
					ps.consecutiveNilPolls = 0
				}
				a.updateDashboardPRCache()
			}
			return a, nil
		}
		// Successful poll: reset the nil counter.
		if ps != nil {
			ps.consecutiveNilPolls = 0
		}
		a.prCache[msg.sessionID] = &prCacheEntry{
			pr:      msg.pr,
			checks:  msg.checks,
			reviews: msg.reviews,
			stack:   msg.stack,
		}
		// Detect PR merge/close and transition to Complete lifecycle phase.
		if msg.pr != nil && (msg.pr.State == "merged" || msg.pr.State == "closed") {
			repoPath := a.repoPathForSession(msg.sessionID)
			if repoPath != "" {
				if mgr := a.managers[repoPath]; mgr != nil {
					if sess := mgr.GetSession(msg.sessionID); sess != nil {
						if sess.LifecyclePhase() == agent.LifecycleShipping {
							sess.SetLifecyclePhase(agent.LifecycleComplete)
						}
					}
				}
			}
		}
		// Detect check state transitions and fire notifications.
		if ps != nil && msg.checks != nil {
			prevState := ps.lastCheckState
			newState := msg.checks.State
			if prevState == "pending" && (newState == "success" || newState == "failure") {
				// Flash the session row.
				ps.flashUntil = time.Now().Add(2 * time.Second)
				if newState == "success" {
					ps.flashColor = "success"
				} else {
					ps.flashColor = "error"
				}
				// Play audio notification, gated by the session's repo AudioEnabled setting
				// (same gate as the idle-transition notification above).
				if a.audioPlayer != nil {
					repoPath := a.repoPathForSession(msg.sessionID)
					if repoPath != "" && a.resolvedCache[repoPath].AudioEnabled {
						a.audioPlayer.Play()
					}
				}
			}
			ps.lastCheckState = newState
		}
		a.updateDashboardPRCache()
		return a, nil

	case resumeDoneMsg:
		for _, repoPath := range msg.repoPaths {
			_ = state.Remove(repoPath)
		}
		a.refreshAgentList()
		return a, nil

	case reviewDiffMsg:
		if msg.err == nil && msg.entry != nil {
			a.reviewDiffCache[msg.sessionID] = msg.entry
		}
		return a, nil

	}

	// Route to the active view.
	switch a.view {
	case ViewDashboard:
		return a.updateDashboard(msg)
	case ViewDiff:
		return a.updateDiff(msg)
	case ViewFileBrowser:
		return a.updateFileBrowser(msg)
	case ViewGlobalConfig:
		return a.updateGlobalConfig(msg)
	case ViewBranchPicker:
		return a.updateBranchPicker(msg)
	case ViewRepoPicker:
		return a.updateRepoPicker(msg)
	}

	return a, nil
}

// addRepo adds a new repo to config, creates its manager, and starts listening.
// Returns a cmd if a new manager was created.
func (a *App) addRepo(path string) tea.Cmd {
	if a.cfg == nil {
		return nil
	}
	if err := config.AddRepo(a.cfg, path); err != nil {
		a.setError(err.Error())
		return nil
	}
	if err := config.Save(a.cfg); err != nil {
		a.setError(err.Error())
	}
	// Resolve to the absolute path that AddRepo stored.
	absPath := a.cfg.Repos[len(a.cfg.Repos)-1].Path

	// Load repo settings and build resolved cache for new repo.
	rs, _ := config.LoadRepoSettings(absPath)
	a.repoSettings[absPath] = rs
	a.resolvedCache[absPath] = config.Resolve(a.globalSettings, rs)

	if a.managers[absPath] == nil {
		mgr := agent.NewManager(absPath, a.resolvedCache[absPath])
		a.managers[absPath] = mgr
		ensureGitignore(absPath)
		a.refreshAgentList()
		return listenEvents(mgr)
	}
	a.refreshAgentList()
	return nil
}

func (a App) updateDashboard(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case configFormSaveMsg:
		// Repo config form saved.
		if a.dashboard.repoConfigForm != nil && a.dashboard.configRepoPath != "" {
			repoPath := a.dashboard.configRepoPath
			alias := strings.TrimSpace(a.dashboard.repoConfigForm.textValue("Alias"))
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
		a.dashboard.panelFocus = focusList
		a.dashboard.repoConfigForm = nil
		return a, nil

	case configFormCancelMsg:
		// Repo config form cancelled.
		a.dashboard.panelFocus = focusList
		a.dashboard.repoConfigForm = nil
		return a, nil

	case tea.PasteMsg:
		// focusLaunch: forward paste to the launch agent. Other panels fall through
		// to dashboard.Update, which handles paste for focusTerminal.
		if a.dashboard.panelFocus == focusLaunch && a.focusLaunchAgent != nil {
			a.focusLaunchAgent.Paste(msg.Content)
			return a, nil
		}

	case tea.KeyPressMsg:
		// focusLaunch: forward all keys to the launch agent; esc/ctrl+e returns to focus pipeline.
		if a.dashboard.panelFocus == focusLaunch {
			a.confirmQuit = false
			if a.focusLaunchAgent == nil {
				a.dashboard.panelFocus = focusList
				a.dashboard.scrollOffset = 0
				return a, nil
			}
			switch msg.String() {
			case "esc", "ctrl+e":
				a.resizeAgentForDashboard(a.focusLaunchAgent)
				a.focusLaunchAgent = nil
				a.focusLaunchSession = nil
				a.dashboard.panelFocus = focusList
				a.dashboard.scrollOffset = 0
			case "shift+esc":
				a.focusLaunchAgent.SendKey(xvt.KeyPressEvent{Code: tea.KeyEscape})
			case "pgup":
				sbLines := len(a.focusLaunchAgent.ScrollbackLines())
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
				if a.focusLaunchSession != nil {
					agents := a.focusLaunchSession.Agents()
					idx := 0
					for i, ag := range agents {
						if ag.ID == a.focusLaunchAgent.ID {
							idx = i
							break
						}
					}
					if msg.String() == "alt+]" {
						idx = (idx + 1) % len(agents)
					} else {
						idx = (idx - 1 + len(agents)) % len(agents)
					}
					a.focusLaunchAgent = agents[idx]
					a.dashboard.scrollOffset = 0
					a.focusLaunchAgent.Resize(a.focusLaunchTermHeight(), a.dashboard.width)
				}
			case "ctrl+t":
				if a.focusLaunchSession != nil {
					repoPath := a.repoPathForSession(a.focusLaunchSession.ID)
					mgr := a.managers[repoPath]
					if mgr != nil {
						resolved := a.resolvedCache[repoPath]
						cfg := agent.Config{
							Rows:              a.focusLaunchTermHeight(),
							Cols:              a.dashboard.width,
							BypassPermissions: resolved.BypassPermissions,
						}
						if newAg, err := mgr.AddShell(a.focusLaunchSession.ID, cfg); err == nil {
							a.focusLaunchAgent = newAg
							a.dashboard.scrollOffset = 0
						} else {
							a.setError(err.Error())
						}
					}
				}
			case "ctrl+n":
				if a.focusLaunchSession != nil {
					repoPath := a.repoPathForSession(a.focusLaunchSession.ID)
					mgr := a.managers[repoPath]
					if mgr != nil {
						resolved := a.resolvedCache[repoPath]
						cfg := agent.Config{
							Rows:              a.focusLaunchTermHeight(),
							Cols:              a.dashboard.width,
							BypassPermissions: resolved.BypassPermissions,
							AgentProgram:      resolved.AgentProgram,
						}
						if newAg, err := mgr.AddAgent(a.focusLaunchSession.ID, cfg); err == nil {
							a.focusLaunchAgent = newAg
							a.dashboard.scrollOffset = 0
						} else {
							a.setError(err.Error())
						}
					}
				}
			case "ctrl+w":
				if a.focusLaunchSession == nil || a.focusLaunchAgent == nil {
					return a, nil
				}
				agents := a.focusLaunchSession.Agents()
				if len(agents) == 0 {
					a.focusLaunchAgent = nil
					a.focusLaunchSession = nil
					a.dashboard.panelFocus = focusList
					a.dashboard.scrollOffset = 0
					return a, nil
				}
				oldID := a.focusLaunchAgent.ID
				sessionID := a.focusLaunchSession.ID
				currentIdx := 0
				for i, ag := range agents {
					if ag.ID == oldID {
						currentIdx = i
						break
					}
				}
				if len(agents) == 1 {
					a.focusLaunchAgent = nil
					a.focusLaunchSession = nil
					a.dashboard.panelFocus = focusList
					a.dashboard.scrollOffset = 0
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
				var nextIdx int
				if currentIdx == len(agents)-1 {
					nextIdx = currentIdx - 1
				} else {
					nextIdx = currentIdx + 1
				}
				a.focusLaunchAgent = agents[nextIdx]
				a.focusLaunchAgent.Resize(a.focusLaunchTermHeight(), a.dashboard.width)
				a.dashboard.scrollOffset = 0
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
			default:
				if msg.Text != "" {
					a.focusLaunchAgent.SendText(msg.Text)
				} else {
					a.focusLaunchAgent.SendKey(xvt.KeyPressEvent(msg))
				}
			}
			return a, nil
		}

		// When the terminal or config panel has focus, skip all app-level bindings.
		if a.dashboard.panelFocus == focusTerminal || a.dashboard.panelFocus == focusConfig {
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
		if a.focusBacklogWarning && msg.String() != "n" {
			a.focusBacklogWarning = false
		}

		// Clear the break short-warning on any key that isn't b.
		if a.focusBreakShortWarning && msg.String() != "b" {
			a.focusBreakShortWarning = false
		}

		// Review panel key handling.
		if a.dashboard.panelFocus == focusReview && a.reviewSession != nil {
			switch msg.String() {
			case "esc":
				// Return to focus mode; session stays InReview.
				a.dashboard.panelFocus = focusList
				a.reviewSession = nil
				return a, nil
			case "d":
				// Defer: back to ReadyForReview, return to focus.
				a.reviewSession.SetLifecyclePhase(agent.LifecycleReadyForReview)
				a.dashboard.panelFocus = focusList
				a.reviewSession = nil
				return a, nil
			case "p":
				// Ship: open PR URL if one exists, transition to Shipping.
				sess := a.reviewSession
				if entry := a.prCache[sess.ID]; entry != nil && entry.pr != nil && entry.pr.URL != "" {
					if err := openURL(entry.pr.URL); err != nil {
						a.setError(err.Error())
						return a, nil
					}
					sess.SetLifecyclePhase(agent.LifecycleShipping)
					a.dashboard.panelFocus = focusList
					a.reviewSession = nil
				} else {
					a.setError("no open PR yet — create one in GitHub first")
				}
				return a, nil
			case "e":
				// Open in editor — same pattern as the existing "i" key handler.
				sess := a.reviewSession
				if sess != nil && sess.Worktree != nil {
					repoPath := a.activeRepo
					ideCmd := strings.TrimSpace(a.resolvedCache[repoPath].IDECommand)
					if ideCmd == "" {
						a.setError("No IDE configured (set 'IDE Command' in settings)")
						return a, nil
					}
					parts := splitIDECommand(ideCmd)
					if len(parts) == 0 {
						a.setError("No IDE configured (set 'IDE Command' in settings)")
						return a, nil
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
				return a, nil
			}
			// All other keys are no-ops in review panel.
			return a, nil
		}

		// Focus-mode key handling (fullscreen pipeline view).
		if a.focusModeActive && a.dashboard.panelFocus != focusReview {
			switch msg.String() {
			case "up", "k":
				a.moveFocusCursorUp()
				a.syncFocusCursorToDashboard()
				return a, nil
			case "down", "j":
				a.moveFocusCursorDown()
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
			case "m":
				// Mark the session under the focus cursor as ReadyForReview.
				inProgress := a.dashboard.allInProgressSessions()
				if a.focusActiveIdx < len(inProgress) {
					sess := inProgress[a.focusActiveIdx].session
					if sess != nil && !sess.DoneAt().IsZero() {
						sess.SetLifecyclePhase(agent.LifecycleReadyForReview)
						a.focusQueueIndex = 0
						return a, a.fetchReviewDiffCmd(sess)
					}
				}
				return a, nil
			case "r":
				reviewItems := a.dashboard.reviewQueueSessions()
				if len(reviewItems) == 0 {
					return a, nil
				}
				idx := a.focusQueueIndex
				if idx >= len(reviewItems) {
					idx = len(reviewItems) - 1
				}
				sess := reviewItems[idx].session
				sess.SetLifecyclePhase(agent.LifecycleInReview)
				a.reviewSession = sess
				a.dashboard.panelFocus = focusReview
				// Fetch diff stats if not already cached.
				if _, ok := a.reviewDiffCache[sess.ID]; !ok {
					return a, a.fetchReviewDiffCmd(sess)
				}
				return a, nil
			case "d":
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
		if (msg.String() == "enter" || msg.String() == "right") && a.dashboard.panelFocus == focusList {
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

		switch msg.String() {
		case "f":
			// Toggle focus mode. When exiting focus mode (entering review),
			// record the review time.
			if a.focusModeActive {
				a.lastReviewAt = time.Now()
				a.focusLaunchAgent = nil
				a.focusLaunchSession = nil
			}
			a.focusModeActive = !a.focusModeActive
			a.focusModeSwitches++
			if a.focusModeActive {
				a.dashboard.panelFocus = focusList
				a.dashboard.clampToRepo()
				// Park the cursor on the first non-empty section so the
				// selection marker is visible the moment focus mode opens,
				// rather than waiting for the next tick.
				a.focusCursorSection = focusSectionActive
				a.clampFocusCursor()
				a.syncFocusCursorToDashboard()
			}
			return a, nil

		case "b":
			if !a.focusModeActive {
				return a, nil
			}
			switch {
			case !a.focusBreakMode:
				// Enter break. Round(0) strips the monotonic reading so
				// time.Since uses wall-clock arithmetic, which keeps the
				// timer honest across suspend/resume.
				a.focusBreakMode = true
				a.focusBreakStart = time.Now().Round(0)
				a.focusBreakShortWarning = false
				a.focusBreakTimerUp = false
				a.focusBreakAnimFrame = 0
			case a.focusBreakTimerUp:
				// Break is fully elapsed; user is opting back in. Exit
				// without any "are you sure" friction.
				a.sessionStart = time.Now()
				a.focusBlockCount++
				a.focusBreakMode = false
				a.focusBreakShortWarning = false
				a.focusBreakTimerUp = false
				a.focusBreakAnimFrame = 0
			case !a.focusBreakShortWarning:
				a.focusBreakShortWarning = true
			default:
				// Third b press while still inside the break window:
				// override the short-break guard and end early.
				a.sessionStart = time.Now()
				a.focusBlockCount++
				a.focusBreakMode = false
				a.focusBreakShortWarning = false
				a.focusBreakTimerUp = false
				a.focusBreakAnimFrame = 0
			}
			return a, nil

		case "n":
			// Create a new session in the repo of the currently selected item.
			repoPath := a.dashboard.selectedRepoPath()
			if repoPath == "" {
				repoPath = a.activeRepo
			}
			if repoPath == "" {
				return a, nil
			}
			a.activeRepo = repoPath

			// Soft agent-count guidance in focus mode.
			if a.focusModeActive {
				resolved := a.resolvedCache[repoPath]
				if resolved.MaxConcurrentAgents > 0 && a.activeAgentCount() >= resolved.MaxConcurrentAgents {
					if !a.agentLimitModalActive {
						a.agentLimitModalActive = true
						return a, nil
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
						if !a.focusBacklogWarning {
							a.focusBacklogWarning = true
							a.setError(fmt.Sprintf("n again to override — %d sessions awaiting review", backlogCount))
							return a, nil
						}
						a.focusBacklogWarning = false // second n: proceed
					} else {
						a.focusBacklogWarning = false
					}
				}
			}

			fixedW := a.dashboard.fixedTermWidth()
			fixedH := a.dashboard.fixedTermHeight()
			if fixedW <= 0 || fixedH <= 0 {
				a.setError("Terminal size not yet known; try again")
				return a, nil
			}
			resolved := a.resolvedCache[repoPath]
			mgr := a.managers[repoPath]
			if mgr == nil {
				return a, nil
			}
			cfg := agent.Config{
				Rows:              fixedH,
				Cols:              fixedW,
				BypassPermissions: resolved.BypassPermissions,
				AgentProgram:      resolved.AgentProgram,
			}
			return a, func() tea.Msg {
				sess, ag, err := mgr.CreateSession(cfg)
				if err != nil {
					return createResultMsg{err: err}
				}
				return createResultMsg{sessionID: sess.ID, agentID: ag.ID}
			}

		case "c":
			// Add an agent to the selected session.
			sess := a.dashboard.selectedSession()
			if sess == nil {
				a.setError("No session selected")
				return a, nil
			}
			repoPath := a.dashboard.selectedRepoPath()
			if repoPath == "" {
				repoPath = a.activeRepo
			}
			mgr := a.managers[repoPath]
			if mgr == nil {
				return a, nil
			}
			fixedW := a.dashboard.fixedTermWidth()
			fixedH := a.dashboard.fixedTermHeight()
			if fixedW <= 0 || fixedH <= 0 {
				a.setError("Terminal size not yet known; try again")
				return a, nil
			}
			resolved := a.resolvedCache[repoPath]
			cfg := agent.Config{
				Rows:              fixedH,
				Cols:              fixedW,
				BypassPermissions: resolved.BypassPermissions,
				AgentProgram:      resolved.AgentProgram,
			}
			sessionID := sess.ID
			return a, func() tea.Msg {
				ag, err := mgr.AddAgent(sessionID, cfg)
				if err != nil {
					return createResultMsg{err: err}
				}
				return createResultMsg{sessionID: sessionID, agentID: ag.ID}
			}

		case "e":
			// Open the selected session's worktree in the configured IDE.
			sess := a.dashboard.selectedSession()
			if sess == nil {
				a.setError("No session selected")
				return a, nil
			}
			repoPath := a.dashboard.selectedRepoPath()
			ideCmd := strings.TrimSpace(a.resolvedCache[repoPath].IDECommand)
			if ideCmd == "" {
				a.setError("No IDE configured (set 'IDE Command' in settings)")
				return a, nil
			}
			parts := splitIDECommand(ideCmd)
			if len(parts) == 0 {
				a.setError("No IDE configured (set 'IDE Command' in settings)")
				return a, nil
			}
			worktreePath := sess.Worktree.Path
			exe := parts[0]
			args := append(parts[1:], worktreePath)
			go func() {
				cmd := exec.Command(exe, args...)
				cmd.Dir = worktreePath
				_ = cmd.Start()
			}()
			return a, nil

		case "a":
			// Open file browser to add a new repo.
			a.repoBrowser = newFileBrowserModel()
			a.repoBrowser.width = a.width
			a.repoBrowser.height = a.height - 1
			a.view = ViewFileBrowser
			return a, nil

		case "o":
			// Open branch picker to create session on existing branch/PR.
			repoPath := a.dashboard.selectedRepoPath()
			if repoPath == "" {
				repoPath = a.activeRepo
			}
			if repoPath == "" {
				a.setError("No repo available")
				return a, nil
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
			return a, loadBranchPickerData(repoPath, a.ghClient, activeBranches)

		case "t":
			// Open or focus a shell terminal in the selected session.
			sess := a.dashboard.selectedSession()
			if sess == nil {
				a.setError("No session selected")
				return a, nil
			}
			if sess.HasShell() {
				// Shell exists — select it and enter focusTerminal.
				for i, item := range a.dashboard.items {
					if item.kind == listItemAgent && item.agent != nil && item.agent.IsShell && item.session == sess {
						a.dashboard.selected = i
						a.dashboard.panelFocus = focusTerminal
						break
					}
				}
				return a, nil
			}
			repoPath := a.dashboard.selectedRepoPath()
			if repoPath == "" {
				repoPath = a.activeRepo
			}
			mgr := a.managers[repoPath]
			if mgr == nil {
				return a, nil
			}
			fixedW := a.dashboard.fixedTermWidth()
			fixedH := a.dashboard.fixedTermHeight()
			if fixedW <= 0 || fixedH <= 0 {
				a.setError("Terminal size not yet known; try again")
				return a, nil
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
			}

		case "p":
			// Open the selected session's PR in the browser.
			sess := a.dashboard.selectedSession()
			if sess != nil {
				if entry := a.prCache[sess.ID]; entry != nil && entry.pr != nil && entry.pr.URL != "" {
					if err := openURL(entry.pr.URL); err != nil {
						a.setError(err.Error())
					}
				}
			}
			return a, nil

		case "s":
			// Open global settings overlay.
			a.globalConfig = newGlobalConfigModel(a.globalSettings, a.width, a.height)
			a.view = ViewGlobalConfig
			return a, nil

		case "d":
			item := a.dashboard.selectedItem()
			if item == nil {
				return a, nil
			}
			if item.kind == listItemSession || item.kind == listItemAgent {
				// Diff the session's worktree.
				sess := item.session
				if sess == nil {
					return a, nil
				}
				rawDiff, err := git.Diff(item.repoPath, sess.Worktree)
				if err != nil {
					a.setError(err.Error())
					return a, nil
				}
				m, err := diffmodel.Parse(rawDiff)
				if err != nil {
					a.setError(err.Error())
					return a, nil
				}
				a.view = ViewDiff
				a.diff = newDiffModel(sess.GetDisplayName(), m, a.width, a.height-1)
				return a, nil
			}
			// Repo header selected — remove the repo.
			if a.cfg != nil {
				_ = config.RemoveRepo(a.cfg, item.repoPath)
				if err := config.Save(a.cfg); err != nil {
					a.setError(err.Error())
				}
				a.refreshAgentList()
			}
			return a, nil

		case "x":
			// Kill the selected agent asynchronously so the UI stays responsive.
			item := a.dashboard.selectedItem()
			if item == nil || item.kind != listItemAgent || item.agent == nil || item.session == nil {
				return a, nil
			}
			mgr := a.managers[item.repoPath]
			if mgr == nil {
				return a, nil
			}
			agentID := item.agent.ID
			sessionID := item.session.ID
			// Already dispatched — no-op to avoid double-kills.
			if a.closingAgents[agentID] {
				return a, nil
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
			}

		case "X":
			// Kill the entire parent session of the selected agent asynchronously.
			item := a.dashboard.selectedItem()
			if item == nil || item.session == nil {
				return a, nil
			}
			mgr := a.managers[item.repoPath]
			if mgr == nil {
				return a, nil
			}
			sessID := item.session.ID
			// Already dispatched — no-op.
			if a.closingSessions[sessID] {
				return a, nil
			}
			var agentIDs []string
			for _, ag := range item.session.Agents() {
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
			}

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
		if msg.Button == tea.MouseLeft {
			// focusLaunch tab bar click — handled before list/preview panel checks
			// because focusLaunch is fullscreen and occupies all columns.
			if a.dashboard.panelFocus == focusLaunch && a.focusLaunchSession != nil {
				tabBarY := dashboardTopY + 1
				if msg.Y == tabBarY {
					if idx := a.focusLaunchTabIndexAt(msg.X); idx >= 0 {
						agents := a.focusLaunchSession.Agents()
						a.focusLaunchAgent = agents[idx]
						a.dashboard.scrollOffset = 0
						a.focusLaunchAgent.Resize(a.focusLaunchTermHeight(), a.dashboard.width)
					}
					return a, nil
				}
			}
			if msg.X < 31 {
				// List panel click — map Y to item index.
				// Subtract 2 for the SESSIONS title row and separator row.
				itemIndex := msg.Y - dashboardTopY - 2
				contentY := itemIndex // content-relative, same space as prSectionY
				if a.dashboard.prSectionY >= 0 && contentY >= a.dashboard.prSectionY {
					// Click in the PR checks summary section — open PR in browser.
					sess := a.dashboard.selectedSession()
					if sess != nil {
						if entry := a.prCache[sess.ID]; entry != nil && entry.pr != nil && entry.pr.URL != "" {
							if err := openURL(entry.pr.URL); err != nil {
								a.setError(err.Error())
							}
						}
					}
				} else if itemIndex >= 0 && itemIndex < len(a.dashboard.items) {
					a.dashboard.selected = itemIndex
					a.dashboard.clampToAgent()
					a.dashboard.panelFocus = focusList
					a.dashboard.scrollOffset = 0
				}
			} else if msg.X >= 32 {
				// Preview panel click — enter focusTerminal if an agent is selected.
				// X==31 is the list panel's right border and is intentionally ignored.
				if a.dashboard.panelFocus == focusList && a.dashboard.selectedAgent() != nil {
					a.dashboard.panelFocus = focusTerminal
				}
				// Seed a fresh selection if the click landed inside the agent's
				// VT viewport. dragSeen=false until a subsequent motion event
				// confirms an actual drag — a click without drag should not
				// produce a 1-cell selection.
				if a.dashboard.panelFocus == focusTerminal {
					if ag := a.dashboard.selectedAgent(); ag != nil {
						if termX, termY, inVP := a.screenToTermCell(msg.X, msg.Y); inVP {
							a.dashboard.selection = selection{
								anchorX: termX,
								anchorY: termY,
								cursorX: termX,
								cursorY: termY,
								active:  true,
								agentID: ag.ID,
							}
						} else {
							// Click outside the viewport (e.g., on the border)
							// drops any prior selection — matches what the user
							// expects from "click somewhere else".
							a.dashboard.clearSelection()
						}
					}
				} else if a.dashboard.panelFocus == focusLaunch && a.focusLaunchAgent != nil {
					if termX, termY, inVP := a.screenToTermCellFocusLaunch(msg.X, msg.Y); inVP {
						a.dashboard.selection = selection{
							anchorX: termX,
							anchorY: termY,
							cursorX: termX,
							cursorY: termY,
							active:  true,
							agentID: a.focusLaunchAgent.ID,
						}
					} else {
						a.dashboard.clearSelection()
					}
				}
			}
		}
		return a, nil
	}

	if msg, ok := msg.(tea.MouseMotionMsg); ok {
		// Drag updates the cursor end of an in-flight selection. A motion
		// event with the left button still held is the only signal we have
		// that the user is dragging — bubbletea's MouseModeCellMotion gives
		// us these while a button is down.
		if a.dashboard.selection.active && msg.Button == tea.MouseLeft {
			if a.dashboard.panelFocus == focusLaunch && a.focusLaunchAgent != nil {
				tx, ty, inVP := a.screenToTermCellFocusLaunch(msg.X, msg.Y)
				if inVP {
					a.dashboard.selection.cursorX = tx
					a.dashboard.selection.cursorY = ty
					a.dashboard.selection.dragSeen = true
				}
			} else {
				termX, termY, _ := a.screenToTermCell(msg.X, msg.Y)
				if w := a.dashboard.fixedTermWidth(); w > 0 {
					if termX < 0 {
						termX = 0
					} else if termX >= w {
						termX = w - 1
					}
				}
				if h := a.dashboard.fixedTermHeight(); h > 0 {
					if termY < 0 {
						termY = 0
					} else if termY >= h {
						termY = h - 1
					}
				}
				a.dashboard.selection.cursorX = termX
				a.dashboard.selection.cursorY = termY
				if termX != a.dashboard.selection.anchorX || termY != a.dashboard.selection.anchorY {
					a.dashboard.selection.dragSeen = true
				}
			}
		}
		return a, nil
	}

	if _, ok := msg.(tea.MouseReleaseMsg); ok {
		if a.dashboard.selection.active {
			if a.dashboard.selection.dragSeen {
				// Real drag — copy the highlighted region. The highlight
				// stays on screen until the next click clears or replaces it.
				if ag := a.dashboard.selectedAgent(); ag != nil && ag.ID == a.dashboard.selection.agentID {
					if sx, sy, ex, ey, ok := a.dashboard.selectionRect(); ok {
						rect := vt.SelectionRect{
							StartX: sx, StartY: sy, EndX: ex, EndY: ey, Active: true,
						}
						var text string
						if a.dashboard.scrollOffset > 0 {
							vpWidth := a.dashboard.previewTermWidth()
							vpHeight := a.dashboard.previewTermHeight()
							text = ag.ExtractTextFromSnapshot(vpWidth, vpHeight, a.dashboard.scrollOffset, rect)
						} else {
							text = ag.ExtractText(rect)
						}
						if text != "" {
							return a, tea.SetClipboard(text)
						}
					}
				} else if a.dashboard.panelFocus == focusLaunch && a.focusLaunchAgent != nil && a.focusLaunchAgent.ID == a.dashboard.selection.agentID {
					if sx, sy, ex, ey, ok := a.dashboard.selectionRect(); ok {
						rect := vt.SelectionRect{
							StartX: sx, StartY: sy, EndX: ex, EndY: ey, Active: true,
						}
						ag := a.focusLaunchAgent
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
		if a.dashboard.panelFocus == focusLaunch && a.focusLaunchAgent != nil {
			ag := a.focusLaunchAgent
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
		if a.dashboard.panelFocus == focusTerminal {
			ag := a.dashboard.selectedAgent()
			if ag != nil {
				// Alt-screen apps (Claude's /tui fullscreen, vim, less) redraw
				// the viewport instead of scrolling — baton's scrollback is
				// inert for them. Forward the wheel event so the app can drive
				// its own scrollback. SendMouse is a no-op unless the app has
				// enabled mouse reporting.
				if ag.IsAltScreen() {
					a.forwardWheelToAgent(ag, msg)
					return a, nil
				}
				switch msg.Button {
				case tea.MouseWheelUp:
					a.dashboard.scrollOffset += 3
					maxOffset := len(ag.ScrollbackLines())
					if a.dashboard.scrollOffset > maxOffset {
						a.dashboard.scrollOffset = maxOffset
					}
				case tea.MouseWheelDown:
					a.dashboard.scrollOffset -= 3
					if a.dashboard.scrollOffset < 0 {
						a.dashboard.scrollOffset = 0
					}
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
		a.recomputePRSectionY()
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
	// remains focused on the same agent's terminal. Any focus or agent change
	// (sidebar nav, esc, click on the list, etc.) drops it.
	if a.dashboard.selection.active {
		ag := a.dashboard.selectedAgent()
		if (a.dashboard.panelFocus != focusTerminal || ag == nil || ag.ID != a.dashboard.selection.agentID) &&
			(a.dashboard.panelFocus != focusLaunch || a.focusLaunchAgent == nil || a.focusLaunchAgent.ID != a.dashboard.selection.agentID) {
			a.dashboard.clearSelection()
		}
	}
	return a, cmd
}

func (a App) updateFileBrowser(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case fileBrowserSelectMsg:
		// Snapshot count before addRepo so we can tell whether registration
		// actually appended a new entry (vs. failing or dedup'ing). Without this
		// the picker would highlight whatever was already last on failure.
		priorRepoCount := 0
		if a.cfg != nil {
			priorRepoCount = len(a.cfg.Repos)
		}
		cmd := a.addRepo(msg.path)
		if a.repoPickerPending {
			a.repoPickerPending = false
			newPath := a.activeRepo
			if a.cfg != nil && len(a.cfg.Repos) > priorRepoCount {
				newPath = a.cfg.Repos[len(a.cfg.Repos)-1].Path
			}
			counts := make(map[string]int, len(a.cfg.Repos))
			for _, repo := range a.cfg.Repos {
				if mgr := a.managers[repo.Path]; mgr != nil {
					counts[repo.Path] = mgr.AgentCount()
				}
			}
			a.repoPicker.width = a.width
			a.repoPicker.height = a.height - 1
			a.repoPicker.setRepos(a.cfg.Repos, counts, newPath)
			a.view = ViewRepoPicker
			return a, cmd
		}
		a.view = ViewDashboard
		return a, cmd
	case fileBrowserCancelMsg:
		if a.repoPickerPending {
			a.repoPickerPending = false
			a.view = ViewRepoPicker
			return a, nil
		}
		a.view = ViewDashboard
		return a, nil
	}
	var cmd tea.Cmd
	a.repoBrowser, cmd = a.repoBrowser.Update(msg)
	return a, cmd
}

func (a App) updateBranchPicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case branchPickerSelectMsg:
		a.view = ViewDashboard
		item := msg.item

		repoPath := a.activeRepo
		mgr := a.managers[repoPath]
		if mgr == nil {
			a.setError("No manager for repo")
			return a, nil
		}

		fixedW := a.dashboard.fixedTermWidth()
		fixedH := a.dashboard.fixedTermHeight()
		if fixedW <= 0 || fixedH <= 0 {
			a.setError("Terminal size not yet known; try again")
			return a, nil
		}

		resolved := a.resolvedCache[repoPath]
		cfg := agent.Config{
			Rows:              fixedH,
			Cols:              fixedW,
			BypassPermissions: resolved.BypassPermissions,
			AgentProgram:      resolved.AgentProgram,
		}

		branch := item.branch
		baseBranch := item.baseBranch
		return a, func() tea.Msg {
			sess, ag, err := mgr.CreateSessionOnBranch(branch, baseBranch, cfg)
			if err != nil {
				return createResultMsg{err: err}
			}
			return createResultMsg{sessionID: sess.ID, agentID: ag.ID}
		}

	case branchPickerCancelMsg:
		a.view = ViewDashboard
		return a, nil
	}

	var cmd tea.Cmd
	a.branchPicker, cmd = a.branchPicker.Update(msg)
	return a, cmd
}

func (a App) updateRepoPicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case repoPickerSelectMsg:
		a.view = ViewDashboard
		repoPath := msg.path
		if repoPath == "" {
			return a, nil
		}
		a.activeRepo = repoPath
		a.refreshAgentList()
		fixedW := a.dashboard.fixedTermWidth()
		fixedH := a.dashboard.fixedTermHeight()
		if fixedW <= 0 || fixedH <= 0 {
			a.setError("Terminal size not yet known; try again")
			return a, nil
		}
		resolved := a.resolvedCache[repoPath]
		mgr := a.managers[repoPath]
		if mgr == nil {
			return a, nil
		}
		pickerCfg := agent.Config{
			Rows:              fixedH,
			Cols:              fixedW,
			BypassPermissions: resolved.BypassPermissions,
			AgentProgram:      resolved.AgentProgram,
		}
		return a, func() tea.Msg {
			sess, ag, err := mgr.CreateSession(pickerCfg)
			if err != nil {
				return createResultMsg{err: err}
			}
			return createResultMsg{sessionID: sess.ID, agentID: ag.ID}
		}

	case repoPickerAddRepoMsg:
		a.repoPickerPending = true
		a.repoBrowser = newFileBrowserModel()
		a.repoBrowser.width = a.width
		a.repoBrowser.height = a.height - 1
		a.view = ViewFileBrowser
		return a, nil

	case repoPickerCancelMsg:
		a.view = ViewDashboard
		return a, nil
	}

	var cmd tea.Cmd
	a.repoPicker, cmd = a.repoPicker.Update(msg)
	return a, cmd
}

func (a App) updateDiff(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case diffCloseMsg:
		a.view = ViewDashboard
		return a, nil
	}

	var cmd tea.Cmd
	a.diff, cmd = a.diff.Update(msg)
	return a, cmd
}

// initRepoConfigForm creates a config form for the given repo and enters config focus.
func (a *App) initRepoConfigForm(repoPath string) {
	rs := a.repoSettings[repoPath]
	if rs == nil {
		rs = &config.RepoSettings{}
	}

	bypassPerms := config.DefaultBypassPermissions
	if rs.BypassPermissions != nil {
		bypassPerms = *rs.BypassPermissions
	}
	defaultBranch := ""
	if rs.DefaultBranch != nil {
		defaultBranch = *rs.DefaultBranch
	}
	branchPrefix := ""
	if rs.BranchPrefix != nil {
		branchPrefix = *rs.BranchPrefix
	}
	agentProgram := ""
	if rs.AgentProgram != nil {
		agentProgram = *rs.AgentProgram
	}
	ideCommand := ""
	if rs.IDECommand != nil {
		ideCommand = *rs.IDECommand
	}
	worktreeDir := ""
	if rs.WorktreeDir != nil {
		worktreeDir = *rs.WorktreeDir
	}
	alias := ""
	for _, r := range a.cfg.Repos {
		if r.Path == repoPath {
			alias = r.Alias
			break
		}
	}

	inputWidth := 30
	var fields []formField
	fields = addTextInput(fields, "Alias", alias, "short nickname", inputWidth)
	fields = addToggle(fields, "Bypass Permissions", bypassPerms)
	fields = addTextInput(fields, "Default Branch", defaultBranch, "auto-detect", inputWidth)
	fields = addTextInput(fields, "Branch Prefix", branchPrefix, config.DefaultBranchPrefix, inputWidth)
	fields = addTextInput(fields, "Agent Program", agentProgram, config.DefaultAgentProgram, inputWidth)
	fields = addEditorFields(fields, ideCommand, inputWidth)
	fields = addTextInput(fields, "Worktree Directory", worktreeDir, config.DefaultWorktreeDir, inputWidth)

	form := newConfigForm(fields, a.dashboard.previewTermWidth())
	a.dashboard.repoConfigForm = &form
	a.dashboard.configRepoPath = repoPath
	a.dashboard.panelFocus = focusConfig
}

// extractRepoSettings reads form values and creates a RepoSettings struct.
func (a App) extractRepoSettings() *config.RepoSettings {
	form := a.dashboard.repoConfigForm
	if form == nil {
		return &config.RepoSettings{}
	}
	s := &config.RepoSettings{}

	bypassPerms := form.toggleValue("Bypass Permissions")
	s.BypassPermissions = &bypassPerms

	if v := form.textValue("Default Branch"); v != "" {
		s.DefaultBranch = &v
	}
	if v := form.textValue("Branch Prefix"); v != "" {
		s.BranchPrefix = &v
	}
	if v := form.textValue("Agent Program"); v != "" {
		s.AgentProgram = &v
	}
	if v := extractIDECommand(*form); v != "" {
		s.IDECommand = &v
	}
	if v := form.textValue("Worktree Directory"); v != "" {
		s.WorktreeDir = &v
	}
	return s
}

func (a App) updateGlobalConfig(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case globalConfigSaveMsg:
		// Persist global settings.
		if err := config.SaveGlobalSettings(msg.settings); err != nil {
			a.setError(err.Error())
			a.view = ViewDashboard
			return a, nil
		}
		a.globalSettings = msg.settings
		// Rebuild resolved cache and push to all managers.
		for repoPath, rs := range a.repoSettings {
			a.resolvedCache[repoPath] = config.Resolve(a.globalSettings, rs)
			if mgr := a.managers[repoPath]; mgr != nil {
				mgr.UpdateSettings(a.resolvedCache[repoPath])
			}
		}
		newResolved := config.Resolve(a.globalSettings, nil)
		if newResolved.SidebarWidth != a.dashboard.sidebarWidth {
			a.dashboard.sidebarWidth = newResolved.SidebarWidth
			a.resizeAllForDashboard()
		}
		// Refresh wellness settings from updated global config. FocusModeEnabled
		// is the startup default; saving the form must not override the live
		// runtime toggle state — only `f` controls that.
		a.focusSessionMinutes = newResolved.FocusSessionMinutes
		a.focusBreakMinutes = newResolved.FocusBreakMinutes
		a.view = ViewDashboard
		return a, nil
	case globalConfigCancelMsg:
		a.view = ViewDashboard
		return a, nil
	}

	var cmd tea.Cmd
	a.globalConfig, cmd = a.globalConfig.Update(msg)
	return a, cmd
}

// resizeSelectedForDashboard resizes the currently selected agent's VT and PTY
// to match the dashboard preview panel dimensions.
func (a *App) resizeSelectedForDashboard() {
	ag := a.dashboard.selectedAgent()
	if ag == nil {
		return
	}
	w := a.dashboard.fixedTermWidth()
	h := a.dashboard.fixedTermHeight()
	if w > 0 && h > 0 {
		ag.Resize(h, w)
	}
}

// resizeAgentForDashboard resizes a specific agent to the dashboard preview dimensions.
func (a *App) resizeAgentForDashboard(ag *agent.Agent) {
	if ag == nil {
		return
	}
	w := a.dashboard.fixedTermWidth()
	h := a.dashboard.fixedTermHeight()
	if w > 0 && h > 0 {
		ag.Resize(h, w)
	}
}

// resizeAllForDashboard resizes every agent to the dashboard preview dimensions.
// Called on WindowSizeMsg so all agents match the new terminal size.
func (a *App) resizeAllForDashboard() {
	w := a.dashboard.fixedTermWidth()
	h := a.dashboard.fixedTermHeight()
	if w <= 0 || h <= 0 {
		return
	}
	for _, ag := range a.dashboard.agentItems() {
		if a.focusLaunchAgent != nil && ag.ID == a.focusLaunchAgent.ID {
			continue
		}
		ag.Resize(h, w)
	}
}

// setError sets an error message that displays for ~3 seconds (30 ticks at 100ms).
func (a *App) setError(msg string) {
	a.err = msg
	a.errTicks = 30
}

// activeRepoDisplayName returns the display name for the active repo (alias or base path).
func (a App) activeRepoDisplayName() string {
	if a.activeRepo == "" {
		return ""
	}
	if a.cfg != nil {
		for _, repo := range a.cfg.Repos {
			if repo.Path == a.activeRepo {
				return repo.DisplayName()
			}
		}
	}
	return filepath.Base(a.activeRepo)
}

// screenToTermCell converts a screen-space mouse coordinate to a VT cell
// coordinate inside the agent preview viewport. inViewport is false when the
// point lies outside the viewport rectangle — callers that want clamping
// should clamp to [0, fixedTermWidth) × [0, fixedTermHeight) themselves.
//
// The translation mirrors the dashboard layout: an optional error/confirm-quit
// banner pushes content down, the list panel and its border occupy columns
// 0..30 (the preview's left border lives at column 31), and the preview's
// lipgloss frame plus the metadata rows above the VT viewport offset the
// top-left cell.
func (a *App) screenToTermCell(screenX, screenY int) (termX, termY int, inViewport bool) {
	dashboardTopY := 0
	if a.err != "" {
		dashboardTopY++
	}
	if a.confirmQuit {
		dashboardTopY++
	}
	const (
		// previewColOffset is the screen column of the preview's left border
		// (= listWidth + list-panel right border = 30 + 1). The preview's left
		// border occupies that column; VT cell 0 sits at previewColOffset + 1.
		previewColOffset  = 31
		previewLeftBorder = 1
		previewTopBorder  = 1
	)
	w := a.dashboard.fixedTermWidth()
	h := a.dashboard.fixedTermHeight()
	termX = screenX - previewColOffset - previewLeftBorder
	termY = screenY - dashboardTopY - previewTopBorder - a.dashboard.previewMetadataRows()
	inViewport = w > 0 && h > 0 && termX >= 0 && termX < w && termY >= 0 && termY < h
	return termX, termY, inViewport
}

// focusLaunchTermHeight returns the terminal height for the focusLaunch view,
// accounting for the header row and the tab bar row.
func (a *App) focusLaunchTermHeight() int {
	return a.dashboard.height - 2
}

// focusLaunchTabIndexAt returns the agent index in focusLaunchSession for a
// tab bar click at column x, or -1 if x doesn't land on any tab. Uses the same
// label formula as renderFocusLaunchTabBar so click targets stay in sync.
func (a *App) focusLaunchTabIndexAt(x int) int {
	if a.focusLaunchSession == nil {
		return -1
	}
	agents := a.focusLaunchSession.Agents()
	col := 0
	for i, ag := range agents {
		label := focusLaunchTabText(ag)
		w := ansi.StringWidth(label)
		if x >= col && x < col+w {
			return i
		}
		col += w + 2 // 2-space separator between tabs
	}
	return -1
}

// screenToTermCellFocusLaunch converts a screen-space mouse coordinate to a VT
// cell coordinate for the fullscreen focusLaunch agent terminal.
func (a *App) screenToTermCellFocusLaunch(screenX, screenY int) (termX, termY int, inViewport bool) {
	dashboardTopY := 0
	if a.err != "" {
		dashboardTopY++
	}
	if a.confirmQuit {
		dashboardTopY++
	}
	termX = screenX
	termY = screenY - dashboardTopY - 2
	w := a.dashboard.width
	h := a.dashboard.height - 2
	inViewport = termX >= 0 && termX < w && termY >= 0 && termY < h
	return
}

// forwardWheelToAgent encodes a mouse wheel event and feeds it to the agent's
// terminal. Coordinates are translated from dashboard-screen space to cells
// relative to the agent's PTY viewport and clamped to [0,W)×[0,H). The
// emulator only emits bytes when the running program has enabled mouse
// reporting (DECSET 1000/1002/1003 + SGR 1006).
func (a *App) forwardWheelToAgent(ag *agent.Agent, msg tea.MouseWheelMsg) {
	termX, termY, _ := a.screenToTermCell(msg.X, msg.Y)
	if termX < 0 {
		termX = 0
	}
	if w := a.dashboard.fixedTermWidth(); w > 0 && termX >= w {
		termX = w - 1
	}
	if termY < 0 {
		termY = 0
	}
	if h := a.dashboard.fixedTermHeight(); h > 0 && termY >= h {
		termY = h - 1
	}
	ag.SendMouse(xvt.MouseWheel{
		X:      termX,
		Y:      termY,
		Button: xvt.MouseButton(msg.Button),
		Mod:    xvt.KeyMod(msg.Mod),
	})
}

func (a *App) refreshAgentList() {
	a.dashboard.closingAgents = a.closingAgents
	a.dashboard.closingSessions = a.closingSessions
	a.dashboard.focusModeActive = a.focusModeActive
	a.dashboard.sessionElapsed = time.Since(a.sessionStart)
	a.dashboard.lastReviewAt = a.lastReviewAt
	a.dashboard.focusSessionMinutes = a.focusSessionMinutes
	a.dashboard.focusBreakMode = a.focusBreakMode
	if a.focusBreakMode {
		a.dashboard.focusBreakElapsed = time.Since(a.focusBreakStart)
	} else {
		a.dashboard.focusBreakElapsed = 0
	}
	a.dashboard.focusBlockCount = a.focusBlockCount
	a.dashboard.focusBreakMinutes = a.focusBreakMinutes
	a.dashboard.focusBreakAnimFrame = a.focusBreakAnimFrame
	a.dashboard.focusBreakShortWarning = a.focusBreakShortWarning
	a.dashboard.focusBreakTimerUp = a.focusBreakTimerUp
	a.dashboard.focusActiveIdx = a.focusActiveIdx
	a.dashboard.focusQueueIndex = a.focusQueueIndex
	a.dashboard.focusCursorSection = a.focusCursorSection
	a.dashboard.activeRepoName = a.activeRepoDisplayName()
	a.dashboard.activeRepoPath = a.activeRepo
	a.dashboard.focusLaunchAgent = a.focusLaunchAgent
	a.dashboard.focusLaunchSession = a.focusLaunchSession
	if a.cfg == nil {
		// Fallback used in tests that set up managers directly without cfg.
		if mgr := a.managers[a.activeRepo]; mgr != nil {
			var items []listItem
			sessions := mgr.ListSessions()
			sort.Slice(sessions, func(i, j int) bool {
				return sessions[i].CreatedAt.Before(sessions[j].CreatedAt)
			})
			for _, sess := range sessions {
				items = append(items, listItem{
					kind:     listItemSession,
					repoPath: a.activeRepo,
					session:  sess,
				})
				for _, ag := range sess.Agents() {
					items = append(items, listItem{
						kind:     listItemAgent,
						repoPath: a.activeRepo,
						session:  sess,
						agent:    ag,
					})
				}
			}
			a.dashboard.items = items
			a.dashboard.clampToAgent()
		}
		return
	}

	// Remember which repo the cursor is in before rebuilding.
	var prevRepo string
	if a.dashboard.selected >= 0 && a.dashboard.selected < len(a.dashboard.items) {
		prevRepo = a.dashboard.items[a.dashboard.selected].repoPath
	}

	// Build hierarchical list: repo > session > agent.
	items := make([]listItem, 0, len(a.cfg.Repos))
	for _, repo := range a.cfg.Repos {
		items = append(items, listItem{
			kind:     listItemRepo,
			repoPath: repo.Path,
			repoName: repo.DisplayName(),
		})
		mgr := a.managers[repo.Path]
		if mgr != nil {
			sessions := mgr.ListSessions()
			sort.Slice(sessions, func(i, j int) bool {
				return sessions[i].CreatedAt.Before(sessions[j].CreatedAt)
			})
			for _, sess := range sessions {
				items = append(items, listItem{
					kind:     listItemSession,
					repoPath: repo.Path,
					session:  sess,
				})
				for _, ag := range sess.Agents() {
					items = append(items, listItem{
						kind:     listItemAgent,
						repoPath: repo.Path,
						session:  sess,
						agent:    ag,
					})
				}
			}
		}
	}

	// Clamp selection to valid range.
	if len(items) > 0 && a.dashboard.selected >= len(items) {
		a.dashboard.selected = len(items) - 1
	}
	a.dashboard.items = items
	if a.focusModeActive {
		a.dashboard.clampToRepo()
	} else {
		a.dashboard.clampToAgent()
	}

	// If the selection landed in a different repo, search backward for the
	// nearest item in the original repo (an agent or the repo header).
	if prevRepo != "" && len(items) > 0 && items[a.dashboard.selected].repoPath != prevRepo {
		for i := a.dashboard.selected; i >= 0; i-- {
			if items[i].repoPath == prevRepo && items[i].kind != listItemSession {
				if !a.focusModeActive || items[i].kind == listItemRepo {
					a.dashboard.selected = i
					break
				}
			}
		}
	}
	a.clampFocusCursor()
	a.recomputePRSectionY()
}

// focusSectionCounts returns the number of rows in each fullscreen-focus section.
func (a *App) focusSectionCounts() (active, review int) {
	return len(a.dashboard.allInProgressSessions()),
		len(a.dashboard.reviewQueueSessions())
}

// clampFocusCursor keeps the per-section indices and the cursor section in
// valid ranges as the underlying lists change (sessions transition phases, etc.).
// When the cursor's current section becomes empty, it falls through to the next
// non-empty section in render order so the cursor stays on a visible row.
func (a *App) clampFocusCursor() {
	actCount, revCount := a.focusSectionCounts()

	clamp := func(idx, n int) int {
		if n <= 0 {
			return 0
		}
		if idx >= n {
			return n - 1
		}
		if idx < 0 {
			return 0
		}
		return idx
	}
	a.focusActiveIdx = clamp(a.focusActiveIdx, actCount)
	a.focusQueueIndex = clamp(a.focusQueueIndex, revCount)

	counts := [2]int{actCount, revCount}
	if counts[int(a.focusCursorSection)] > 0 {
		return
	}
	for i, c := range counts {
		if c > 0 {
			a.focusCursorSection = focusSection(i)
			return
		}
	}
	a.focusCursorSection = focusSectionActive
}

// moveFocusCursorUp moves the fullscreen-focus cursor up one row. When at the
// top of the current section, it transitions to the previous non-empty section.
func (a *App) moveFocusCursorUp() {
	actCount, _ := a.focusSectionCounts()

	switch a.focusCursorSection {
	case focusSectionActive:
		if a.focusActiveIdx > 0 {
			a.focusActiveIdx--
		}
	case focusSectionReview:
		if a.focusQueueIndex > 0 {
			a.focusQueueIndex--
			return
		}
		if actCount > 0 {
			a.focusCursorSection = focusSectionActive
			a.focusActiveIdx = actCount - 1
		}
	}
}

// syncFocusCursorToDashboard mirrors the cursor-related App fields onto the
// dashboard model so the next render reflects navigation immediately, without
// waiting for the 100ms tick that drives refreshAgentList.
func (a *App) syncFocusCursorToDashboard() {
	a.dashboard.focusActiveIdx = a.focusActiveIdx
	a.dashboard.focusQueueIndex = a.focusQueueIndex
	a.dashboard.focusCursorSection = a.focusCursorSection
}

// activateFocusCursor opens the row currently under the fullscreen-focus
// cursor. Active sessions jump into a focusLaunch terminal (openSessionInFocusLaunch
// picks the highest-priority agent, so waiting agents are correctly targeted);
// review-queue sessions open the review panel. Returns ok=false when the
// cursor's section has no actionable row.
func (a *App) activateFocusCursor() (tea.Cmd, bool) {
	switch a.focusCursorSection {
	case focusSectionActive:
		sessions := a.dashboard.allInProgressSessions()
		if len(sessions) == 0 {
			return nil, false
		}
		idx := a.focusActiveIdx
		if idx >= len(sessions) {
			idx = len(sessions) - 1
		}
		return nil, a.openSessionInFocusLaunch(sessions[idx].session)
	case focusSectionReview:
		reviewItems := a.dashboard.reviewQueueSessions()
		if len(reviewItems) == 0 {
			return nil, false
		}
		idx := a.focusQueueIndex
		if idx >= len(reviewItems) {
			idx = len(reviewItems) - 1
		}
		sess := reviewItems[idx].session
		sess.SetLifecyclePhase(agent.LifecycleInReview)
		a.reviewSession = sess
		a.dashboard.panelFocus = focusReview
		if _, ok := a.reviewDiffCache[sess.ID]; !ok {
			return a.fetchReviewDiffCmd(sess), true
		}
		return nil, true
	}
	return nil, false
}

// openSessionInFocusLaunch picks the most-active agent in sess and opens it
// fullscreen in focusLaunch. Priority: Active > Waiting > Idle > Starting >
// Done/Error. Falls back to agents[0] when all have equal priority.
func (a *App) openSessionInFocusLaunch(sess *agent.Session) bool {
	if sess == nil {
		return false
	}
	agents := sess.Agents()
	if len(agents) == 0 {
		return false
	}
	statusPriority := func(ag *agent.Agent) int {
		switch ag.Status() {
		case agent.StatusActive:
			return 5
		case agent.StatusWaiting:
			return 4
		case agent.StatusIdle:
			return 3
		case agent.StatusStarting:
			return 2
		default:
			return 1
		}
	}
	target := agents[0]
	bestPri := statusPriority(agents[0])
	for _, ag := range agents[1:] {
		if pri := statusPriority(ag); pri > bestPri {
			bestPri = pri
			target = ag
		}
	}
	a.focusLaunchAgent = target
	a.focusLaunchSession = sess
	a.dashboard.panelFocus = focusLaunch
	a.dashboard.scrollOffset = 0
	a.focusLaunchAgent.Resize(a.focusLaunchTermHeight(), a.dashboard.width)
	return true
}

// moveFocusCursorDown moves the fullscreen-focus cursor down one row. When at
// the bottom of the current section, it transitions to the next non-empty section.
func (a *App) moveFocusCursorDown() {
	actCount, revCount := a.focusSectionCounts()

	switch a.focusCursorSection {
	case focusSectionActive:
		if a.focusActiveIdx < actCount-1 {
			a.focusActiveIdx++
			return
		}
		if revCount > 0 {
			a.focusCursorSection = focusSectionReview
			a.focusQueueIndex = 0
		}
	case focusSectionReview:
		if a.focusQueueIndex < revCount-1 {
			a.focusQueueIndex++
		}
	}
}

func (a App) View() tea.View {
	var content string

	switch a.view {
	case ViewDashboard:
		if a.dashboard.panelFocus == focusReview && a.reviewSession != nil {
			entry := a.reviewDiffCache[a.reviewSession.ID]
			v := tea.NewView(renderReviewPanel(a.reviewSession, entry, a.width, a.height))
			v.AltScreen = true
			return v
		}
		body := a.dashboard.View()
		hints := dashboardHints
		if a.focusModeActive && a.dashboard.panelFocus != focusReview {
			hints = focusModeHints
		}
		switch a.dashboard.panelFocus {
		case focusTerminal:
			hints = focusTerminalHints
		case focusConfig:
			hints = repoConfigHints
		case focusLaunch:
			hints = focusLaunchHints
		}
		// Break mode hint or work-mode break suggestion.
		// Skip when in focusLaunch: b routes to the agent terminal there, not to break control.
		if a.focusModeActive && a.dashboard.panelFocus != focusLaunch {
			if a.focusBreakMode {
				if a.focusBreakTimerUp {
					hints = []keyHint{{key: "b", desc: "resume focus"}}
				} else {
					hints = []keyHint{{key: "b", desc: "exit early"}}
				}
			} else if a.focusSessionMinutes > 0 {
				hints = append(hints, keyHint{key: "b", desc: "take a break"})
			}
		}
		// Agent-limit modal overlay: replace body with centered modal when active.
		if a.agentLimitModalActive {
			modalW := a.width / 2
			if modalW > 60 {
				modalW = 60
			}
			if modalW < 40 {
				modalW = 40
			}
			activeCount := a.activeAgentCount()
			limitLine := StyleWarning.Render(fmt.Sprintf(
				"You're already running %d agents — beyond ~3, oversight cost exceeds output value.",
				activeCount,
			))
			overlayContent := lipgloss.JoinVertical(
				lipgloss.Left,
				StyleTitle.Render("Focus limit reached"),
				"",
				limitLine,
				"",
				"Press [n] again to spawn anyway",
				"Any other key to cancel",
			)
			overlay := lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				Padding(0, 1).
				Width(modalW).
				Render(overlayContent)
			body = lipgloss.Place(a.width, a.height-1, lipgloss.Center, lipgloss.Center, overlay)
		}
		statusbar := renderStatusBar(hints, a.width)
		content = lipgloss.JoinVertical(lipgloss.Left, body, statusbar)
	case ViewDiff:
		body := a.diff.View()
		statusbar := renderStatusBar(diffHints, a.width)
		content = lipgloss.JoinVertical(lipgloss.Left, body, statusbar)
	case ViewFileBrowser:
		body := a.repoBrowser.View()
		statusbar := renderStatusBar(repoBrowsingHints, a.width)
		content = lipgloss.JoinVertical(lipgloss.Left, body, statusbar)
	case ViewGlobalConfig:
		content = a.globalConfig.View()
	case ViewBranchPicker:
		body := a.branchPicker.View()
		statusbar := renderStatusBar(branchPickerHints, a.width)
		content = lipgloss.JoinVertical(lipgloss.Left, body, statusbar)
	case ViewRepoPicker:
		body := a.repoPicker.View()
		statusbar := renderStatusBar(repoPickerHints, a.width)
		content = lipgloss.JoinVertical(lipgloss.Left, body, statusbar)
	}

	// Show quit confirmation.
	if a.confirmQuit {
		confirmLine := StyleWarning.Render("Agents are running. Press q again to detach, any other key to cancel.")
		content = lipgloss.JoinVertical(lipgloss.Left, confirmLine, content)
	}

	// Show error (auto-cleared after ~3 seconds).
	if a.err != "" {
		errLine := StyleError.Render("Error: " + a.err)
		content = lipgloss.JoinVertical(lipgloss.Left, errLine, content)
	}

	v := tea.NewView(content)
	v.AltScreen = true
	if a.view == ViewDashboard {
		v.MouseMode = tea.MouseModeCellMotion
		if a.dashboard.panelFocus == focusTerminal && a.dashboard.scrollOffset == 0 {
			if item := a.dashboard.selectedItem(); item != nil && item.kind == listItemAgent && item.agent != nil && item.agent.CursorVisible() {
				cursorX, cursorY := item.agent.CursorPosition()
				dashboardTopY := 0
				if a.err != "" {
					dashboardTopY++
				}
				if a.confirmQuit {
					dashboardTopY++
				}
				// previewColOffset is the column of the preview's left border;
				// VT cell 0 lives one column to its right. Mirrors the constant
				// in screenToTermCell — the two formulas must move in lockstep.
				const previewColOffset = 31
				screenX := cursorX + previewColOffset + 1
				screenY := cursorY + dashboardTopY + 1 + a.dashboard.previewMetadataRows()
				v.Cursor = tea.NewCursor(screenX, screenY)
			}
		} else if a.dashboard.panelFocus == focusLaunch && a.dashboard.scrollOffset == 0 && a.focusLaunchAgent != nil {
			ag := a.focusLaunchAgent
			if !ag.IsAltScreen() && ag.CursorVisible() {
				cursorX, cursorY := ag.CursorPosition()
				dashboardTopY := 0
				if a.err != "" {
					dashboardTopY++
				}
				if a.confirmQuit {
					dashboardTopY++
				}
				screenX := cursorX
				screenY := cursorY + dashboardTopY + 2 // header row + tab bar row
				v.Cursor = tea.NewCursor(screenX, screenY)
			}
		}
	}
	return v
}

// refreshDiffStatsCmd returns a Cmd that fetches diff stats for the currently selected session.
func (a *App) refreshDiffStatsCmd() tea.Cmd {
	sess := a.dashboard.selectedSession()
	if sess == nil {
		return nil
	}
	repoPath := a.dashboard.selectedRepoPath()
	if repoPath == "" {
		return nil
	}
	a.diffRefreshInFlight = true
	sessionID := sess.ID
	wt := sess.Worktree
	return func() tea.Msg {
		fileStats, agg, err := git.GetPerFileDiffStats(repoPath, wt)
		if err != nil {
			return diffStatsMsg{sessionID: sessionID, stats: nil}
		}
		// Convert git.FileStat to diffFileStat.
		var files []diffFileStat
		for _, fs := range fileStats {
			files = append(files, diffFileStat{
				Path:       fs.Path,
				Status:     fs.Status,
				Insertions: fs.Insertions,
				Deletions:  fs.Deletions,
			})
		}
		return diffStatsMsg{
			sessionID: sessionID,
			stats: &diffSummaryData{
				Files: files,
				Aggregate: diffAggregateStats{
					Files:      agg.Files,
					Insertions: agg.Insertions,
					Deletions:  agg.Deletions,
				},
			},
		}
	}
}

// updateDashboardDiffStats passes cached diff stats to the dashboard for the current selection.
func (a *App) updateDashboardDiffStats() {
	sess := a.dashboard.selectedSession()
	if sess == nil {
		a.dashboard.diffStats = nil
		return
	}
	if entry, ok := a.diffStatsCache[sess.ID]; ok {
		a.dashboard.diffStats = entry.stats
	} else {
		a.dashboard.diffStats = nil
	}
}

// updateDashboardPRCache passes the PR cache and poll states to the dashboard for rendering.
func (a *App) updateDashboardPRCache() {
	a.dashboard.prCache = a.prCache
	a.dashboard.prPollStates = a.prPollStates
	a.recomputePRSectionY()
}

// repoPathForSession returns the repo path containing the given session, or "" if not found.
func (a *App) repoPathForSession(sessionID string) string {
	if a.cfg == nil {
		return ""
	}
	for _, repo := range a.cfg.Repos {
		mgr := a.managers[repo.Path]
		if mgr == nil {
			continue
		}
		for _, sess := range mgr.ListSessions() {
			if sess.ID == sessionID {
				return repo.Path
			}
		}
	}
	return ""
}

// cleanStaleCaches removes diff stats and PR cache entries for sessions that no longer exist.
func (a *App) cleanStaleCaches() {
	activeSessions := make(map[string]bool)
	for _, mgr := range a.managers {
		for _, sess := range mgr.ListSessions() {
			activeSessions[sess.ID] = true
		}
	}
	for id := range a.diffStatsCache {
		if !activeSessions[id] {
			delete(a.diffStatsCache, id)
		}
	}
	for id := range a.prCache {
		if !activeSessions[id] {
			delete(a.prCache, id)
		}
	}
	for id := range a.prPollStates {
		if !activeSessions[id] {
			delete(a.prPollStates, id)
		}
	}
}

// pollAllSessions returns Cmds for sessions that are due for a PR status poll.
// It respects adaptive intervals and limits concurrent in-flight polls.
func (a *App) pollAllSessions() []tea.Cmd {
	const (
		maxConcurrent    = 3
		shaCheckInterval = 2 * time.Second
	)

	var cmds []tea.Cmd
	now := time.Now()

outer:
	for _, repo := range a.cfg.Repos {
		mgr := a.managers[repo.Path]
		if mgr == nil {
			continue
		}
		for _, sess := range mgr.ListSessions() {
			if a.prPollsInFlight >= maxConcurrent {
				break outer
			}

			ps := a.prPollStates[sess.ID]
			if ps == nil {
				ps = &prSessionState{}
				a.prPollStates[sess.ID] = ps
			}
			if ps.inFlight {
				continue
			}

			// Determine adaptive polling interval.
			interval := a.prPollInterval(sess.ID, ps)
			if now.Sub(ps.lastPoll) < interval {
				// Push detection runs for every session — including those with a
				// cached PR — so new commits, force-pushes, and rewrites get
				// picked up promptly instead of waiting the 30s stable interval.
				// Throttled to once per shaCheckInterval so git rev-parse does
				// not block the Bubble Tea main goroutine on every tick.
				if now.Sub(ps.lastSHACheck) < shaCheckInterval {
					continue
				}
				ps.lastSHACheck = now

				// Detect external branch renames (e.g. `git branch -m`) before
				// querying the remote — a stale in-memory branch name causes
				// getRemoteSHA to always return "" and drops the 30s stable poll.
				if actualBranch := getCurrentHeadBranch(sess.Worktree.Path); actualBranch != "" && actualBranch != sess.Branch() {
					mgr.ReconcileExternalBranchRename(sess.ID, actualBranch)
					// Skip remote SHA check this tick; EventBranchRenamed will
					// arm the burst and the next tick has the correct branch.
					continue
				}

				sha := getRemoteSHA(repo.Path, sess.Branch())
				if sha == "" || sha == ps.lastRemoteSHA {
					continue
				}
				ps.lastRemoteSHA = sha
				// SHA changed — arm a burst so the next minute of polls runs
				// on the short (2s) cadence, then fall through to schedule an
				// immediate poll.
				ps.burstUntil = now.Add(60 * time.Second)
			}

			ps.lastPoll = now
			ps.inFlight = true
			a.prPollsInFlight++
			cmds = append(cmds, a.refreshPRStatusForSession(sess.ID, sess.Branch(), repo.Path, sess.Worktree.Path))
		}
	}
	return cmds
}

// prPollInterval returns the adaptive polling interval for a session.
func (a *App) prPollInterval(sessionID string, ps *prSessionState) time.Duration {
	// Event-driven burst (branch rename, new push): poll aggressively for a
	// short window so state transitions become visible within ~2s.
	if ps != nil && time.Now().Before(ps.burstUntil) {
		return 2 * time.Second
	}
	entry := a.prCache[sessionID]
	// No PR found yet but branch may have been pushed.
	if entry == nil || entry.pr == nil {
		if ps.lastRemoteSHA != "" {
			return 10 * time.Second // branch pushed, waiting for PR
		}
		return 30 * time.Second // stable, no activity
	}
	// PR exists — adapt based on check state.
	if entry.checks != nil && entry.checks.State == "pending" {
		return 5 * time.Second
	}
	return 30 * time.Second
}

// getRemoteSHA runs `git rev-parse origin/<branch>` to detect pushes.
// Returns empty string on any error.
func getRemoteSHA(repoPath, branch string) string {
	out, err := exec.Command("git", "-C", repoPath, "rev-parse", "origin/"+branch).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// getCurrentHeadBranch returns the branch name that HEAD points to in the given
// worktree. Returns "" on any error or when HEAD is detached (rev-parse returns
// "HEAD"). Mirrors the getLocalHeadSHA pattern.
func getCurrentHeadBranch(worktreePath string) string {
	if worktreePath == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", worktreePath, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(out))
	if branch == "HEAD" {
		return ""
	}
	return branch
}

// getLocalHeadSHA returns the local HEAD SHA for a worktree. Used as a
// fallback when getRemoteSHA returns "" (branch not yet pushed under the
// current name after a rename). Silent on error.
func getLocalHeadSHA(worktreePath string) string {
	if worktreePath == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", worktreePath, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// refreshPRStatusForSession returns a Cmd that polls PR, check, and review status for a single session.
// worktreePath is used as a fallback SHA source when the branch hasn't been pushed under its current name.
func (a *App) refreshPRStatusForSession(sessionID, branch, repoPath, worktreePath string) tea.Cmd {
	ghClient := a.ghClient
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		rawURL, err := git.GetRemoteURL(repoPath)
		if err != nil {
			return prPollMsg{sessionID: sessionID, err: err}
		}
		owner, repo, err := github.ParseRemoteURL(rawURL)
		if err != nil {
			return prPollMsg{sessionID: sessionID, err: err}
		}

		// Prefer SHA-based lookup: invariant to branch renames, so a PR opened
		// under a random baton/<adj>-<noun> name (before Haiku rename finishes)
		// is still discovered after the rename. Fall back to branch lookup when
		// the commit hasn't been pushed or SHA lookup returns no PR.
		var pr *github.PRState
		sha := getRemoteSHA(repoPath, branch)
		if sha != "" {
			pr, _ = ghClient.GetPRBySHA(ctx, owner, repo, sha)
		}
		// If the remote SHA is missing (branch not yet pushed under current name
		// after a rename), try the local HEAD SHA — the commit may have been
		// pushed before the rename and GitHub still associates the PR with it.
		if pr == nil && sha == "" {
			if localSHA := getLocalHeadSHA(worktreePath); localSHA != "" {
				pr, _ = ghClient.GetPRBySHA(ctx, owner, repo, localSHA)
			}
		}
		if pr == nil {
			var err error
			pr, err = ghClient.GetPR(ctx, owner, repo, branch)
			if err != nil {
				return prPollMsg{sessionID: sessionID, err: err}
			}
		}
		var checks *github.CheckStatus
		var reviews *github.ReviewStatus
		var stack []*prCacheEntry
		if pr != nil {
			var err error
			// Prefer SHA for checks when available — matches what CI ran against.
			checkRef := branch
			if sha != "" {
				checkRef = sha
			}
			checks, err = ghClient.GetChecks(ctx, owner, repo, checkRef)
			if err != nil {
				return prPollMsg{sessionID: sessionID, err: err}
			}
			reviews, err = ghClient.GetReviews(ctx, owner, repo, pr.Number)
			if err != nil {
				return prPollMsg{sessionID: sessionID, err: err}
			}

			// Walk up the base-branch chain for stacked PR support (best-effort,
			// max 3 levels). Stop when the base targets a trunk branch, no PR is
			// found, or a branch is revisited (cycle guard).
			defaultBranches := map[string]bool{"main": true, "master": true, "develop": true}
			visited := map[string]bool{pr.HeadBranch: true}
			cur := pr
			for i := 0; i < 3; i++ {
				baseBranch := cur.BaseBranch
				if baseBranch == "" || defaultBranches[baseBranch] || visited[baseBranch] {
					break
				}
				basePR, _ := ghClient.GetPR(ctx, owner, repo, baseBranch)
				if basePR == nil {
					break
				}
				// Post-fetch cycle check catches diamond topologies where two PRs
				// share a base and HeadBranch != baseBranch used for lookup.
				if visited[basePR.HeadBranch] {
					break
				}
				visited[basePR.HeadBranch] = true
				entry := &prCacheEntry{pr: basePR}
				entry.checks, _ = ghClient.GetChecks(ctx, owner, repo, basePR.HeadBranch)
				entry.reviews, _ = ghClient.GetReviews(ctx, owner, repo, basePR.Number)
				stack = append(stack, entry)
				cur = basePR
			}
		}

		return prPollMsg{
			sessionID: sessionID,
			pr:        pr,
			checks:    checks,
			reviews:   reviews,
			stack:     stack,
		}
	}
}

// activeAgentCount returns the count of live non-shell agents across all repos.
// Used to enforce the soft concurrent-agent limit in focus mode.
func (a *App) activeAgentCount() int {
	count := 0
	for _, mgr := range a.managers {
		for _, sess := range mgr.ListSessions() {
			for _, ag := range sess.Agents() {
				if !ag.IsShell {
					count++
				}
			}
		}
	}
	return count
}

// wellnessLogEntry is the JSON structure written on session end.
type wellnessLogEntry struct {
	Date            string `json:"date"`
	DurationMin     int    `json:"duration_min"`
	AgentsCreated   int    `json:"agents_created"`
	SessionsCreated int    `json:"sessions_created"`
	FocusSwitches   int    `json:"focus_switches"`
	BlocksCompleted int    `json:"blocks_completed"`
}

// writeWellnessLog appends a single JSON line to <repoPath>/.baton/logs/wellness.log.
// Best-effort: any error is silently dropped so it never blocks shutdown.
func (a *App) writeWellnessLog() {
	repoPath := a.activeRepo
	if repoPath == "" && a.cfg != nil && len(a.cfg.Repos) > 0 {
		repoPath = a.cfg.Repos[0].Path
	}
	if repoPath == "" {
		return
	}

	logPath := filepath.Join(repoPath, ".baton", "logs", "wellness.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return
	}

	elapsed := time.Since(a.appStart)
	entry := wellnessLogEntry{
		Date:            time.Now().UTC().Format(time.RFC3339),
		DurationMin:     int(elapsed.Minutes()),
		AgentsCreated:   a.agentsCreatedCount,
		SessionsCreated: a.sessionsCreatedCount,
		FocusSwitches:   a.focusModeSwitches,
		BlocksCompleted: a.focusBlockCount,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(string(data) + "\n")
}

// reviewDiffEntry caches diff stats for a session in the review panel.
type reviewDiffEntry struct {
	files     []git.FileStat
	aggregate *git.DiffStats
}

// reviewDiffMsg carries the result of an async review diff fetch.
type reviewDiffMsg struct {
	sessionID string
	entry     *reviewDiffEntry
	err       error
}

// fetchReviewDiffCmd returns a Cmd that fetches diff stats for a session to populate the review cache.
func (a App) fetchReviewDiffCmd(sess *agent.Session) tea.Cmd {
	sessID := sess.ID
	wt := sess.Worktree
	repoPath := a.activeRepo
	return func() tea.Msg {
		files, agg, err := git.GetPerFileDiffStats(repoPath, wt)
		if err != nil {
			return reviewDiffMsg{sessionID: sessID, err: err}
		}
		return reviewDiffMsg{sessionID: sessID, entry: &reviewDiffEntry{files: files, aggregate: agg}}
	}
}

// ensureGitignore adds .baton/ to .gitignore in the given path if not already present.
func ensureGitignore(path string) {
	const entry = ".baton/"
	gitignorePath := filepath.Join(path, ".gitignore")

	// Check if .gitignore exists and already contains .baton/.
	data, _ := os.ReadFile(gitignorePath)
	if len(data) > 0 {
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) == entry {
				return // already present
			}
		}
	}

	// Append .baton/ to .gitignore.
	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return // best-effort
	}
	defer func() { _ = f.Close() }()

	// Add newline before entry if file doesn't end with one.
	if len(data) > 0 && data[len(data)-1] != '\n' {
		_, _ = f.WriteString("\n")
	}
	_, _ = f.WriteString(entry + "\n")
}

// openURL opens the given URL in the system's default browser. Fire-and-forget.
func openURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		// Escape embedded quotes to avoid shell injection via cmd.exe.
		safeURL := strings.ReplaceAll(url, `"`, `%22`)
		cmd = exec.Command("cmd", "/c", "start", `"`+safeURL+`"`)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// recomputePRSectionY updates d.prSectionY with the content-relative row index
// (0-indexed, after the AGENTS title and separator) where the PR checks section
// begins, or -1 when no PR section is rendered. Call after any layout change.
// Must mirror the truncation logic in dashboardModel.renderList so mouse clicks
// map to the correct visual rows.
func (a *App) recomputePRSectionY() {
	d := &a.dashboard
	d.prSectionY = -1

	sess := d.selectedSession()
	if sess == nil {
		return
	}
	entry := a.prCache[sess.ID]
	if entry == nil || entry.pr == nil {
		return
	}

	contentH := d.contentHeight()
	// Budget for the PR panel, matching renderList.
	prBudget := 6
	if half := contentH / 2; prBudget > half {
		prBudget = half
	}
	if prBudget < 2 {
		return
	}

	agentListHeight := len(d.items)
	// Apply the same list truncation renderList performs, so availCheckHeight
	// reflects the post-truncation list length.
	if agentListHeight > contentH-prBudget {
		maxList := contentH - prBudget
		if maxList < 1 {
			maxList = 1
		}
		agentListHeight = maxList
	}
	maxCheckHeight := contentH / 3
	availCheckHeight := contentH - agentListHeight
	if availCheckHeight > maxCheckHeight && maxCheckHeight >= prBudget {
		availCheckHeight = maxCheckHeight
	}
	if availCheckHeight < 2 {
		return
	}

	d.prSectionY = contentH - availCheckHeight
}
