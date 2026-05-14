//go:build e2e

package e2e

import "testing"

// TestQuitNoAgents verifies that pressing "q" with no running agents exits
// refrain immediately with exit code 0.
func TestQuitNoAgents(t *testing.T) {
	s := newSession(t)
	s.Start()

	s.WaitForText("FOCUS", 10000)

	s.Press("q")

	alive, exitCode := s.WaitForExit(5000)
	if alive {
		t.Fatalf("expected process to have exited, but it is still alive")
	}
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
}

// TestQuitConfirmation verifies the detach (q) flow when agents are running:
// first "q" shows a confirmation message, second "q" detaches and exits.
func TestQuitConfirmation(t *testing.T) {
	s := newSession(t)
	s.Start()

	s.WaitForText("FOCUS", 10000)

	// Create a new agent session (bash).
	s.Press("n")
	s.WaitForText(`\$`, 10000)

	// Return to list focus so quit key is handled at the app level.
	s.Press("Escape")
	s.WaitForText("navigate", 10000)

	// First "q" — should show confirmation banner. The banner is distinctive
	// (only rendered when confirmQuit is set); the always-present status bar
	// hints don't say "Agents are running".
	s.Press("q")
	s.WaitForText("Agents are running", 5000)

	// Second "q" — actually detach and exit.
	s.Press("q")

	alive, exitCode := s.WaitForExit(10000)
	if alive {
		t.Fatalf("expected process to have exited after detach, but it is still alive")
	}
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
}
