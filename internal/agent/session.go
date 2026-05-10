package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/devenjarvis/baton/internal/git"
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
	renaming       bool // true while an async branch-rename is in flight; gates double-dispatch
	lifecyclePhase LifecyclePhase
	originalPrompt string
	doneAt         time.Time
	ownsBranch     bool   // true if this session created the branch (cleanup should delete it)
	hookSocketPath string // absolute path to the manager's hook socket ("" disables hooks)
	taskSummary    string // short summary of the session's task, set once by the summarizer goroutine
	hasTaskSummary bool   // true once SetTaskSummary has been called
	summarizing    bool   // true while an async task-summary goroutine is in flight; gates double-dispatch
	drafting       bool   // true while a plan-drafting subprocess is in flight; gates double-dispatch
	draftCancel    context.CancelFunc
	draftErr       error // last drafting error, surfaced by the Planning card; cleared on successful draft
	revising       bool  // true while a plan-revising subprocess is in flight; gates double-dispatch
	reviseCancel   context.CancelFunc
	reviseErr      error // last revise error, surfaced by the editor; cleared on successful revise
	// Plan-content cache, keyed by the most recent successful WritePlan.
	// Populated lazily by CachedPlan() on first read so resumed sessions
	// also benefit. The dashboard hot path (per-render Planning card)
	// reads this instead of os.ReadFile to avoid a stat+read+parse cycle
	// at every tick.
	planCacheLoaded  bool
	planCachePresent bool
	planCacheContent string
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
// (created it), the branch is also deleted. Attached sessions preserve the branch.
func (s *Session) Cleanup(repoPath string) error {
	return git.RemoveWorktree(repoPath, s.Worktree, s.ownsBranch)
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
	return s.renaming
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
	if s.hasClaudeName || s.renaming {
		return false
	}
	s.renaming = true
	return true
}

// finishRename clears the in-flight rename flag. Called from the deferred
// cleanup of the goroutine spawned after TryStartRename returns true.
func (s *Session) finishRename() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renaming = false
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
// run `git branch -m` outside baton. Setting hasClaudeName prevents the Haiku
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
func (s *Session) AddTestAgent(id string, isShell bool, status Status) *Agent {
	a := &Agent{ID: id, IsShell: isShell, CreatedAt: time.Now()}
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
	if s.hasTaskSummary || s.summarizing {
		return false
	}
	s.summarizing = true
	return true
}

// finishTaskSummary clears the in-flight summarizing flag. Called from the
// deferred cleanup of the goroutine spawned after TryStartTaskSummary returns true.
func (s *Session) finishTaskSummary() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.summarizing = false
}

// IsDrafting reports whether a plan-drafting subprocess is currently in flight.
func (s *Session) IsDrafting() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.drafting
}

// TryStartDraft atomically returns true if a plan-drafting subprocess should
// start now, or false if one is already in flight. The provided cancel func
// is stored so CancelDraft can cancel an in-flight subprocess (e.g. when
// KillSession is called or the manager shuts down). Callers that receive
// true must call finishDraft when done.
func (s *Session) TryStartDraft(cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.drafting {
		return false
	}
	s.drafting = true
	s.draftCancel = cancel
	return true
}

// finishDraft clears the in-flight draft flag and releases the stored cancel
// func by invoking it (idempotent — calling cancel after the context already
// fired is a no-op). Called from the deferred cleanup of the goroutine
// spawned after TryStartDraft returns true. Does NOT clear draftErr — the
// last error stays on the session so the Planning card can show it.
func (s *Session) finishDraft() {
	s.mu.Lock()
	cancel := s.draftCancel
	s.drafting = false
	s.draftCancel = nil
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
	cancel := s.draftCancel
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
	s.draftErr = err
}

// DraftError returns the last drafting error, or nil if the most recent
// drafting attempt succeeded (or no draft has run).
func (s *Session) DraftError() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.draftErr
}

// IsRevising reports whether a plan-revising subprocess is currently in flight.
func (s *Session) IsRevising() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revising
}

// TryStartRevise atomically returns true if a plan-revising subprocess should
// start now, or false if one is already in flight. Mirrors TryStartDraft.
// Drafting and revising are mutually exclusive — you cannot revise while a
// draft is still landing — so this also returns false during drafting.
// Callers that receive true must call finishRevise when done.
func (s *Session) TryStartRevise(cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revising || s.drafting {
		return false
	}
	s.revising = true
	s.reviseCancel = cancel
	return true
}

// finishRevise clears the in-flight revise flag and releases the stored cancel
// func by invoking it (idempotent). Does NOT clear reviseErr.
func (s *Session) finishRevise() {
	s.mu.Lock()
	cancel := s.reviseCancel
	s.revising = false
	s.reviseCancel = nil
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
	cancel := s.reviseCancel
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
	s.reviseErr = err
}

// ReviseError returns the last revise error, or nil if the most recent revise
// attempt succeeded (or no revise has run).
func (s *Session) ReviseError() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.reviseErr
}

// PlanPath returns the absolute path to the session's plan markdown file
// (<worktree>/.claude/plan.md). Always returns a path even if the file does
// not exist — callers use HasPlan or ReadPlan to test for presence.
func (s *Session) PlanPath() string {
	return filepath.Join(s.Worktree.Path, ".claude", "plan.md")
}

// PrevPlanPath returns the absolute path to the session's previous-plan
// snapshot (<worktree>/.claude/plan.prev.md). The file is written by
// RevisePlan as a single-step undo target; RestorePrevPlan reads it back.
func (s *Session) PrevPlanPath() string {
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

	// Update the in-memory cache so the dashboard render path can skip
	// os.Stat + os.ReadFile on subsequent ticks. Locking is brief; we
	// only enter after the file write has succeeded.
	s.mu.Lock()
	s.planCacheLoaded = true
	s.planCachePresent = true
	s.planCacheContent = content
	s.mu.Unlock()

	return nil
}

// HasPlan reports whether the session's plan file exists on disk.
func (s *Session) HasPlan() bool {
	_, err := os.Stat(s.PlanPath())
	return err == nil
}

// CachedPlan returns the most recently written plan content along with a
// flag indicating whether a plan exists. The cache is primed by WritePlan
// (and indirectly by RestorePrevPlan, which calls WritePlan); on a
// resumed session that was created before the cache existed, the first
// call lazily loads from disk. Use this in hot paths like the dashboard
// render loop instead of HasPlan() + ReadPlan(), which together do a
// stat + read on every TUI tick.
//
// Returns ("", false) when no plan file exists. Read errors are not
// surfaced — callers in render hot paths can't usefully react to them
// and the plain ReadPlan() path is still available for callers that need
// to handle errors explicitly.
func (s *Session) CachedPlan() (string, bool) {
	s.mu.RLock()
	if s.planCacheLoaded {
		content, present := s.planCacheContent, s.planCachePresent
		s.mu.RUnlock()
		return content, present
	}
	s.mu.RUnlock()

	// Lazy load. Drop the read lock before doing I/O so a concurrent
	// WritePlan can populate the cache while we read; if it does, we
	// just observe its write on the next call.
	data, err := os.ReadFile(s.PlanPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.mu.Lock()
			if !s.planCacheLoaded {
				s.planCacheLoaded = true
				s.planCachePresent = false
				s.planCacheContent = ""
			}
			s.mu.Unlock()
			return "", false
		}
		// Any other error: don't poison the cache. Callers in render
		// paths see ("", false) and fall back to their no-plan branch.
		return "", false
	}
	content := string(data)

	s.mu.Lock()
	if !s.planCacheLoaded {
		s.planCacheLoaded = true
		s.planCachePresent = true
		s.planCacheContent = content
	} else {
		// A WritePlan landed during our read; trust the cache.
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
