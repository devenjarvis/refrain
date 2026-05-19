package tui

import (
	"testing"

	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/config"
)

// TestSessionManager_AgentSatisfies pins the compile-time guarantee in a
// runtime test as well: *agent.Manager must satisfy SessionManager. Without
// this, a future agent.Manager method-signature change could silently break
// the seam if the var-assertion in manager_iface.go is removed.
func TestSessionManager_AgentSatisfies(t *testing.T) {
	var _ SessionManager = (*agent.Manager)(nil)
}

// TestSessionManager_FakeSatisfies pins the same guarantee for the in-memory
// fake used by the tests below.
func TestSessionManager_FakeSatisfies(t *testing.T) {
	var _ SessionManager = (*fakeManager)(nil)
}

// TestSessionManager_FakeDrivesKillSession demonstrates the unit-testing seam:
// a fake manager is injected into App, App.KillSessionCmd via PanelServices is
// invoked, and the test asserts on the fake's call counters without ever
// starting a real PTY/git/hook stack.
func TestSessionManager_FakeDrivesKillSession(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "session-one")
	repo := "/fake/repo"
	fake := newFakeManager(repo, sess)

	app := NewApp()
	app.activeRepo = repo
	app.managers[repo] = fake
	app.cfg = &config.Config{Repos: []config.Repo{{Path: repo}}}
	app.resolvedCache[repo] = config.Resolve(nil, nil)

	// Drive KillSessionCmd through the panel-services seam (the same path
	// the shipping panel takes when the user confirms session teardown).
	svc := app.panelServices()
	if svc.KillSessionCmd == nil {
		t.Fatal("KillSessionCmd should be wired by panelServices")
	}
	cmd := svc.KillSessionCmd(sess, repo)
	if cmd == nil {
		t.Fatal("KillSessionCmd returned nil for a known session")
	}

	// Executing the returned tea.Cmd is what calls into the manager.
	_ = cmd()

	if fake.killSessionCalls[sess.ID] != 1 {
		t.Errorf("KillSession call count = %d, want 1", fake.killSessionCalls[sess.ID])
	}
}

// TestSessionManager_FakeReportsListSessions covers the read-side path the
// dashboard uses on every refresh.
func TestSessionManager_FakeReportsListSessions(t *testing.T) {
	s1 := agent.NewSessionForTest("s1", "one")
	s2 := agent.NewSessionForTest("s2", "two")
	fake := newFakeManager("/r", s1, s2)

	got := fake.ListSessions()
	if len(got) != 2 {
		t.Fatalf("ListSessions returned %d sessions, want 2", len(got))
	}
	if got[0].ID != "s1" || got[1].ID != "s2" {
		t.Errorf("ListSessions order: got [%s %s], want [s1 s2]", got[0].ID, got[1].ID)
	}
	// Mutating the returned slice must not affect the manager's state.
	got[0] = nil
	if fake.ListSessions()[0] == nil {
		t.Error("ListSessions should return a defensive copy")
	}
}

// TestSessionManager_FakeUpdateSettings records pushed settings so a test
// can assert that App propagated a global-config change.
func TestSessionManager_FakeUpdateSettings(t *testing.T) {
	fake := newFakeManager("/r")
	rs := config.Resolve(nil, nil)
	fake.UpdateSettings(rs)
	if len(fake.updateSettings) != 1 {
		t.Fatalf("UpdateSettings recorded %d calls, want 1", len(fake.updateSettings))
	}
}
