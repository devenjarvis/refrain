package tui

import (
	"os/exec"

	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/config"
	"github.com/devenjarvis/refrain/internal/state"
)

// fakeManager is a deterministic in-memory SessionManager for TUI unit tests.
// It satisfies the full interface without spinning up PTYs, git worktrees, or
// a hook socket. Test code constructs one inline, seeds whatever sessions it
// needs via newFakeManager, and drives App.Update against it.
//
// Each method records its call (so tests can assert on dispatch) and returns
// either the seeded data or a sensible zero value. New methods get default
// no-op behavior — flesh them out only when a test actually depends on the
// response.
type fakeManager struct {
	repoPath         string
	sessions         []*agent.Session
	events           chan agent.Event
	plannerQuestions chan agent.PlannerQuestion

	// Invocation counters; tests assert on these where behavior matters.
	killSessionCalls map[string]int
	killAgentCalls   map[string]int
	shutdownCalls    int
	updateSettings   []config.ResolvedSettings
}

// Compile-time guarantee: keep fakeManager in sync with the SessionManager
// interface. The whole point of the fake is that tests can rely on it being
// a drop-in for *agent.Manager.
var _ SessionManager = (*fakeManager)(nil)

func newFakeManager(repoPath string, sessions ...*agent.Session) *fakeManager {
	return &fakeManager{
		repoPath:         repoPath,
		sessions:         sessions,
		events:           make(chan agent.Event, 16),
		plannerQuestions: make(chan agent.PlannerQuestion, 8),
		killSessionCalls: make(map[string]int),
		killAgentCalls:   make(map[string]int),
	}
}

func (f *fakeManager) AgentCount() int {
	n := 0
	for _, s := range f.sessions {
		n += len(s.Agents())
	}
	return n
}

func (f *fakeManager) RepoPath() string { return f.repoPath }

func (f *fakeManager) ListSessions() []*agent.Session {
	out := make([]*agent.Session, len(f.sessions))
	copy(out, f.sessions)
	return out
}

func (f *fakeManager) GetSession(id string) *agent.Session {
	for _, s := range f.sessions {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (f *fakeManager) Get(id string) *agent.Agent {
	for _, s := range f.sessions {
		for _, ag := range s.Agents() {
			if ag.ID == id {
				return ag
			}
		}
	}
	return nil
}

func (f *fakeManager) FindAgentAndSession(agentID string) (*agent.Agent, *agent.Session) {
	for _, s := range f.sessions {
		for _, ag := range s.Agents() {
			if ag.ID == agentID {
				return ag, s
			}
		}
	}
	return nil, nil
}

func (f *fakeManager) Events() <-chan agent.Event                     { return f.events }
func (f *fakeManager) PlannerQuestions() <-chan agent.PlannerQuestion { return f.plannerQuestions }

func (f *fakeManager) CreateSession(_ agent.Config) (*agent.Session, *agent.Agent, error) {
	return nil, nil, nil
}

func (f *fakeManager) CreateSessionWithCommand(_ agent.Config, _ func(string) *exec.Cmd) (*agent.Session, *agent.Agent, error) {
	return nil, nil, nil
}

func (f *fakeManager) CreateSessionOnBranch(_, _ string, _ agent.Config) (*agent.Session, *agent.Agent, error) {
	return nil, nil, nil
}

func (f *fakeManager) CreateSessionForPlanning(_ agent.Config) (*agent.Session, error) {
	return nil, nil
}

func (f *fakeManager) AddAgent(_ string, _ agent.Config) (*agent.Agent, error) {
	return nil, nil
}

func (f *fakeManager) AddShell(_ string, _ agent.Config) (*agent.Agent, error) {
	return nil, nil
}

func (f *fakeManager) KillAgent(sessionID, agentID string) error {
	f.killAgentCalls[sessionID+"/"+agentID]++
	return nil
}

func (f *fakeManager) KillSession(sessionID string) error {
	f.killSessionCalls[sessionID]++
	return nil
}

func (f *fakeManager) ResumeSession(_ state.SessionState, _ agent.Config) error { return nil }

func (f *fakeManager) StartDraft(_, _ string) error              { return nil }
func (f *fakeManager) RevisePlan(_, _ string) error              { return nil }
func (f *fakeManager) SetPlanDrafter(_ agent.PlanDrafter)        {}
func (f *fakeManager) ReviewerAgent() agent.ReviewerAgent        { return nil }
func (f *fakeManager) ReconcileExternalBranchRename(_, _ string) {}

func (f *fakeManager) UpdateSettings(s config.ResolvedSettings) {
	f.updateSettings = append(f.updateSettings, s)
}

func (f *fakeManager) Detach() *state.RefrainState { return nil }

func (f *fakeManager) Shutdown() {
	f.shutdownCalls++
	close(f.events)
	close(f.plannerQuestions)
}
