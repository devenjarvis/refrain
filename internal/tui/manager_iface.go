package tui

import (
	"os/exec"

	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/config"
	"github.com/devenjarvis/refrain/internal/state"
)

// SessionManager is the slice of agent.Manager the TUI actually depends on.
// The TUI holds map[string]SessionManager rather than map[string]*agent.Manager
// so unit tests can inject a deterministic in-memory fake without spinning up
// real PTYs, git worktrees, or a hook socket.
//
// New TUI -> Manager call sites add the corresponding method here; the
// compile-time assertion at the bottom of this file guarantees *agent.Manager
// still satisfies the interface, so this seam never silently drifts.
type SessionManager interface {
	// Lifecycle / lookup
	AgentCount() int
	ActiveSessionCount() int
	RepoPath() string
	ListSessions() []*agent.Session
	GetSession(id string) *agent.Session
	Get(id string) *agent.Agent
	FindAgentAndSession(agentID string) (*agent.Agent, *agent.Session)

	// Event streams
	Events() <-chan agent.Event
	PlannerQuestions() <-chan agent.PlannerQuestion

	// Session / agent CRUD
	CreateSession(cfg agent.Config) (*agent.Session, *agent.Agent, error)
	CreateSessionWithCommand(cfg agent.Config, cmd func(name string) *exec.Cmd) (*agent.Session, *agent.Agent, error)
	CreateSessionOnBranch(branch, baseBranch string, cfg agent.Config) (*agent.Session, *agent.Agent, error)
	CreateSessionNoAgent(cfg agent.Config) (*agent.Session, error)
	AddAgent(sessionID string, cfg agent.Config) (*agent.Agent, error)
	AddShell(sessionID string, cfg agent.Config) (*agent.Agent, error)
	KillAgent(sessionID, agentID string) error
	KillSession(sessionID string) error
	ResumeSession(ss state.SessionState, cfg agent.Config) error

	// Planning subprocess control
	StartDraft(sessionID, prompt string, opts ...agent.DraftOption) error
	RevisePlan(sessionID, critique string, opts ...agent.DraftOption) error
	SetPlanDrafter(p agent.PlanDrafter)

	// Review subprocess
	ReviewerAgent() agent.ReviewerAgent

	// External coordination
	ReconcileExternalBranchRename(sessionID, newBranch string)
	UpdateSettings(s config.ResolvedSettings)

	// Teardown
	Detach() *state.RefrainState
	Shutdown()
}

// Compile-time assertion: *agent.Manager must satisfy SessionManager. If a
// future agent.Manager method-signature change breaks this seam, the
// compiler flags it here rather than at every call site.
var _ SessionManager = (*agent.Manager)(nil)
