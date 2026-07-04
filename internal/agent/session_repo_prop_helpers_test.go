package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"pgregory.net/rapid"
)

// fataler is the intersection of *testing.T and *rapid.T used by property test
// helpers that need to abort on failure. Both types satisfy this interface.
// Note: rapid.T does not have Helper(), so it is omitted.
type fataler interface {
	Fatal(args ...any)
	Fatalf(format string, args ...any)
}

// isSubdir reports whether child is a subdirectory of parent. Both paths are
// cleaned and compared via filepath.Rel; a relative result that does not start
// with ".." indicates containment.
func isSubdir(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}

// createTestSession creates a session on mgr using a bash sleep stub.
func createTestSession(t fataler, mgr *Manager) (*Session, *Agent) {
	cfg := Config{Task: "prop-test", Rows: 24, Cols: 80}
	sess, ag, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatalf("createTestSession: %v", err)
	}
	return sess, ag
}

// createTestSessionPlanOnly creates a planning-only session (no agent process).
func createTestSessionPlanOnly(t fataler, mgr *Manager) *Session {
	cfg := Config{Task: "prop-test-plan", Rows: 24, Cols: 80}
	sess, err := mgr.CreateSessionNoAgent(cfg)
	if err != nil {
		t.Fatalf("createTestSessionPlanOnly: %v", err)
	}
	return sess
}

// assertWorktreeUnderRepo fails the test if the session's worktree path is not
// a subdirectory of repoPath.
func assertWorktreeUnderRepo(t fataler, repoPath string, sess *Session) {
	if sess.Worktree == nil {
		t.Fatal("session.Worktree is nil")
	}
	if !isSubdir(repoPath, sess.Worktree.Path) {
		t.Fatalf("Worktree.Path %q is not under repo %q", sess.Worktree.Path, repoPath)
	}
}

// assertAgentPathsMatch fails the test if any agent's WorktreePath differs
// from the session's Worktree.Path.
func assertAgentPathsMatch(t fataler, sess *Session) {
	want := sess.Worktree.Path
	for _, a := range sess.Agents() {
		if a.WorktreePath != want {
			t.Fatalf("agent %s WorktreePath = %q, want %q (session worktree)", a.ID, a.WorktreePath, want)
		}
	}
}

// genForwardPhaseSequence generates a random-length ascending subsequence of
// forward lifecycle phases (Planning through Complete). The sequence always
// starts with Planning and includes 1–6 phases.
func genForwardPhaseSequence(t *rapid.T) []LifecyclePhase {
	allForward := []LifecyclePhase{
		LifecyclePlanning,
		LifecycleInProgress,
		LifecycleReadyForReview,
		LifecycleInReview,
		LifecycleShipping,
		LifecycleComplete,
	}
	seq := []LifecyclePhase{LifecyclePlanning}
	for i := 1; i < len(allForward); i++ {
		if rapid.Bool().Draw(t, allForward[i].String()) {
			seq = append(seq, allForward[i])
		}
	}
	return seq
}

// capturingDrafter implements PlanDrafter and records every DraftRequest.Cwd
// and ReviseRequest.Cwd it sees. Thread-safe.
type capturingDrafter struct {
	mu         sync.Mutex
	draftCwds  []string
	reviseCwds []string
	planBody   string
}

func newCapturingDrafter(planBody string) *capturingDrafter {
	return &capturingDrafter{planBody: planBody}
}

func (c *capturingDrafter) Draft(_ context.Context, req DraftRequest) (string, error) {
	c.mu.Lock()
	c.draftCwds = append(c.draftCwds, req.Cwd)
	c.mu.Unlock()
	return c.planBody, nil
}

func (c *capturingDrafter) Revise(_ context.Context, req ReviseRequest) (string, error) {
	c.mu.Lock()
	c.reviseCwds = append(c.reviseCwds, req.Cwd)
	c.mu.Unlock()
	return c.planBody, nil
}

func (c *capturingDrafter) DraftCwds() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.draftCwds))
	copy(out, c.draftCwds)
	return out
}

func (c *capturingDrafter) ReviseCwds() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.reviseCwds))
	copy(out, c.reviseCwds)
	return out
}

// setupTestRepoForProp creates a temporary git repo with an initial commit.
// Unlike setupTestRepo, it does not require *testing.T so it can be called
// inside rapid.Check closures. The caller must defer os.RemoveAll on the
// returned path.
func setupTestRepoForProp() string {
	dir, err := os.MkdirTemp("", "refrain-prop-*")
	if err != nil {
		panic("setupTestRepoForProp: " + err.Error())
	}
	for _, args := range [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			panic("setupTestRepoForProp " + args[1] + ": " + err.Error() + "\n" + string(out))
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0o644); err != nil {
		panic("setupTestRepoForProp: " + err.Error())
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "initial"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			panic("setupTestRepoForProp " + args[1] + ": " + err.Error() + "\n" + string(out))
		}
	}
	return dir
}
