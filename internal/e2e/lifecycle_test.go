//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// listAnchor appears only in the session-list status bar — no overlay's hint
// set includes the PR action.
const listAnchor = "p PR"

// newSessionAnchor is the full-viewport composition screen's header.
const newSessionAnchor = "NEW SESSION"

// launchAnchor appears in the focusLaunch status bar ("esc back").
const launchAnchor = "back"

// createBlankSession drives the raw new-session flow: n opens the composition
// screen, an empty enter spawns a blank claude REPL, and the app lands in the
// fullscreen agent terminal.
func createBlankSession(t *testing.T, s *Session) {
	t.Helper()
	s.Press("n")
	s.WaitForText(newSessionAnchor, 10000)
	s.Press("enter")
	s.WaitForText(launchAnchor, 10000)
}

// TestSessionCreation verifies the raw new-session flow: n opens the
// composition screen, empty enter creates a blank session and auto-focuses
// the terminal, and Escape returns to the list where a session card is now
// visible.
func TestSessionCreation(t *testing.T) {
	s := newScrimSession(t)
	s.Start()

	s.WaitForText(listAnchor, 10000)

	if countSessionCards(s.Screenshot()) != 0 {
		t.Fatalf("expected 0 session cards before create\n%s", s.Screenshot())
	}

	createBlankSession(t, s)

	s.Press("Escape")
	s.WaitForText(listAnchor, 10000)

	if got := countSessionCards(s.Screenshot()); got < 1 {
		t.Errorf("expected at least 1 session card after create, got %d\n%s",
			got, s.Screenshot())
	}
}

// TestCheckoutSessionCreation verifies the Context toggle: selecting "Current
// checkout" in the new-session form creates a session in the repo's main
// working tree, rendered with the distinct checkout tag.
func TestCheckoutSessionCreation(t *testing.T) {
	s := newScrimSession(t)
	s.Start()

	s.WaitForText(listAnchor, 10000)

	s.Press("n")
	s.WaitForText(newSessionAnchor, 10000)

	// Tab to the Context row, cycle to "Current checkout", tab through the
	// remaining override rows back to the textarea, and submit empty (a
	// blank REPL in the real working tree).
	s.Press("Tab")
	s.WaitForText("CONTEXT", 5000)
	s.Press("Right")
	s.WaitForText("Current checkout", 5000)
	s.Press("Tab", "Tab", "Tab", "Tab")
	s.Press("enter")
	s.WaitForText(launchAnchor, 10000)

	// The launch header flags the checkout session.
	s.AssertScreenContains("checkout session")

	s.Press("Escape")
	s.WaitForText(listAnchor, 10000)

	// The card's context tag reads "checkout @ <branch>", not "worktree".
	s.AssertScreenContains("checkout @")
	s.AssertScreenNotContains("· worktree")
}

// TestAgentAddition verifies that pressing "c" on a session in the list adds
// a second agent to the cursor-selected session.
func TestAgentAddition(t *testing.T) {
	s := newScrimSession(t)
	s.Start()

	s.WaitForText(listAnchor, 10000)

	createBlankSession(t, s)
	s.Press("Escape")
	s.WaitForText(listAnchor, 10000)

	if countSessionCards(s.Screenshot()) < 1 {
		t.Fatalf("expected at least 1 session card before adding agent\n%s", s.Screenshot())
	}

	s.Press("c")
	s.WaitForText(launchAnchor, 10000)
	s.Press("Escape")
	s.WaitForText(listAnchor, 10000)

	if got := countSessionCards(s.Screenshot()); got != 1 {
		t.Errorf("expected exactly 1 session card after adding agent, got %d\n%s",
			got, s.Screenshot())
	}
	s.AssertScreenContains("2 agents")
}

// TestAgentKill verifies that pressing "x" kills the cursor-selected session's
// primary agent. With one agent in the session, killing it removes the whole
// session.
func TestAgentKill(t *testing.T) {
	s := newScrimSession(t)
	s.Start()

	s.WaitForText(listAnchor, 10000)

	createBlankSession(t, s)
	s.Press("Escape")
	s.WaitForText(listAnchor, 10000)

	if countSessionCards(s.Screenshot()) == 0 {
		t.Fatalf("expected session card before kill, got none\n%s", s.Screenshot())
	}

	s.Press("x")

	if !waitForSessionCount(s, 0, 10000) {
		t.Errorf("expected 0 session cards after kill, got %d\n%s",
			countSessionCards(s.Screenshot()), s.Screenshot())
	}
}

// TestSessionKill verifies that pressing "X" kills the entire session and
// removes its card from the list.
func TestSessionKill(t *testing.T) {
	s := newScrimSession(t)
	s.Start()

	s.WaitForText(listAnchor, 10000)

	createBlankSession(t, s)
	s.Press("Escape")
	s.WaitForText(listAnchor, 10000)

	if countSessionCards(s.Screenshot()) == 0 {
		t.Fatalf("expected session card before session kill, got 0\n%s", s.Screenshot())
	}

	s.Press("X")

	if !waitForSessionCount(s, 0, 10000) {
		t.Errorf("expected 0 session cards after session kill, got %d\n%s",
			countSessionCards(s.Screenshot()), s.Screenshot())
	}
}

// waitForSessionCount polls until the session card count matches `want`
// or the timeout elapses.
func waitForSessionCount(s *Session, want, timeoutMs int) bool {
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for time.Now().Before(deadline) {
		if countSessionCards(s.Screenshot()) == want {
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return countSessionCards(s.Screenshot()) == want
}

// countSessionCards counts session cards in the list view. Each card is 2
// lines; the second line always carries the context tag — "· worktree" for
// worktree sessions or "checkout @" for checkout sessions.
func countSessionCards(screen string) int {
	count := 0
	for _, line := range strings.Split(screen, "\n") {
		if strings.Contains(line, "· worktree") || strings.Contains(line, "checkout @") {
			count++
		}
	}
	return count
}
