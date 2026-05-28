package tui

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/audio"
	"github.com/devenjarvis/refrain/internal/config"
	"github.com/devenjarvis/refrain/internal/git"
	"github.com/devenjarvis/refrain/internal/github"
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
	sessionID       string
	agentID         string
	err             error
	isNewSession    bool
	skipFocusLaunch bool // when true, don't enter focusLaunch; cursor still moves
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
//
// repoPath identifies which repo owns the killed session — required because
// session IDs collide across managers (each manager mints session-1, session-2,
// …). Without it the closing-set cleanup would key on bare sessionID and clear
// the wrong repo's badge.
type killResultMsg struct {
	scope     killScope
	repoPath  string
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

// diffStatsMsg carries the result of an async diff stats refresh. repoPath
// identifies the repo that owns sessionID so the handler keys the cache by
// (repoPath, sessionID) rather than colliding across repos with the same
// session counter (session-1, session-2, …).
type diffStatsMsg struct {
	sessionID string
	repoPath  string
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
	repoPath           string
	pr                 *github.PRState
	transitionShipping bool
	err                error
}

// mergePRMsg carries the result of an async PR merge attempt.
type mergePRMsg struct {
	sessionID string
	repoPath  string
	err       error
}

// diffStatsEntry holds cached diff stats for a single session.
type diffStatsEntry struct {
	stats       *diffSummaryData
	lastRefresh time.Time
}

// App is the root Bubble Tea model.
type App struct {
	// managers is keyed by repo path. The value satisfies the narrow
	// SessionManager interface (see manager_iface.go); production uses
	// *agent.Manager but tests can inject a deterministic fake.
	managers     map[string]SessionManager
	activeRepo   string
	cfg          *config.Config
	repoBrowser  fileBrowserModel
	branchPicker branchPickerModel
	repoPicker   repoPickerModel

	// repoPickerPending is set when the file browser was opened from the repo
	// picker. After the browser emits a select or cancel, control returns to
	// ViewRepoPicker rather than the dashboard.
	repoPickerPending bool

	// repoPickerPendingFromConfig is set when the repo config form was opened
	// from the manage-mode picker. After save or cancel, control returns to
	// ViewRepoPicker rather than the dashboard.
	repoPickerPendingFromConfig bool

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

	lastKnownStatus map[string]agent.Status // keyed by agentCacheKey(repoPath, agentID)
	audioPlayer     *audio.Player

	// wellness owns the focus-block timer, break overlay, and counters
	// flushed to the wellness log on quit. See wellness.go.
	wellness wellnessState

	sessionLimitModalActive bool
	cursor                  FocusedCursor // pipeline cursor: section + per-section indices
	// modals owns panel focus and the lifetime of every overlay model. The
	// invariant "the model for panelFocus X is non-nil iff modals.Current() == X"
	// is enforced by the Modals type; see internal/tui/modals.go. App callers
	// must reach overlay models via modals.Review(), modals.Shipping(), etc.,
	// and must transition via app.openReview / openShipping / openPlanEditor /
	// openConfig / openLaunch / closeModal helpers (which keep the dashboard
	// mirror fields in sync).
	modals          Modals
	reviewDiffCache map[string]*reviewDiffEntry                // keyed by cacheKey(repoPath, sessionID); lifetime exceeds panel
	validationRuns  map[string]*validationRunState             // keyed by sessionID; lifetime exceeds panel
	feedbackTriage  map[string]map[string]*feedbackTriageEntry // keyed by cacheKey(repoPath, sessionID) → itemKey
	promptModal     promptModalModel                           // overlay for plan-first new-session prompt

	// closingAgents and closingSessions track in-flight kill requests so the
	// dashboard can render a "closing…" indicator while the async teardown runs.
	// Lives in the TUI because it's purely a UI concern. closingAgents is
	// keyed by agentCacheKey(repoPath, agentID) and closingSessions by
	// cacheKey(repoPath, sessionID); without the repo prefix, two repos
	// with overlapping session counters (session-1 in both) would clobber
	// each other's closing badge.
	closingAgents   map[string]bool
	closingSessions map[string]bool

	// Pipeline mouse click tracking for double-click detection.
	lastPipelineClick    time.Time
	lastPipelineClickSec focusSection
	lastPipelineClickIdx int

	diffStatsCache      map[string]*diffStatsEntry // keyed by cacheKey(repoPath, sessionID)
	diffRefreshInFlight bool

	ghClient        *github.Client
	prCache         map[string]*prCacheEntry   // keyed by cacheKey(repoPath, sessionID)
	prPollStates    map[string]*prSessionState // keyed by cacheKey(repoPath, sessionID)
	prPollsInFlight int                        // count of concurrent in-flight polls

	prDraftInFlight  bool   // true while startPRDraftCmd is running; prevents double-trigger
	prDraftSessionID string // ID of the session whose PR draft is in flight; "" when idle
	prDraftRepoPath  string // repo path of the session whose PR draft is in flight; "" when idle

	// keys holds the dashboard action→key bindings. Stored on App so tests
	// and future rebinding flows can swap a non-default map.
	keys KeyMap

	// PR compose modal and its associated session context.
	prComposeModal   prComposeModal
	prModalSessionID string
	prModalRepoPath  string
	prModalOwner     string
	prModalRepo      string
	prModalHead      string
	prModalBase      string
	// prModalTransitionShipping is true when the modal was opened from the
	// review panel (p key), where confirming the PR should transition the
	// session to LifecycleShipping and close the review panel.
	prModalTransitionShipping bool
}

// syncModalsToDashboard mirrors the current Modals state into the dashboard
// model's render-time fields. The dashboard renderer reads these fields
// directly; calling this after every Modals mutation keeps the two in sync.
// Modal callers should prefer the openX / closeModal helpers below, which
// invoke this automatically.
func (a *App) syncModalsToDashboard() {
	a.dashboard.panelFocus = a.modals.Current()
	a.dashboard.repoConfigForm = a.modals.Config()
	a.dashboard.configRepoPath = a.modals.ConfigRepoPath()
	a.dashboard.focusLaunchAgent = a.modals.LaunchAgent()
	a.dashboard.focusLaunchSession = a.modals.LaunchSession()
}

// openReview opens the review panel and syncs the dashboard mirror.
func (a *App) openReview(rp *reviewPanelModel) {
	a.modals.OpenReview(rp)
	a.syncModalsToDashboard()
}

// openShipping opens the shipping panel and syncs the dashboard mirror.
func (a *App) openShipping(sp *shippingPanelModel) {
	a.modals.OpenShipping(sp)
	a.syncModalsToDashboard()
}

// openPlanEditorPanel installs an existing plan editor model and syncs the
// dashboard mirror. The high-level "open the plan editor for this session"
// flow lives in openPlanEditor (which builds the model, then calls this).
func (a *App) openPlanEditorPanel(pe *planEditorModel) {
	a.modals.OpenPlanEditor(pe)
	a.syncModalsToDashboard()
}

// openConfigForm opens the per-repo config form for repoPath and syncs.
func (a *App) openConfigForm(form *configForm, repoPath string) {
	a.modals.OpenConfig(form, repoPath)
	a.syncModalsToDashboard()
}

// openLaunchPanel sets the fullscreen agent terminal target and syncs.
// repoPath pins which repo owns sess so ctrl+t/ctrl+n/ctrl+w inside the
// launch view route to the right manager without re-searching by ID.
func (a *App) openLaunchPanel(sess *agent.Session, ag *agent.Agent, repoPath string) {
	a.modals.OpenLaunch(sess, ag, repoPath)
	a.syncModalsToDashboard()
}

// closeModal returns focus to the pipeline list and nils every overlay model.
// This is the canonical "close any panel" path.
func (a *App) closeModal() {
	a.modals.Close()
	a.syncModalsToDashboard()
}

func NewApp() App {
	return App{
		view:            ViewDashboard,
		dashboard:       newDashboardModel(),
		cursor:          NewFocusedCursor(),
		keys:            DefaultKeyMap(),
		wellness:        newWellnessState(),
		managers:        make(map[string]SessionManager),
		repoSettings:    make(map[string]*config.RepoSettings),
		resolvedCache:   make(map[string]config.ResolvedSettings),
		lastKnownStatus: make(map[string]agent.Status),
		diffStatsCache:  make(map[string]*diffStatsEntry),
		reviewDiffCache: make(map[string]*reviewDiffEntry),
		validationRuns:  make(map[string]*validationRunState),
		prCache:         make(map[string]*prCacheEntry),
		prPollStates:    make(map[string]*prSessionState),
		closingAgents:   make(map[string]bool),
		closingSessions: make(map[string]bool),
		feedbackTriage:  make(map[string]map[string]*feedbackTriageEntry),
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
	return tea.Tick(DashboardTickInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func listenEvents(mgr SessionManager) tea.Cmd {
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

func listenPlannerQuestions(mgr SessionManager) tea.Cmd {
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
		// WindowSizeMsg is the only case that intentionally falls through
		// to the view-router below: resize side effects must propagate to
		// whichever view is active.
		a = a.handleWindowSize(msg)

	case reviewReworkRequestMsg:
		return a.handleReviewReworkRequest(msg)
	case shippingFeedbackRequestMsg:
		return a.handleShippingFeedbackRequest(msg)
	case initAppMsg:
		return a.handleInit(msg)
	case tickMsg:
		return a.handleTick(msg)
	case agentEventMsg:
		return a.handleAgentEvent(msg)
	case plannerQuestionMsg:
		return a.handlePlannerQuestion(msg)
	case createResultMsg:
		return a.handleCreateResult(msg)
	case killResultMsg:
		return a.handleKillResult(msg)
	case planEditorCloseMsg:
		return a.handlePlanEditorClose(msg)
	case planEditorSavedMsg:
		// File-saved confirmation lives inside the editor itself; nothing to
		// do at the App level beyond not propagating the message further.
		_ = msg
		return a, nil
	case planEditorAbandonMsg:
		return a.handlePlanEditorAbandon(msg)
	case planEditorReviseMsg:
		return a.handlePlanEditorRevise(msg)
	case planEditorRetryMsg:
		return a.handlePlanEditorRetry(msg)
	case planEditorRestoreMsg:
		return a.handlePlanEditorRestore(msg)
	case planEditorApproveMsg:
		return a.approvePlanAndSpawn(msg)
	case promptModalCancelMsg:
		// User dismissed the modal — nothing else to do.
		_ = msg
		return a, nil
	case promptModalSubmitMsg:
		return a.submitPromptModal(msg)
	case prComposeCancelMsg:
		_ = msg
		return a, nil
	case prComposeSubmitMsg:
		return a.submitPRComposeModal(msg)
	case prDraftReadyMsg:
		return a.handlePRDraftReady(msg)
	case prCreatedMsg:
		return a.handlePRCreated(msg)
	case diffStatsMsg:
		return a.handleDiffStats(msg)
	case prPollMsg:
		return a.handlePRPoll(msg)
	case mergePRMsg:
		return a.handleMergePR(msg)
	case resumeDoneMsg:
		return a.handleResumeDone(msg)
	case reviewDiffMsg:
		return a.handleReviewDiff(msg)
	case reviewVerdictMsg:
		return a.handleReviewVerdict(msg)
	case reviewOpenTaskDiffMsg:
		return a.handleReviewOpenTaskDiff(msg)
	case validationCheckResultMsg:
		a.handleValidationCheckResult(msg)
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

// returnFromConfigForm closes the repo config form and returns to the repo
// picker if one was pending, or back to the dashboard list otherwise.

// initRepoConfigForm creates a config form for the given repo and enters config focus.

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
	launchAgent := a.modals.LaunchAgent()
	for _, ag := range a.dashboard.agentItems() {
		if launchAgent != nil && ag.ID == launchAgent.ID {
			continue
		}
		ag.Resize(h, w)
	}
}

// setError sets an error message that displays for ErrorOverlayTicks of the
// dashboard tick (~3 seconds at the current 100ms tick interval).
func (a *App) setError(msg string) {
	a.err = msg
	a.errTicks = ErrorOverlayTicks
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
	sess := a.modals.LaunchSession()
	if sess == nil {
		return -1
	}
	agents := sess.Agents()
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

// panelServices builds a fresh PanelServices struct closing over a's current
// state. Panels receive this on every Update so they always see live App
// state without holding a back-pointer. Cmd factories returned here must be
// pure: they produce tea.Cmds, never mutate App directly.
func (a *App) panelServices() PanelServices {
	return PanelServices{
		Width:         a.width,
		Height:        a.height,
		DashboardTopY: a.dashboardTopY(),
		Manager: func(repoPath string) SessionManager {
			return a.managers[repoPath]
		},
		Resolved: func(repoPath string) config.ResolvedSettings {
			return a.resolvedCache[repoPath]
		},
		GHClient: func() *github.Client { return a.ghClient },
		PRCache: func(repoPath, sessionID string) *prCacheEntry {
			return a.prCache[cacheKey(repoPath, sessionID)]
		},
		ReviewCache: func(repoPath, sessionID string) *reviewDiffEntry {
			return a.reviewDiffCache[cacheKey(repoPath, sessionID)]
		},
		ClosePanel: func() {
			// Drop the active overlay panel and return focus to the pipeline.
			// Modals.Close enforces the invariant by nilling every owned model.
			a.closeModal()
		},
		OpenInLaunch: func(sess *agent.Session, repoPath string) bool {
			return a.openSessionInFocusLaunch(sess, repoPath)
		},
		OpenPlanEditor: func(sess *agent.Session, repoPath string) {
			a.openPlanEditor(sess, repoPath)
		},
		OpenURL:  openURL,
		SetError: a.setError,
		MergePRCmd: func(sessionID, repoPath string, force bool) tea.Cmd {
			if force {
				return a.forceMergePRCmd(sessionID, repoPath)
			}
			return a.mergePRCmd(sessionID, repoPath)
		},
		StartPRDraftCmd: func(sess *agent.Session, repoPath string, transitionShipping bool) tea.Cmd {
			a.prDraftInFlight = true
			a.prDraftSessionID = sess.ID
			a.prDraftRepoPath = repoPath
			a.prModalTransitionShipping = transitionShipping
			return a.startPRDraftCmd(sess, repoPath, transitionShipping)
		},
		KillSessionCmd: func(sess *agent.Session, repoPath string) tea.Cmd {
			if repoPath == "" {
				return nil
			}
			mgr := a.managers[repoPath]
			if mgr == nil {
				return nil
			}
			var agentIDs []string
			for _, ag := range sess.Agents() {
				agentIDs = append(agentIDs, ag.ID)
				a.closingAgents[agentCacheKey(repoPath, ag.ID)] = true
			}
			sessID := sess.ID
			a.closingSessions[cacheKey(repoPath, sessID)] = true
			return func() tea.Msg {
				return killResultMsg{
					scope:     killScopeSession,
					repoPath:  repoPath,
					sessionID: sessID,
					agentIDs:  agentIDs,
					err:       filterNotFound(mgr.KillSession(sessID)),
				}
			}
		},
		FetchReviewDiff: func(sess *agent.Session, repoPath string) tea.Cmd { return a.fetchReviewDiffCmd(sess, repoPath) },
		prDraftInFlightFor: func(sessionID, repoPath string) bool {
			return a.prDraftInFlight && a.prDraftSessionID == sessionID && a.prDraftRepoPath == repoPath
		},
		ValidationRuns: func(sessID string) *validationRunState {
			return a.validationRuns[sessID]
		},
		TriggerValidationRerun: func(sessID, repoPath, worktreePath string, checks []config.ValidationCheck) tea.Cmd {
			return triggerValidationRun(a, sessID, repoPath, worktreePath, checks)
		},
		FeedbackTriage: func(repoPath, sessionID string) map[string]*feedbackTriageEntry {
			return a.feedbackTriage[cacheKey(repoPath, sessionID)]
		},
		SetFeedbackVerdict: a.setFeedbackVerdict,
		SetFeedbackNote:    a.setFeedbackNote,
	}
}

// dashboardTopY returns the screen Y offset where the dashboard content
// begins, accounting for any error or confirm-quit rows rendered above it.
func (a *App) dashboardTopY() int {
	y := 0
	if a.err != "" {
		y++
	}
	if a.confirmQuit {
		y++
	}
	return y
}

func (a *App) refreshAgentList() {
	a.dashboard.closingAgents = a.closingAgents
	a.dashboard.closingSessions = a.closingSessions
	a.dashboard.sessionElapsed = time.Since(a.wellness.sessionStart)
	a.dashboard.lastReviewAt = a.wellness.lastReviewAt
	a.dashboard.focusSessionMinutes = a.wellness.focusSessionMinutes
	a.dashboard.focusBreakMode = a.wellness.focusBreakMode
	if a.wellness.focusBreakMode {
		a.dashboard.focusBreakElapsed = time.Since(a.wellness.focusBreakStart)
	} else {
		a.dashboard.focusBreakElapsed = 0
	}
	a.dashboard.focusBlockCount = a.wellness.focusBlockCount
	a.dashboard.focusBreakMinutes = a.wellness.focusBreakMinutes
	a.dashboard.focusBreakAnimFrame = a.wellness.focusBreakAnimFrame
	a.dashboard.focusBreakShortWarning = a.wellness.focusBreakShortWarning
	a.dashboard.focusBreakTimerUp = a.wellness.focusBreakTimerUp
	a.dashboard.cursor = a.cursor
	a.dashboard.prDraftSessionID = a.prDraftSessionID
	a.dashboard.prDraftRepoPath = a.prDraftRepoPath
	a.dashboard.activeRepoName = a.activeRepoDisplayName()
	a.dashboard.activeRepoPath = a.activeRepo
	a.syncModalsToDashboard()
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
	a.cursor.Clamp(a.dashboard.sectionCounts())
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

	rowCursor := headerRows + sepRows + pipelineRows + blankRows
	for _, section := range focusSectionsInOrder() {
		items := a.dashboard.sectionItems(section)
		if len(items) == 0 {
			continue
		}
		rowCursor += labelRows
		for i := range items {
			rowH := rowsPerItem(section)
			start := rowCursor
			end := start + rowH
			if dashboardContentY >= start && dashboardContentY < end {
				return section, i, true
			}
			rowCursor = end
			if i < len(items)-1 {
				rowCursor += blankRows
			}
		}
		rowCursor += blankRows
	}
	return focusSectionPlanning, 0, false
}

// cursorSelectedSession returns the session under the pipeline cursor, or nil
// when the cursor's section is empty. Workflow keys (c, x, X, t, e, p, d) use
// this rather than dashboard.selectedSession() because the pipeline addresses
// sessions by section + index, not by a `selected` row in the items list.
func (a *App) cursorSelectedSession() *agent.Session {
	section := a.cursor.Section()
	items := a.dashboard.sectionItems(section)
	if len(items) == 0 {
		return nil
	}
	idx := a.cursor.Index(section)
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

// syncFocusCursorToDashboard mirrors the cursor-related App fields onto the
// dashboard model so the next render reflects navigation immediately, without
// waiting for the 100ms tick that drives refreshAgentList.
func (a *App) syncFocusCursorToDashboard() {
	a.dashboard.cursor = a.cursor
	a.dashboard.prDraftSessionID = a.prDraftSessionID
	a.dashboard.prDraftRepoPath = a.prDraftRepoPath
}

// activateFocusCursor opens the row currently under the fullscreen-focus
// cursor. Planning + Building rows jump into a focusLaunch terminal so the
// user can drive the agent. Reviewing rows open the review panel. Shipping
// rows open the PR URL when one is cached, otherwise fall back to the agent
// terminal so the user can run gh manually. Returns ok=false when the cursor's
// section has no actionable row.
func (a *App) activateFocusCursor() (tea.Cmd, bool) {
	section := a.cursor.Section()
	items := a.dashboard.sectionItems(section)
	if len(items) == 0 {
		return nil, false
	}
	idx := a.cursor.Index(section)
	if idx >= len(items) {
		idx = len(items) - 1
	}
	sess := items[idx].session

	switch section {
	case focusSectionPlanning:
		// Planning rows open the plan editor — there is no agent yet to drop
		// into a focusLaunch terminal. Drafting sessions also live in the
		// Planning section; the editor renders a "Drafting…" placeholder
		// until the background draft lands and reloads.
		a.openPlanEditor(sess, items[idx].repoPath)
		return nil, true
	case focusSectionBuilding:
		return nil, a.openSessionInFocusLaunch(sess, items[idx].repoPath)
	case focusSectionReview:
		sess.SetLifecyclePhase(agent.LifecycleInReview)
		rp := items[idx].repoPath
		a.openReview(newReviewPanel(sess, rp, a.width, a.height))
		if _, ok := a.reviewDiffCache[cacheKey(rp, sess.ID)]; !ok {
			return a.fetchReviewDiffCmd(sess, rp), true
		}
		return nil, true
	case focusSectionShipping:
		a.openShipping(newShippingPanel(sess, items[idx].repoPath, a.width, a.height-1))
		return nil, true
	}
	return nil, false
}

// openSessionInFocusLaunch picks the most-active agent in sess and opens it
// fullscreen in focusLaunch. repoPath is required so ctrl+t / ctrl+n / ctrl+w
// inside the launch view can route to the correct repo without falling back
// to an ambiguous sessionID lookup. Priority is shared with
// Session.PrimaryAgent via agent.AgentStatusPriority. Falls back to agents[0]
// when all have equal priority.
func (a *App) openSessionInFocusLaunch(sess *agent.Session, repoPath string) bool {
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
	a.openLaunchPanel(sess, target, repoPath)
	a.dashboard.scrollOffset = 0
	target.Resize(a.focusLaunchTermHeight(), a.dashboard.width)
	return true
}

// submitPromptModal handles a promptModalSubmitMsg by creating a session
// and dispatching to either the plan-drafting flow (default `enter`) or
// today's immediate-spawn flow (`ctrl+enter` skip). The modal has already
// closed itself by the time this fires.
func (a App) View() tea.View {
	var content string

	switch a.view {
	case ViewDashboard:
		if rp := a.modals.Review(); rp != nil {
			var panelStr string
			if a.prComposeModal.Active() {
				panelStr = a.prComposeModal.View()
			} else {
				panelStr = rp.View(a.panelServices())
			}
			v := tea.NewView(panelStr)
			v.AltScreen = true
			return v
		}
		if sp := a.modals.Shipping(); sp != nil {
			panel := sp.View(a.panelServices())
			if sp.NoteActive() {
				panel = lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center, sp.NoteView())
			}
			v := tea.NewView(panel)
			v.AltScreen = true
			return v
		}
		if pe := a.modals.PlanEditor(); pe != nil {
			v := tea.NewView(pe.View())
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
			if a.wellness.focusBreakMode {
				if a.wellness.focusBreakTimerUp {
					hints = []keyHint{{key: "b", desc: "resume focus"}}
				} else {
					hints = []keyHint{{key: "b", desc: "exit early"}}
				}
			} else if a.cursor.Section() != focusSectionPlanning && a.wellness.focusSessionMinutes > 0 {
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
			body = a.prComposeModal.View()
		}
		// Agent-limit modal overlay: replace body with centered modal when active.
		if a.sessionLimitModalActive {
			modalW := a.width / 2
			if modalW > 60 {
				modalW = 60
			}
			if modalW < 40 {
				modalW = 40
			}
			activeCount := a.activeSessionCount()
			limitLine := StyleWarning.Render(fmt.Sprintf(
				"You're already running %d active sessions — beyond ~3, oversight cost exceeds output value.",
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
		hints := repoPickerHints
		if a.repoPicker.mode == repoPickerModeManage {
			hints = repoPickerManageHints
		}
		statusbar := renderStatusBar(hints, a.width)
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
		if ag := a.modals.LaunchAgent(); ag != nil && a.dashboard.scrollOffset == 0 {
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
			return diffStatsMsg{sessionID: sessionID, repoPath: repoPath, stats: nil}
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
			repoPath:  repoPath,
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
	repoPath := a.dashboard.selectedRepoPath()
	if repoPath == "" {
		a.dashboard.diffStats = nil
		return
	}
	if entry, ok := a.diffStatsCache[cacheKey(repoPath, sess.ID)]; ok {
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

// cacheKey composes a repo-scoped key for App-level per-session caches.
// Always use this — never key a session cache by sessionID alone. Session
// IDs are minted by a per-manager counter (session-1, session-2, …), so with
// multiple repos configured the same ID exists in two managers and a bare-ID
// key clobbers across repos.
//
// The "\x00" separator is safe because POSIX paths and session IDs never
// contain NUL.
func cacheKey(repoPath, sessionID string) string {
	return repoPath + "\x00" + sessionID
}

// agentCacheKey is the per-agent equivalent of cacheKey. Agent IDs are
// generated as {sessionID}-agent-N (see internal/agent/session.go), so they
// inherit the same per-manager collision and need the same scoping.
func agentCacheKey(repoPath, agentID string) string {
	return repoPath + "\x00" + agentID
}

// repoPathForSession returns the repo path containing the given session, or
// "" if not found. Fails closed (returns "") when more than one repo claims
// the same session ID, so callers don't silently route to the wrong repo.
//
// Prefer passing repoPath explicitly through messages and panel state. This
// helper exists as a fallback for the few message paths (notably the plan
// editor) where the editor model already holds repoPath but a fallback is
// useful when that model has been torn down mid-flight.
func (a *App) repoPathForSession(sessionID string) string {
	if a.cfg == nil {
		return ""
	}
	var found string
	for _, repo := range a.cfg.Repos {
		mgr := a.managers[repo.Path]
		if mgr == nil {
			continue
		}
		for _, sess := range mgr.ListSessions() {
			if sess.ID == sessionID {
				if found != "" {
					// Ambiguous: same session ID exists in multiple repos.
					// Fail closed rather than guess.
					return ""
				}
				found = repo.Path
			}
		}
	}
	return found
}

// sessionByIDInRepo returns the session with the given ID from the manager at
// repoPath. Returns nil when the manager is not found or the session does not
// exist. This is the unambiguous lookup; callers must thread repoPath through
// from a message, panel, or dashboard listItem rather than searching by ID.
func (a *App) sessionByIDInRepo(repoPath, sessionID string) *agent.Session {
	mgr := a.managers[repoPath]
	if mgr == nil {
		return nil
	}
	for _, sess := range mgr.ListSessions() {
		if sess.ID == sessionID {
			return sess
		}
	}
	return nil
}

// cleanStaleCaches removes diff stats and PR cache entries for sessions that no
// longer exist. Keys are composed via cacheKey(repoPath, sessionID), so the
// active set must be built the same way — iterating the managers map gives both
// repoPath and the session list together.
func (a *App) cleanStaleCaches() {
	activeSessions := make(map[string]bool)
	activeAgents := make(map[string]bool)
	for repoPath, mgr := range a.managers {
		for _, sess := range mgr.ListSessions() {
			activeSessions[cacheKey(repoPath, sess.ID)] = true
			for _, ag := range sess.Agents() {
				activeAgents[agentCacheKey(repoPath, ag.ID)] = true
			}
		}
	}
	for k := range a.diffStatsCache {
		if !activeSessions[k] {
			delete(a.diffStatsCache, k)
		}
	}
	for k := range a.prCache {
		if !activeSessions[k] {
			delete(a.prCache, k)
		}
	}
	for k := range a.prPollStates {
		if !activeSessions[k] {
			delete(a.prPollStates, k)
		}
	}
	for k := range a.reviewDiffCache {
		if !activeSessions[k] {
			delete(a.reviewDiffCache, k)
		}
	}
	for k := range a.feedbackTriage {
		if !activeSessions[k] {
			delete(a.feedbackTriage, k)
		}
	}
	for k := range a.lastKnownStatus {
		if !activeAgents[k] {
			delete(a.lastKnownStatus, k)
		}
	}
}

// pollAllSessions returns Cmds for sessions that are due for a PR status poll.
// It respects adaptive intervals and limits concurrent in-flight polls.
func (a *App) activeSessionCount() int {
	count := 0
	for _, mgr := range a.managers {
		count += mgr.ActiveSessionCount()
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

// writeWellnessLog appends a single JSON line to <repoPath>/.refrain/logs/wellness.log.
// Best-effort: any error is silently dropped so it never blocks shutdown.
func (a *App) writeWellnessLog() {
	repoPath := a.activeRepo
	if repoPath == "" && a.cfg != nil && len(a.cfg.Repos) > 0 {
		repoPath = a.cfg.Repos[0].Path
	}
	if repoPath == "" {
		return
	}

	logPath := filepath.Join(repoPath, ".refrain", "logs", "wellness.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return
	}

	elapsed := time.Since(a.wellness.appStart)
	entry := wellnessLogEntry{
		Date:            time.Now().UTC().Format(time.RFC3339),
		DurationMin:     int(elapsed.Minutes()),
		AgentsCreated:   a.wellness.agentsCreatedCount,
		SessionsCreated: a.wellness.sessionsCreatedCount,
		BlocksCompleted: a.wellness.focusBlockCount,
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

// writePlannerLog appends a single JSON line to <repoPath>/.refrain/logs/planner.log.
// Best-effort: any error is silently dropped so it never blocks the UI loop.
// repoPath is passed explicitly (rather than read from a.activeRepo) because
// the caller has the exact repo path from the message in multi-repo configs.
func (a *App) writePlannerLog(repoPath string, entry plannerLogEntry) {
	if repoPath == "" {
		return
	}
	logPath := filepath.Join(repoPath, ".refrain", "logs", "planner.log")
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

// validationCheckState tracks the lifecycle of one validation check run.
type validationCheckState int

const (
	checkPending validationCheckState = iota
	checkRunning
	checkPassed
	checkFailed
	checkError
)

// validationCheckResult holds the result of one completed (or pending) check.
type validationCheckResult struct {
	state    validationCheckState
	output   string
	exitCode int
	duration time.Duration
	err      error
}

// validationCheckResultMsg is emitted by a check goroutine when it finishes.
type validationCheckResultMsg struct {
	sessionID  string
	repoPath   string
	checkIndex int
	runID      int
	state      validationCheckState
	output     string
	exitCode   int
	duration   time.Duration
	err        error
}

// validationRunState holds the current run of validation checks for a session.
type validationRunState struct {
	checks  []config.ValidationCheck
	results []validationCheckResult
	runID   int
}

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

// reviewOpenTaskDiffMsg is emitted by the review panel when the user presses
// enter on a task row that has a non-empty rawDiff. App opens the full-screen
// diff viewer scoped to that task without closing the review modal.
type reviewOpenTaskDiffMsg struct {
	rawDiff   string
	taskLabel string
}

// reviewDiffMsg carries the result of an async review diff fetch.
type reviewDiffMsg struct {
	sessionID string
	repoPath  string
	entry     *reviewDiffEntry
	err       error
}

// reviewVerdictMsg carries the result of a single per-task reviewer subprocess.
// repoPath identifies the repo that owns sessionID so the handler keys the
// cache by (repoPath, sessionID) and never reads a colliding repo's entry.
type reviewVerdictMsg struct {
	sessionID string
	repoPath  string
	taskIndex int
	verdict   agent.ReviewVerdict
	err       error
}

// fetchReviewDiffCmd returns a Cmd that fetches diff stats for a session to
// populate the review cache. When the session has a plan, it also computes
// per-task commit groups and per-group diff stats so the review panel can
// render a task-by-task view.
func ensureGitignore(path string) {
	const entry = ".refrain/"
	gitignorePath := filepath.Join(path, ".gitignore")

	// Check if .gitignore exists and already contains .refrain/.
	data, _ := os.ReadFile(gitignorePath)
	if len(data) > 0 {
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) == entry {
				return // already present
			}
		}
	}

	// Append .refrain/ to .gitignore.
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
