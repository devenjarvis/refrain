package agent

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/devenjarvis/refrain/internal/config"
	"github.com/devenjarvis/refrain/internal/git"
	"github.com/devenjarvis/refrain/internal/hook"
	"github.com/devenjarvis/refrain/internal/migrate"
	"github.com/devenjarvis/refrain/internal/planner"
	"github.com/devenjarvis/refrain/internal/setlist"
	"github.com/devenjarvis/refrain/internal/songs"
	"github.com/devenjarvis/refrain/internal/state"
)

// EventType represents the kind of agent event.
type EventType int

const (
	EventCreated EventType = iota
	EventStatusChanged
	EventOutput
	EventDone
	EventError
	EventSessionClosed
	// EventBranchRenamed fires after a session's branch has been renamed
	// (e.g. by the smart-branch-name Haiku flow). Consumers should refresh
	// any state keyed by branch name — notably PR lookups — since GitHub
	// may still have the PR indexed under the old name until the next push.
	EventBranchRenamed
)

// Event represents something that happened to an agent.
type Event struct {
	Type      EventType
	AgentID   string
	SessionID string
	Status    Status
	// Branch is populated only for EventBranchRenamed and carries the new
	// branch name. Zero-value for all other event types.
	Branch string
}

// Manager manages the lifecycle of all sessions and their agents.
type Manager struct {
	repoPath string
	settings config.ResolvedSettings

	mu       sync.RWMutex
	sessions map[string]*Session
	// pendingNames holds session names reserved by an in-flight
	// createSessionWorktree / createSessionOnBranchWorktree call. The git
	// worktree creation runs without holding mu so reads of m.sessions don't
	// block on git I/O; this set lets concurrent callers see the reservation
	// and pick a different name.
	pendingNames map[string]struct{}
	// checkoutPending reserves the repo's single checkout-session slot while
	// a CreateSessionInDir call is in flight but the session isn't yet in
	// m.sessions. Mirrors pendingNames: without it, two concurrent calls
	// could both pass the one-per-repo check before either publishes.
	checkoutPending bool
	nextID          int

	events       chan Event
	done         chan struct{}
	watchers     sync.WaitGroup
	shutdownOnce sync.Once

	// sendsMu protects sends into m.events and m.plannerQuestions from racing
	// the close() calls in Shutdown/Detach. emit() and the pumpPlannerQuestions
	// send case acquire RLock; Shutdown/Detach acquire Lock around setting
	// sendsClosed and closing the channels. An atomic flag is not enough —
	// even with a pre-check, a concurrent send can pass the check and then
	// hit the close, panicking with "send on closed channel". The lock
	// ensures any send completes (or is short-circuited) before close runs.
	sendsMu     sync.RWMutex
	sendsClosed bool

	hookServer     *hook.Server
	hookSocketPath string
	hookDispatcher sync.WaitGroup

	branchNamer    BranchNamer
	taskSummarizer TaskSummarizer
	planDrafter    PlanDrafter
	reviewerAgent  ReviewerAgent

	// plannerQuestions aggregates ask_user calls from every in-flight draft's
	// per-session question server. The TUI App reads from PlannerQuestions()
	// and routes each event to the matching plan editor by SessionID.
	plannerQuestions chan PlannerQuestion
}

// PlannerQuestion is a clarifying question raised by the planner Sonnet
// subprocess for SessionID. The receiver MUST eventually send a single
// answer (possibly empty) on AnswerCh; failing to do so leaves the planner
// subprocess parked on the IPC connection until the manager cancels it.
type PlannerQuestion struct {
	SessionID string
	Question  string
	AnswerCh  chan<- string
}

// haikuNamePerAttemptTimeout bounds how long a single Haiku summarization
// subprocess may run before it's cancelled. With buildClaudeHaikuArgs's
// always-on speedup flags (and --bare when ANTHROPIC_API_KEY is set) cold
// starts typically drop well under 10s, but 45s leaves slack for the long
// tail without truncating retry attempts inside the overall budget.
//
// Declared as var (not const) so tests can swap to fast values via
// setHaikuRetryForTesting.
var haikuNamePerAttemptTimeout = 45 * time.Second

// haikuNameOverallTimeout caps the total wall-clock time spent across all
// retry attempts. Sized to cover 3 × per-attempt + 1s + 3s backoff. On
// timeout the random branch persists and the next actionable prompt retries.
var haikuNameOverallTimeout = 140 * time.Second

// haikuNameAttempts is the maximum number of times callNamerWithRetry will
// invoke the namer per UserPromptSubmit. Each attempt is bounded by
// haikuNamePerAttemptTimeout; the whole sequence is bounded by
// haikuNameOverallTimeout.
var haikuNameAttempts = 3

// haikuNameBackoff is the wait between attempts N and N+1. Linear small
// backoff is enough at this scale — exponential is overkill for 3 attempts.
var haikuNameBackoff = []time.Duration{1 * time.Second, 3 * time.Second}

// haikuSummaryPerAttemptTimeout / OverallTimeout / Attempts / Backoff bound the
// task-summary retry loop. Same shape as the branch-namer budgets but a
// shorter overall ceiling: summaries are advisory display text, so we don't
// want to keep a retry sequence alive for as long as the rename flow does.
//
// Declared as vars so tests can swap to fast values via TestMain.
var (
	haikuSummaryPerAttemptTimeout = 45 * time.Second
	haikuSummaryOverallTimeout    = 120 * time.Second
	haikuSummaryAttempts          = 3
	haikuSummaryBackoff           = []time.Duration{1 * time.Second, 3 * time.Second}
)

// planDraftAttempts / planDraftPerAttemptCap / planDraftBackoff bound the
// per-draft retry loop. The claude CLI does its own internal retry-with-backoff
// before exiting non-zero, so the outer loop stays small: 3 attempts with 2s
// and 5s gaps keeps worst-case wall-clock under ~15s of added overhead.
// Declared as vars so tests can swap to fast values via setPlanDraftRetryForTesting.
//
// planDraftPerAttemptCap is 10 minutes (not 5): the richer prompt introduced
// in dcf8b94 instructs the drafter to research aggressively before writing,
// and on larger codebases that research phase routinely exceeds 5 minutes,
// causing a SIGKILL (signal killed, empty stdout) before any output is written.
var (
	planDraftAttempts      = 3
	planDraftPerAttemptCap = 10 * time.Minute
	planDraftBackoff       = []time.Duration{2 * time.Second, 5 * time.Second}
)

// ErrSessionNotFound is returned by KillSession when the given session ID is
// not present in the manager. Callers that tolerate concurrent cleanup races
// should use errors.Is to suppress it.
var ErrSessionNotFound = errors.New("session not found")

// NewManager creates a new agent manager for the given repo.
//
// The manager owns a hook.Server listening on <repoPath>/.refrain/hook.sock that
// routes Claude Code hook events to agents by REFRAIN_AGENT_ID. If the socket
// fails to start (e.g. filesystem permissions), the manager logs to stderr
// and continues with hooks disabled; spawned agents will then never transition
// out of Active.
func NewManager(repoPath string, settings config.ResolvedSettings) *Manager {
	m := &Manager{
		repoPath:         repoPath,
		settings:         settings,
		sessions:         make(map[string]*Session),
		pendingNames:     make(map[string]struct{}),
		events:           make(chan Event, 64),
		done:             make(chan struct{}),
		branchNamer:      DefaultBranchNamer(),
		taskSummarizer:   DefaultTaskSummarizer(),
		planDrafter:      DefaultPlanDrafter(settings.PlanModel),
		reviewerAgent:    DefaultReviewerAgent(settings.ReviewerModel),
		plannerQuestions: make(chan PlannerQuestion, 8),
	}

	if err := migrate.RepoState(repoPath); err != nil {
		fmt.Fprintf(os.Stderr, "refrain: %v\n", err)
	}
	refrainDir := filepath.Join(repoPath, ".refrain")
	if err := os.MkdirAll(refrainDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "refrain: creating %s: %v (hooks disabled)\n", refrainDir, err)
		return m
	}
	socketPath := hookSocketPath(repoPath)
	srv, err := hook.NewServer(socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "refrain: starting hook server on %s: %v (hooks disabled)\n", socketPath, err)
		return m
	}
	m.hookServer = srv
	m.hookSocketPath = socketPath

	m.hookDispatcher.Add(1)
	go m.dispatchHookEvents()

	return m
}

// HookSocketPath returns the unix socket path the manager's hook server is
// listening on, or "" if the server failed to start.
func (m *Manager) HookSocketPath() string {
	return m.hookSocketPath
}

// SetBranchNamer overrides the BranchNamer used for smart branch renaming.
// Intended for tests so they can stub out the Haiku subprocess. Safe to call
// while the manager is running — the namer is swapped atomically.
func (m *Manager) SetBranchNamer(n BranchNamer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.branchNamer = n
}

// SetTaskSummarizer overrides the TaskSummarizer used for generating plain-English
// task descriptions. Passing nil disables the feature gracefully. Safe to call
// while the manager is running — the summarizer is swapped atomically.
func (m *Manager) SetTaskSummarizer(ts TaskSummarizer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.taskSummarizer = ts
}

// SetPlanDrafter overrides the PlanDrafter used for generating and revising
// plan markdown. Intended for tests so they can stub out the Sonnet
// subprocess. Passing nil disables planning gracefully — StartDraft will
// return an error rather than spawning a broken subprocess.
func (m *Manager) SetPlanDrafter(p PlanDrafter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.planDrafter = p
}

// SetReviewerAgent overrides the ReviewerAgent used for per-task code review
// subprocesses. Intended for tests so they can stub out the Sonnet subprocess.
// Safe to call while the manager is running — the agent is swapped atomically.
func (m *Manager) SetReviewerAgent(r ReviewerAgent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reviewerAgent = r
}

// ReviewerAgent returns the current ReviewerAgent under a read lock.
func (m *Manager) ReviewerAgent() ReviewerAgent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.reviewerAgent
}

// getTaskSummarizer returns the current TaskSummarizer under a read lock.
func (m *Manager) getTaskSummarizer() TaskSummarizer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.taskSummarizer
}

// hookSocketPath returns the unix socket path for a given repoPath.
//
// Preferred layout: <repoPath>/.refrain/hook.sock — easy to inspect and cleaned
// up with the rest of refrain's per-repo state. macOS limits unix socket paths
// to 104 bytes, so when the preferred path would exceed a safe threshold we
// fall back to a short hashed name under os.TempDir(). Tests exercise the
// fallback path via deeply nested temp directories.
func hookSocketPath(repoPath string) string {
	preferred := filepath.Join(repoPath, ".refrain", "hook.sock")
	// 104 is the darwin sun_path limit; leave headroom for the trailing NUL
	// and any quirks. 100 is comfortably below.
	if len(preferred) < 100 {
		return preferred
	}
	h := sha256.Sum256([]byte(repoPath))
	return filepath.Join(os.TempDir(), fmt.Sprintf("refrain-%x.sock", h[:8]))
}

// dispatchHookEvents reads hook events from the server and routes each to the
// agent named by AgentID. Unknown agent IDs are dropped silently. On the
// first UserPromptSubmit for an agent, the prompt is forwarded to the
// configured branch namer (Haiku by default) so the session's branch and
// display name update once a meaningful name can be derived.
func (m *Manager) dispatchHookEvents() {
	defer m.hookDispatcher.Done()
	for e := range m.hookServer.Events() {
		a, sess := m.findAgentAndSession(e.AgentID)
		if a == nil {
			// This can happen if an agent has already been killed but Claude's
			// final Stop/SessionEnd hook is still in flight. Drop silently.
			continue
		}
		sessID := sess.ID
		if changed := a.OnHookEvent(e); changed {
			// Refresh commit task count synchronously before emitting the
			// status-change event so the TUI has current values when it
			// evaluates auto-promotion.
			if e.Kind == hook.KindStop && sess.LifecyclePhase() == LifecycleInProgress {
				if err := sess.RefreshCommitTaskCount(); err != nil {
					fmt.Fprintf(os.Stderr, "refrain: refresh commit task count: %v\n", err)
				}
			}
			m.emit(Event{
				Type:      EventStatusChanged,
				AgentID:   a.ID,
				SessionID: sessID,
				Status:    a.Status(),
			})
		}

		if e.Kind == hook.KindUserPromptSubmit {
			// Capture the original prompt before dispatching the rename so
			// retries on later UserPromptSubmit events drive the namer with
			// the user's first intent, not the most recent follow-up prompt.
			// SetOriginalPrompt is idempotent — only the first non-empty
			// actionable prompt sticks.
			if IsActionablePrompt(e.Prompt) {
				sess.SetOriginalPrompt(e.Prompt)
			}
			m.maybeRenameFromPrompt(sess, a, e.Prompt)
			m.maybeStartTaskSummary(sess)
		}
	}
}

// maybeRenameFromPrompt renames the session's branch and updates the agent
// and session display names based on the user's first real prompt by asking
// the configured BranchNamer (Haiku by default) to summarize it. Idempotent:
// sessions that already have a Claude-derived name are skipped, and noise
// prompts (empty, whitespace, slash-only) are ignored so the next prompt
// gets another chance.
//
// On namer error or empty result, the session keeps its random branch and
// hasClaudeName stays false — the next UserPromptSubmit will retry.
// Session.TryStartRename gates double-dispatch, so a second prompt arriving
// mid-Haiku is a no-op rather than a duplicate subprocess.
func (m *Manager) maybeRenameFromPrompt(sess *Session, a *Agent, prompt string) {
	if sess == nil || sess.HasClaudeName() {
		return
	}
	// A stray UserPromptSubmit for a Done/Error agent must not trigger
	// a rename or auto-name update on a terminal row.
	if st := a.Status(); st == StatusDone || st == StatusError {
		return
	}
	// Empty / slash-only prompts carry no actionable text. Skip before
	// touching TryStartRename so legitimate retries on the next prompt
	// aren't blocked by an in-flight gate that will never be released.
	if !IsActionablePrompt(prompt) {
		return
	}

	// Prefer the session's original prompt (captured by the dispatcher on the
	// first actionable UserPromptSubmit) so retries after a failed first
	// rename drive the namer with the user's original intent rather than
	// whatever follow-up prompt happened to land next.
	if original := sess.OriginalPrompt(); original != "" {
		prompt = original
	}

	m.mu.RLock()
	prefix := m.settings.BranchPrefix
	template := m.settings.BranchNamePrompt
	namer := m.branchNamer
	m.mu.RUnlock()

	if namer == nil {
		return
	}

	// Mark the agent as auto-named so its label is no longer treated as
	// a placeholder. We do this even when Haiku ultimately fails — the
	// session-level gate (sess.HasClaudeName) drives the rename retry,
	// while this flag just tells the rest of the system "this agent had
	// its naming chance" so we don't, e.g., resume-restore a placeholder
	// over a user-set name later.
	a.SetClaudeName(true)

	// Render the user-configurable instruction template. Forgive a
	// missing placeholder by appending the prompt — never silently drop
	// the user's text.
	var instruction string
	if strings.Contains(template, "{prompt}") {
		instruction = strings.ReplaceAll(template, "{prompt}", prompt)
	} else {
		instruction = template + "\n\n" + prompt
	}

	if !sess.TryStartRename() {
		return
	}

	m.watchers.Add(1)
	go func() {
		defer m.watchers.Done()
		defer sess.finishRename()

		ctx, cancel := context.WithTimeout(context.Background(), haikuNameOverallTimeout)
		defer cancel()

		// Cancel the Haiku subprocess if the manager shuts down mid-call.
		doneCh := make(chan struct{})
		defer close(doneCh)
		go func() {
			select {
			case <-m.done:
				cancel()
			case <-doneCh:
			}
		}()

		repoPath := m.repoPath
		sessID := sess.ID
		logAttempt := func(attempt int, s string, err error, took time.Duration) {
			haikuLogAttempt(repoPath, sessID, haikuKindBranch, attempt, s, err, took)
		}

		start := time.Now()
		suffix, err := callNamerWithRetry(
			ctx, namer, instruction, m.done,
			haikuNameAttempts, haikuNamePerAttemptTimeout, haikuNameBackoff,
			logAttempt,
		)
		if err != nil || suffix == "" {
			haikuLogOutcome(repoPath, sessID, haikuKindBranch, suffix, err, time.Since(start))
			return
		}

		newBranch := config.ExpandBranchPrefix(prefix) + suffix
		if !m.renameSessionBranch(sess, newBranch) {
			haikuLogOutcome(repoPath, sessID, haikuKindBranch, "", fmt.Errorf("git rename failed or session closed (target=%s)", newBranch), time.Since(start))
			return
		}
		haikuLogOutcome(repoPath, sessID, haikuKindBranch, suffix, nil, time.Since(start))
		// The session display name updates to the Haiku-derived task name so
		// the sidebar separator shows what the session is working on. Agents
		// keep their stable track identities (Track 1, Track 2, ...) and are
		// never renamed here. Both writes must happen before EventBranchRenamed
		// fires so subscribers (PR scheduler, TUI) see a coherent snapshot.
		sess.SetDisplayName(suffix)
		m.emitBranchRenamed(sess, a, newBranch)
	}()
}

// maybeStartTaskSummary generates a short plain-English task description from
// the session's original prompt and stores it via Session.SetTaskSummary.
// It reads sess.OriginalPrompt() (set idempotently by the dispatcher) so
// slash-only and empty prompts that never stored an original prompt are
// silently skipped. Session.TryStartTaskSummary gates double-dispatch so a
// second prompt arriving while the summarizer is running is a no-op.
//
// Failures are retried by callHaikuWithRetry up to haikuSummaryAttempts times,
// with each attempt + the final outcome written to .refrain/logs/haiku.log
// under kind=summary so a single shared file traces both the branch-namer
// and the summarizer flows. The DefaultTaskSummarizer's silent ("", nil)
// return contract is preserved at the public boundary: this function always
// stores the summary (possibly "") via Session.SetTaskSummary so the
// summarizing flag clears and the session moves on.
func (m *Manager) maybeStartTaskSummary(sess *Session) {
	if sess == nil {
		return
	}

	prompt := sess.OriginalPrompt()
	if prompt == "" {
		return
	}

	ts := m.getTaskSummarizer()
	if ts == nil {
		return
	}

	if !sess.TryStartTaskSummary() {
		return
	}

	m.watchers.Add(1)
	go func() {
		defer m.watchers.Done()
		defer sess.finishTaskSummary()

		ctx, cancel := context.WithTimeout(context.Background(), haikuSummaryOverallTimeout)
		defer cancel()

		// Cancel the summarizer subprocess if the manager shuts down mid-call.
		doneCh := make(chan struct{})
		defer close(doneCh)
		go func() {
			select {
			case <-m.done:
				cancel()
			case <-doneCh:
			}
		}()

		repoPath := m.repoPath
		sessID := sess.ID
		logAttempt := func(attempt int, result string, err error, took time.Duration) {
			haikuLogAttempt(repoPath, sessID, haikuKindSummary, attempt, result, err, took)
		}

		// Bind the summarizer to a per-attempt callable. The inner subprocess
		// is invoked via the configured TaskSummarizer so tests can stub
		// failures via Manager.SetTaskSummarizer; the helper sees real
		// (ctx, error) results and can decide whether to retry.
		call := func(attemptCtx context.Context) (string, error) {
			return ts(attemptCtx, prompt)
		}

		start := time.Now()
		summary, err := callHaikuWithRetry(
			ctx, call, m.done,
			haikuSummaryAttempts, haikuSummaryPerAttemptTimeout, haikuSummaryBackoff,
			false, // empty result is retryable for summaries
			logAttempt,
		)
		haikuLogOutcome(repoPath, sessID, haikuKindSummary, summary, err, time.Since(start))

		// Always set the summary so finishTaskSummary clears the flag and the
		// session never sits indefinitely with a stale "summarizing" state.
		// Failures coerce to "" — callers treat that as "no summary available".
		if err != nil {
			summary = ""
		}
		sess.SetTaskSummary(summary)
	}()
}

// ErrDraftInFlight is returned when StartDraft is called for a session that
// already has an in-flight drafting subprocess. Surfaces double-dispatch from
// the UI layer (e.g. user pressed `enter` twice on the modal) without
// silently spawning a duplicate Sonnet call.
var ErrDraftInFlight = errors.New("draft already in flight for session")

// ErrPlanDrafterNotConfigured is returned when StartDraft is called but the
// manager has no plan drafter (e.g. a test set it to nil to disable).
var ErrPlanDrafterNotConfigured = errors.New("plan drafter not configured")

// draftOptions holds optional per-call parameters for StartDraft / RevisePlan.
type draftOptions struct {
	model string
}

// DraftOption is a functional option for StartDraft and RevisePlan.
type DraftOption func(*draftOptions)

// WithPlanModel returns a DraftOption that overrides the model used for this
// single Draft or Revise call. When set, the value is forwarded into
// DraftRequest.Model / ReviseRequest.Model so the drafter uses it instead of
// its stored model. Does NOT mutate the manager's stored planDrafter.
func WithPlanModel(model string) DraftOption {
	return func(o *draftOptions) { o.model = model }
}

// PlannerQuestions returns a channel that emits one PlannerQuestion per
// `ask_user` tool call raised by any in-flight draft. The channel stays
// open across the manager's lifetime and is closed by Shutdown / Detach.
// The TUI subscribes once at startup and routes each event to the matching
// plan editor.
func (m *Manager) PlannerQuestions() <-chan PlannerQuestion { return m.plannerQuestions }

// StartDraft begins async drafting of a plan for sessionID with the given
// user prompt. Transitions the session to LifecycleDrafting, then spawns a
// goroutine that calls PlanDrafter.Draft, writes the result via
// Session.WritePlan, and transitions to LifecyclePlanning on success or
// LifecyclePlanning(error) on failure. The goroutine is tracked in
// m.watchers so Shutdown drains cleanly; cancellation occurs when m.done
// closes (manager shutdown), KillSession is called, or CancelDraft is
// called directly. Drafting subprocesses are NOT counted against
// MaxConcurrentSessions — they are transient text-generation calls, not
// long-lived agents.
//
// StartDraft also spawns a per-session planner.Server bound to a unix socket
// under .refrain/ so the planner Sonnet subprocess can call ask_user back into
// refrain. The server is created BEFORE the drafter runs and torn down by
// runDraft after the subprocess exits, so a partially-started draft never
// leaks a listener. If the server fails to bind (rare — typically the macOS
// 104-byte sun_path limit), drafting still proceeds with ask_user disabled
// rather than failing the whole flow.
func (m *Manager) StartDraft(sessionID, prompt string, opts ...DraftOption) error {
	m.mu.RLock()
	sess := m.sessions[sessionID]
	drafter := m.planDrafter
	m.mu.RUnlock()

	if sess == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}
	if drafter == nil {
		return ErrPlanDrafterNotConfigured
	}
	if !IsActionablePrompt(prompt) {
		return fmt.Errorf("prompt is empty or non-actionable")
	}

	var o draftOptions
	for _, opt := range opts {
		opt(&o)
	}

	// No wall-clock timeout: Sonnet drafting can legitimately take minutes on
	// complex prompts and the user is actively waiting for the editor. The
	// only cancellation paths are user-initiated — KillSession, manager
	// shutdown via m.done in runDraft, or an explicit CancelDraft.
	ctx, cancel := context.WithCancel(context.Background())
	if !sess.TryStartDraft(cancel) {
		cancel()
		return ErrDraftInFlight
	}

	sess.SetOriginalPrompt(prompt)
	sess.SetLifecyclePhase(LifecycleDrafting)
	sess.SetDraftError(nil)
	m.emit(Event{Type: EventStatusChanged, SessionID: sessionID})

	qServer, qSocket := m.startPlannerQuestionServer(sess.ID)

	m.watchers.Add(1)
	go m.runDraft(ctx, sess, drafter, prompt, qServer, qSocket, o.model)
	return nil
}

// startPlannerQuestionServer brings up a fresh planner.Server for sessionID
// and pumps its events onto m.plannerQuestions tagged with the session ID.
// Returns (nil, "") if binding the socket fails; callers must accept that
// the resulting draft will run without ask_user.
func (m *Manager) startPlannerQuestionServer(sessionID string) (*planner.Server, string) {
	socketPath := plannerQuestionSocketPath(m.repoPath, sessionID)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return nil, ""
	}
	srv, err := planner.NewServer(socketPath)
	if err != nil {
		return nil, ""
	}
	m.watchers.Add(1)
	go m.pumpPlannerQuestions(srv, sessionID)
	return srv, srv.SocketPath()
}

// pumpPlannerQuestions forwards PlannerQuestionEvent values from a
// per-session planner.Server onto the manager-level aggregate channel,
// tagging each one with sessionID. Exits when the server's Events channel
// closes (Server.Close has run).
func (m *Manager) pumpPlannerQuestions(srv *planner.Server, sessionID string) {
	defer m.watchers.Done()
	for ev := range srv.Events() {
		// Same gate as emit(): hold sendsMu.RLock while we test sendsClosed
		// and execute the select. Without this, Shutdown/Detach could close
		// m.plannerQuestions between the check and the send.
		if !m.tryPumpPlannerQuestion(sessionID, ev) {
			ev.AnswerCh <- ""
			return
		}
	}
}

// tryPumpPlannerQuestion forwards a planner ask_user event onto
// m.plannerQuestions, returning false when Shutdown/Detach has gated the
// channel (in which case the caller answers the event with the empty string
// so the planner subprocess unblocks).
func (m *Manager) tryPumpPlannerQuestion(sessionID string, ev planner.PlannerQuestionEvent) bool {
	m.sendsMu.RLock()
	defer m.sendsMu.RUnlock()
	if m.sendsClosed {
		return false
	}
	select {
	case m.plannerQuestions <- PlannerQuestion{
		SessionID: sessionID,
		Question:  ev.Question,
		AnswerCh:  ev.AnswerCh,
	}:
		return true
	case <-m.done:
		return false
	}
}

// plannerQuestionSocketPath returns the unix socket path for a per-session
// planner question server. Mirrors hookSocketPath: prefer .refrain/ for
// observability, fall back to a hashed name in os.TempDir when the path
// would exceed the macOS 104-byte sun_path limit.
func plannerQuestionSocketPath(repoPath, sessionID string) string {
	preferred := filepath.Join(repoPath, ".refrain", "planner-q-"+sessionID+".sock")
	if len(preferred) < 100 {
		return preferred
	}
	h := sha256.Sum256([]byte(repoPath + "|" + sessionID))
	return filepath.Join(os.TempDir(), fmt.Sprintf("refrain-pq-%x.sock", h[:8]))
}

// runDraft executes a Draft call against drafter and writes the resulting
// plan markdown via sess.WritePlan. Any failure path (drafter error, empty
// output, write error) lands the session in LifecyclePlanning with
// DraftError set so the Planning card can render a useful error badge —
// the user can then retry via the editor's revise flow or by pressing
// `n` again. Always emits EventStatusChanged on transition so the UI
// repaints.
//
// qServer is the per-draft planner.Server (may be nil if startup failed);
// it is closed after the drafter returns so a wedged ask_user handler
// drains promptly.
func (m *Manager) runDraft(ctx context.Context, sess *Session, drafter PlanDrafter, prompt string, qServer *planner.Server, qSocket string, model string) {
	defer m.watchers.Done()
	defer sess.finishDraft()
	defer func() {
		if qServer != nil {
			_ = qServer.Close()
		}
	}()

	// Cancel the drafting subprocess if the manager shuts down mid-call.
	doneCh := make(chan struct{})
	defer close(doneCh)
	go func() {
		select {
		case <-m.done:
			sess.CancelDraft()
		case <-doneCh:
		}
	}()

	body, err := runDraftWithRetry(ctx, drafter, DraftRequest{UserPrompt: prompt, Model: model, QuestionSocket: qSocket, Cwd: sess.Worktree.Path}, m.done, func(cur, max int) {
		sess.SetDraftAttempt(cur, max)
	})

	// If the session has already been removed from the manager (e.g. by a
	// completed KillSession), skip the post-draft writes — there is nothing
	// for the resulting state to attach to. Mirrors renameSessionBranch's
	// gating pattern. This does NOT eliminate a narrow race where KillSession
	// is in the middle of Cleanup but has not yet deleted the map entry: in
	// that window WritePlan can still race directory removal. The race is
	// benign — WritePlan returns an error and we set DraftError on a session
	// that's about to be garbage-collected — so we accept it for parity with
	// the rest of the manager.
	m.mu.RLock()
	_, stillOpen := m.sessions[sess.ID]
	m.mu.RUnlock()
	if !stillOpen {
		return
	}

	if err == nil && strings.TrimSpace(body) == "" {
		err = errors.New("planner returned empty plan")
	}
	if err == nil {
		if writeErr := sess.WritePlan(body); writeErr != nil {
			err = writeErr
		}
	}

	sess.SetDraftError(err)
	sess.SetLifecyclePhase(LifecyclePlanning)
	m.emit(Event{Type: EventStatusChanged, SessionID: sess.ID})
}

// ErrReviseInFlight is returned when RevisePlan is called for a session that
// already has an in-flight revising subprocess. Mirrors ErrDraftInFlight.
var ErrReviseInFlight = errors.New("revise already in flight for session")

// ErrNoPlanToRevise is returned when RevisePlan is called against a session
// with no plan yet on disk. Without a current plan there is nothing for the
// drafter to revise from — callers should run StartDraft first or hand-write
// a plan in the editor.
var ErrNoPlanToRevise = errors.New("no plan to revise")

// RevisePlan runs an async revise pass against the session's current plan.
// Saves the current plan to .claude/plan.prev.md before invoking the drafter
// so the user can `u` to undo a single step, then calls PlanDrafter.Revise
// and writes the result via Session.WritePlan. Mirrors StartDraft's
// goroutine-tracked pattern: m.watchers ensures Shutdown drains cleanly,
// CancelRevise / KillSession / Shutdown abort the subprocess. Drafting and
// revising are mutually exclusive — Session.TryStartRevise returns false
// while a draft is in flight.
//
// On success, sess.ReviseError() is nil and the editor reloads the new plan
// via the EventStatusChanged the runner emits. On failure (drafter error,
// empty output, write error), the prior plan is left in place and
// ReviseError is set so the editor can render the failure inline.
func (m *Manager) RevisePlan(sessionID, critique string, opts ...DraftOption) error {
	m.mu.RLock()
	sess := m.sessions[sessionID]
	drafter := m.planDrafter
	m.mu.RUnlock()

	if sess == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}
	if drafter == nil {
		return ErrPlanDrafterNotConfigured
	}
	if strings.TrimSpace(critique) == "" {
		return ErrEmptyCritique
	}

	var o draftOptions
	for _, opt := range opts {
		opt(&o)
	}

	current, err := sess.ReadPlan()
	if err != nil {
		return fmt.Errorf("read plan: %w", err)
	}
	if strings.TrimSpace(current) == "" {
		return ErrNoPlanToRevise
	}

	// Snapshot before we even gate, so a TryStartRevise=false from a racing
	// caller doesn't corrupt the snapshot that's about to be used by the
	// in-flight revise. The snapshot is benign on the failure path: the next
	// successful revise will overwrite it before any user action would
	// observe it as the undo target.
	if err := sess.snapshotPlanToPrev(); err != nil {
		return fmt.Errorf("snapshot plan: %w", err)
	}

	// No wall-clock timeout for the same reason as StartDraft: revise is a
	// Sonnet call the user is actively waiting on. User cancels via the TUI;
	// closeSession / KillSession / Shutdown propagate via m.done in runRevise.
	ctx, cancel := context.WithCancel(context.Background())
	if !sess.TryStartRevise(cancel) {
		cancel()
		return ErrReviseInFlight
	}

	sess.SetReviseError(nil)
	m.emit(Event{Type: EventStatusChanged, SessionID: sessionID})

	m.watchers.Add(1)
	go m.runRevise(ctx, sess, drafter, current, critique, o.model)
	return nil
}

// runRevise executes a Revise call against drafter and writes the resulting
// plan markdown via sess.WritePlan. Mirrors runDraft's gating pattern:
// post-call still-open check, error coercion for empty output, always emits
// EventStatusChanged so the UI repaints. On failure the prior plan is left
// untouched and ReviseError is set; the user can retry via `r` or undo via
// `u` (which restores the snapshot we wrote above).
func (m *Manager) runRevise(ctx context.Context, sess *Session, drafter PlanDrafter, current, critique, model string) {
	defer m.watchers.Done()
	defer sess.finishRevise()

	// Cancel the revising subprocess if the manager shuts down mid-call.
	doneCh := make(chan struct{})
	defer close(doneCh)
	go func() {
		select {
		case <-m.done:
			sess.CancelRevise()
		case <-doneCh:
		}
	}()

	body, err := drafter.Revise(ctx, ReviseRequest{CurrentPlan: current, Critique: critique, Cwd: sess.Worktree.Path, Model: model})

	m.mu.RLock()
	_, stillOpen := m.sessions[sess.ID]
	m.mu.RUnlock()
	if !stillOpen {
		return
	}

	if err == nil && strings.TrimSpace(body) == "" {
		err = errors.New("planner returned empty plan")
	}
	if err == nil {
		if writeErr := sess.WritePlan(body); writeErr != nil {
			err = writeErr
		}
	}

	sess.SetReviseError(err)
	m.emit(Event{Type: EventStatusChanged, SessionID: sess.ID})
}

// IsActionablePrompt reports whether prompt carries enough text for a
// meaningful branch name. Empty/whitespace-only prompts and pure slash
// commands (e.g. "/clear") return false.
func IsActionablePrompt(prompt string) bool {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "/") {
		idx := strings.IndexAny(trimmed, " \t")
		if idx < 0 {
			return false
		}
		return strings.TrimSpace(trimmed[idx+1:]) != ""
	}
	return true
}

// renameSessionBranch performs the git branch rename only. It does NOT emit
// any events — callers should update display names first and then call
// emitBranchRenamed so subscribers observe a coherent snapshot. Returns true
// if the rename succeeded.
func (m *Manager) renameSessionBranch(sess *Session, newBranch string) bool {
	// If the session was closed while the async namer was running, skip the
	// git op — the worktree is gone, so `git branch -m` would fail.
	m.mu.RLock()
	_, stillOpen := m.sessions[sess.ID]
	m.mu.RUnlock()
	if !stillOpen {
		return false
	}

	// Best effort: on failure, hasClaudeName stays false so the next prompt
	// will retry. Writing to stderr during an active TUI alt-screen scrambles
	// the rendered UI, so the error is swallowed here — users see the
	// unchanged branch label in the preview as the implicit signal.
	if _, err := sess.RenameBranch(m.repoPath, newBranch); err != nil {
		return false
	}
	return true
}

// emitBranchRenamed emits an EventStatusChanged followed by EventBranchRenamed
// for a session whose branch (and typically display name) just updated. Call
// after both the rename and any dependent display-name writes so subscribers
// see consistent state.
func (m *Manager) emitBranchRenamed(sess *Session, _ *Agent, newBranch string) {
	if agents := sess.Agents(); len(agents) > 0 {
		lead := agents[0]
		m.emit(Event{
			Type:      EventStatusChanged,
			AgentID:   lead.ID,
			SessionID: sess.ID,
			Status:    lead.Status(),
		})
	}
	m.emit(Event{
		Type:      EventBranchRenamed,
		SessionID: sess.ID,
		Branch:    newBranch,
	})
}

// ReconcileExternalBranchRename updates the in-memory branch name for a session
// after the user has renamed the branch outside refrain (e.g. `git branch -m`),
// then fires EventBranchRenamed so the burst-polling window arms and the sidebar
// label refreshes. No git operations are performed.
func (m *Manager) ReconcileExternalBranchRename(sessionID, newBranch string) {
	m.mu.RLock()
	sess := m.sessions[sessionID]
	m.mu.RUnlock()

	if sess == nil {
		return
	}

	sess.UpdateBranch(newBranch)
	m.emitBranchRenamed(sess, nil, newBranch)
}

// findAgentAndSession locates an agent across all sessions and returns it
// alongside its containing session.
func (m *Manager) findAgentAndSession(agentID string) (*Agent, *Session) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.sessions {
		if a := s.GetAgent(agentID); a != nil {
			return a, s
		}
	}
	return nil, nil
}

// FindAgentAndSession returns the agent and session for the given agent ID.
// Returns nil, nil if not found.
func (m *Manager) FindAgentAndSession(agentID string) (*Agent, *Session) {
	return m.findAgentAndSession(agentID)
}

// UpdateSettings replaces the manager's resolved settings.
// New sessions will use the updated values; existing sessions are unaffected.
//
// If the resolved PlanModel changed and the current planDrafter is still the
// package default (i.e. tests haven't injected a mock via SetPlanDrafter),
// the drafter is rebuilt with the new model so the next StartDraft picks up
// the change without requiring a refrain restart. A test-injected drafter is
// left alone — overriding it here would silently break SetPlanDrafter's
// contract.
func (m *Manager) UpdateSettings(s config.ResolvedSettings) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings = s
	if d, ok := m.planDrafter.(*defaultPlanDrafter); ok && d.Model() != s.PlanModel {
		m.planDrafter = DefaultPlanDrafter(s.PlanModel)
	}
}

// Settings returns the current resolved settings.
func (m *Manager) Settings() config.ResolvedSettings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings
}

// CreateSession starts a new session with its first agent using the default claude command.
func (m *Manager) CreateSession(cfg Config) (*Session, *Agent, error) {
	sess, err := m.createSessionWorktree(cfg)
	if err != nil {
		return nil, nil, err
	}

	a, err := sess.AddAgentDefault(cfg)
	if err != nil {
		// Clean up worktree on failure.
		_ = sess.Cleanup(m.repoPath)
		m.mu.Lock()
		delete(m.sessions, sess.ID)
		m.mu.Unlock()
		return nil, nil, err
	}

	m.emit(Event{Type: EventCreated, AgentID: a.ID, SessionID: sess.ID, Status: StatusStarting})

	m.watchers.Add(1)
	go func() {
		defer m.watchers.Done()
		m.watchAgent(a, sess.ID)
	}()

	return sess, a, nil
}

// CreateSessionWithCommand starts a new session with its first agent using a custom command.
func (m *Manager) CreateSessionWithCommand(cfg Config, cmd func(name string) *exec.Cmd) (*Session, *Agent, error) {
	sess, err := m.createSessionWorktree(cfg)
	if err != nil {
		return nil, nil, err
	}

	a, err := sess.AddAgent(cfg, cmd)
	if err != nil {
		_ = sess.Cleanup(m.repoPath)
		m.mu.Lock()
		delete(m.sessions, sess.ID)
		m.mu.Unlock()
		return nil, nil, err
	}

	m.emit(Event{Type: EventCreated, AgentID: a.ID, SessionID: sess.ID, Status: StatusStarting})

	m.watchers.Add(1)
	go func() {
		defer m.watchers.Done()
		m.watchAgent(a, sess.ID)
	}()

	return sess, a, nil
}

// CreateSessionOnBranch starts a new session on an existing branch using the default claude command.
func (m *Manager) CreateSessionOnBranch(branch, baseBranch string, cfg Config) (*Session, *Agent, error) {
	sess, err := m.createSessionOnBranchWorktree(branch, baseBranch, cfg)
	if err != nil {
		return nil, nil, err
	}

	a, err := sess.AddAgentDefault(cfg)
	if err != nil {
		_ = sess.Cleanup(m.repoPath)
		m.mu.Lock()
		delete(m.sessions, sess.ID)
		m.mu.Unlock()
		return nil, nil, err
	}

	m.emit(Event{Type: EventCreated, AgentID: a.ID, SessionID: sess.ID, Status: StatusStarting})

	m.watchers.Add(1)
	go func() {
		defer m.watchers.Done()
		m.watchAgent(a, sess.ID)
	}()

	return sess, a, nil
}

// CreateSessionOnBranchWithCommand starts a new session on an existing branch using a custom command.
func (m *Manager) CreateSessionOnBranchWithCommand(branch, baseBranch string, cfg Config, cmd func(name string) *exec.Cmd) (*Session, *Agent, error) {
	sess, err := m.createSessionOnBranchWorktree(branch, baseBranch, cfg)
	if err != nil {
		return nil, nil, err
	}

	a, err := sess.AddAgent(cfg, cmd)
	if err != nil {
		_ = sess.Cleanup(m.repoPath)
		m.mu.Lock()
		delete(m.sessions, sess.ID)
		m.mu.Unlock()
		return nil, nil, err
	}

	m.emit(Event{Type: EventCreated, AgentID: a.ID, SessionID: sess.ID, Status: StatusStarting})

	m.watchers.Add(1)
	go func() {
		defer m.watchers.Done()
		m.watchAgent(a, sess.ID)
	}()

	return sess, a, nil
}

// ErrCheckoutSessionExists is returned by CreateSessionInDir when the repo
// already has a live checkout session (or one is mid-creation). Two agent
// groups sharing one working tree would fight over the same files; callers
// should offer to add an agent to the existing session instead. Check with
// errors.Is.
var ErrCheckoutSessionExists = errors.New("checkout session already exists for this repo")

// CreateSessionInDir starts a new checkout session: a session whose working
// directory is the repo's main working tree, on whatever branch is currently
// checked out. No worktree is created and no branch is owned — killing the
// session kills agents only, and Session.Cleanup is a guaranteed no-op on
// the tree. At most one checkout session may exist per repo
// (ErrCheckoutSessionExists); multiple agents within it are fine.
func (m *Manager) CreateSessionInDir(cfg Config) (*Session, *Agent, error) {
	sess, err := m.createSessionCheckout(cfg)
	if err != nil {
		return nil, nil, err
	}

	a, err := sess.AddAgentDefault(cfg)
	if err != nil {
		// No Cleanup: there is no worktree to remove and the tree must
		// never be touched. Dropping the map entry frees the checkout slot.
		m.mu.Lock()
		delete(m.sessions, sess.ID)
		m.mu.Unlock()
		return nil, nil, err
	}

	m.emit(Event{Type: EventCreated, AgentID: a.ID, SessionID: sess.ID, Status: StatusStarting})

	m.watchers.Add(1)
	go func() {
		defer m.watchers.Done()
		m.watchAgent(a, sess.ID)
	}()

	return sess, a, nil
}

// CreateSessionInDirWithCommand starts a new checkout session with a custom
// command. Mirrors CreateSessionInDir; used by tests that substitute bash
// for claude.
func (m *Manager) CreateSessionInDirWithCommand(cfg Config, cmd func(name string) *exec.Cmd) (*Session, *Agent, error) {
	sess, err := m.createSessionCheckout(cfg)
	if err != nil {
		return nil, nil, err
	}

	a, err := sess.AddAgent(cfg, cmd)
	if err != nil {
		m.mu.Lock()
		delete(m.sessions, sess.ID)
		m.mu.Unlock()
		return nil, nil, err
	}

	m.emit(Event{Type: EventCreated, AgentID: a.ID, SessionID: sess.ID, Status: StatusStarting})

	m.watchers.Add(1)
	go func() {
		defer m.watchers.Done()
		m.watchAgent(a, sess.ID)
	}()

	return sess, a, nil
}

// createSessionCheckout creates a KindCheckout session rooted at the repo's
// main working tree. A sibling of createSessionOnBranchWorktree, not of
// createSessionWorktree: it never runs `git worktree add`.
//
// Safety properties (see the rollback design doc §4.3):
//   - ownsBranch stays false and hasClaudeName is set at creation, so the
//     first-prompt Haiku rename can never touch the user's real branch.
//   - sess.kind = KindCheckout makes Cleanup a guaranteed no-op on the tree.
//   - the one-per-repo slot is reserved (checkoutPending) before any git I/O
//     so concurrent callers see the reservation.
func (m *Manager) createSessionCheckout(cfg Config) (*Session, error) {
	m.mu.Lock()
	if m.checkoutPending {
		m.mu.Unlock()
		return nil, ErrCheckoutSessionExists
	}
	for _, s := range m.sessions {
		if s.Kind() == KindCheckout {
			m.mu.Unlock()
			return nil, ErrCheckoutSessionExists
		}
	}
	m.checkoutPending = true
	m.mu.Unlock()

	// Release the slot reservation on every path; on success the session is
	// already published in m.sessions (which is what the one-per-repo check
	// reads first), so there is no gap.
	defer func() {
		m.mu.Lock()
		m.checkoutPending = false
		m.mu.Unlock()
	}()

	// Current branch of the main working tree — the session runs on it as-is.
	branch, err := git.BaseBranch(m.repoPath)
	if err != nil {
		return nil, fmt.Errorf("detecting current branch: %w", err)
	}

	m.mu.Lock()
	existing := m.allReservedNamesLocked()
	name := slugifyBranchName(branch, existing)
	m.pendingNames[name] = struct{}{}
	m.nextID++
	id := fmt.Sprintf("session-%d", m.nextID)
	settings := m.settings
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.pendingNames, name)
		m.mu.Unlock()
	}()

	cfg.RepoPath = m.repoPath
	if cfg.AgentProgram == "" {
		cfg.AgentProgram = settings.AgentProgram
	}

	// Detect a base branch for later diffing. Best-effort and read-only —
	// unlike createSessionWorktree we deliberately skip the fetch/fast-forward,
	// since a checkout session must not mutate the user's refs.
	baseBranch := settings.DefaultBranch
	if baseBranch == "" {
		if detected, err := git.RemoteDefaultBranch(m.repoPath); err == nil {
			baseBranch = detected
		} else {
			baseBranch = branch
		}
	}

	wt := &git.WorktreeInfo{
		Name:       name,
		Path:       m.repoPath,
		Branch:     branch,
		BaseBranch: baseBranch,
	}

	sess := newSession(id, name, wt)
	sess.hookSocketPath = m.hookSocketPath
	sess.kind = KindCheckout
	// ownsBranch stays false — the user's branch is never deleted.
	// hasClaudeName suppresses the first-prompt Haiku branch rename exactly
	// as the attach path does. Safety-critical: without it, the first prompt
	// would rename the user's real branch.
	sess.SetClaudeName(true)

	m.mu.Lock()
	m.sessions[id] = sess
	m.mu.Unlock()

	return sess, nil
}

// createSessionOnBranchWorktree creates a session attached to an existing branch.
// The session does NOT own the branch — cleanup removes the worktree but preserves it.
// If baseBranch is non-empty, it overrides the default base on the returned WorktreeInfo.
func (m *Manager) createSessionOnBranchWorktree(branch, baseBranch string, cfg Config) (*Session, error) {
	m.mu.Lock()
	existing := m.allReservedNamesLocked()
	name := slugifyBranchName(branch, existing)
	m.pendingNames[name] = struct{}{}
	m.nextID++
	id := fmt.Sprintf("session-%d", m.nextID)
	settings := m.settings
	m.mu.Unlock()

	// Always release the reservation; on success we replace it with the real
	// session entry below, on failure we just clear it.
	defer func() {
		m.mu.Lock()
		delete(m.pendingNames, name)
		m.mu.Unlock()
	}()

	cfg.RepoPath = m.repoPath
	if cfg.AgentProgram == "" {
		cfg.AgentProgram = settings.AgentProgram
	}

	wt, err := git.AttachWorktree(m.repoPath, name, settings.WorktreeDir, branch)
	if err != nil {
		return nil, fmt.Errorf("attaching worktree: %w", err)
	}

	// Override base branch if caller provided one (e.g. from PR data).
	if baseBranch != "" {
		wt.BaseBranch = baseBranch
	}

	sess := newSession(id, name, wt)
	sess.hookSocketPath = m.hookSocketPath
	// ownsBranch stays false — we didn't create this branch.
	// Attached sessions already have a meaningful branch name (the one the
	// user picked), so skip the first-prompt rename heuristic.
	sess.SetClaudeName(true)

	m.mu.Lock()
	m.sessions[id] = sess
	m.mu.Unlock()

	return sess, nil
}

// allReservedNamesLocked returns every session name currently in use or
// reserved by an in-flight create. m.mu must be held.
func (m *Manager) allReservedNamesLocked() []string {
	out := make([]string, 0, len(m.sessions)+len(m.pendingNames))
	for _, s := range m.sessions {
		out = append(out, s.CurrentName())
	}
	for n := range m.pendingNames {
		out = append(out, n)
	}
	return out
}

// slugifyBranchName derives a session name from a branch name.
// Takes the last path segment (e.g. "feature/add-auth" → "add-auth"), slugifies it,
// and falls back to RandomName if the result is empty or collides.
func slugifyBranchName(branch string, existing []string) string {
	parts := strings.Split(branch, "/")
	last := parts[len(parts)-1]
	name := slugify(last)

	if name == "" {
		return RandomName(existing)
	}

	// Check for collision.
	for _, e := range existing {
		if e == name {
			return RandomName(existing)
		}
	}

	return name
}

// createSessionWorktree creates a session with its worktree, adds it to the map.
func (m *Manager) createSessionWorktree(cfg Config) (*Session, error) {
	// Generate session name. Reserve the name in pendingNames so a concurrent
	// CreateSession can't pick the same slug while we're doing git I/O.
	m.mu.Lock()
	existing := m.allReservedNamesLocked()
	track := songs.Pick(existing)
	name := track.Slug()
	m.pendingNames[name] = struct{}{}
	m.nextID++
	id := fmt.Sprintf("session-%d", m.nextID)
	settings := m.settings
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.pendingNames, name)
		m.mu.Unlock()
	}()

	cfg.RepoPath = m.repoPath
	if cfg.AgentProgram == "" {
		cfg.AgentProgram = settings.AgentProgram
	}

	// Determine base branch: use configured default, otherwise the remote's
	// default branch (origin/HEAD). The local HEAD is only used as a final
	// fallback for repos without an origin remote — new worktrees should
	// branch off the canonical trunk, not whatever happens to be checked
	// out in the main working tree.
	baseBranch := settings.DefaultBranch
	if baseBranch == "" {
		if detected, err := git.RemoteDefaultBranch(m.repoPath); err == nil {
			baseBranch = detected
		} else if detected, err := git.BaseBranch(m.repoPath); err == nil {
			baseBranch = detected
		}
	}

	// Cut the worktree from origin/<baseBranch> whenever the remote ref exists
	// locally — falling back to local HEAD would inherit whatever branch the
	// user happens to have checked out in the main worktree.
	// Best-effort fetch + fast-forward local ref so a subsequent diff
	// against baseBranch is fresh; ignore the error.
	_ = git.UpdateBaseBranch(m.repoPath, baseBranch)
	startPoint := ""
	if baseBranch != "" && git.HasRemoteBranch(m.repoPath, baseBranch) {
		startPoint = "origin/" + baseBranch
	}

	wt, err := git.CreateWorktree(m.repoPath, name, config.ExpandBranchPrefix(settings.BranchPrefix), settings.WorktreeDir, baseBranch, startPoint)
	if err != nil {
		return nil, fmt.Errorf("creating worktree: %w", err)
	}

	sess := newSession(id, name, wt)
	sess.hookSocketPath = m.hookSocketPath
	sess.ownsBranch = true

	m.mu.Lock()
	m.sessions[id] = sess
	m.mu.Unlock()

	// Best-effort: record the song that "played" for this session. Failure
	// must not block session creation, and we don't surface the error since
	// stderr writes would corrupt the TUI.
	_ = setlist.Append(setlist.Entry{
		PlayedAt:  time.Now(),
		Name:      track.Name,
		Artist:    track.Artist,
		ISRC:      track.ISRC,
		Slug:      name,
		Repo:      m.repoPath,
		SessionID: id,
	})

	return sess, nil
}

// CreateSessionNoAgent creates a session with a worktree but no agent
// process — the "session exists before its first agent" primitive. Callers
// typically follow with StartDraft to kick off plan generation, then
// AddAgent on approval (the plan-first flow).
func (m *Manager) CreateSessionNoAgent(cfg Config) (*Session, error) {
	sess, err := m.createSessionWorktree(cfg)
	if err != nil {
		return nil, err
	}
	m.emit(Event{Type: EventCreated, SessionID: sess.ID})
	return sess, nil
}

// AddAgent adds an agent to an existing session using the default claude command.
func (m *Manager) AddAgent(sessionID string, cfg Config) (*Agent, error) {
	m.mu.RLock()
	sess := m.sessions[sessionID]
	settings := m.settings
	m.mu.RUnlock()

	if sess == nil {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	cfg.RepoPath = m.repoPath
	if cfg.AgentProgram == "" {
		cfg.AgentProgram = settings.AgentProgram
	}

	a, err := sess.AddAgentDefault(cfg)
	if err != nil {
		return nil, err
	}

	m.emit(Event{Type: EventCreated, AgentID: a.ID, SessionID: sessionID, Status: StatusStarting})

	m.watchers.Add(1)
	go func() {
		defer m.watchers.Done()
		m.watchAgent(a, sessionID)
	}()

	return a, nil
}

// AddAgentWithCommand adds an agent to an existing session using a custom command.
func (m *Manager) AddAgentWithCommand(sessionID string, cfg Config, cmd func(name string) *exec.Cmd) (*Agent, error) {
	m.mu.RLock()
	sess := m.sessions[sessionID]
	m.mu.RUnlock()

	if sess == nil {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	cfg.RepoPath = m.repoPath

	a, err := sess.AddAgent(cfg, cmd)
	if err != nil {
		return nil, err
	}

	m.emit(Event{Type: EventCreated, AgentID: a.ID, SessionID: sessionID, Status: StatusStarting})

	m.watchers.Add(1)
	go func() {
		defer m.watchers.Done()
		m.watchAgent(a, sessionID)
	}()

	return a, nil
}

// AddShell adds a shell agent to an existing session.
func (m *Manager) AddShell(sessionID string, cfg Config) (*Agent, error) {
	m.mu.RLock()
	sess := m.sessions[sessionID]
	m.mu.RUnlock()

	if sess == nil {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	cfg.RepoPath = m.repoPath

	a, err := sess.AddShell(cfg)
	if err != nil {
		return nil, err
	}

	m.emit(Event{Type: EventCreated, AgentID: a.ID, SessionID: sessionID, Status: StatusStarting})

	m.watchers.Add(1)
	go func() {
		defer m.watchers.Done()
		m.watchAgent(a, sessionID)
	}()

	return a, nil
}

// Create starts a new session with its first agent (backward-compatible wrapper).
func (m *Manager) Create(cfg Config) (*Agent, error) {
	_, a, err := m.CreateSession(cfg)
	return a, err
}

// CreateWithCommand starts a new session with a custom command (backward-compatible wrapper).
func (m *Manager) CreateWithCommand(cfg Config, cmd func(name string) *exec.Cmd) (*Agent, error) {
	_, a, err := m.CreateSessionWithCommand(cfg, cmd)
	return a, err
}

// GetSession returns a session by ID, or nil if not found.
func (m *Manager) GetSession(id string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[id]
}

// ListSessions returns all sessions.
func (m *Manager) ListSessions() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		result = append(result, s)
	}
	return result
}

// AddSessionForTest injects a session directly into the manager's session map.
// Intended for tests outside the agent package that need a manager with a
// pre-populated session without spawning a real worktree or agent subprocess.
func (m *Manager) AddSessionForTest(sess *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[sess.ID] = sess
}

// Get returns an agent by ID (searches all sessions).
func (m *Manager) Get(id string) *Agent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.sessions {
		if a := s.GetAgent(id); a != nil {
			return a
		}
	}
	return nil
}

// List returns all agents across all sessions.
func (m *Manager) List() []*Agent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Agent
	for _, s := range m.sessions {
		result = append(result, s.Agents()...)
	}
	return result
}

// KillAgent kills a single agent within a session. If the session becomes
// empty after the kill, the session is automatically cleaned up and removed.
func (m *Manager) KillAgent(sessionID, agentID string) error {
	m.mu.RLock()
	sess := m.sessions[sessionID]
	m.mu.RUnlock()

	if sess == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}

	if sess.GetAgent(agentID) == nil {
		return fmt.Errorf("agent %s not found in session %s", agentID, sessionID)
	}

	sess.KillAgent(agentID)

	// Auto-close empty sessions.
	if sess.AgentCount() == 0 {
		m.closeSession(sessionID, sess)
	}

	return nil
}

// KillSession kills all agents in a session, removes the worktree, and deletes the session.
func (m *Manager) KillSession(sessionID string) error {
	m.mu.RLock()
	sess := m.sessions[sessionID]
	m.mu.RUnlock()

	if sess == nil {
		return ErrSessionNotFound
	}

	// Cancel any in-flight plan-drafting subprocess so the goroutine drains
	// before we remove the worktree. WritePlan would otherwise race with
	// directory removal. CancelDraft is a no-op when no draft is running.
	// Same applies to revising — both are bounded by m.watchers and write
	// to the same .claude/ directory.
	sess.CancelDraft()
	sess.CancelRevise()

	sess.KillAll()

	// Delete the session from the map BEFORE removing the worktree so
	// runDraft's stillOpen guard observes the session as gone before any
	// post-Cleanup WritePlan attempt. The previous order (Cleanup first,
	// delete second) left a window where stillOpen=true while the worktree
	// directory was already removed, relying on an implicit contract that
	// PlanDrafter.Draft must return a non-nil error on context cancellation.
	// Mirrors closeSession's delete-then-cleanup ordering.
	m.mu.Lock()
	delete(m.sessions, sessionID)
	m.mu.Unlock()

	if err := sess.Cleanup(m.repoPath); err != nil {
		return fmt.Errorf("cleanup session %s: %w", sessionID, err)
	}

	return nil
}

// Kill terminates an agent and cleans up its session (backward-compatible).
// Finds the session containing the agent and kills the entire session.
func (m *Manager) Kill(id string) error {
	m.mu.RLock()
	for _, sess := range m.sessions {
		if a := sess.GetAgent(id); a != nil {
			sessID := sess.ID
			m.mu.RUnlock()
			return m.KillSession(sessID)
		}
	}
	m.mu.RUnlock()
	return fmt.Errorf("agent %s not found", id)
}

// Events returns a channel that emits agent lifecycle events.
func (m *Manager) Events() <-chan Event {
	return m.events
}

// Shutdown kills all sessions and cleans up. Safe to call multiple times.
func (m *Manager) Shutdown() {
	m.shutdownOnce.Do(func() {
		close(m.done)

		// Stop the hook server first so no new rename goroutines can be spawned
		// by late UserPromptSubmit events, then drain in-flight watchers
		// (watchAgent + async branch-rename goroutines). Without this ordering,
		// a rename goroutine can race Session.Cleanup and mutate Worktree state
		// while the worktree is being removed from disk.
		m.stopHookServer()
		m.watchers.Wait()

		m.mu.RLock()
		sessions := make([]*Session, 0, len(m.sessions))
		for _, s := range m.sessions {
			sessions = append(sessions, s)
		}
		m.mu.RUnlock()

		var wg sync.WaitGroup
		wg.Add(len(sessions))
		for _, s := range sessions {
			go func() {
				defer wg.Done()
				s.KillAll()
				_ = s.Cleanup(m.repoPath)
			}()
		}
		wg.Wait()

		m.mu.Lock()
		m.sessions = make(map[string]*Session)
		m.mu.Unlock()

		// Gate all emitters BEFORE closing the channels. Holding sendsMu.Lock
		// across the close ensures any concurrent emit/pumpPlannerQuestions
		// either completed before we entered, or is now short-circuited by the
		// sendsClosed check it sees once it acquires its RLock. Without this
		// ordering, a late caller (StartDraft, ResumeSession, a watchAgent
		// finishing) would panic with "send on closed channel".
		m.sendsMu.Lock()
		m.sendsClosed = true
		close(m.events)
		close(m.plannerQuestions)
		m.sendsMu.Unlock()
	})
}

// stopHookServer closes the hook server and waits for the dispatcher goroutine.
// Safe to call multiple times; no-op if the server never started.
func (m *Manager) stopHookServer() {
	if m.hookServer == nil {
		return
	}
	_ = m.hookServer.Close()
	m.hookDispatcher.Wait()
	m.hookServer = nil
}

// AgentCount returns the total number of live, non-shell agents across all
// sessions. Shells and naturally-exited (Done/Error) agents are excluded.
// Used by the quit guard and repo-remove guard where the concern is running
// processes, not human oversight load.
func (m *Manager) AgentCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, s := range m.sessions {
		count += s.LiveAgentCount()
	}
	return count
}

// ActiveSessionCount returns the number of sessions requiring human oversight.
// Shipping and Complete sessions are excluded (parked on CI/reviews or done),
// as are sessions with no live agents. This is the denominator for the soft
// concurrency warning: the BCG "3-agent ceiling" is about concurrent tasks
// requiring attention, not subprocess count.
func (m *Manager) ActiveSessionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, s := range m.sessions {
		switch s.LifecyclePhase() {
		case LifecycleShipping, LifecycleComplete:
			continue
		}
		if s.LiveAgentCount() == 0 {
			continue
		}
		count++
	}
	return count
}

// RepoPath returns the manager's repo path.
func (m *Manager) RepoPath() string {
	return m.repoPath
}

// Detach snapshots all sessions into a RefrainState, kills all agents but preserves
// worktrees, and shuts down the manager. Returns the state for persistence.
//
// Shares m.shutdownOnce with Shutdown: both close m.done, drain watchers, and
// close the sends channels exactly once. If Shutdown ran first, Detach returns
// nil; if Detach ran first, a later Shutdown is a no-op (which is correct —
// Detach intentionally leaves snapshotted sessions alive for a later resume).
// Without this gate, a defer mgr.Shutdown() after Detach panics on
// close-of-closed-channel.
func (m *Manager) Detach() *state.RefrainState {
	var result *state.RefrainState
	m.shutdownOnce.Do(func() {
		close(m.done)

		// Stop hooks + drain watchers before snapshotting so any in-flight
		// branch rename has settled and the snapshot captures the final name.
		m.stopHookServer()
		m.watchers.Wait()

		m.mu.RLock()
		sessions := make([]*Session, 0, len(m.sessions))
		for _, s := range m.sessions {
			sessions = append(sessions, s)
		}
		m.mu.RUnlock()

		// Partition: complete sessions are cleaned up; others are snapshotted.
		var toSnapshot, toClean []*Session
		for _, s := range sessions {
			if s.LifecyclePhase() == LifecycleComplete {
				toClean = append(toClean, s)
			} else {
				toSnapshot = append(toSnapshot, s)
			}
		}

		// Snapshot state before killing agents.
		sessionStates := make([]state.SessionState, 0, len(toSnapshot))
		for _, s := range toSnapshot {
			var doneAt *time.Time
			if t := s.DoneAt(); !t.IsZero() {
				doneAt = &t
			}
			ss := state.SessionState{
				ID:             s.ID,
				Name:           s.CurrentName(),
				DisplayName:    s.GetDisplayName(),
				WorktreePath:   s.Worktree.Path,
				Branch:         s.Branch(),
				BaseBranch:     s.Worktree.BaseBranch,
				OwnsBranch:     s.ownsBranch,
				Kind:           string(s.Kind()),
				HasClaudeName:  s.HasClaudeName(),
				LifecyclePhase: s.LifecyclePhase().String(),
				OriginalPrompt: s.OriginalPrompt(),
				DoneAt:         doneAt,
			}
			for _, a := range s.Agents() {
				as := state.AgentState{
					ID:              a.ID,
					Name:            a.Name,
					DisplayName:     a.GetDisplayName(),
					Task:            a.Task,
					ClaudeSessionID: a.ClaudeSessionID(),
				}
				ss.Agents = append(ss.Agents, as)
			}
			sessionStates = append(sessionStates, ss)
		}

		// Kill complete sessions and remove their worktrees.
		var cleanWg sync.WaitGroup
		cleanWg.Add(len(toClean))
		for _, s := range toClean {
			go func() {
				defer cleanWg.Done()
				s.KillAll()
				_ = s.Cleanup(m.repoPath)
			}()
		}
		cleanWg.Wait()

		// Kill agents for snapshotted sessions but do NOT call Cleanup (preserve worktrees).
		var wg sync.WaitGroup
		wg.Add(len(toSnapshot))
		for _, s := range toSnapshot {
			go func() {
				defer wg.Done()
				s.KillAll()
			}()
		}
		wg.Wait()

		m.mu.Lock()
		m.sessions = make(map[string]*Session)
		m.mu.Unlock()

		// Same gate as Shutdown — see the comment there.
		m.sendsMu.Lock()
		m.sendsClosed = true
		close(m.events)
		close(m.plannerQuestions)
		m.sendsMu.Unlock()

		if len(sessionStates) == 0 {
			return
		}

		result = &state.RefrainState{
			Version:  1,
			SavedAt:  time.Now(),
			Sessions: sessionStates,
		}
	})
	return result
}

// ResumeSession recreates a session from saved state without creating a new worktree.
// It verifies the worktree directory exists, constructs a Session from saved data,
// and spawns agents with --resume flags.
func (m *Manager) ResumeSession(ss state.SessionState, cfg Config) error {
	// Verify worktree directory exists.
	if _, err := os.Stat(ss.WorktreePath); err != nil {
		return fmt.Errorf("worktree %s not found: %w", ss.WorktreePath, err)
	}

	wt := &git.WorktreeInfo{
		Name:       ss.Name,
		Path:       ss.WorktreePath,
		Branch:     ss.Branch,
		BaseBranch: ss.BaseBranch,
	}

	// A checkout session runs on whatever branch the user's main working
	// tree has checked out, and that may have changed while refrain was
	// detached. Re-read HEAD so the session reflects reality rather than
	// the snapshot. Best-effort: on error the persisted branch stands.
	if SessionKindFromString(ss.Kind) == KindCheckout {
		if cur, err := git.BaseBranch(ss.WorktreePath); err == nil && cur != "" {
			wt.Branch = cur
		}
	}

	sess := newSession(ss.ID, ss.Name, wt)
	sess.hookSocketPath = m.hookSocketPath
	sess.ownsBranch = ss.OwnsBranch
	sess.kind = SessionKindFromString(ss.Kind)
	sess.SetClaudeName(ss.HasClaudeName)
	if ss.DisplayName != "" && ss.DisplayName != ss.Name {
		sess.SetDisplayName(ss.DisplayName)
	}
	sess.SetLifecyclePhase(LifecyclePhaseFromString(ss.LifecyclePhase))
	if ss.OriginalPrompt != "" {
		sess.SetOriginalPrompt(ss.OriginalPrompt)
	}
	if ss.DoneAt != nil {
		sess.RestoreDoneAt(*ss.DoneAt)
	}

	m.mu.Lock()
	m.sessions[ss.ID] = sess
	// Parse session ID number to avoid collisions with nextID.
	if num := parseSessionNum(ss.ID); num >= m.nextID {
		m.nextID = num + 1
	}
	m.mu.Unlock()

	settings := m.Settings()

	for _, as := range ss.Agents {
		agentCfg := Config{
			Name:              as.Name,
			Task:              as.Task,
			Rows:              cfg.Rows,
			Cols:              cfg.Cols,
			RepoPath:          m.repoPath,
			BypassPermissions: cfg.BypassPermissions,
			AgentProgram:      settings.AgentProgram,
			AgentModel:        settings.AgentModel,
		}

		a, err := sess.AddAgentResumed(agentCfg, as.ClaudeSessionID)
		if err != nil {
			// Clean up any agents already created in this session.
			sess.KillAll()
			m.mu.Lock()
			delete(m.sessions, ss.ID)
			m.mu.Unlock()
			// Surface the failure so the TUI can show it; the caller in
			// app.go currently discards the error. Don't Cleanup() the
			// worktree — it may contain uncommitted user work.
			m.emit(Event{Type: EventError, SessionID: ss.ID, Status: StatusError})
			return fmt.Errorf("resuming agent %s: %w", as.Name, err)
		}

		// Restore display name from saved state.
		if as.DisplayName != "" {
			a.SetDisplayName(as.DisplayName)
			a.SetClaudeName(true)
		}

		m.emit(Event{Type: EventCreated, AgentID: a.ID, SessionID: sess.ID, Status: StatusStarting})

		m.watchers.Add(1)
		go func() {
			defer m.watchers.Done()
			m.watchAgent(a, sess.ID)
		}()
	}

	return nil
}

// parseSessionNum extracts the numeric ID from a session ID like "session-3".
func parseSessionNum(id string) int {
	parts := strings.SplitN(id, "-", 2)
	if len(parts) != 2 {
		return 0
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0
	}
	return n
}

func (m *Manager) watchAgent(a *Agent, sessionID string) {
	select {
	case <-a.Done():
		status := a.Status()
		m.emit(Event{Type: EventDone, AgentID: a.ID, SessionID: sessionID, Status: status})
	case <-m.done:
	}
}

// closeSession cleans up and removes a session, emitting EventSessionClosed.
// Safe to call concurrently — only the first caller performs cleanup.
func (m *Manager) closeSession(sessionID string, sess *Session) {
	m.mu.Lock()
	if _, exists := m.sessions[sessionID]; !exists {
		m.mu.Unlock()
		return // already cleaned up by another goroutine
	}
	delete(m.sessions, sessionID)
	m.mu.Unlock()

	// Cancel any in-flight plan-drafting / revising subprocess for symmetry
	// with KillSession. The current pipeline never reaches closeSession with
	// a draft or revise running (both precede building), but StartDraft /
	// RevisePlan do not enforce that precondition — without this call,
	// Shutdown would block on m.watchers indefinitely if the gap is ever
	// reached, since drafting has no wall-clock timeout. Both Cancel* calls
	// are no-ops when nothing's running.
	sess.CancelDraft()
	sess.CancelRevise()
	sess.KillAll()
	_ = sess.Cleanup(m.repoPath)

	m.emit(Event{Type: EventSessionClosed, SessionID: sessionID})
}

func (m *Manager) emit(e Event) {
	// Gate: Shutdown/Detach holds sendsMu.Lock around setting sendsClosed and
	// close(m.events). Holding RLock through the send ensures the close
	// cannot run while we're inside the select; without this guard a late
	// caller (StartDraft, CreateAgent, ResumeSession, a watchAgent finishing
	// during teardown) would panic with "send on closed channel" — the
	// select's default only protects against a full buffer, not a closed one.
	m.sendsMu.RLock()
	defer m.sendsMu.RUnlock()
	if m.sendsClosed {
		return
	}
	select {
	case m.events <- e:
	default:
	}
}
