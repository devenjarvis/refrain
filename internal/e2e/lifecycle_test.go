//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// emptyPipelineMarker is rendered (in the pipeline widget) when there are no
// sessions; section labels (PLANNING/BUILDING/REVIEWING/SHIPPING) only appear
// when at least one session is in that phase. Used as a "fresh dashboard"
// anchor.
const emptyPipelineMarker = "BUILDING"

// TestSessionCreation verifies that pressing "n" on the dashboard creates a
// new session, auto-focuses focusLaunch, and that pressing Escape returns to
// the pipeline where a session card is now visible.
func TestSessionCreation(t *testing.T) {
	s := newScrimSession(t)
	s.Start()

	s.WaitForText("FOCUS", 10000)

	if countSessionCards(s.Screenshot()) != 0 {
		t.Fatalf("expected 0 session cards before create\n%s", s.Screenshot())
	}

	s.Press("n")
	s.WaitForText("back", 10000)

	s.Press("Escape")
	s.WaitForText("navigate", 10000)

	if got := countSessionCards(s.Screenshot()); got < 1 {
		t.Errorf("expected at least 1 session card after create, got %d\n%s",
			got, s.Screenshot())
	}
}

// TestAgentAddition verifies that pressing "c" on a session in the pipeline
// adds a second agent to the cursor-selected session.
func TestAgentAddition(t *testing.T) {
	s := newScrimSession(t)
	s.Start()

	s.WaitForText("FOCUS", 10000)

	s.Press("n")
	s.WaitForText("back", 10000)
	s.Press("Escape")
	s.WaitForText("navigate", 10000)

	if countSessionCards(s.Screenshot()) < 1 {
		t.Fatalf("expected at least 1 session card before adding agent\n%s", s.Screenshot())
	}

	s.Press("c")
	s.WaitForText("back", 10000)
	s.Press("Escape")
	s.WaitForText("navigate", 10000)

	if got := countSessionCards(s.Screenshot()); got != 1 {
		t.Errorf("expected exactly 1 session card after adding agent, got %d\n%s",
			got, s.Screenshot())
	}
}

// TestAgentKill verifies that pressing "x" kills the cursor-selected session's
// primary agent. With one agent in the session, killing it removes the whole
// session.
func TestAgentKill(t *testing.T) {
	s := newScrimSession(t)
	s.Start()

	s.WaitForText("FOCUS", 10000)

	s.Press("n")
	s.WaitForText("back", 10000)
	s.Press("Escape")
	s.WaitForText("navigate", 10000)

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
// removes its card from the pipeline.
func TestSessionKill(t *testing.T) {
	s := newScrimSession(t)
	s.Start()

	s.WaitForText("FOCUS", 10000)

	s.Press("n")
	s.WaitForText("back", 10000)
	s.Press("Escape")
	s.WaitForText("navigate", 10000)

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

// countSessionCards counts session cards in the pipeline view. Each card has
// 4 rows; the first row begins with a stripe glyph, a single space, and then
// a non-space character (the session name). The other 3 rows begin with the
// stripe glyph plus a 3-space indent. Counting only name lines gives the
// number of cards.
func countSessionCards(screen string) int {
	count := 0
	for _, line := range strings.Split(screen, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		runes := []rune(trimmed)
		if len(runes) >= 3 && runes[0] == '▎' && runes[1] == ' ' && runes[2] != ' ' {
			count++
		}
	}
	return count
}
