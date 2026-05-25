package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/devenjarvis/refrain/internal/git"
)

// Session owns a git worktree and holds one or more agents that share it.
type Session struct {
	ID        string
	Name      string
	Worktree  *git.WorktreeInfo
	CreatedAt time.Time

	mu             sync.RWMutex
	agents         map[string]*Agent
	nextAgentNum   int
	displayName    string
	hasClaudeName  bool // true once the session's branch has been renamed from its random placeholder
	lifecyclePhase LifecyclePhase
	originalPrompt string
	doneAt         time.Time
	ownsBranch     bool   // true if this session created the branch (cleanup should delete it)
	hookSocketPath string // absolute path to the manager's hook socket ("" disables hooks)
	taskSummary    string // short summary of the session's task, set once by the summarizer goroutine
	hasTaskSummary bool   // true once SetTaskSummary has been called
	// Async-task state. Each asyncJob tracks one in-flight goroutine and is
	// accessed under s.mu (the type carries no mutex of its own — see
	// asyncjob.go). Adding a new async task is one asyncJob field plus the
	// thin wrappers below; the cross-job exclusivity rules (e.g. revising
	// blocked while drafting) live in the wrappers, not on the asyncJob.
	renameJob  asyncJob // branch rename via Haiku
	summaryJob asyncJob // task-summary via Haiku
	draftJob   asyncJob // plan draft via Sonnet (uses attempt/max + cancel + err)
	reviseJob  asyncJob // plan revise via Sonnet (uses cancel + err)
	// Plan-content cache, keyed by the mtime of the last observed plan.md.
	// Populated lazily by CachedPlan() on first read so resumed sessions
	// also benefit. The dashboard hot path (per-render Building card) stats
	// plan.md each tick; if the mtime is unchanged it returns the cached
	// content without a ReadFile. This lets the build agent's external
	// checkbox edits (via Claude's Edit tool, bypassing WritePlan) show
	// through within one tick.
	planCacheLoaded  bool
	planCachePresent bool
	planCacheContent string
	planCacheMTime   time.Time

	// Commit-task progress cache. Refreshed on KindStop events for
	// LifecycleInProgress sessions via RefreshCommitTaskCount; read on the
	// render path via CommitTaskCount without any shell-out.
	commitTaskDone   int
	commitTaskMaxIdx int

	// cleanupOnce makes Session.Cleanup idempotent. Both watchAgent (via
	// closeSession on natural exit) and Shutdown's teardown loop can call
	// Cleanup against the same session; without this guard the second caller
	// hits git's "not a worktree" error and surfaces a spurious shutdown
	// failure. The Once also pins the result so all callers see the same
	// error value.
	cleanupOnce sync.Once
	cleanupErr  error
}

// newSession creates a session with the given worktree. New sessions land in
// LifecyclePlanning; the user advances to LifecycleInProgress (Building) with
// the 'b' key once they're done scoping the work. Restored sessions overwrite
// this default via SetLifecyclePhase from the persisted state.
func newSession(id, name string, wt *git.WorktreeInfo) *Session {
	return &Session{
		ID:             id,
		Name:           name,
		Worktree:       wt,
		CreatedAt:      time.Now(),
		agents:         make(map[string]*Agent),
		lifecyclePhase: LifecyclePlanning,
	}
}

// AddAgent creates and starts a new agent within this session using the session's worktree.
// cmdFactory is called after the agent name has been assigned so the factory
// receives the resolved name ("track-1", etc.) rather than the empty string
// that cfg.Name holds before the lock.
func (s *Session) AddAgent(cfg Config, cmdFactory func(name string) *exec.Cmd) (*Agent, error) {
	s.mu.Lock()
	s.nextAgentNum++
	num := s.nextAgentNum
	autoNamed := cfg.Name == ""
	if autoNamed {
		cfg.Name = fmt.Sprintf("track-%d", num)
	}
	id := fmt.Sprintf("%s-agent-%d", s.ID, num)
	s.mu.Unlock()

	a, err := newAgentWithCommand(id, cfg, s.Worktree.Path, cmdFactory(cfg.Name))
	if err != nil {
		return nil, err
	}

	if autoNamed && !a.HasDisplayName() {
		a.SetDisplayName(fmt.Sprintf("Track %d", num))
	}

	s.mu.Lock()
	s.agents[id] = a
	s.mu.Unlock()

	return a, nil
}

// AddAgentDefault creates and starts a new agent using the default claude command.
func (s *Session) AddAgentDefault(cfg Config) (*Agent, error) {
	s.mu.Lock()
	s.nextAgentNum++
	num := s.nextAgentNum
	autoNamed := cfg.Name == ""
	if autoNamed {
		cfg.Name = fmt.Sprintf("track-%d", num)
	}
	id := fmt.Sprintf("%s-agent-%d", s.ID, num)
	socketPath := s.hookSocketPath
	s.mu.Unlock()

	a, err := newAgent(id, cfg, s.Worktree.Path, socketPath)
	if err != nil {
		return nil, err
	}

	if autoNamed && !a.HasDisplayName() {
		a.SetDisplayName(fmt.Sprintf("Track %d", num))
	}

	s.mu.Lock()
	s.agents[id] = a
	s.mu.Unlock()

	return a, nil
}

// AddAgentResumed creates and starts a new agent that resumes a previous Claude session.
func (s *Session) AddAgentResumed(cfg Config, claudeSessionID string) (*Agent, error) {
	s.mu.Lock()
	s.nextAgentNum++
	num := s.nextAgentNum
	autoNamed := cfg.Name == ""
	if autoNamed {
		cfg.Name = fmt.Sprintf("track-%d", num)
	}
	id := fmt.Sprintf("%s-agent-%d", s.ID, num)
	socketPath := s.hookSocketPath
	s.mu.Unlock()

	a, err := newResumedAgent(id, cfg, s.Worktree.Path, claudeSessionID, socketPath)
	if err != nil {
		return nil, err
	}

	if autoNamed && !a.HasDisplayName() {
		a.SetDisplayName(fmt.Sprintf("Track %d", num))
	}

	s.mu.Lock()
	s.agents[id] = a
	s.mu.Unlock()

	return a, nil
}

// AddShell creates and starts a shell agent within this session.
// Only one shell per session is allowed.
func (s *Session) AddShell(cfg Config) (*Agent, error) {
	s.mu.Lock()
	for _, a := range s.agents {
		if a.IsShell {
			s.mu.Unlock()
			return nil, fmt.Errorf("session %s already has a shell agent", s.ID)
		}
	}
	s.nextAgentNum++
	num := s.nextAgentNum
	id := fmt.Sprintf("%s-agent-%d", s.ID, num)
	s.mu.Unlock()

	a, err := newShellAgent(id, cfg, s.Worktree.Path)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.agents[id] = a
	s.mu.Unlock()

	return a, nil
}

// HasShell reports whether this session has a shell agent.
func (s *Session) HasShell() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.agents {
		if a.IsShell {
			return true
		}
	}
	return false
}

// GetAgent returns an agent by ID, or nil if not found.
func (s *Session) GetAgent(id string) *Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.agents[id]
}

// Agents returns all agents sorted by CreatedAt.
func (s *Session) Agents() []*Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Agent, 0, len(s.agents))
	for _, a := range s.agents {
		result = append(result, a)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result
}

// Status returns a composite status across all agents in the session.
// Priority: any Active→Active, any Starting→Starting, any Idle→Idle,
// any Error→Error, all Done→Done, no agents→Idle.
func (s *Session) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.agents) == 0 {
		return StatusIdle
	}

	hasStarting := false
	hasIdle := false
	hasError := false
	allDone := true
	nonShellCount := 0

	for _, a := range s.agents {
		if a.IsShell {
			continue
		}
		nonShellCount++
		st := a.Status()
		switch st {
		case StatusActive, StatusWaiting:
			// Waiting rolls up as Active at the session header so a session
			// with any waiting agent still reads as attention-worthy.
			return StatusActive
		case StatusStarting:
			hasStarting = true
			allDone = false
		case StatusIdle:
			hasIdle = true
			allDone = false
		case StatusError:
			hasError = true
			allDone = false
		case StatusDone:
			// continue
		default:
			allDone = false
		}
	}

	if nonShellCount == 0 {
		return StatusIdle
	}
	if hasStarting {
		return StatusStarting
	}
	if hasIdle {
		return StatusIdle
	}
	if hasError {
		return StatusError
	}
	if allDone {
		return StatusDone
	}
	return StatusIdle
}

// IsReviewable reports whether the session is at a natural review point: it
// has at least one non-shell agent, and every non-shell agent is in
// {StatusIdle, StatusDone, StatusError}. Equivalently, no non-shell agent is
// Active, Waiting, or Starting. Shell agents are ignored — they're long-lived
// helpers, not work that produces a reviewable result. This is additive to
// MarkDone()/DoneAt(), which still mean "process exited"; IsReviewable also
// returns true between Claude turns when the agent is Idle but hasn't /exit'd.
func (s *Session) IsReviewable() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	nonShell := 0
	for _, a := range s.agents {
		if a.IsShell {
			continue
		}
		nonShell++
		switch a.Status() {
		case StatusIdle, StatusDone, StatusError:
			// reviewable
		default:
			return false
		}
	}
	return nonShell > 0
}

// AgentCount returns the number of agents in this session.
func (s *Session) AgentCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.agents)
}

// LiveAgentCount returns the number of non-shell agents whose status is
// neither StatusDone nor StatusError. This is the right count for capacity
// checks (concurrent-agent limits, the quit "agents running" warning) where
// shells and naturally-exited agents shouldn't trigger the warning.
func (s *Session) LiveAgentCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, a := range s.agents {
		if a.IsShell {
			continue
		}
		switch a.Status() {
		case StatusDone, StatusError:
			continue
		}
		count++
	}
	return count
}

// AgentStatusPriority returns a sortable rank for an agent's current status,
// used by both Session.PrimaryAgent (deterministic pick for pipeline keys) and
// the App-level focusLaunch auto-open pick. Order: Active > Waiting > Idle >
// Starting > Done/Error. Defining it once avoids the two callers drifting if a
// new status is ever added.
func AgentStatusPriority(a *Agent) int {
	switch a.Status() {
	case StatusActive:
		return 5
	case StatusWaiting:
		return 4
	case StatusIdle:
		return 3
	case StatusStarting:
		return 2
	default:
		return 1
	}
}

// PrimaryAgent returns the highest-priority non-shell agent in the session, or
// nil if the session has no agents at all. Shell agents are skipped unless
// they are the only thing in the session, in which case the first shell is
// returned. Used by pipeline workflow keys (c, x) to pick a deterministic
// target without forcing the user to drill into focusLaunch.
func (s *Session) PrimaryAgent() *Agent {
	agents := s.Agents()
	if len(agents) == 0 {
		return nil
	}
	var best *Agent
	bestPri := -1
	for _, a := range agents {
		if a.IsShell {
			continue
		}
		if pri := AgentStatusPriority(a); pri > bestPri {
			bestPri = pri
			best = a
		}
	}
	if best != nil {
		return best
	}
	return agents[0]
}

// KillAgent kills a single agent but does not remove the session.
func (s *Session) KillAgent(id string) {
	s.mu.RLock()
	a := s.agents[id]
	s.mu.RUnlock()

	if a == nil {
		return
	}

	a.Kill()
	<-a.Done()

	s.mu.Lock()
	delete(s.agents, id)
	s.mu.Unlock()
}

// KillAll kills all agents in this session.
func (s *Session) KillAll() {
	s.mu.RLock()
	agents := make([]*Agent, 0, len(s.agents))
	for _, a := range s.agents {
		agents = append(agents, a)
	}
	s.mu.RUnlock()

	var wg sync.WaitGroup
	wg.Add(len(agents))
	for _, a := range agents {
		go func() {
			defer wg.Done()
			a.Kill()
			<-a.Done()
		}()
	}
	wg.Wait()

	s.mu.Lock()
	s.agents = make(map[string]*Agent)
	s.mu.Unlock()
}

// Cleanup removes the session's worktree. If the session owns its branch
// (created it), the branch is also deleted. Attached sessions preserve the
// branch. Idempotent: a second call is a no-op and returns the same error
// (if any) the first call returned — both Shutdown's teardown loop and
// closeSession from a natural agent exit can race to clean the same session.
func (s *Session) Cleanup(repoPath string) error {
	s.cleanupOnce.Do(func() {
		s.cleanupErr = git.RemoveWorktree(repoPath, s.Worktree, s.ownsBranch)
	})
	return s.cleanupErr
}

// SetDisplayName sets a human-readable display name for the session.
func (s *Session) SetDisplayName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.displayName = name
}

// GetDisplayName returns the display name if set, otherwise falls back to Name.
func (s *Session) GetDisplayName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.displayName != "" {
		return s.displayName
	}
	return s.Name
}

// HasDisplayName reports whether a display name has been set.
func (s *Session) HasDisplayName() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.displayName != ""
}

// HasClaudeName reports whether this session's branch has been renamed from
// its initial random placeholder to one derived from the user's first prompt.
func (s *Session) HasClaudeName() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hasClaudeName
}

// IsRenaming reports whether a Haiku rename goroutine is currently in flight.
func (s *Session) IsRenaming() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.renameJob.running
}

// SetClaudeName marks whether this session has a Claude-derived branch name.
func (s *Session) SetClaudeName(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hasClaudeName = v
}

// TryStartRename atomically returns true if a branch rename should start now,
// or false if one is already in flight or the session has already been renamed.
// Callers that receive true must call finishRename when done.
func (s *Session) TryStartRename() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.renameJob.tryStart(nil, s.hasClaudeName)
}

// finishRename clears the in-flight rename flag. Called from the deferred
// cleanup of the goroutine spawned after TryStartRename returns true.
func (s *Session) finishRename() {
	s.mu.Lock()
	_ = s.renameJob.finish()
	s.mu.Unlock()
}

// RenameBranch renames the session's git branch to newBranch. If the rename
// succeeds, Session.Worktree.Branch, Session.Name, and hasClaudeName are
// updated atomically under the session mutex. The actual new branch name
// (which may include a collision suffix from git.RenameBranch) is returned.
//
// The on-disk worktree directory is intentionally NOT moved. `git worktree
// move` would rename the directory under a running Claude process — even
// though the kernel keeps the cwd inode reference valid, the process's PWD
// env goes stale, the absolute --settings path baked into Claude's argv
// stops resolving, and any cached absolute paths inside Claude (session
// files indexed by cwd, subprocess working dirs, etc.) break. Keeping the
// branch rename atomic and leaving the worktree path frozen at its initial
// adjective-noun preserves the invariant documented in CLAUDE.md: the
// worktree's HEAD symref updates atomically and Claude's cwd stays valid.
//
// If the session already has a Claude-derived name, this is a no-op and
// returns the current branch.
//
// The session mutex is held across the git subprocess call so concurrent
// callers observe a consistent view and cannot both attempt a rename.
func (s *Session) RenameBranch(repoPath, newBranch string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.hasClaudeName {
		return s.Worktree.Branch, nil
	}

	actual, err := git.RenameBranch(repoPath, s.Worktree.Branch, newBranch)
	if err != nil {
		return "", err
	}

	s.Worktree.Branch = actual
	if last := lastBranchSegment(actual); last != "" {
		s.Name = last
	}
	s.hasClaudeName = true

	return actual, nil
}

// UpdateBranch updates the in-memory branch name and session display name to
// match an externally applied git branch rename. This is the no-git-ops
// counterpart to RenameBranch: it reconciles state after the user has already
// run `git branch -m` outside refrain. Setting hasClaudeName prevents the Haiku
// namer from overwriting the externally-set name on the next user prompt.
func (s *Session) UpdateBranch(newBranch string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Worktree.Branch = newBranch
	if last := lastBranchSegment(newBranch); last != "" {
		s.Name = last
	}
	s.hasClaudeName = true
}

// Branch returns the session's current git branch, safe for concurrent reads
// while RenameBranch may be mutating it.
func (s *Session) Branch() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Worktree.Branch
}

// CurrentName returns the session's current Name, safe for concurrent reads.
func (s *Session) CurrentName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Name
}

func lastBranchSegment(branch string) string {
	parts := strings.Split(branch, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			return parts[i]
		}
	}
	return ""
}

// Elapsed returns how long the session has been running.
func (s *Session) Elapsed() time.Duration {
	return time.Since(s.CreatedAt)
}

// LifecyclePhase returns the current lifecycle phase of the session.
func (s *Session) LifecyclePhase() LifecyclePhase {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lifecyclePhase
}

// SetLifecyclePhase sets the lifecycle phase of the session.
func (s *Session) SetLifecyclePhase(p LifecyclePhase) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lifecyclePhase = p
}

// OriginalPrompt returns the user's original prompt for this session.
func (s *Session) OriginalPrompt() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.originalPrompt
}

// SetOriginalPrompt stores p as the session's original intent if none has been stored yet.
// Empty strings are silently ignored — callers must filter non-actionable prompts before calling.
func (s *Session) SetOriginalPrompt(p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.originalPrompt == "" {
		s.originalPrompt = p
	}
}

// DoneAt returns the time the session's work finished, or zero if not yet marked done.
func (s *Session) DoneAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.doneAt
}

// MarkDone records the time the session's work finished. No-op if already set.
func (s *Session) MarkDone() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.doneAt.IsZero() {
		s.doneAt = time.Now()
	}
}

// RestoreDoneAt sets doneAt directly (used only when restoring from persisted state).
func (s *Session) RestoreDoneAt(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.doneAt = t
}

// NewSessionForTest creates a Session for use in tests outside the agent package.
func NewSessionForTest(id, name string) *Session {
	return newSession(id, name, &git.WorktreeInfo{})
}

// NewSessionForTestWithPath creates a Session whose worktree path is set, so
// callers can exercise plan-file helpers (WritePlan, ReadPlan, HasPlan,
// HasPrevPlan, RestorePrevPlan) without spinning up a real git worktree.
// Intended for tests outside the agent package; production code constructs
// sessions through the manager.
func NewSessionForTestWithPath(id, name, worktreePath string) *Session {
	return newSession(id, name, &git.WorktreeInfo{Path: worktreePath})
}

// AddTestAgent injects a synthetic agent with the given id/shell flag/status
// into the session for use in tests outside the agent package. It bypasses the
// normal PTY-spawning path so callers don't need a real subprocess to exercise
// status-dependent session logic (e.g. IsReviewable). The status write is
// guarded by a.mu to match the pattern of every production writer of status.
// The done and writeLoopDone channels are pre-closed so Kill() and KillAll()
// treat the agent as already finished without blocking on a real process.
func (s *Session) AddTestAgent(id string, isShell bool, status Status) *Agent {
	done := make(chan struct{})
	close(done)
	writeLoopDone := make(chan struct{})
	close(writeLoopDone)
	a := &Agent{
		ID:            id,
		IsShell:       isShell,
		CreatedAt:     time.Now(),
		done:          done,
		writeLoopDone: writeLoopDone,
	}
	a.mu.Lock()
	a.status = status
	a.mu.Unlock()
	s.mu.Lock()
	s.agents[id] = a
	s.mu.Unlock()
	return a
}

// TaskSummary returns the session's task summary. Empty until SetTaskSummary is called.
func (s *Session) TaskSummary() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.taskSummary
}

// HasTaskSummary reports whether a task summary has been set.
func (s *Session) HasTaskSummary() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hasTaskSummary
}

// SetTaskSummary stores the task summary and marks hasTaskSummary=true.
// It is safe to call with an empty string (haiku failed but we don't retry).
func (s *Session) SetTaskSummary(summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taskSummary = summary
	s.hasTaskSummary = true
}

// TryStartTaskSummary atomically returns true if a task-summary goroutine should
// start now, or false if one is already in flight or the session already has a
// summary. Callers that receive true must call finishTaskSummary when done.
func (s *Session) TryStartTaskSummary() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.summaryJob.tryStart(nil, s.hasTaskSummary)
}

// finishTaskSummary clears the in-flight summarizing flag. Called from the
// deferred cleanup of the goroutine spawned after TryStartTaskSummary returns true.
func (s *Session) finishTaskSummary() {
	s.mu.Lock()
	_ = s.summaryJob.finish()
	s.mu.Unlock()
}

// IsDrafting reports whether a plan-drafting subprocess is currently in flight.
func (s *Session) IsDrafting() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.draftJob.running
}

// TryStartDraft atomically returns true if a plan-drafting subprocess should
// start now, or false if one is already in flight. The provided cancel func
// is stored so CancelDraft can cancel an in-flight subprocess (e.g. when
// KillSession is called or the manager shuts down). Callers that receive
// true must call finishDraft when done.
func (s *Session) TryStartDraft(cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.draftJob.tryStart(cancel, false)
}

// DraftAttempt returns the in-flight attempt counter as (current, max).
// Both values are 0 outside a draft, 1-based during one.
func (s *Session) DraftAttempt() (current, max int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.draftJob.attempt, s.draftJob.maxAttempt
}

// SetDraftAttempt records the current retry attempt. Called by runDraftWithRetry
// before each subprocess invocation so the dashboard badge can show progress.
func (s *Session) SetDraftAttempt(current, max int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.draftJob.attempt = current
	s.draftJob.maxAttempt = max
}

// finishDraft clears the in-flight draft flag and releases the stored cancel
// func by invoking it (idempotent — calling cancel after the context already
// fired is a no-op). Called from the deferred cleanup of the goroutine
// spawned after TryStartDraft returns true. Does NOT clear draftErr — the
// last error stays on the session so the Planning card can show it.
func (s *Session) finishDraft() {
	s.mu.Lock()
	cancel := s.draftJob.finish()
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// CancelDraft cancels the in-flight drafting subprocess if any. Safe to call
// when no draft is in flight (no-op). Used by Manager.KillSession and
// Manager.Shutdown to abort the subprocess promptly so the goroutine can
// drain through finishDraft.
func (s *Session) CancelDraft() {
	s.mu.RLock()
	cancel := s.draftJob.cancel
	s.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

// SetDraftError stores the latest drafting error (or nil to clear). Cleared
// only when a subsequent draft succeeds; finishDraft does NOT clear it so
// the Planning card can render the failure even after the goroutine exits.
func (s *Session) SetDraftError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.draftJob.err = err
}

// DraftError returns the last drafting error, or nil if the most recent
// drafting attempt succeeded (or no draft has run).
func (s *Session) DraftError() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.draftJob.err
}

// IsRevising reports whether a plan-revising subprocess is currently in flight.
func (s *Session) IsRevising() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.reviseJob.running
}

// TryStartRevise atomically returns true if a plan-revising subprocess should
// start now, or false if one is already in flight. Mirrors TryStartDraft.
// Drafting and revising are mutually exclusive — you cannot revise while a
// draft is still landing — so this also returns false during drafting.
// Callers that receive true must call finishRevise when done.
func (s *Session) TryStartRevise(cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reviseJob.tryStart(cancel, s.draftJob.running)
}

// finishRevise clears the in-flight revise flag and releases the stored cancel
// func by invoking it (idempotent). Does NOT clear reviseErr.
func (s *Session) finishRevise() {
	s.mu.Lock()
	cancel := s.reviseJob.finish()
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// CancelRevise cancels the in-flight revising subprocess if any. Safe to call
// when no revise is in flight (no-op). Used by KillSession and Shutdown to
// abort the subprocess promptly so the goroutine can drain through finishRevise.
func (s *Session) CancelRevise() {
	s.mu.RLock()
	cancel := s.reviseJob.cancel
	s.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

// SetReviseError stores the latest revise error (or nil to clear). Cleared
// when a subsequent revise succeeds; finishRevise does NOT clear it.
func (s *Session) SetReviseError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reviseJob.err = err
}

// ReviseError returns the last revise error, or nil if the most recent revise
// attempt succeeded (or no revise has run).
func (s *Session) ReviseError() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.reviseJob.err
}

// PlanTask is a single task item parsed from a plan file.
type PlanTask struct {
	Index int    // 1-based, counting all "- [ ]" and "- [x]" lines top-to-bottom
	Text  string // task description without the leading "- [ ] " or "- [x] "
	Done  bool   // true if marked [x] or [X]
	Body  string // raw sub-bullet lines that follow the checkbox, preserving indentation
}

// ParsePlanTasks extracts ordered task items from plan markdown using the same
// counting rules as planTaskCounts in internal/tui/dashboard.go: every "- [ ]"
// or "- [x]" line (leading whitespace stripped) inside the "## Tasks" section
// is a task, indexed 1-based in the order they appear. If the plan has no
// "## Tasks" heading we fall back to scanning the entire document so freeform
// plans still report a useful count; with a "## Tasks" heading present, only
// checkboxes within that section count — stray "- [ ]" lines in Spec or
// Verification do not corrupt the [task N] commit-to-task mapping.
func ParsePlanTasks(plan string) []PlanTask {
	tasks := make([]PlanTask, 0, 16)
	idx := 0
	for _, raw := range ScanTaskLines(plan) {
		line := strings.TrimLeft(raw, " \t")
		isCheckbox := strings.HasPrefix(line, "- [") && len(line) >= 6 && line[4] == ']'
		if isCheckbox {
			if len(tasks) > 0 {
				tasks[len(tasks)-1].Body = strings.TrimRight(tasks[len(tasks)-1].Body, " \t\n")
			}
			idx++
			marker := line[3]
			done := marker == 'x' || marker == 'X'
			text := strings.TrimSpace(line[5:])
			tasks = append(tasks, PlanTask{Index: idx, Text: text, Done: done})
		} else if len(tasks) > 0 {
			tasks[len(tasks)-1].Body += raw + "\n"
		}
	}
	if len(tasks) > 0 {
		tasks[len(tasks)-1].Body = strings.TrimRight(tasks[len(tasks)-1].Body, " \t\n")
	}
	return tasks
}

// ScanTaskLines returns the lines of plan that should be considered for task
// counting. When the plan contains a "## Tasks" heading we return only the
// lines inside that section (up to the next "## " heading or EOF); otherwise
// we return every line in the document so freeform plans without a Tasks
// section still produce a count. Exported so internal/tui can share the same
// scoping rule as ParsePlanTasks without duplicating the state machine.
func ScanTaskLines(plan string) []string {
	lines := strings.Split(plan, "\n")
	start, end := -1, len(lines)
	for i, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if start == -1 {
			if isTasksHeading(trimmed) {
				start = i + 1
			}
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			end = i
			break
		}
	}
	if start == -1 {
		return lines
	}
	return lines[start:end]
}

// isTasksHeading reports whether a trimmed line is the "## Tasks" heading.
// Matches "## Tasks" only (case-insensitive on the word) so it doesn't
// misfire on "## Task Status" or "## Tasks (revised)".
func isTasksHeading(trimmed string) bool {
	if !strings.HasPrefix(trimmed, "## ") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(trimmed[3:]), "Tasks")
}

// PlanSections holds the named sections extracted from a plan markdown document.
type PlanSections struct {
	Goal, Spec, Verification, NotInScope string
}

// ParsePlanSections extracts Goal, Spec, Verification, and Not in scope bodies
// from plan markdown. It uses a simple state machine: each recognized heading
// (`# Goal`, `## Spec`, `## Verification`, `## Not in scope`) switches the
// active section; lines between headings accumulate into the corresponding
// field. The heading lines themselves are excluded. Body text is trimmed of
// trailing whitespace.
func ParsePlanSections(plan string) PlanSections {
	type field int
	const (
		fieldNone field = iota
		fieldGoal
		fieldSpec
		fieldVerification
		fieldNotInScope
	)

	var (
		cur   field
		goal  []string
		spec  []string
		verif []string
		notin []string
	)

	for _, line := range strings.Split(plan, "\n") {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case "# Goal":
			cur = fieldGoal
			continue
		case "## Spec":
			cur = fieldSpec
			continue
		case "## Verification":
			cur = fieldVerification
			continue
		case "## Not in scope":
			cur = fieldNotInScope
			continue
		default:
			// Any other heading (## Context, ## Reuse, etc.) ends the current section.
			if strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ") {
				cur = fieldNone
				continue
			}
		}

		switch cur {
		case fieldGoal:
			goal = append(goal, line)
		case fieldSpec:
			spec = append(spec, line)
		case fieldVerification:
			verif = append(verif, line)
		case fieldNotInScope:
			notin = append(notin, line)
		}
	}

	join := func(lines []string) string {
		return strings.TrimSpace(strings.Join(lines, "\n"))
	}
	return PlanSections{
		Goal:         join(goal),
		Spec:         join(spec),
		Verification: join(verif),
		NotInScope:   join(notin),
	}
}

// taskSubjectRE matches "[task N]" at the start of a commit subject where N is
// a positive integer. Case-insensitive so "[Task 3]" is also accepted.
var taskSubjectRE = regexp.MustCompile(`(?i)^\[task\s+(\d+)\]`)

// CommitGroup holds the commits associated with a single plan task (or the
// "other" bucket for commits without a recognizable [task N] prefix).
type CommitGroup struct {
	// TaskIndex is 1-based and matches the PlanTask.Index it belongs to.
	// Zero means the group is the "Other changes" bucket (no [task N] prefix
	// and/or uncommitted working-tree changes).
	TaskIndex int
	Commits   []git.Commit
}

// GroupCommitsByTask partitions commits by their "[task N]" subject prefix.
// Commits without the prefix land in a group with TaskIndex=0 ("Other
// changes"). Within each group, commits preserve their input order (oldest
// first). The returned slice is sorted by TaskIndex ascending, with the
// TaskIndex=0 group appended last so it renders at the bottom of the review
// task list.
func GroupCommitsByTask(commits []git.Commit) []CommitGroup {
	byIndex := make(map[int]*CommitGroup)
	for _, c := range commits {
		idx := 0
		if m := taskSubjectRE.FindStringSubmatch(c.Subject); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
				idx = n
			}
		}
		if byIndex[idx] == nil {
			byIndex[idx] = &CommitGroup{TaskIndex: idx}
		}
		byIndex[idx].Commits = append(byIndex[idx].Commits, c)
	}

	// Collect and sort task-indexed groups; append "other" (0) last.
	var groups []CommitGroup
	var otherGroup *CommitGroup
	for idx, g := range byIndex {
		if idx == 0 {
			otherGroup = g
		} else {
			groups = append(groups, *g)
		}
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].TaskIndex < groups[j].TaskIndex
	})
	if otherGroup != nil {
		groups = append(groups, *otherGroup)
	}
	return groups
}

// PlanPath returns the absolute path to the session's plan markdown file
// (<worktree>/.claude/plan.md). Always returns a path even if the file does
// not exist — callers use HasPlan or ReadPlan to test for presence.
func (s *Session) PlanPath() string {
	if s.Worktree == nil {
		return ""
	}
	return filepath.Join(s.Worktree.Path, ".claude", "plan.md")
}

// PrevPlanPath returns the absolute path to the session's previous-plan
// snapshot (<worktree>/.claude/plan.prev.md). The file is written by
// RevisePlan as a single-step undo target; RestorePrevPlan reads it back.
func (s *Session) PrevPlanPath() string {
	if s.Worktree == nil {
		return ""
	}
	return filepath.Join(s.Worktree.Path, ".claude", "plan.prev.md")
}

// ReadPlan returns the contents of the session's plan file. Returns
// ("", nil) if the file does not exist (no plan written yet) — callers
// should treat absence and emptiness identically.
func (s *Session) ReadPlan() (string, error) {
	data, err := os.ReadFile(s.PlanPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("plan: reading %s: %w", s.PlanPath(), err)
	}
	return string(data), nil
}

// WritePlan writes content to the session's plan file using a temp+rename
// for atomicity. Ensures <worktree>/.claude/ exists, and on first write
// also appends ".claude/" to the worktree's root .gitignore if not already
// covered, so the plan stays out of the eventual PR diff. Concurrent reads
// during a write see either the old content or the new content, never a
// partial write — os.Rename is atomic on Unix.
func (s *Session) WritePlan(content string) error {
	planDir := filepath.Join(s.Worktree.Path, ".claude")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		return fmt.Errorf("plan: creating %s: %w", planDir, err)
	}

	// Best effort: gitignore management must not block writing the plan.
	// Stderr writes during an active TUI alt-screen scramble the rendered
	// UI, so the error is swallowed here — the worst case is the plan
	// shows up in `git status`, which is recoverable.
	_ = s.ensureClaudeIgnored()

	planPath := s.PlanPath()
	tmp, err := os.CreateTemp(planDir, "plan-*.md.tmp")
	if err != nil {
		return fmt.Errorf("plan: creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("plan: writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("plan: closing temp file: %w", err)
	}
	if err := os.Rename(tmpName, planPath); err != nil {
		return fmt.Errorf("plan: renaming temp file to %s: %w", planPath, err)
	}
	committed = true

	// Record mtime alongside content so CachedPlan's mtime check skips
	// the ReadFile on the immediate next tick.
	var mtime time.Time
	if fi, err2 := os.Stat(planPath); err2 == nil {
		mtime = fi.ModTime()
	}
	s.mu.Lock()
	s.planCacheLoaded = true
	s.planCachePresent = true
	s.planCacheContent = content
	s.planCacheMTime = mtime
	s.mu.Unlock()

	return nil
}

// HasPlan reports whether the session's plan file exists on disk.
func (s *Session) HasPlan() bool {
	_, err := os.Stat(s.PlanPath())
	return err == nil
}

// CachedPlan returns the most recently written plan content along with a
// flag indicating whether a plan exists. The cache is keyed by the mtime of
// plan.md: each call stats the file; if the mtime is unchanged the cached
// content is returned without a ReadFile. This lets external edits (e.g. the
// build agent toggling checkboxes via Claude's Edit tool, bypassing WritePlan)
// show through within one 100ms dashboard tick.
//
// Returns ("", false) when no plan file exists. Read errors are not surfaced —
// callers in render hot paths can't usefully react to them.
func (s *Session) CachedPlan() (string, bool) {
	planPath := s.PlanPath()
	if planPath == "" {
		return "", false
	}

	fi, err := os.Stat(planPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Skip the write lock if the cache already reflects "absent" — the
			// common case for Planning-phase sessions before a plan is written,
			// where CachedPlan is called on every 100ms render tick.
			s.mu.RLock()
			alreadyAbsent := s.planCacheLoaded && !s.planCachePresent
			s.mu.RUnlock()
			if !alreadyAbsent {
				s.mu.Lock()
				s.planCacheLoaded = true
				s.planCachePresent = false
				s.planCacheContent = ""
				s.planCacheMTime = time.Time{}
				s.mu.Unlock()
			}
			return "", false
		}
		// Other stat error: return cached value if we have one so a transient
		// I/O hiccup doesn't blank the dashboard.
		s.mu.RLock()
		content, present := s.planCacheContent, s.planCachePresent
		s.mu.RUnlock()
		return content, present
	}

	mtime := fi.ModTime()

	s.mu.RLock()
	if s.planCacheLoaded && mtime.Equal(s.planCacheMTime) {
		content, present := s.planCacheContent, s.planCachePresent
		s.mu.RUnlock()
		return content, present
	}
	s.mu.RUnlock()

	// mtime changed (or first call): read the file outside any lock.
	data, err := os.ReadFile(planPath)
	if err != nil {
		// Don't poison the cache on a transient read error.
		s.mu.RLock()
		content, present := s.planCacheContent, s.planCachePresent
		s.mu.RUnlock()
		return content, present
	}
	content := string(data)

	s.mu.Lock()
	// Only update if this mtime is at least as recent as what we have,
	// so a concurrent WritePlan with a later mtime is never regressed.
	if !mtime.Before(s.planCacheMTime) {
		s.planCacheLoaded = true
		s.planCachePresent = true
		s.planCacheContent = content
		s.planCacheMTime = mtime
	} else {
		content = s.planCacheContent
	}
	present := s.planCachePresent
	s.mu.Unlock()
	return content, present
}

// HasPrevPlan reports whether a previous-plan snapshot exists on disk.
// Used by the editor to decide whether to render the `u` (undo) hint.
func (s *Session) HasPrevPlan() bool {
	_, err := os.Stat(s.PrevPlanPath())
	return err == nil
}

// snapshotPlanToPrev reads the current plan.md and writes it to plan.prev.md
// using a temp+rename so a concurrent reader never sees a partial write.
// Used by Manager.RevisePlan before invoking the drafter so the user can
// undo to the pre-revise version. Returns nil when no plan.md exists yet
// (the revise path covers that case by falling through to "no undo target").
func (s *Session) snapshotPlanToPrev() error {
	current, err := s.ReadPlan()
	if err != nil {
		return err
	}
	if current == "" {
		return nil
	}
	return s.writePrevPlan(current)
}

// writePrevPlan writes content to plan.prev.md atomically. Mirrors WritePlan
// but does not touch .gitignore (the dir already exists by the time this is
// called, since plan.md was just read).
func (s *Session) writePrevPlan(content string) error {
	planDir := filepath.Join(s.Worktree.Path, ".claude")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		return fmt.Errorf("plan: creating %s: %w", planDir, err)
	}
	prevPath := s.PrevPlanPath()
	tmp, err := os.CreateTemp(planDir, "plan-prev-*.md.tmp")
	if err != nil {
		return fmt.Errorf("plan: creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("plan: writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("plan: closing temp file: %w", err)
	}
	if err := os.Rename(tmpName, prevPath); err != nil {
		return fmt.Errorf("plan: renaming temp file to %s: %w", prevPath, err)
	}
	committed = true
	return nil
}

// RestorePrevPlan replaces plan.md with the contents of plan.prev.md and
// removes the snapshot. Single-step undo only — after restore, the previous
// plan is gone and a second `u` press is a no-op (HasPrevPlan returns false).
// Returns ("", false, nil) when no snapshot exists, in which case the caller
// renders the no-op hint. The restored plan content is returned so the editor
// can refresh in-memory state without re-reading.
func (s *Session) RestorePrevPlan() (string, bool, error) {
	data, err := os.ReadFile(s.PrevPlanPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("plan: reading %s: %w", s.PrevPlanPath(), err)
	}
	prev := string(data)
	if err := s.WritePlan(prev); err != nil {
		return "", false, err
	}
	// Best effort: remove the snapshot so a second `u` doesn't loop. A failed
	// remove leaves a stale snapshot but the next revise will overwrite it,
	// so we surface the read/write success even if the cleanup misses.
	_ = os.Remove(s.PrevPlanPath())
	return prev, true, nil
}

// CommitTaskCount returns the cached count of distinct [task N] indices and
// the maximum task index seen in commits ahead of the base branch. Both values
// are zero until RefreshCommitTaskCount is called at least once.
func (s *Session) CommitTaskCount() (done, maxIdx int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.commitTaskDone, s.commitTaskMaxIdx
}

// RefreshCommitTaskCount reads commits ahead of the base branch (via
// git.LogCommitsAgainstBase), groups them by [task N] prefix, and caches the
// resulting (distinct count, max index) pair. Callers that want to avoid a
// shell-out on the render path should call this from a background goroutine and
// read the cached result via CommitTaskCount.
//
// Returns nil and writes (0, 0) when the session's Worktree is nil or its path
// is empty, so dashboard tests with synthetic sessions render without error.
func (s *Session) RefreshCommitTaskCount() error {
	if s.Worktree == nil || s.Worktree.Path == "" {
		return nil
	}
	commits, err := git.LogCommitsAgainstBase(s.Worktree)
	if err != nil {
		return err
	}
	groups := GroupCommitsByTask(commits)
	done := 0
	maxIdx := 0
	for _, g := range groups {
		if g.TaskIndex > 0 {
			done++
			if g.TaskIndex > maxIdx {
				maxIdx = g.TaskIndex
			}
		}
	}
	s.mu.Lock()
	s.commitTaskDone = done
	s.commitTaskMaxIdx = maxIdx
	s.mu.Unlock()
	return nil
}

// SetCommitTaskCountForTest injects a cached commit-task count for testing.
// Test-only: do not call from production code.
func (s *Session) SetCommitTaskCountForTest(done, maxIdx int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commitTaskDone = done
	s.commitTaskMaxIdx = maxIdx
}

// ensureClaudeIgnored appends ".claude/" to the worktree's root .gitignore
// if not already covered. Writes the file even if it didn't exist previously.
// Errors are returned but treated as non-fatal by WritePlan — the worst case
// is the plan shows up in `git status`, which the user can fix manually.
func (s *Session) ensureClaudeIgnored() error {
	gitignorePath := filepath.Join(s.Worktree.Path, ".gitignore")
	existing, err := os.ReadFile(gitignorePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("plan: reading %s: %w", gitignorePath, err)
	}
	if claudeIgnoreCovered(string(existing)) {
		return nil
	}
	addition := ".claude/\n"
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		addition = "\n" + addition
	}
	combined := append(existing, []byte(addition)...)
	if err := os.WriteFile(gitignorePath, combined, 0o644); err != nil {
		return fmt.Errorf("plan: writing %s: %w", gitignorePath, err)
	}
	return nil
}

// claudeIgnoreCovered reports whether body of a .gitignore already excludes
// the .claude/ directory. Only matches the exact patterns ".claude/" and
// ".claude" — fancier glob analysis would risk a false positive that misses
// a duplicate-add edge case. Comments (lines starting with `#`) are skipped.
func claudeIgnoreCovered(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == ".claude" || trimmed == ".claude/" {
			return true
		}
	}
	return false
}

// LastOutputTime returns the most recent lastOutput time across all non-shell
// agents in this session. Returns the zero time if there are no such agents or
// none have produced any output yet.
func (s *Session) LastOutputTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var latest time.Time
	for _, a := range s.agents {
		if a.IsShell {
			continue
		}
		a.mu.RLock()
		t := a.lastOutput
		a.mu.RUnlock()
		if t.After(latest) {
			latest = t
		}
	}
	return latest
}
