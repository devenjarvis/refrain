package agent

import (
	"os/exec"
	"testing"
	"time"
)

func TestReconcileExternalBranchRename(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	cfg := Config{Task: "test", Rows: 24, Cols: 80}
	sess, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 5")
	})
	if err != nil {
		t.Fatal(err)
	}

	origBranch := sess.Branch()
	newBranch := origBranch + "-renamed"

	mgr.ReconcileExternalBranchRename(sess.ID, newBranch)

	if got := sess.Branch(); got != newBranch {
		t.Errorf("sess.Branch() = %q, want %q", got, newBranch)
	}

	// Drain events until we see EventBranchRenamed or timeout.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-mgr.Events():
			if ev.Type == EventBranchRenamed && ev.SessionID == sess.ID && ev.Branch == newBranch {
				return // success
			}
		case <-deadline:
			t.Error("timed out waiting for EventBranchRenamed")
			return
		}
	}
}

func TestReconcileExternalBranchRename_UnknownSession(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	// Should not panic on unknown session ID.
	mgr.ReconcileExternalBranchRename("nonexistent-id", "refrain/new-branch")
}
