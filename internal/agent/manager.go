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

	"github.com/devenjarvis/baton/internal/config"
	"github.com/devenjarvis/baton/internal/git"
	"github.com/devenjarvis/baton/internal/hook"
	"github.com/devenjarvis/baton/internal/setlist"
	"github.com/devenjarvis/baton/internal/songs"
	"github.com/devenjarvis/baton/internal/state"
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
	nextID       int

	events   chan Event
	done     chan struct{}
	watchers sync.WaitGroup

	hookServer     *hook.Server
	hookSocketPath string
	hookDispatcher sync.WaitGroup

	branchNamer    BranchNamer
	taskSummarizer TaskSummarizer
	planDrafter    PlanDrafter
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

// NewManager creates a new agent manager for the given repo.
//
// The manager owns a hook.Server listening on <repoPath>/.baton/hook.sock that
// routes Claude Code hook events to agents by BATON_AGENT_ID. If the socket
// fails to start (e.g. filesystem permissions), the manager logs to stderr
// and continues with hooks disabled; spawned agents will then never transition
// out of Active.
func NewManager(repoPath string, settings config.ResolvedSettings) *Manager {
	m := &Manager{
		repoPath:       repoPath,
		settings:       settings,
		sessions:       make(map[string]*Session),
		pendingNames:   make(map[string]struct{}),
		events:         make(chan Event, 64),
		done:           make(chan struct{}),
		branchNamer:    DefaultBranchNamer(),
		taskSummarizer: DefaultTaskSummarizer(),
		planDrafter:    DefaultPlanDrafter(),
	}

	batonDir := filepath.Join(repoPath, ".baton")
	if err := os.MkdirAll(batonDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "baton: creating %s: %v (hooks disabled)\n", batonDir, err)
		return m
	}
	socketPath := hookSocketPath(repoPath)
	srv, err := hook.NewServer(socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "baton: starting hook server on %s: %v (hooks disabled)\n", socketPath, err)
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

// getTaskSummarizer returns the current TaskSummarizer under a read lock.
func (m *Manager) getTaskSummarizer() TaskSummarizer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.taskSummarizer
}

// hookSocketPath returns the unix socket path for a given repoPath.
//
// Preferred layout: <repoPath>/.baton/hook.sock — easy to inspect and cleaned
// up with the rest of baton's per-repo state. macOS limits unix socket paths
// to 104 bytes, so when the preferred path would exceed a safe threshold we
// fall back to a short hashed name under os.TempDir(). Tests exercise the
// fallback path via deeply nested temp directories.
func hookSocketPath(repoPath string) string {
	preferred := filepath.Join(repoPath, ".baton", "hook.sock")
	// 104 is the darwin sun_path limit; leave headroom for the trailing NUL
	// and any quirks. 100 is comfortably below.
	if len(preferred) < 100 {
		return preferred
	}
	h := sha256.Sum256([]byte(repoPath))
	return filepath.Join(os.TempDir(), fmt.Sprintf("baton-%x.sock", h[:8]))
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
// with each attempt + the final outcome written to .baton/logs/haiku.log
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

// StartDraft begins async drafting of a plan for sessionID with the given
// user prompt. Transitions the session to LifecycleDrafting, then spawns a
// goroutine that calls PlanDrafter.Draft, writes the result via
// Session.WritePlan, and transitions to LifecyclePlanning on success or
// LifecyclePlanning(error) on failure. The goroutine is tracked in
// m.watchers so Shutdown drains cleanly; cancellation occurs when m.done
// closes (manager shutdown), KillSession is called, or CancelDraft is
// called directly. Drafting subprocesses are NOT counted against
// MaxConcurrentAgents — they are transient text-generation calls, not
// long-lived agents.
func (m *Manager) StartDraft(sessionID, prompt string) error {
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

	ctx, cancel := context.WithTimeout(context.Background(), PlanDraftTimeout)
	if !sess.TryStartDraft(cancel) {
		cancel()
		return ErrDraftInFlight
	}

	sess.SetOriginalPrompt(prompt)
	sess.SetLifecyclePhase(LifecycleDrafting)
	sess.SetDraftError(nil)
	m.emit(Event{Type: EventStatusChanged, SessionID: sessionID})

	m.watchers.Add(1)
	go m.runDraft(ctx, sess, drafter, prompt)
	return nil
}

// runDraft executes a Draft call against drafter and writes the resulting
// plan markdown via sess.WritePlan. Any failure path (drafter error, empty
// output, write error) lands the session in LifecyclePlanning with
// DraftError set so the Planning card can render a useful error badge —
// the user can then retry via the editor's revise flow or by pressing
// `n` again. Always emits EventStatusChanged on transition so the UI
// repaints.
func (m *Manager) runDraft(ctx context.Context, sess *Session, drafter PlanDrafter, prompt string) {
	defer m.watchers.Done()
	defer sess.finishDraft()

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

	body, err := drafter.Draft(ctx, DraftRequest{UserPrompt: prompt})

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
// after the user has renamed the branch outside baton (e.g. `git branch -m`),
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
func (m *Manager) UpdateSettings(s config.ResolvedSettings) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings = s
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

	// Determine base branch: use configured default, or auto-detect.
	baseBranch := settings.DefaultBranch
	if baseBranch == "" {
		if detected, err := git.BaseBranch(m.repoPath); err == nil {
			baseBranch = detected
		}
	}

	// Best-effort: update base branch from remote so the worktree
	// starts from the latest code. If offline, fall back to local HEAD.
	startPoint := ""
	if baseBranch != "" {
		if err := git.UpdateBaseBranch(m.repoPath, baseBranch); err == nil {
			startPoint = "origin/" + baseBranch
		}
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
		return fmt.Errorf("session %s not found", sessionID)
	}

	// Cancel any in-flight plan-drafting subprocess so the goroutine drains
	// before we remove the worktree. WritePlan would otherwise race with
	// directory removal. CancelDraft is a no-op when no draft is running.
	sess.CancelDraft()

	sess.KillAll()

	if err := sess.Cleanup(m.repoPath); err != nil {
		return fmt.Errorf("cleanup session %s: %w", sessionID, err)
	}

	m.mu.Lock()
	delete(m.sessions, sessionID)
	m.mu.Unlock()

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

// Shutdown kills all sessions and cleans up.
func (m *Manager) Shutdown() {
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

	close(m.events)
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

// AgentCount returns the total number of agents across all sessions.
func (m *Manager) AgentCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, s := range m.sessions {
		count += s.AgentCount()
	}
	return count
}

// RepoPath returns the manager's repo path.
func (m *Manager) RepoPath() string {
	return m.repoPath
}

// Detach snapshots all sessions into a BatonState, kills all agents but preserves
// worktrees, and shuts down the manager. Returns the state for persistence.
func (m *Manager) Detach() *state.BatonState {
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

	// Snapshot state before killing agents.
	sessionStates := make([]state.SessionState, 0, len(sessions))
	for _, s := range sessions {
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

	// Kill all agents but do NOT call Cleanup (preserve worktrees).
	var wg sync.WaitGroup
	wg.Add(len(sessions))
	for _, s := range sessions {
		go func() {
			defer wg.Done()
			s.KillAll()
		}()
	}
	wg.Wait()

	m.mu.Lock()
	m.sessions = make(map[string]*Session)
	m.mu.Unlock()

	close(m.events)

	if len(sessionStates) == 0 {
		return nil
	}

	return &state.BatonState{
		Version:  1,
		SavedAt:  time.Now(),
		Sessions: sessionStates,
	}
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

	sess := newSession(ss.ID, ss.Name, wt)
	sess.hookSocketPath = m.hookSocketPath
	sess.ownsBranch = ss.OwnsBranch
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

		// Auto-close session if all agents are done.
		m.mu.RLock()
		sess := m.sessions[sessionID]
		m.mu.RUnlock()
		if sess != nil && sess.Status() == StatusDone {
			m.closeSession(sessionID, sess)
		}
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

	sess.KillAll()
	_ = sess.Cleanup(m.repoPath)

	m.emit(Event{Type: EventSessionClosed, SessionID: sessionID})
}

func (m *Manager) emit(e Event) {
	select {
	case m.events <- e:
	default:
	}
}
