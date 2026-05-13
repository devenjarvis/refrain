package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
//
// isNewSession distinguishes "fresh session" call sites (CreateSession,
// CreateSessionOnBranch, the plan-first skip path, and approvePlanAndSpawn's
// first AddAgent) from "added an agent to an existing session" sites
// (AddAgent via `c`, AddShell via `t`). The handler increments
// sessionsCreatedCount only when isNewSession is true. We carry the bit on
// the message rather than inferring it from sess.AgentCount() == 1, because
// the plan-first approve path adds its first agent to a session that was
// already created earlier — the heuristic would double-count if we
// incremented on session creation as well.
type createResultMsg struct {
	sessionID    string
	agentID      string
	err          error
	isNewSession bool
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

// filterNotFound returns nil if err wraps agent.ErrSessionNotFound, otherwise
// returns err unchanged. Used to suppress benign double-cleanup races where two
// concurrent paths both try to KillSession the same session.
func filterNotFound(err error) error {
	if errors.Is(err, agent.ErrSessionNotFound) {
		return nil
	}
	return err
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

// prDraftReadyMsg carries the result of the async push+draft pipeline.
// On success Title/Body are non-empty. On error Err is set.
type prDraftReadyMsg struct {
	sessionID          string
	title              string
	body               string
	owner              string
	repo               string
	head               string
	base               string
	repoPath           string
	transitionShipping bool
	err                error
}

// prCreatedMsg carries the result of the async github.Client.CreatePR call.
type prCreatedMsg struct {
	sessionID          string
	pr                 *github.PRState
	transitionShipping bool
	err                error
}

// mergePRMsg carries the result of an async PR merge attempt.
type mergePRMsg struct {
	sessionID string
	err       error
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

	// Wellness state.
	appStart               time.Time // set once at init; never reset; used for total session duration in wellness log
	sessionStart           time.Time // per-block work timer; reset on each break completion
	lastReviewAt           time.Time
	agentLimitModalActive  bool
	focusSessionMinutes    int          // cached from resolved global settings
	focusBreakMinutes      int          // cached from resolved global settings
	focusPlanningIdx       int          // index into planningSessions()
	focusBuildingIdx       int          // index into buildingSessions()
	focusReviewIdx         int          // index into reviewQueueSessions()
	focusShippingIdx       int          // index into shippingSessions()
	focusCursorSection     focusSection // which section the pipeline cursor is on
	focusLaunchAgent       *agent.Agent
	focusLaunchSession     *agent.Session
	focusBacklogWarning    bool // first n at backlog limit shows warning; second proceeds
	focusBreakMode         bool
	focusBreakStart        time.Time // wall-clock; monotonic stripped so suspend counts toward elapsed
	focusBlockCount        int
	focusBreakShortWarning bool
	focusBreakTimerUp      bool // break duration elapsed; waiting on user to resume
	focusBreakAnimFrame    int
	reviewDiffCache        map[string]*reviewDiffEntry                // keyed by session ID
	reviewSession          *agent.Session                             // session currently open in review panel
	reviewTaskCursor       int                                        // selected task row in the review task list
	shippingSession        *agent.Session                             // session currently open in shipping panel
	feedbackTriage         map[string]map[string]*feedbackTriageEntry // keyed by sessionID → itemKey
	shippingFeedbackCursor int                                        // cursor row in the feedback list pane
	shippingDetailScroll   int                                        // scroll offset in the feedback detail pane
	feedbackNote           feedbackNoteModal                          // overlay for adding a guidance note to a feedback item
	planEditor             *planEditorModel                           // non-nil while panelFocus == focusPlanEditor
	promptModal            promptModalModel                           // overlay for plan-first new-session prompt

	// Wellness counters (written to log on quit).
	agentsCreatedCount   int
	sessionsCreatedCount int

	// closingAgents and closingSessions track in-flight kill requests so the
	// dashboard can render a "closing…" indicator while the async teardown runs.
	// Lives in the TUI because it's purely a UI concern.
	closingAgents   map[string]bool
	closingSessions map[string]bool

	// Pipeline mouse click tracking for double-click detection.
	lastPipelineClick    time.Time
	lastPipelineClickSec focusSection
	lastPipelineClickIdx int

	diffStatsCache      map[string]*diffStatsEntry // keyed by session ID
	diffRefreshInFlight bool

	ghClient         *github.Client
	prCache          map[string]*prCacheEntry   // keyed by session ID
	prPollStates     map[string]*prSessionState // keyed by session ID
	prPollsInFlight  int                        // count of concurrent in-flight polls
	prDraftInFlight  bool                       // true while startPRDraftCmd is running; prevents double-trigger
	prDraftSessionID string                     // ID of the session whose PR draft is in flight; "" when idle

	// PR compose modal and its associated session context.
	prComposeModal   prComposeModal
	prModalSessionID string
	prModalOwner     string
	prModalRepo      string
	prModalHead      string
	prModalBase      string
	// prModalTransitionShipping is true when the modal was opened from the
	// review panel (p key), where confirming the PR should transition the
	// session to LifecycleShipping and close the review panel.
	prModalTransitionShipping bool
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
		feedbackTriage:  make(map[string]map[string]*feedbackTriageEntry),
		feedbackNote:    newFeedbackNoteModal(),
		promptModal:     newPromptModal(),
		prComposeModal:  newPRComposeModal(),
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

// plannerQuestionMsg wraps an aggregated planner ask_user event for the TUI.
// The App routes the question to the matching plan editor and immediately
// re-subscribes so the next question lands without a gap.
type plannerQuestionMsg struct {
	question agent.PlannerQuestion
	repoPath string
}

func listenPlannerQuestions(mgr *agent.Manager) tea.Cmd {
	return func() tea.Msg {
		q, ok := <-mgr.PlannerQuestions()
		if !ok {
			return nil // channel closed (shutdown)
		}
		return plannerQuestionMsg{question: q, repoPath: mgr.RepoPath()}
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
		// A resize remaps the VT viewport — any in-flight selection is now
		// pointing at stale cells. Drop it.
		a.dashboard.clearSelection()

		// Resize agent terminals to match their current display container.
		if a.view == ViewDashboard {
			a.resizeAllForDashboard()
			if a.dashboard.panelFocus == focusLaunch && a.focusLaunchAgent != nil {
				a.focusLaunchAgent.Resize(a.focusLaunchTermHeight(), a.dashboard.width)
			}
			if a.dashboard.panelFocus == focusPlanEditor && a.planEditor != nil {
				a.planEditor.SetSize(msg.Width, msg.Height-1)
			}
			a.promptModal.SetSize(msg.Width, msg.Height-1)
			a.prComposeModal.SetSize(msg.Width, msg.Height-1)
			a.feedbackNote.SetSize(msg.Width, msg.Height)
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
		a.focusSessionMinutes = resolved.FocusSessionMinutes
		a.focusBreakMinutes = resolved.FocusBreakMinutes
		a.focusCursorSection = focusSectionPlanning
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
					AgentModel:        resolved.AgentModel,
					BuildSystemPrompt: resolved.BuildSystemPrompt,
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
		} else if a.focusSessionMinutes > 0 &&
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
		// If the editor is open on the session whose drafting/revising just
		// landed, refresh its content + state so the placeholder swaps to
		// the rendered plan without requiring a re-open.
		if msg.event.Type == agent.EventStatusChanged && a.planEditor != nil &&
			a.planEditor.sess != nil && a.planEditor.sess.ID == msg.event.SessionID {
			// Read both flags up-front so the Reload decision sees a coherent
			// snapshot. RevisePlan emits EventStatusChanged synchronously with
			// IsRevising=true (before runRevise spawns), so a naive
			// "Reload when !drafting" path would reset scrollOff at the moment
			// the revise banner appears. Only Reload when neither subprocess
			// is in flight — that's the single state where plan.md is stable
			// and the editor view should reflect disk.
			drafting := a.planEditor.sess.IsDrafting()
			revising := a.planEditor.sess.IsRevising()
			a.planEditor.SetDrafting(drafting)
			a.planEditor.SetRevising(revising)
			if !drafting && !revising {
				a.planEditor.Reload()
				if derr := a.planEditor.sess.DraftError(); derr != nil {
					a.planEditor.SetError("draft failed: " + derr.Error())
				} else if rerr := a.planEditor.sess.ReviseError(); rerr != nil {
					a.planEditor.SetError("revise failed: " + rerr.Error())
				}
			}
		}

		// Refresh list on any agent event — all repos are visible in the dashboard.
		a.refreshAgentList()
		if mgr := a.managers[msg.repoPath]; mgr != nil {
			return a, listenEvents(mgr)
		}
		return a, nil

	case plannerQuestionMsg:
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

	case createResultMsg:
		if msg.err != nil {
			a.setError(msg.err.Error())
			return a, nil
		}
		a.agentsCreatedCount++
		if msg.isNewSession {
			a.sessionsCreatedCount++
		}
		a.refreshAgentList()
		// Find the new agent by ID, select it, and auto-focus its terminal in
		// focusLaunch.
		if msg.agentID != "" {
			for i, item := range a.dashboard.items {
				if item.kind == listItemAgent && item.agent != nil && item.agent.ID == msg.agentID {
					a.dashboard.selected = i
					a.focusLaunchAgent = item.agent
					a.focusLaunchSession = item.session
					a.dashboard.panelFocus = focusLaunch
					a.dashboard.scrollOffset = 0
					item.agent.Resize(a.focusLaunchTermHeight(), a.dashboard.width)
					// Move the pipeline cursor to the new session so when the
					// user esc's back to focusList their cursor is on the row
					// they just spawned. Walk every section because newSession
					// defaults to LifecyclePlanning and AddAgent/Restore paths
					// can land in any phase.
					if item.session != nil {
					Sections:
						for _, section := range focusSectionsInOrder() {
							for idx, s := range a.focusSectionItems(section) {
								if s.session == item.session {
									*a.focusSectionIdx(section) = idx
									a.focusCursorSection = section
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

	case planEditorCloseMsg:
		// If the editor was parked on a planner question, answer it with the
		// skip-signal before tearing the editor down so the planner subprocess
		// unblocks promptly instead of waiting for its server to close.
		if a.planEditor != nil && a.planEditor.HasPendingQuestion() {
			a.planEditor.resolveQuestion("")
		}
		a.dashboard.panelFocus = focusList
		a.planEditor = nil
		return a, nil

	case planEditorSavedMsg:
		// File-saved confirmation lives inside the editor itself; nothing to
		// do at the App level beyond not propagating the message further.
		return a, nil

	case planEditorAbandonMsg:
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

	case planEditorReviseMsg:
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

	case planEditorRestoreMsg:
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

	case planEditorApproveMsg:
		return a.approvePlanAndSpawn(msg)

	case promptModalCancelMsg:
		// User dismissed the modal — nothing else to do.
		return a, nil

	case promptModalSubmitMsg:
		return a.submitPromptModal(msg)

	case prComposeCancelMsg:
		return a, nil

	case prComposeSubmitMsg:
		return a.submitPRComposeModal(msg)

	case prDraftReadyMsg:
		a.prDraftInFlight = false
		a.prDraftSessionID = ""
		if msg.err != nil {
			a.setError("PR draft failed: " + msg.err.Error())
			return a, nil
		}
		// Store context for the CreatePR call that follows confirmation.
		a.prModalSessionID = msg.sessionID
		a.prModalOwner = msg.owner
		a.prModalRepo = msg.repo
		a.prModalHead = msg.head
		a.prModalBase = msg.base
		a.prModalTransitionShipping = msg.transitionShipping
		resolved := a.resolvedCache[msg.repoPath]
		cmd := a.prComposeModal.Open(msg.title, msg.body, resolved.PRDraftByDefault)
		return a, cmd

	case prCreatedMsg:
		if msg.err != nil {
			a.setError("create PR failed: " + msg.err.Error())
			return a, nil
		}
		a.prCache[msg.sessionID] = &prCacheEntry{pr: msg.pr}
		if msg.transitionShipping {
			if sess := a.sessionByID(msg.sessionID); sess != nil {
				sess.SetLifecyclePhase(agent.LifecycleShipping)
				if a.reviewSession != nil && a.reviewSession.ID == msg.sessionID {
					a.dashboard.panelFocus = focusList
					a.reviewSession = nil
				}
			}
		}
		a.updateDashboardPRCache()
		// Re-arm a burst poll so the new PR is discovered quickly.
		if ps := a.prPollStates[msg.sessionID]; ps != nil {
			ps.burstUntil = time.Now().Add(60 * time.Second)
		}
		// Auto-open in browser if configured.
		repoPath := a.repoPathForSession(msg.sessionID)
		resolved := a.resolvedCache[repoPath]
		if resolved.AutoOpenPRInBrowser && msg.pr != nil && msg.pr.URL != "" {
			if err := openURL(msg.pr.URL); err != nil {
				a.setError(err.Error())
			}
		}
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
			threads: msg.threads,
			stack:   msg.stack,
		}
		// Arm a short burst so the unknown → known transition resolves promptly.
		// Use max semantics to preserve a longer push burst that may already be active.
		if ps != nil && (msg.pr.MergeableState == "" || msg.pr.MergeableState == "unknown") {
			if newBurst := time.Now().Add(15 * time.Second); newBurst.After(ps.burstUntil) {
				ps.burstUntil = newBurst
			}
		}
		// Auto-promote to Shipping when an open PR is discovered externally.
		if msg.pr != nil && msg.pr.State == "open" {
			if sess := a.sessionByID(msg.sessionID); sess != nil {
				switch sess.LifecyclePhase() {
				case agent.LifecycleInProgress, agent.LifecycleReadyForReview, agent.LifecycleInReview:
					sess.SetLifecyclePhase(agent.LifecycleShipping)
					if a.reviewSession != nil && a.reviewSession.ID == msg.sessionID {
						a.dashboard.panelFocus = focusList
						a.reviewSession = nil
					}
				}
			}
		}
		// Detect PR merge/close and trigger async session cleanup.
		var cmds []tea.Cmd
		if msg.pr != nil && (msg.pr.State == "merged" || msg.pr.State == "closed") {
			repoPath := a.repoPathForSession(msg.sessionID)
			if repoPath != "" {
				if mgr := a.managers[repoPath]; mgr != nil {
					if sess := mgr.GetSession(msg.sessionID); sess != nil {
						if sess.LifecyclePhase() == agent.LifecycleShipping {
							sessID := msg.sessionID
							if !a.closingSessions[sessID] {
								sess.SetLifecyclePhase(agent.LifecycleComplete)
								// Close the shipping panel if this session is currently open in it.
								if a.shippingSession != nil && a.shippingSession.ID == sessID {
									a.shippingSession = nil
									a.dashboard.panelFocus = focusList
								}
								var agentIDs []string
								for _, ag := range sess.Agents() {
									agentIDs = append(agentIDs, ag.ID)
									a.closingAgents[ag.ID] = true
								}
								a.closingSessions[sessID] = true
								cmds = append(cmds, func() tea.Msg {
									return killResultMsg{
										scope:     killScopeSession,
										sessionID: sessID,
										agentIDs:  agentIDs,
										err:       filterNotFound(mgr.KillSession(sessID)),
									}
								})
							}
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
		return a, tea.Batch(cmds...)

	case mergePRMsg:
		if msg.err != nil {
			a.setError("merge failed: " + msg.err.Error())
			return a, nil
		}
		a.shippingSession = nil
		a.dashboard.panelFocus = focusList
		repoPath := a.repoPathForSession(msg.sessionID)
		if repoPath == "" {
			return a, nil
		}
		mgr := a.managers[repoPath]
		sess := mgr.GetSession(msg.sessionID)
		if mgr == nil || sess == nil || a.closingSessions[msg.sessionID] {
			return a, nil
		}
		sess.SetLifecyclePhase(agent.LifecycleComplete)
		var agentIDs []string
		for _, ag := range sess.Agents() {
			agentIDs = append(agentIDs, ag.ID)
			a.closingAgents[ag.ID] = true
		}
		sessID := msg.sessionID
		a.closingSessions[sessID] = true
		return a, func() tea.Msg {
			return killResultMsg{
				scope:     killScopeSession,
				sessionID: sessID,
				agentIDs:  agentIDs,
				err:       filterNotFound(mgr.KillSession(sessID)),
			}
		}

	case resumeDoneMsg:
		for _, repoPath := range msg.repoPaths {
			_ = state.Remove(repoPath)
		}
		a.refreshAgentList()
		return a, nil

	case reviewDiffMsg:
		if msg.err == nil && msg.entry != nil {
			a.reviewDiffCache[msg.sessionID] = msg.entry
			// If the entry has task groups, dispatch a reviewer per group.
			if len(msg.entry.groups) > 0 {
				repoPath := a.repoPathForSession(msg.sessionID)
				if repoPath == "" {
					repoPath = a.activeRepo
				}
				var cmds []tea.Cmd
				mgr := a.managers[repoPath]
				var reviewer agent.ReviewerAgent
				if mgr != nil {
					reviewer = mgr.ReviewerAgent()
				}
				if reviewer != nil {
					sess := a.sessionByID(msg.sessionID)
					if sess != nil {
						for _, g := range msg.entry.groups {
							// Mark running before dispatching so the spinner shows.
							if v, ok := msg.entry.verdicts[g.taskIndex]; ok {
								v.state = verdictRunning
							}
							cmds = append(cmds, a.reviewTaskCmd(sess, g, reviewer))
						}
					}
				}
				if len(cmds) > 0 {
					return a, tea.Batch(cmds...)
				}
				// Reviewer unavailable (nil reviewer or nil session): mark all
				// pending verdicts as error so they don't stay at "···" forever.
				for _, rec := range msg.entry.verdicts {
					if rec.state == verdictPending {
						rec.state = verdictErr
						rec.err = errors.New("reviewer unavailable")
					}
				}
			}
		}
		return a, nil

	case reviewVerdictMsg:
		entry := a.reviewDiffCache[msg.sessionID]
		if entry != nil && entry.verdicts != nil {
			rec := entry.verdicts[msg.taskIndex]
			if rec != nil {
				if msg.err != nil {
					rec.state = verdictErr
					rec.err = msg.err
				} else {
					rec.state = verdictDone
					rec.verdict = msg.verdict
				}
			}
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
		return tea.Batch(listenEvents(mgr), listenPlannerQuestions(mgr))
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
		if a.dashboard.panelFocus == focusLaunch && a.focusLaunchAgent != nil {
			a.focusLaunchAgent.Paste(msg.Content)
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
							AgentModel:        resolved.AgentModel,
							BuildSystemPrompt: resolved.BuildSystemPrompt,
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

		// When the config panel has focus, skip all app-level bindings.
		if a.dashboard.panelFocus == focusConfig {
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

		// Plan editor key handling. The editor has its own internal modes
		// (scroll/edit/revise-input) and emits planEditor*Msg values handled
		// by the App below. All keys are consumed by the editor while focused.
		if a.dashboard.panelFocus == focusPlanEditor && a.planEditor != nil {
			cmd := a.planEditor.Update(msg)
			return a, cmd
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
				// Ship: if an open PR exists, open it in the browser and transition
				// to Shipping. If no open PR, push the branch and draft a new one.
				// TODO(stacked-PR): when entry.stack is non-empty, offer a way
				// to cycle through stack entries instead of always opening the
				// head PR.
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
					if a.ghClient == nil {
						a.setError("GitHub auth not available")
						return a, nil
					}
					if a.prDraftInFlight {
						return a, nil
					}
					a.prDraftInFlight = true
					a.prDraftSessionID = sess.ID
					repoPath := a.repoPathForSession(sess.ID)
					return a, a.startPRDraftCmd(sess, repoPath, true)
				}
				return a, nil
			case "t":
				// Open the session's most-active agent in the fullscreen
				// focusLaunch terminal. Useful for sessions with no PR yet
				// (run gh pr create manually) or for inspecting individual
				// agents within a multi-agent session via the tab bar.
				sess := a.reviewSession
				a.reviewSession = nil
				if !a.openSessionInFocusLaunch(sess) {
					a.setError("session has no agents to open")
				}
				return a, nil
			case "c":
				// Mark complete without a PR (e.g. design docs, exploratory
				// branches). Closes the panel and cleans up the session.
				sess := a.reviewSession
				a.dashboard.panelFocus = focusList
				a.reviewSession = nil
				repoPath := a.repoPathForSession(sess.ID)
				if repoPath == "" {
					return a, nil
				}
				mgr := a.managers[repoPath]
				if mgr == nil || mgr.GetSession(sess.ID) == nil || a.closingSessions[sess.ID] {
					return a, nil
				}
				sess.SetLifecyclePhase(agent.LifecycleComplete)
				var agentIDs []string
				for _, ag := range sess.Agents() {
					agentIDs = append(agentIDs, ag.ID)
					a.closingAgents[ag.ID] = true
				}
				sessID := sess.ID
				a.closingSessions[sessID] = true
				return a, func() tea.Msg {
					return killResultMsg{
						scope:     killScopeSession,
						sessionID: sessID,
						agentIDs:  agentIDs,
						err:       filterNotFound(mgr.KillSession(sessID)),
					}
				}
			case "e":
				// Open in editor — same pattern as the existing "i" key handler.
				sess := a.reviewSession
				if sess != nil && sess.Worktree != nil {
					// Resolve the IDE command from the session's owning repo
					// rather than a.activeRepo: pipeline cursor selection lets
					// the user reach a session in any registered repo.
					repoPath := a.repoPathForSession(sess.ID)
					if repoPath == "" {
						repoPath = a.activeRepo
					}
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
			case "j", "down":
				// Move cursor down in the task list.
				if entry := a.reviewDiffCache[a.reviewSession.ID]; entry != nil {
					max := reviewTaskCount(entry) - 1
					if a.reviewTaskCursor < max {
						a.reviewTaskCursor++
					}
				}
				return a, nil
			case "k", "up":
				// Move cursor up in the task list.
				if a.reviewTaskCursor > 0 {
					a.reviewTaskCursor--
				}
				return a, nil
			case "f":
				// Toggle the user-flag on the cursor row. The flag survives
				// panel close/re-open and is folded into the `b` rework prompt
				// alongside any AI concerns/fails.
				entry := a.reviewDiffCache[a.reviewSession.ID]
				if entry == nil {
					return a, nil
				}
				idx, ok := reviewTaskIndexAtCursor(entry, a.reviewTaskCursor)
				if !ok {
					// Synthetic Overview row (no-plan session): nothing to flag.
					return a, nil
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
				return a, nil
			case "b":
				// Back to build: spawn a new agent in the existing worktree with
				// a prompt built from the AI reviewer's concerns/fails plus any
				// rows the user flagged with `f`. Session returns to InProgress.
				return a.addressReviewFeedback(a.reviewSession)
			case "enter", "space":
				// Drill into the selected task's diff. Opens the diff browser
				// filtered to the commits for that task; esc returns to the
				// task list (handled by diffCloseMsg).
				entry := a.reviewDiffCache[a.reviewSession.ID]
				if entry == nil || len(entry.groups) == 0 {
					return a, nil
				}
				group := reviewTaskGroupAtCursor(entry, a.reviewTaskCursor)
				if group == nil || group.rawDiff == "" {
					return a, nil
				}
				m, err := diffmodel.Parse(group.rawDiff)
				if err != nil {
					a.setError(err.Error())
					return a, nil
				}
				taskLabel := a.reviewSession.GetDisplayName()
				if group.taskIndex > 0 {
					taskLabel += fmt.Sprintf(" · task %d", group.taskIndex)
				} else {
					taskLabel += " · other changes"
				}
				a.view = ViewDiff
				a.diff = newDiffModel(taskLabel, m, a.width, a.height-1)
				return a, nil
			}
			// All other keys are no-ops in review panel.
			return a, nil
		}

		// Shipping panel key handling.
		if a.dashboard.panelFocus == focusShipping && a.shippingSession != nil {
			// Modal intercepts all keys first.
			if a.feedbackNote.Active() {
				cmd, submitted, note := a.feedbackNote.Update(msg)
				if submitted {
					a.setFeedbackNote(a.shippingSession.ID, a.feedbackNote.itemKey, note)
				}
				return a, cmd
			}
			entry := a.prCache[a.shippingSession.ID]
			items := feedbackItems(entryThreads(entry))
			halfPane := a.height / 4
			if halfPane < 1 {
				halfPane = 1
			}
			switch msg.String() {
			case "j", "down":
				max := len(items) - 1
				if max < 0 {
					max = 0
				}
				if a.shippingFeedbackCursor < max {
					a.shippingFeedbackCursor++
				}
				a.shippingDetailScroll = 0
			case "k", "up":
				if a.shippingFeedbackCursor > 0 {
					a.shippingFeedbackCursor--
				}
				a.shippingDetailScroll = 0
			case "pgdown", "ctrl+d":
				a.shippingDetailScroll += halfPane
				if a.shippingDetailScroll < 0 { // overflow guard only; render clamps to real max
					a.shippingDetailScroll = 0
				}
			case "pgup", "ctrl+u":
				a.shippingDetailScroll -= halfPane
				if a.shippingDetailScroll < 0 {
					a.shippingDetailScroll = 0
				}
			case "a":
				if len(items) > 0 && a.shippingFeedbackCursor < len(items) {
					key := feedbackItemKey(items[a.shippingFeedbackCursor])
					a.setFeedbackVerdict(a.shippingSession.ID, key, feedbackApproved)
				}
			case "x":
				if len(items) > 0 && a.shippingFeedbackCursor < len(items) {
					key := feedbackItemKey(items[a.shippingFeedbackCursor])
					a.setFeedbackVerdict(a.shippingSession.ID, key, feedbackDisagreed)
				}
			case "u":
				if len(items) > 0 && a.shippingFeedbackCursor < len(items) {
					key := feedbackItemKey(items[a.shippingFeedbackCursor])
					a.setFeedbackVerdict(a.shippingSession.ID, key, feedbackNeutral)
				}
			case "n":
				if len(items) > 0 && a.shippingFeedbackCursor < len(items) {
					item := items[a.shippingFeedbackCursor]
					key := feedbackItemKey(item)
					existing := ""
					if m := a.feedbackTriage[a.shippingSession.ID]; m != nil {
						if e := m[key]; e != nil {
							existing = e.Note
						}
					}
					return a, a.feedbackNote.Open(key, existing)
				}
			case "esc":
				a.dashboard.panelFocus = focusList
				a.shippingSession = nil
			case "t":
				sess := a.shippingSession
				a.shippingSession = nil
				a.dashboard.panelFocus = focusList
				if !a.openSessionInFocusLaunch(sess) {
					a.setError("session has no agents to open")
				}
			case "p":
				if entry != nil && entry.pr != nil && entry.pr.URL != "" {
					if err := openURL(entry.pr.URL); err != nil {
						a.setError(err.Error())
					}
				} else {
					a.setError("no PR URL available")
				}
			case "m":
				// Merge: gated on isMergeReady.
				if !isMergeReady(entry) {
					a.setError("not ready to merge — use M to force")
					return a, nil
				}
				return a, a.mergePRCmd(a.shippingSession.ID)
			case "M":
				// Force merge: bypasses isMergeReady check.
				if entry == nil || entry.pr == nil {
					a.setError("no PR found")
					return a, nil
				}
				return a, a.mergePRCmd(a.shippingSession.ID)
			case "r":
				// Address feedback: synthesize a prompt and spawn a new agent.
				return a.addressFeedback(a.shippingSession)
			}
			return a, nil
		}

		// Pipeline view key handling (the only dashboard mode).
		if a.dashboard.panelFocus != focusReview && a.dashboard.panelFocus != focusShipping {
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
			case "b":
				// Context-sensitive: when the cursor is on a Planning row,
				// 'b' advances it to Building. Otherwise we leave the case
				// without returning so the global "take a break" handler in
				// the switch below catches the press. Picking a Planning row
				// to advance is a deliberate action, while taking a break is
				// the catch-all everywhere else — so the cursor location is
				// the disambiguator the user already has at hand.
				if a.focusCursorSection == focusSectionPlanning {
					planning := a.dashboard.planningSessions()
					if len(planning) > 0 {
						idx := a.focusPlanningIdx
						if idx >= len(planning) {
							idx = len(planning) - 1
						}
						if sess := planning[idx].session; sess != nil {
							sess.SetLifecyclePhase(agent.LifecycleInProgress)
							a.clampFocusCursor()
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
				switch a.focusCursorSection {
				case focusSectionPlanning:
					planning := a.dashboard.planningSessions()
					if a.focusPlanningIdx < len(planning) {
						sess = planning[a.focusPlanningIdx].session
					}
				case focusSectionBuilding:
					building := a.dashboard.buildingSessions()
					if a.focusBuildingIdx < len(building) {
						sess = building[a.focusBuildingIdx].session
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
				a.focusReviewIdx = 0
				return a, a.fetchReviewDiffCmd(sess)
			case "r":
				reviewItems := a.dashboard.reviewQueueSessions()
				if len(reviewItems) == 0 {
					a.setError("review queue is empty — press m on a finished session first")
					return a, nil
				}
				idx := a.focusReviewIdx
				if idx >= len(reviewItems) {
					idx = len(reviewItems) - 1
				}
				sess := reviewItems[idx].session
				sess.SetLifecyclePhase(agent.LifecycleInReview)
				a.reviewSession = sess
				a.reviewTaskCursor = 0
				a.dashboard.panelFocus = focusReview
				// Fetch diff stats if not already cached.
				if _, ok := a.reviewDiffCache[sess.ID]; !ok {
					return a, a.fetchReviewDiffCmd(sess)
				}
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
		case "b":
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

			// Soft agent-count guidance.
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

			fixedW := a.dashboard.fixedTermWidth()
			fixedH := a.dashboard.fixedTermHeight()
			if fixedW <= 0 || fixedH <= 0 {
				a.setError("Terminal size not yet known; try again")
				return a, nil
			}
			mgr := a.managers[repoPath]
			if mgr == nil {
				return a, nil
			}

			// Plan-first flow: open the prompt modal so the user can describe
			// the task before any subprocess spawns. The modal's submit
			// message routes through submitPromptModal which decides between
			// the planning path (StartDraft + editor) and the skip path
			// (today's flow).
			if resolved.PlanFirstEnabled {
				return a, a.promptModal.Open()
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
			}

		case "c":
			// Add an agent to the cursor-selected session.
			sess := a.cursorSelectedSession()
			if sess == nil {
				a.setError("No session selected")
				return a, nil
			}
			repoPath := a.cursorSelectedRepoPath()
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
			}

		case "e":
			// Open the cursor-selected session's worktree in the configured IDE.
			sess := a.cursorSelectedSession()
			if sess == nil {
				a.setError("No session selected")
				return a, nil
			}
			repoPath := a.cursorSelectedRepoPath()
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
			// `o` is not session-scoped; the picker always targets the active repo.
			repoPath := a.cursorSelectedRepoPath()
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
			// Open or focus a shell terminal in the cursor-selected session.
			sess := a.cursorSelectedSession()
			if sess == nil {
				a.setError("No session selected")
				return a, nil
			}
			if sess.HasShell() {
				// Shell exists — open it in focusLaunch directly.
				for _, ag := range sess.Agents() {
					if ag.IsShell {
						a.focusLaunchAgent = ag
						a.focusLaunchSession = sess
						a.dashboard.panelFocus = focusLaunch
						a.dashboard.scrollOffset = 0
						ag.Resize(a.focusLaunchTermHeight(), a.dashboard.width)
						break
					}
				}
				return a, nil
			}
			repoPath := a.cursorSelectedRepoPath()
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
						return a, nil
					}
					phase := sess.LifecyclePhase()
					if phase != agent.LifecycleReadyForReview && phase != agent.LifecycleInReview {
						return a, nil
					}
					if a.prDraftInFlight {
						return a, nil
					}
					a.prDraftInFlight = true
					a.prDraftSessionID = sess.ID
					repoPath := a.cursorSelectedRepoPath()
					return a, a.startPRDraftCmd(sess, repoPath, false)
				}
			}
			return a, nil

		case "s":
			// Open global settings overlay.
			a.globalConfig = newGlobalConfigModel(a.globalSettings, a.width, a.height)
			a.view = ViewGlobalConfig
			return a, nil

		case "d":
			// Diff the cursor-selected session's worktree.
			sess := a.cursorSelectedSession()
			if sess == nil {
				return a, nil
			}
			repoPath := a.cursorSelectedRepoPath()
			rawDiff, err := git.Diff(repoPath, sess.Worktree)
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

		case "x":
			// Kill the cursor-selected session's primary agent asynchronously.
			sess := a.cursorSelectedSession()
			if sess == nil {
				a.setError("No session selected")
				return a, nil
			}
			ag := sess.PrimaryAgent()
			if ag == nil {
				a.setError("Session has no agents")
				return a, nil
			}
			repoPath := a.cursorSelectedRepoPath()
			mgr := a.managers[repoPath]
			if mgr == nil {
				return a, nil
			}
			agentID := ag.ID
			sessionID := sess.ID
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
			// Kill the cursor-selected session entirely.
			sess := a.cursorSelectedSession()
			if sess == nil {
				return a, nil
			}
			repoPath := a.cursorSelectedRepoPath()
			mgr := a.managers[repoPath]
			if mgr == nil {
				return a, nil
			}
			sessID := sess.ID
			// Already dispatched — no-op.
			if a.closingSessions[sessID] {
				return a, nil
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
		if msg.Button != tea.MouseLeft {
			return a, nil
		}

		// focusLaunch: tab bar click switches the active agent; clicks inside
		// the agent terminal seed a text selection.
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
			if a.focusLaunchAgent != nil {
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
			return a, nil
		}

		// focusReview: left-pane row clicks move the review task cursor.
		if a.dashboard.panelFocus == focusReview && a.reviewSession != nil {
			entry := a.reviewDiffCache[a.reviewSession.ID]
			if entry != nil && a.width >= 80 {
				headerH := len(renderReviewHeader(a.reviewSession, a.width))
				leftW := a.width * 4 / 10
				if leftW < 32 {
					leftW = 32
				}
				paneTop := dashboardTopY + headerH
				if rowIdx := reviewListPaneRowAt(entry, msg.X, msg.Y, paneTop, 0, leftW); rowIdx >= 0 {
					// renderTaskListPane scrolls so the cursor stays centred; reproduce
					// its offset computation so clicking visual row N maps to data row
					// offset+N rather than jumping the cursor back to N.
					footerLines := 3
					if a.prDraftInFlight {
						footerLines++
					}
					bodyH := a.height - dashboardTopY - headerH - footerLines
					if bodyH < 4 {
						bodyH = 4
					}
					const listHeaderLines = 2
					rowsH := bodyH - listHeaderLines
					if rowsH < 1 {
						rowsH = 1
					}
					nRows := reviewTaskCount(entry)
					offset := a.reviewTaskCursor - rowsH/2
					if offset < 0 {
						offset = 0
					}
					if offset+rowsH > nRows {
						offset = nRows - rowsH
						if offset < 0 {
							offset = 0
						}
					}
					a.reviewTaskCursor = offset + rowIdx
					return a, nil
				}
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
			items := a.focusSectionItems(section)
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

		a.focusCursorSection = section
		*a.focusSectionIdx(section) = idx
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
			a.dashboard.panelFocus == focusLaunch && a.focusLaunchAgent != nil {
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
				if a.dashboard.panelFocus == focusLaunch && a.focusLaunchAgent != nil && a.focusLaunchAgent.ID == a.dashboard.selection.agentID {
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
		if a.dashboard.panelFocus != focusLaunch || a.focusLaunchAgent == nil || a.focusLaunchAgent.ID != a.dashboard.selection.agentID {
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
			AgentModel:        resolved.AgentModel,
			BuildSystemPrompt: resolved.BuildSystemPrompt,
		}

		branch := item.branch
		baseBranch := item.baseBranch
		return a, func() tea.Msg {
			sess, ag, err := mgr.CreateSessionOnBranch(branch, baseBranch, cfg)
			if err != nil {
				return createResultMsg{err: err}
			}
			// Branch-picker sessions spawn an agent immediately on the
			// chosen branch; they belong in BUILDING. See the legacy n
			// path for the same rationale.
			sess.SetLifecyclePhase(agent.LifecycleInProgress)
			return createResultMsg{sessionID: sess.ID, agentID: ag.ID, isNewSession: true}
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
		// Plan-first flow: same gate as the single-repo `n` keybind. Without
		// this branch, multi-repo users would silently bypass PlanFirstEnabled
		// and spawn the real agent immediately.
		if resolved.PlanFirstEnabled {
			return a, a.promptModal.Open()
		}
		pickerCfg := agent.Config{
			Rows:              fixedH,
			Cols:              fixedW,
			BypassPermissions: resolved.BypassPermissions,
			AgentProgram:      resolved.AgentProgram,
			AgentModel:        resolved.AgentModel,
			BuildSystemPrompt: resolved.BuildSystemPrompt,
		}
		return a, func() tea.Msg {
			sess, ag, err := mgr.CreateSession(pickerCfg)
			if err != nil {
				return createResultMsg{err: err}
			}
			// Repo-picker sessions spawn an agent immediately; they
			// belong in BUILDING. See the legacy n path for rationale.
			sess.SetLifecyclePhase(agent.LifecycleInProgress)
			return createResultMsg{sessionID: sess.ID, agentID: ag.ID, isNewSession: true}
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
	planModel := ""
	if rs.PlanModel != nil {
		planModel = *rs.PlanModel
	}
	agentModel := ""
	if rs.AgentModel != nil {
		agentModel = *rs.AgentModel
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
	fields = addSelect(fields, "Plan Model", config.KnownModels, optionIndex(config.KnownModels, planModel))
	fields = addSelect(fields, "Agent Model", config.KnownAgentModels, optionIndex(config.KnownAgentModels, agentModel))
	fields = addEditorFields(fields, ideCommand)
	fields = addTextInput(fields, "Worktree Directory", worktreeDir, config.DefaultWorktreeDir, inputWidth)

	form := newConfigForm(fields, a.dashboard.fixedTermWidth())
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
	if v := form.selectValue("Plan Model"); v != "" {
		s.PlanModel = &v
	}
	if v := form.selectValue("Agent Model"); v != "" {
		s.AgentModel = &v
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
		// Refresh wellness settings from updated global config.
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
	return termX, termY, inViewport
}

func (a *App) refreshAgentList() {
	a.dashboard.closingAgents = a.closingAgents
	a.dashboard.closingSessions = a.closingSessions
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
	a.dashboard.focusPlanningIdx = a.focusPlanningIdx
	a.dashboard.focusBuildingIdx = a.focusBuildingIdx
	a.dashboard.focusReviewIdx = a.focusReviewIdx
	a.dashboard.focusShippingIdx = a.focusShippingIdx
	a.dashboard.focusCursorSection = a.focusCursorSection
	a.dashboard.prDraftSessionID = a.prDraftSessionID
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
			repoName := a.activeRepoDisplayName()
			for _, sess := range sessions {
				items = append(items, listItem{
					kind:     listItemSession,
					repoPath: a.activeRepo,
					repoName: repoName,
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
					repoName: repo.DisplayName(),
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
	a.dashboard.clampToRepo()

	// If the selection landed in a different repo, search backward for the
	// nearest repo header in the original repo so the dashboard's items
	// `selected` row stays anchored to the right repo for things like
	// activeRepoPath and the cross-repo summary.
	if prevRepo != "" && len(items) > 0 && items[a.dashboard.selected].repoPath != prevRepo {
		for i := a.dashboard.selected; i >= 0; i-- {
			if items[i].repoPath == prevRepo && items[i].kind == listItemRepo {
				a.dashboard.selected = i
				break
			}
		}
	}
	a.clampFocusCursor()
}

// focusSectionCounts returns the number of rows in each fullscreen-focus
// section, indexed by focusSection. Order matches focusSectionsInOrder().
func (a *App) focusSectionCounts() [4]int {
	return [4]int{
		focusSectionPlanning: len(a.dashboard.planningSessions()),
		focusSectionBuilding: len(a.dashboard.buildingSessions()),
		focusSectionReview:   len(a.dashboard.reviewQueueSessions()),
		focusSectionShipping: len(a.dashboard.shippingSessions()),
	}
}

// focusSectionIdx returns a pointer to the per-section cursor index field for
// the given section, so navigation/clamp logic can move and bound-check them
// without a fan-out switch in every caller. Panics on an unknown section so a
// future focusSection constant added without updating this switch surfaces as
// a clear test failure rather than silently corrupting focusBuildingIdx.
func (a *App) focusSectionIdx(s focusSection) *int {
	switch s {
	case focusSectionPlanning:
		return &a.focusPlanningIdx
	case focusSectionBuilding:
		return &a.focusBuildingIdx
	case focusSectionReview:
		return &a.focusReviewIdx
	case focusSectionShipping:
		return &a.focusShippingIdx
	}
	panic(fmt.Sprintf("focusSectionIdx: unknown focusSection %d", s))
}

// focusSectionItems returns the listItem slice that backs the given section.
// Panics on an unknown section, matching focusSectionIdx, so a missing case
// fails fast in tests instead of silently rendering an empty section.
func (a *App) focusSectionItems(s focusSection) []listItem {
	switch s {
	case focusSectionPlanning:
		return a.dashboard.planningSessions()
	case focusSectionBuilding:
		return a.dashboard.buildingSessions()
	case focusSectionReview:
		return a.dashboard.reviewQueueSessions()
	case focusSectionShipping:
		return a.dashboard.shippingSessions()
	}
	panic(fmt.Sprintf("focusSectionItems: unknown focusSection %d", s))
}

// pipelineHitTest maps a mouse-click Y coordinate (relative to the dashboard
// content origin, not screen) to the session under the click. Mirrors the
// vertical layout in dashboardModel.renderFullscreenFocus, walking sections in
// focusSectionsInOrder():
//
//	row 0:                 header line
//	row 1:                 separator
//	rows 2..5:             pipeline widget (4 rows)
//	row 6:                 blank
//	(per non-empty section, in order Planning → Building → Reviewing → Shipping:)
//	  label row + N rows per item (4 for Planning, 4–5 for Building cards depending on plan progress, 2 for Reviewing/Shipping)
//	  blank row between rows; trailing blank row before next section
//
// Returns (section, index, true) when the click landed on a session row,
// otherwise ok=false. dashboardContentY is the click's Y relative to the
// dashboard content origin (i.e. screenY - dashboardTopY).
func (a *App) pipelineHitTest(dashboardContentY int) (focusSection, int, bool) {
	const (
		headerRows   = 1
		sepRows      = 1
		pipelineRows = 4
		blankRows    = 1
		labelRows    = 1
		cardRows     = 4
		queueRows    = 2
	)
	rowsPerItem := func(s focusSection) int {
		switch s {
		case focusSectionPlanning, focusSectionBuilding:
			return cardRows
		default:
			return queueRows
		}
	}

	cursor := headerRows + sepRows + pipelineRows + blankRows
	for _, section := range focusSectionsInOrder() {
		items := a.focusSectionItems(section)
		if len(items) == 0 {
			continue
		}
		cursor += labelRows
		for i := range items {
			rowH := rowsPerItem(section)
			start := cursor
			end := start + rowH
			if dashboardContentY >= start && dashboardContentY < end {
				return section, i, true
			}
			cursor = end
			if i < len(items)-1 {
				cursor += blankRows
			}
		}
		cursor += blankRows
	}
	return focusSectionPlanning, 0, false
}

// cursorSelectedSession returns the session under the pipeline cursor, or nil
// when the cursor's section is empty. Workflow keys (c, x, X, t, e, p, d) use
// this rather than dashboard.selectedSession() because the pipeline addresses
// sessions by section + index, not by a `selected` row in the items list.
func (a *App) cursorSelectedSession() *agent.Session {
	items := a.focusSectionItems(a.focusCursorSection)
	if len(items) == 0 {
		return nil
	}
	idx := *a.focusSectionIdx(a.focusCursorSection)
	if idx < 0 || idx >= len(items) {
		idx = 0
	}
	return items[idx].session
}

// cursorSelectedRepoPath returns the repo path of the session under the
// pipeline cursor, or the active repo when no session is selected.
func (a *App) cursorSelectedRepoPath() string {
	sess := a.cursorSelectedSession()
	if sess != nil {
		// Find the repo that owns this session.
		for _, item := range a.dashboard.items {
			if item.kind == listItemSession && item.session == sess {
				return item.repoPath
			}
			if item.kind == listItemAgent && item.session == sess {
				return item.repoPath
			}
		}
	}
	return a.activeRepo
}

// clampFocusCursor keeps the per-section indices and the cursor section in
// valid ranges as the underlying lists change (sessions transition phases, etc.).
// When the cursor's current section becomes empty, it falls through to the next
// non-empty section in render order so the cursor stays on a visible row.
func (a *App) clampFocusCursor() {
	counts := a.focusSectionCounts()

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
	a.focusPlanningIdx = clamp(a.focusPlanningIdx, counts[focusSectionPlanning])
	a.focusBuildingIdx = clamp(a.focusBuildingIdx, counts[focusSectionBuilding])
	a.focusReviewIdx = clamp(a.focusReviewIdx, counts[focusSectionReview])
	a.focusShippingIdx = clamp(a.focusShippingIdx, counts[focusSectionShipping])

	if counts[a.focusCursorSection] > 0 {
		return
	}
	for _, s := range focusSectionsInOrder() {
		if counts[s] > 0 {
			a.focusCursorSection = s
			return
		}
	}
	a.focusCursorSection = focusSectionPlanning
}

// moveFocusCursorUp moves the fullscreen-focus cursor up one row. When at the
// top of the current section, it transitions to the previous non-empty section.
func (a *App) moveFocusCursorUp() {
	idx := a.focusSectionIdx(a.focusCursorSection)
	if *idx > 0 {
		*idx--
		return
	}
	// Walk render-order sections backwards from the current one; jump to the
	// last row of the first non-empty earlier section.
	order := focusSectionsInOrder()
	cur := -1
	for i, s := range order {
		if s == a.focusCursorSection {
			cur = i
			break
		}
	}
	counts := a.focusSectionCounts()
	for i := cur - 1; i >= 0; i-- {
		s := order[i]
		if counts[s] > 0 {
			a.focusCursorSection = s
			*a.focusSectionIdx(s) = counts[s] - 1
			return
		}
	}
}

// moveFocusCursorDown moves the fullscreen-focus cursor down one row. When at
// the bottom of the current section, it transitions to the next non-empty section.
func (a *App) moveFocusCursorDown() {
	counts := a.focusSectionCounts()
	idx := a.focusSectionIdx(a.focusCursorSection)
	if *idx < counts[a.focusCursorSection]-1 {
		*idx++
		return
	}
	order := focusSectionsInOrder()
	cur := -1
	for i, s := range order {
		if s == a.focusCursorSection {
			cur = i
			break
		}
	}
	for i := cur + 1; i < len(order); i++ {
		s := order[i]
		if counts[s] > 0 {
			a.focusCursorSection = s
			*a.focusSectionIdx(s) = 0
			return
		}
	}
}

// syncFocusCursorToDashboard mirrors the cursor-related App fields onto the
// dashboard model so the next render reflects navigation immediately, without
// waiting for the 100ms tick that drives refreshAgentList.
func (a *App) syncFocusCursorToDashboard() {
	a.dashboard.focusPlanningIdx = a.focusPlanningIdx
	a.dashboard.focusBuildingIdx = a.focusBuildingIdx
	a.dashboard.focusReviewIdx = a.focusReviewIdx
	a.dashboard.focusShippingIdx = a.focusShippingIdx
	a.dashboard.focusCursorSection = a.focusCursorSection
	a.dashboard.prDraftSessionID = a.prDraftSessionID
}

// activateFocusCursor opens the row currently under the fullscreen-focus
// cursor. Planning + Building rows jump into a focusLaunch terminal so the
// user can drive the agent. Reviewing rows open the review panel. Shipping
// rows open the PR URL when one is cached, otherwise fall back to the agent
// terminal so the user can run gh manually. Returns ok=false when the cursor's
// section has no actionable row.
func (a *App) activateFocusCursor() (tea.Cmd, bool) {
	items := a.focusSectionItems(a.focusCursorSection)
	if len(items) == 0 {
		return nil, false
	}
	idx := *a.focusSectionIdx(a.focusCursorSection)
	if idx >= len(items) {
		idx = len(items) - 1
	}
	sess := items[idx].session

	switch a.focusCursorSection {
	case focusSectionPlanning:
		// Planning rows open the plan editor — there is no agent yet to drop
		// into a focusLaunch terminal. Drafting sessions also live in the
		// Planning section; the editor renders a "Drafting…" placeholder
		// until the background draft lands and reloads.
		a.openPlanEditor(sess, items[idx].repoPath)
		return nil, true
	case focusSectionBuilding:
		return nil, a.openSessionInFocusLaunch(sess)
	case focusSectionReview:
		sess.SetLifecyclePhase(agent.LifecycleInReview)
		a.reviewSession = sess
		a.reviewTaskCursor = 0
		a.dashboard.panelFocus = focusReview
		if _, ok := a.reviewDiffCache[sess.ID]; !ok {
			return a.fetchReviewDiffCmd(sess), true
		}
		return nil, true
	case focusSectionShipping:
		a.shippingSession = sess
		a.shippingFeedbackCursor = 0
		a.shippingDetailScroll = 0
		a.dashboard.panelFocus = focusShipping
		return nil, true
	}
	return nil, false
}

// openSessionInFocusLaunch picks the most-active agent in sess and opens it
// fullscreen in focusLaunch. Priority is shared with Session.PrimaryAgent via
// agent.AgentStatusPriority. Falls back to agents[0] when all have equal
// priority.
func (a *App) openSessionInFocusLaunch(sess *agent.Session) bool {
	if sess == nil {
		return false
	}
	agents := sess.Agents()
	if len(agents) == 0 {
		return false
	}
	target := agents[0]
	bestPri := agent.AgentStatusPriority(agents[0])
	for _, ag := range agents[1:] {
		if pri := agent.AgentStatusPriority(ag); pri > bestPri {
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

// submitPromptModal handles a promptModalSubmitMsg by creating a session
// and dispatching to either the plan-drafting flow (default `enter`) or
// today's immediate-spawn flow (`ctrl+enter` skip). The modal has already
// closed itself by the time this fires.
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
			a.focusPlanningIdx = idx
			a.focusCursorSection = focusSectionPlanning
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
		return createResultMsg{sessionID: sessID, agentID: ag.ID, isNewSession: true}
	}
}

func (a App) View() tea.View {
	var content string

	switch a.view {
	case ViewDashboard:
		if a.dashboard.panelFocus == focusReview && a.reviewSession != nil {
			entry := a.reviewDiffCache[a.reviewSession.ID]
			var panelStr string
			if a.prComposeModal.Active() {
				panelStr = lipgloss.Place(a.width, a.height-1, lipgloss.Center, lipgloss.Center, a.prComposeModal.View())
			} else {
				panelStr = renderReviewPanel(a.reviewSession, entry, a.width, a.height, a.reviewTaskCursor, a.prDraftInFlight && a.prDraftSessionID == a.reviewSession.ID)
			}
			v := tea.NewView(panelStr)
			v.AltScreen = true
			return v
		}
		if a.dashboard.panelFocus == focusShipping && a.shippingSession != nil {
			entry := a.prCache[a.shippingSession.ID]
			sessID := a.shippingSession.ID
			panel := renderShippingPanel(a.shippingSession, entry, a.width, a.height, a.shippingFeedbackCursor, a.shippingDetailScroll, a.feedbackTriage[sessID])
			if a.feedbackNote.Active() {
				panel = lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center, a.feedbackNote.View())
			}
			v := tea.NewView(panel)
			v.AltScreen = true
			return v
		}
		if a.dashboard.panelFocus == focusPlanEditor && a.planEditor != nil {
			v := tea.NewView(a.planEditor.View())
			v.AltScreen = true
			return v
		}
		body := a.dashboard.View()
		hints := dashboardHints
		switch a.dashboard.panelFocus {
		case focusConfig:
			hints = repoConfigHints
		case focusLaunch:
			hints = focusLaunchHints
		}
		// `b` is dual-purpose: it advances a Planning session to Building when
		// the cursor is on Planning, and otherwise triggers the wellness break.
		// We expose this through a single hint slot to keep the bar from
		// overflowing 120 cols — when the cursor is not on Planning AND the
		// wellness timer is enabled, swap the desc on the static `b` entry to
		// "break". Skip in focusLaunch: b routes to the agent terminal there.
		if a.dashboard.panelFocus != focusLaunch {
			if a.focusBreakMode {
				if a.focusBreakTimerUp {
					hints = []keyHint{{key: "b", desc: "resume focus"}}
				} else {
					hints = []keyHint{{key: "b", desc: "exit early"}}
				}
			} else if a.focusCursorSection != focusSectionPlanning && a.focusSessionMinutes > 0 {
				// Copy first — `hints := dashboardHints` aliases the package
				// var's backing array, and we'd otherwise mutate it globally.
				hints = append([]keyHint(nil), hints...)
				for i := range hints {
					if hints[i].key == "b" {
						hints[i].desc = "break"
						break
					}
				}
			}
		}
		// Prompt modal overlay (plan-first new-session input). Centered over
		// the body, replaces it while active so the dashboard does not
		// receive input.
		if a.promptModal.Active() {
			body = lipgloss.Place(a.width, a.height-1, lipgloss.Center, lipgloss.Center, a.promptModal.View())
		}
		// PR compose modal overlay.
		if a.prComposeModal.Active() {
			body = lipgloss.Place(a.width, a.height-1, lipgloss.Center, lipgloss.Center, a.prComposeModal.View())
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
		if a.dashboard.panelFocus == focusLaunch && a.dashboard.scrollOffset == 0 && a.focusLaunchAgent != nil {
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

// reviewTaskGroupAtCursor returns the taskReviewGroup at position cursor in the
// review task list, following the same row ordering as renderTaskListPane: plan
// tasks in index order, then the "Other changes" group (taskIndex == 0) last.
// Returns nil if cursor is out of range or no groups exist.
func reviewTaskGroupAtCursor(entry *reviewDiffEntry, cursor int) *taskReviewGroup {
	if entry == nil {
		return nil
	}
	groupByIdx := make(map[int]*taskReviewGroup, len(entry.groups))
	for i := range entry.groups {
		g := &entry.groups[i]
		groupByIdx[g.taskIndex] = g
	}
	row := 0
	for _, t := range entry.tasks {
		if row == cursor {
			return groupByIdx[t.Index]
		}
		row++
	}
	// Check "Other changes" row.
	if g, ok := groupByIdx[0]; ok {
		if row == cursor {
			return g
		}
	}
	return nil
}

// reviewTaskIndexAtCursor returns the task index at cursor following the same
// row ordering as reviewTaskGroupAtCursor (plan tasks first, then the "Other
// changes" row when present). Unlike reviewTaskGroupAtCursor, this resolves
// even when the plan task has no associated commit group — so the user can
// flag a never-touched task for rework. Returns (0, false) for the synthetic
// Overview row used in no-plan sessions.
func reviewTaskIndexAtCursor(entry *reviewDiffEntry, cursor int) (int, bool) {
	if entry == nil || cursor < 0 {
		return 0, false
	}
	if cursor < len(entry.tasks) {
		return entry.tasks[cursor].Index, true
	}
	// "Other changes" row, only present when some commit has no [task N] prefix.
	if cursor == len(entry.tasks) {
		for i := range entry.groups {
			if entry.groups[i].taskIndex == 0 {
				return 0, true
			}
		}
	}
	return 0, false
}

// reviewTaskCount returns the number of task rows in a review entry (plan tasks
// plus the "other" group if present). For no-plan sessions with aggregate data,
// returns 1 for the synthetic "Overview" row.
func reviewTaskCount(entry *reviewDiffEntry) int {
	if entry == nil {
		return 0
	}
	// No-plan session: synthetic Overview row.
	if len(entry.tasks) == 0 && len(entry.groups) == 0 && entry.aggregate != nil {
		return 1
	}
	n := len(entry.tasks)
	for _, g := range entry.groups {
		if g.taskIndex == 0 {
			n++ // "Other changes" row
			break
		}
	}
	return n
}

// sessionByID returns the Session with the given ID across all managed repos,
// or nil if not found.
func (a *App) sessionByID(sessionID string) *agent.Session {
	if a.cfg == nil {
		return nil
	}
	for _, repo := range a.cfg.Repos {
		mgr := a.managers[repo.Path]
		if mgr == nil {
			continue
		}
		for _, sess := range mgr.ListSessions() {
			if sess.ID == sessionID {
				return sess
			}
		}
	}
	return nil
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
			fetchThreads := sess.LifecyclePhase() == agent.LifecycleShipping
			cmds = append(cmds, a.refreshPRStatusForSession(sess.ID, sess.Branch(), repo.Path, sess.Worktree.Path, fetchThreads))
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
func (a *App) refreshPRStatusForSession(sessionID, branch, repoPath, worktreePath string, fetchThreads bool) tea.Cmd {
	// Guard: ensure the caller passed the repo that actually owns this session.
	// This catches programming errors (e.g. passing cfg.Repos[0].Path for a
	// session that belongs to a different repo) before the poll fires.
	if owning := a.repoPathForSession(sessionID); owning != "" && owning != repoPath {
		mismatchErr := fmt.Errorf("internal: refreshPRStatus: repoPath %q does not own session %s (owner=%q)", repoPath, sessionID, owning)
		return func() tea.Msg { return prPollMsg{sessionID: sessionID, err: mismatchErr} }
	}
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
		var threads []github.ReviewThread
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
			// Threads are only needed for the shipping panel — skip the fetch for
			// building/reviewing sessions to avoid doubling review API calls.
			if fetchThreads {
				threads, _ = ghClient.GetReviewThreads(ctx, owner, repo, pr.Number)
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
			threads:   threads,
			stack:     stack,
		}
	}
}

// entryThreads safely returns threads from a prCacheEntry (nil-safe).
func entryThreads(entry *prCacheEntry) []github.ReviewThread {
	if entry == nil {
		return nil
	}
	return entry.threads
}

// setFeedbackVerdict lazily allocates the per-session triage map and sets the
// verdict on the item with the given key. For feedbackNeutral with an empty
// note, the entry is deleted to keep the map clean.
func (a *App) setFeedbackVerdict(sessID, itemKey string, v feedbackVerdict) {
	if a.feedbackTriage[sessID] == nil {
		a.feedbackTriage[sessID] = make(map[string]*feedbackTriageEntry)
	}
	m := a.feedbackTriage[sessID]
	if v == feedbackNeutral {
		if e := m[itemKey]; e == nil || strings.TrimSpace(e.Note) == "" {
			delete(m, itemKey)
			return
		}
	}
	if m[itemKey] == nil {
		m[itemKey] = &feedbackTriageEntry{}
	}
	m[itemKey].Verdict = v
}

// setFeedbackNote lazily allocates the per-session triage map and sets the
// note on the item. If the resulting entry is neutral with an empty note, it
// is deleted.
func (a *App) setFeedbackNote(sessID, itemKey, note string) {
	if a.feedbackTriage[sessID] == nil {
		a.feedbackTriage[sessID] = make(map[string]*feedbackTriageEntry)
	}
	m := a.feedbackTriage[sessID]
	if m[itemKey] == nil {
		if note == "" {
			return
		}
		m[itemKey] = &feedbackTriageEntry{}
	}
	m[itemKey].Note = note
	// Clean up neutral entries with no note.
	if m[itemKey].Verdict == feedbackNeutral && strings.TrimSpace(m[itemKey].Note) == "" {
		delete(m, itemKey)
	}
}

// addressFeedback synthesizes a prompt from failing CI checks and unresolved
// review comments, spawns a new agent in the session's existing worktree, and
// transitions the session back to LifecycleInProgress. The PR stays open.
func (a *App) addressFeedback(sess *agent.Session) (tea.Model, tea.Cmd) {
	if sess == nil {
		return a, nil
	}
	repoPath := a.repoPathForSession(sess.ID)
	if repoPath == "" {
		a.setError("no repo found for session")
		return a, nil
	}
	mgr := a.managers[repoPath]
	if mgr == nil {
		a.setError("session manager not found")
		return a, nil
	}

	entry := a.prCache[sess.ID]
	prompt := buildFeedbackPrompt(entry, a.feedbackTriage[sess.ID])
	if prompt == "" {
		prompt = "Address the CI failures and review feedback on this PR."
	}

	resolved := a.resolvedCache[repoPath]
	fixedW := a.dashboard.fixedTermWidth()
	fixedH := a.dashboard.fixedTermHeight()
	if fixedW <= 0 || fixedH <= 0 {
		a.setError("terminal size not yet known; try again")
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

	sessID := sess.ID
	a.shippingSession = nil
	a.dashboard.panelFocus = focusList
	delete(a.feedbackTriage, sessID)
	return a, func() tea.Msg {
		ag, err := mgr.AddAgent(sessID, cfg)
		if err != nil {
			return createResultMsg{err: err}
		}
		sess.SetLifecyclePhase(agent.LifecycleInProgress)
		return createResultMsg{sessionID: sessID, agentID: ag.ID}
	}
}

// buildFeedbackPrompt synthesizes an agent prompt from the failing CI checks
// and review feedback, bucketed by user triage verdicts. Returns "" when
// nothing is actionable (all disagreed with no notes, and no failing CI).
// triage may be nil (treats all items as neutral).
func buildFeedbackPrompt(entry *prCacheEntry, triage map[string]*feedbackTriageEntry) string {
	if entry == nil {
		return ""
	}
	var b strings.Builder
	wrote := false

	// ── Failing CI checks (unchanged) ────────────────────────────────────────
	if entry.checks != nil {
		var failingRuns []github.CheckRun
		for _, run := range entry.checks.Runs {
			if run.Status == "completed" &&
				run.Conclusion != "success" &&
				run.Conclusion != "skipped" &&
				run.Conclusion != "neutral" {
				failingRuns = append(failingRuns, run)
			}
		}
		if len(failingRuns) > 0 {
			b.WriteString("## Failing CI Checks\n\n")
			for _, run := range failingRuns {
				b.WriteString("- ")
				b.WriteString(run.Name)
				if run.URL != "" {
					b.WriteString(" (see ")
					b.WriteString(run.URL)
					b.WriteString(")")
				}
				b.WriteByte('\n')
			}
			b.WriteByte('\n')
			wrote = true
		}
	}

	// ── Review feedback, bucketed by triage ──────────────────────────────────
	// Pre-compute actionable threads using the same filter as the original code:
	// CHANGES_REQUESTED always actionable; COMMENTED only when it has inline comments.
	actionableThreads := make(map[string]bool, len(entry.threads))
	for _, thread := range entry.threads {
		if thread.State == "CHANGES_REQUESTED" || (thread.State == "COMMENTED" && len(thread.Comments) > 0) {
			actionableThreads[thread.Reviewer] = true
		}
	}

	items := feedbackItems(entry.threads)
	var addressItems, disputedItems []feedbackItem
	var disputedNotes []string

	for _, item := range items {
		// Apply actionability filter.
		if !actionableThreads[item.Reviewer] {
			continue
		}
		key := feedbackItemKey(item)
		var e *feedbackTriageEntry
		if triage != nil {
			e = triage[key]
		}
		if e != nil && e.Verdict == feedbackDisagreed {
			if strings.TrimSpace(e.Note) == "" {
				// Disagreed with no note: skip entirely.
				continue
			}
			disputedItems = append(disputedItems, item)
			disputedNotes = append(disputedNotes, strings.TrimSpace(e.Note))
		} else {
			addressItems = append(addressItems, item)
		}
	}

	writeFeedbackItem := func(item feedbackItem) {
		b.WriteString("- ")
		if item.IsInline {
			b.WriteString(item.Reviewer)
			b.WriteString(" (")
			b.WriteString(item.Path)
			if item.Line > 0 {
				b.WriteString(fmt.Sprintf(":%d", item.Line))
			}
			b.WriteString("): ")
		} else {
			b.WriteString("**")
			b.WriteString(item.Reviewer)
			b.WriteString("**: ")
		}
		b.WriteString(item.Body)
		b.WriteByte('\n')
	}

	if len(addressItems) > 0 {
		b.WriteString("## Feedback to address\n\n")
		for _, item := range addressItems {
			writeFeedbackItem(item)
		}
		b.WriteByte('\n')
		wrote = true
	}

	if len(disputedItems) > 0 {
		b.WriteString("## Disputed feedback (advisory — do not change unless you find a strong reason)\n\n")
		for i, item := range disputedItems {
			writeFeedbackItem(item)
			if i < len(disputedNotes) && disputedNotes[i] != "" {
				b.WriteString(fmt.Sprintf("  > I disagree because: %s\n", disputedNotes[i]))
			}
		}
		b.WriteByte('\n')
		wrote = true
	}

	if !wrote {
		return ""
	}
	return "The following issues need to be addressed on this PR:\n\n" + b.String() + "Please fix each issue, commit your changes, and push."
}

// addressReviewFeedback spawns a new agent in the session's existing worktree
// with a prompt synthesized from the review panel's AI verdicts and user flags,
// then transitions the session back to LifecycleInProgress. The reviewDiffCache
// entry is cleared so the next entry into review re-runs the AI reviewer on the
// new commit history. Mirrors addressFeedback (the shipping→build path) but
// draws from in-panel verdicts rather than PR state.
func (a *App) addressReviewFeedback(sess *agent.Session) (tea.Model, tea.Cmd) {
	if sess == nil {
		return a, nil
	}
	entry := a.reviewDiffCache[sess.ID]
	prompt := buildReviewReworkPrompt(entry)
	if prompt == "" {
		a.setError("no tasks flagged or marked concerns/fail")
		return a, nil
	}
	repoPath := a.repoPathForSession(sess.ID)
	if repoPath == "" {
		a.setError("no repo found for session")
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
		a.setError("terminal size not yet known; try again")
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
	sessID := sess.ID
	// Drop the panel and the cached diff/verdicts: when the user returns to
	// review after the rework round, fetchReviewDiffCmd will re-parse the plan
	// and re-run verdicts over the augmented commit history.
	a.reviewSession = nil
	a.reviewTaskCursor = 0
	delete(a.reviewDiffCache, sessID)
	a.dashboard.panelFocus = focusList
	return a, func() tea.Msg {
		ag, err := mgr.AddAgent(sessID, cfg)
		if err != nil {
			return createResultMsg{err: err}
		}
		sess.SetLifecyclePhase(agent.LifecycleInProgress)
		return createResultMsg{sessionID: sessID, agentID: ag.ID}
	}
}

// buildReviewReworkPrompt synthesizes a builder-agent prompt from the per-task
// verdicts and user flags in entry. Returns "" when no task qualifies (no flag
// set and no AI verdict of concerns/fail), so the caller can surface an error.
//
// The prompt instructs the agent to use `[task N]` commit prefixes so a
// subsequent review groups round-2 commits under the same task index — this is
// what keeps the build↔review round-trip coherent.
func buildReviewReworkPrompt(entry *reviewDiffEntry) string {
	if entry == nil || entry.verdicts == nil {
		return ""
	}
	// Build a taskIndex → text lookup, including the special index 0 used for
	// commits without a `[task N]` prefix.
	taskText := map[int]string{0: "Other changes"}
	for _, t := range entry.tasks {
		taskText[t.Index] = t.Text
	}

	type entryRow struct {
		idx        int
		text       string
		flagged    bool
		hasVerdict bool
		noCommits  bool
		kind       agent.VerdictKind
		rationale  string
	}
	rows := make([]entryRow, 0, len(entry.verdicts))
	for idx, rec := range entry.verdicts {
		if rec == nil {
			continue
		}
		hasVerdict := rec.state == verdictDone &&
			(rec.verdict.Kind == agent.VerdictConcerns || rec.verdict.Kind == agent.VerdictFail)
		if !rec.userFlagged && !hasVerdict {
			continue
		}
		text, ok := taskText[idx]
		if !ok {
			text = fmt.Sprintf("(task %d)", idx)
		}
		rows = append(rows, entryRow{
			idx:        idx,
			text:       text,
			flagged:    rec.userFlagged,
			hasVerdict: hasVerdict,
			noCommits:  rec.state == verdictNoDiff,
			kind:       rec.verdict.Kind,
			rationale:  strings.TrimSpace(rec.verdict.Rationale),
		})
	}
	if len(rows) == 0 {
		return ""
	}
	// Stable ordering: by task index ascending, with "Other changes" (index 0)
	// last so it doesn't lead the list when present.
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].idx, rows[j].idx
		if a == 0 {
			return false
		}
		if b == 0 {
			return true
		}
		return a < b
	})

	var b strings.Builder
	b.WriteString("The following tasks need rework based on the review:\n\n")
	for _, r := range rows {
		if r.idx > 0 {
			fmt.Fprintf(&b, "## Task %d: %s\n", r.idx, r.text)
		} else {
			b.WriteString("## Other changes\n")
		}
		if r.hasVerdict {
			fmt.Fprintf(&b, "AI reviewer verdict: %s\n", r.kind)
			if r.rationale != "" {
				fmt.Fprintf(&b, "Rationale: %s\n", r.rationale)
			}
		} else if r.noCommits {
			b.WriteString("Status: no commits yet for this task.\n")
		}
		if r.flagged {
			b.WriteString("Flagged by you: yes\n")
		}
		b.WriteByte('\n')
	}
	b.WriteString("Please address each task above. Re-read `.claude/plan.md` for full context.\n")
	b.WriteString("When you commit fixes, prefix each commit subject with `[task N]` matching the task numbers above so the next review groups commits correctly. For \"Other changes\", commit without a `[task N]` prefix.\n")
	return b.String()
}

// mergePRCmd returns a Cmd that merges the PR for the given session using the
// repo-configured merge method (default squash). The caller is responsible for
// any merge-readiness gating before invoking this.
func (a *App) mergePRCmd(sessionID string) tea.Cmd {
	ghClient := a.ghClient
	if ghClient == nil {
		return func() tea.Msg {
			return mergePRMsg{sessionID: sessionID, err: fmt.Errorf("GitHub client not available")}
		}
	}
	entry := a.prCache[sessionID]
	if entry == nil || entry.pr == nil {
		return func() tea.Msg { return mergePRMsg{sessionID: sessionID, err: fmt.Errorf("no PR cached")} }
	}
	repoPath := a.repoPathForSession(sessionID)
	if repoPath == "" {
		return func() tea.Msg { return mergePRMsg{sessionID: sessionID, err: fmt.Errorf("session repo not found")} }
	}
	method := a.resolvedCache[repoPath].MergeMethod
	switch method {
	case "merge", "squash", "rebase":
	case "":
		method = "squash"
	default:
		return func() tea.Msg {
			return mergePRMsg{sessionID: sessionID, err: fmt.Errorf("invalid merge_method %q: must be merge, squash, or rebase", method)}
		}
	}
	prNum := entry.pr.Number
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		rawURL, err := git.GetRemoteURL(repoPath)
		if err != nil {
			return mergePRMsg{sessionID: sessionID, err: err}
		}
		owner, repo, err := github.ParseRemoteURL(rawURL)
		if err != nil {
			return mergePRMsg{sessionID: sessionID, err: err}
		}
		if err := ghClient.MergePR(ctx, owner, repo, prNum, method); err != nil {
			return mergePRMsg{sessionID: sessionID, err: err}
		}
		return mergePRMsg{sessionID: sessionID}
	}
}

// activeAgentCount returns the count of live non-shell agents across all repos.
// Used to enforce the soft concurrent-agent limit in focus mode. Defers to
// Manager.AgentCount, which already excludes shells and exited (Done/Error)
// agents — keeping the "live" definition in one place so all three call sites
// (quit guard, soft cap, repo-picker counts) can't drift apart.
func (a *App) activeAgentCount() int {
	count := 0
	for _, mgr := range a.managers {
		count += mgr.AgentCount()
	}
	return count
}

// wellnessLogEntry is the JSON structure written on session end.
type wellnessLogEntry struct {
	Date            string `json:"date"`
	DurationMin     int    `json:"duration_min"`
	AgentsCreated   int    `json:"agents_created"`
	SessionsCreated int    `json:"sessions_created"`
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

// plannerLogEntry is the JSON structure written on each planner question event.
// PanelFocus is the human-readable name of the active panel at arrival time
// (e.g. "list", "plan-editor") so log readers don't have to cross-reference
// the panelFocus iota in keymap.go.
type plannerLogEntry struct {
	Time        string `json:"time"`
	SessionID   string `json:"session_id"`
	Disposition string `json:"disposition"`
	PanelFocus  string `json:"panel_focus"`
}

// panelFocusName returns a human-readable name for a panelFocus value. New
// panelFocus values must be added here so the planner log stays self-documenting.
func panelFocusName(f panelFocus) string {
	switch f {
	case focusList:
		return "list"
	case focusConfig:
		return "config"
	case focusReview:
		return "review"
	case focusLaunch:
		return "launch"
	case focusPlanEditor:
		return "plan-editor"
	default:
		return fmt.Sprintf("unknown(%d)", int(f))
	}
}

// writePlannerLog appends a single JSON line to <repoPath>/.baton/logs/planner.log.
// Best-effort: any error is silently dropped so it never blocks the UI loop.
// repoPath is passed explicitly (rather than read from a.activeRepo) because
// the caller has the exact repo path from the message in multi-repo configs.
func (a *App) writePlannerLog(repoPath string, entry plannerLogEntry) {
	if repoPath == "" {
		return
	}
	logPath := filepath.Join(repoPath, ".baton", "logs", "planner.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return
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

// verdictState tracks the progress of a per-task reviewer subprocess.
type verdictState int

const (
	verdictPending verdictState = iota // reviewer not yet dispatched
	verdictRunning                     // reviewer subprocess in flight
	verdictDone                        // reviewer returned a verdict
	verdictErr                         // reviewer subprocess errored
	verdictNoDiff                      // task has no matching commits — nothing to review
)

// taskVerdictRecord holds the verdict state for one task row.
type taskVerdictRecord struct {
	state   verdictState
	verdict agent.ReviewVerdict
	err     error
	// userFlagged is set when the human reviewer presses `f` on this row to
	// override the AI verdict — the task gets included in the next
	// review→build feedback prompt regardless of what the AI returned.
	userFlagged bool
}

// taskReviewGroup holds one plan task's commits and their resolved diff stats.
type taskReviewGroup struct {
	taskIndex int
	commits   []git.Commit
	files     []git.FileStat
	stats     *git.DiffStats
	rawDiff   string
}

// reviewDiffEntry caches diff stats for a session in the review panel.
type reviewDiffEntry struct {
	// Aggregate file stats (always populated, even when no plan exists).
	files     []git.FileStat
	aggregate *git.DiffStats

	// Plan-driven fields; non-nil only when the session has a plan.
	tasks    []agent.PlanTask
	groups   []taskReviewGroup          // per-task + "other" commit groups
	verdicts map[int]*taskVerdictRecord // keyed by taskIndex (0 = other)
}

// reviewDiffMsg carries the result of an async review diff fetch.
type reviewDiffMsg struct {
	sessionID string
	entry     *reviewDiffEntry
	err       error
}

// reviewVerdictMsg carries the result of a single per-task reviewer subprocess.
type reviewVerdictMsg struct {
	sessionID string
	taskIndex int
	verdict   agent.ReviewVerdict
	err       error
}

// fetchReviewDiffCmd returns a Cmd that fetches diff stats for a session to
// populate the review cache. When the session has a plan, it also computes
// per-task commit groups and per-group diff stats so the review panel can
// render a task-by-task view.
func (a App) fetchReviewDiffCmd(sess *agent.Session) tea.Cmd {
	sessID := sess.ID
	wt := sess.Worktree
	// Use the session's owning repo, not a.activeRepo: with cursor-based
	// selection the targeted session can live in any registered repo. Falling
	// back to activeRepo only when the lookup fails keeps single-repo flows
	// working as before.
	repoPath := a.repoPathForSession(sessID)
	if repoPath == "" {
		repoPath = a.activeRepo
	}
	planContent, hasPlan := sess.CachedPlan()
	return func() tea.Msg {
		files, agg, err := git.GetPerFileDiffStats(repoPath, wt)
		if err != nil {
			return reviewDiffMsg{sessionID: sessID, err: err}
		}
		entry := &reviewDiffEntry{files: files, aggregate: agg}

		if hasPlan && planContent != "" {
			entry.tasks = agent.ParsePlanTasks(planContent)
			commits, logErr := git.LogCommitsAgainstBase(wt)
			if logErr == nil && len(commits) > 0 {
				commitGroups := agent.GroupCommitsByTask(commits)
				entry.groups = make([]taskReviewGroup, 0, len(commitGroups))
				entry.verdicts = make(map[int]*taskVerdictRecord)
				for _, cg := range commitGroups {
					hashes := make([]string, len(cg.Commits))
					for i, c := range cg.Commits {
						hashes[i] = c.Hash
					}
					gFiles, gStats, rawDiff, diffErr := git.DiffForCommits(wt, hashes)
					if diffErr != nil {
						gStats = &git.DiffStats{}
					}
					entry.groups = append(entry.groups, taskReviewGroup{
						taskIndex: cg.TaskIndex,
						commits:   cg.Commits,
						files:     gFiles,
						stats:     gStats,
						rawDiff:   rawDiff,
					})
					entry.verdicts[cg.TaskIndex] = &taskVerdictRecord{state: verdictPending}
				}
				// Mark plan tasks that have no matching commit group so the review
				// panel can surface the gap instead of silently omitting the row.
				// This intentionally only runs when len(commits) > 0: a session
				// with no commits at all leaves entry.verdicts nil, which the
				// render loop treats as "not yet reviewed" rather than "missing
				// diff". Moving this loop outside the len(commits) guard would
				// also require initialising entry.verdicts in the outer block
				// and would change that loading-state semantics.
				populateNoDiffVerdicts(entry)
			}
		}

		return reviewDiffMsg{sessionID: sessID, entry: entry}
	}
}

// populateNoDiffVerdicts stamps verdictNoDiff on every plan task in entry that
// has no matching commit group. It must only be called when entry.verdicts is
// already initialised (i.e. the session has at least one commit), so that the
// nil-verdicts "not yet reviewed" state remains distinct from verdictNoDiff.
func populateNoDiffVerdicts(entry *reviewDiffEntry) {
	for _, t := range entry.tasks {
		if _, matched := entry.verdicts[t.Index]; !matched {
			entry.verdicts[t.Index] = &taskVerdictRecord{state: verdictNoDiff}
		}
	}
}

// reviewTaskCmd returns a Cmd that runs a reviewer subprocess for one task
// group and returns a reviewVerdictMsg when done.
func (a App) reviewTaskCmd(sess *agent.Session, group taskReviewGroup, reviewer agent.ReviewerAgent) tea.Cmd {
	sessID := sess.ID
	originalPrompt := sess.OriginalPrompt()
	taskIndex := group.taskIndex
	rawDiff := group.rawDiff

	// Find task text from the entry if available.
	taskText := fmt.Sprintf("Task %d", taskIndex)
	if taskIndex == 0 {
		taskText = "Other changes"
	} else if entry := a.reviewDiffCache[sessID]; entry != nil {
		for _, t := range entry.tasks {
			if t.Index == taskIndex {
				taskText = t.Text
				break
			}
		}
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		verdict, err := reviewer.Review(ctx, agent.ReviewRequest{
			TaskIndex:      taskIndex,
			TaskText:       taskText,
			TaskDiff:       rawDiff,
			OriginalPrompt: originalPrompt,
		})
		return reviewVerdictMsg{sessionID: sessID, taskIndex: taskIndex, verdict: verdict, err: err}
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

// startPRDraftCmd returns an async Cmd that pushes sess's branch and calls the
// PRDrafter. On completion it emits a prDraftReadyMsg. repoPath is the parent
// repo; worktreePath is used for git operations inside the worktree.
func (a *App) startPRDraftCmd(sess *agent.Session, repoPath string, transitionShipping bool) tea.Cmd {
	if sess == nil || sess.Worktree == nil {
		return func() tea.Msg {
			return prDraftReadyMsg{err: fmt.Errorf("no worktree for session")}
		}
	}
	branch := sess.Branch()
	worktreePath := sess.Worktree.Path
	sessionID := sess.ID

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		// Resolve owner/repo from the parent repo's remote URL.
		rawURL, err := git.GetRemoteURL(repoPath)
		if err != nil {
			return prDraftReadyMsg{sessionID: sessionID, err: fmt.Errorf("get remote url: %w", err)}
		}
		owner, repo, err := github.ParseRemoteURL(rawURL)
		if err != nil {
			return prDraftReadyMsg{sessionID: sessionID, err: fmt.Errorf("parse remote url: %w", err)}
		}

		// Determine base branch (local, no network).
		base := sess.Worktree.BaseBranch
		if base == "" {
			base = "main"
		}
		worktree := sess.Worktree

		// Push and draft concurrently; total latency = max(push, drafter).
		// innerCtx is cancelled by push failure so a fast push error (e.g. auth
		// failure) immediately aborts the expensive Haiku subprocess rather than
		// waiting out the full 90s parent timeout.
		innerCtx, innerCancel := context.WithCancel(ctx)
		defer innerCancel()

		var (
			pushErr  error
			draftErr error
			draft    *agent.PRDraft
			wg       sync.WaitGroup
		)

		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := git.Push(worktreePath, branch); err != nil {
				pushErr = fmt.Errorf("push branch: %w", err)
				innerCancel()
			}
		}()
		go func() {
			defer wg.Done()
			commits := ""
			if cs, logErr := git.LogCommitsAgainstBase(worktree); logErr == nil && len(cs) > 0 {
				var sb strings.Builder
				for _, c := range cs {
					sb.WriteString(c.Subject)
					sb.WriteString("\n")
					if c.Body != "" {
						sb.WriteString(c.Body)
						sb.WriteString("\n")
					}
				}
				commits = strings.TrimSpace(sb.String())
			}

			diffstat := ""
			if stats, statsErr := git.GetDiffStats(repoPath, worktree); statsErr == nil {
				diffstat = fmt.Sprintf("%d file(s) changed, +%d -%d lines",
					stats.Files, stats.Insertions, stats.Deletions)
			}

			template := git.FindPRTemplate(worktreePath)

			taskPrompt := sess.TaskSummary()
			if taskPrompt == "" {
				taskPrompt = sess.GetDisplayName()
			}

			drafter := agent.DefaultPRDrafter()
			var err error
			draft, err = drafter(innerCtx, commits, diffstat, taskPrompt, template)
			if err != nil {
				draftErr = fmt.Errorf("draft PR: %w", err)
			}
		}()
		wg.Wait()

		// If push failed, drafter may have been cancelled via innerCtx — return
		// only the push error; the drafter error is expected noise in that case.
		if pushErr != nil {
			return prDraftReadyMsg{sessionID: sessionID, err: pushErr}
		}
		if draftErr != nil {
			return prDraftReadyMsg{sessionID: sessionID, err: draftErr}
		}

		return prDraftReadyMsg{
			sessionID:          sessionID,
			title:              draft.Title,
			body:               draft.Body,
			owner:              owner,
			repo:               repo,
			head:               branch,
			base:               base,
			repoPath:           repoPath,
			transitionShipping: transitionShipping,
		}
	}
}

// submitPRComposeModal handles a prComposeSubmitMsg by calling CreatePR and
// emitting a prCreatedMsg.
func (a *App) submitPRComposeModal(msg prComposeSubmitMsg) (tea.Model, tea.Cmd) {
	ghClient := a.ghClient
	if ghClient == nil {
		return a, func() tea.Msg {
			return prCreatedMsg{sessionID: a.prModalSessionID, err: fmt.Errorf("GitHub auth not available")}
		}
	}
	owner := a.prModalOwner
	repo := a.prModalRepo
	head := a.prModalHead
	base := a.prModalBase
	sessionID := a.prModalSessionID
	transitionShipping := a.prModalTransitionShipping
	return a, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pr, err := ghClient.CreatePR(ctx, owner, repo, head, base, msg.title, msg.body, msg.draft)
		if err != nil {
			return prCreatedMsg{sessionID: sessionID, err: err}
		}
		return prCreatedMsg{sessionID: sessionID, pr: pr, transitionShipping: transitionShipping}
	}
}

// openURL opens the given URL in the system's default browser. Fire-and-forget.
// Declared as a var so tests can swap in a no-op to avoid launching a real browser.
var openURL = func(url string) error {
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
