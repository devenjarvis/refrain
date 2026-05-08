//go:build e2e

package e2e

import (
	"path/filepath"
	"testing"
)

func TestDashboardRendersOnStartup(t *testing.T) {
	s := newSession(t)
	s.Start()

	// Wait for the dashboard to render.
	s.WaitForText("FOCUS", 10000)

	// Verify: "FOCUS" header appears in sidebar.
	s.AssertScreenContains("FOCUS")

	// Verify: status bar hints render.
	s.AssertScreenContains("navigate")
	s.AssertScreenContains("new session")
	s.AssertScreenContains("quit")

	// Verify: the repo basename appears on screen. The directory is named
	// "e2erepo-<suffix>" so it's distinctive enough that a substring match
	// can't false-positive on common UI text.
	s.AssertScreenContains(filepath.Base(s.repoDir))
}

func TestNavigationJK(t *testing.T) {
	s := newSession(t)
	s.Start()

	// Wait for the dashboard to render.
	s.WaitForText("FOCUS", 10000)

	// Create first session: press "n" which creates a session and auto-focuses terminal.
	// Wait for the status bar to show terminal-focus hints (e.g., "back" for esc).
	s.Press("n")
	s.WaitForText("back", 10000)

	// Press Escape to return to list mode. Wait for "navigate" hint in status bar.
	s.Press("Escape")
	s.WaitForText("navigate", 10000)

	// Create second session.
	s.Press("n")
	s.WaitForText("back", 10000)

	// Return to list mode.
	s.Press("Escape")
	s.WaitForText("navigate", 10000)

	// After creating 2 sessions, the selection is on the last agent (bottom of list).
	// Take a screenshot to record the initial position.
	initial := s.Screenshot()

	// Press "k" to move up to the first agent.
	s.Press("k")
	s.WaitStable(500)
	afterK := s.Screenshot()

	// Verify the screen changed (selection moved up).
	if initial == afterK {
		t.Errorf("expected screen to change after pressing k, but it did not\nScreen:\n%s", afterK)
	}

	// Press "j" to move back down to the second agent.
	s.Press("j")
	s.WaitStable(500)
	afterJ := s.Screenshot()

	// Verify the screen changed again (selection moved down).
	if afterK == afterJ {
		t.Errorf("expected screen to change after pressing j, but it did not\nScreen:\n%s", afterJ)
	}
}
