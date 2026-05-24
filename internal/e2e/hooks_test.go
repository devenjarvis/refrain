//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// hookStartScenario fires a notification mid-replay, giving the test a
// session-start → notification → stop sequence via scrim's hook pipeline.
var hookStartScenario = scenarioFile{
	name: "hook_start.yaml",
	content: `name: hook_start
match:
  prompt: "start"
session:
  id: "e2e-hook-pipeline"
  model: "claude-sonnet-4-6"
turns:
  - delay: "1s"
    notification:
      message: "Claude needs permission"
    assistant:
      - type: text
        text: "Waiting for permission..."
  - delay: "3s"
    assistant:
      - type: text
        text: "Done with first task."
`,
}

// hookContinueScenario is a clean scenario (no notification) for the re-arm
// test. The 2s delay gives the test enough polling time to observe the Active
// badge from UserPromptSubmit before Stop fires.
var hookContinueScenario = scenarioFile{
	name: "hook_continue.yaml",
	content: `name: hook_continue
match:
  prompt: "continue"
session:
  id: "e2e-hook-pipeline"
  model: "claude-sonnet-4-6"
turns:
  - delay: "2s"
    assistant:
      - type: text
        text: "Done with second task."
`,
}

// TestHookPipeline drives refrain through scrim's scenario replay engine and
// asserts the dashboard transitions Active → Waiting → Idle → Active → Idle.
// This is the end-to-end check that hooks-file wiring, socket forwarding,
// agent status transitions, and dashboard rendering all work in concert.
//
// Scrim fires hook events through the same settings-file mechanism as real
// Claude Code: refrain writes hooks.json, passes --settings to scrim, and
// scrim executes `refrain hook <event>` at each lifecycle point.
//
// Both inputs ("start" and "continue") are typed while in focusLaunch, before
// the session auto-promotes to REVIEWING. Scrim buffers the second line in
// stdin and reads it after the first scenario completes. This avoids the need
// to navigate back to the agent terminal from the REVIEWING state (where
// Enter opens the review panel, not focusLaunch).
func TestHookPipeline(t *testing.T) {
	s := newScrimSession(t, hookStartScenario, hookContinueScenario)
	s.Start()

	s.WaitForText("FOCUS", 10000)

	// Create a session. Scrim starts in interactive mode and fires
	// SessionStart immediately → agent marked Active.
	s.Press("n")
	s.WaitForText("back", 10000)

	// Type both inputs while in focusLaunch. Scrim reads "start" first,
	// fires UserPromptSubmit, matches hook_start, and replays two turns:
	// turn 1 (1s delay → notification → text) then turn 2 (3s delay → text
	// → stop). "continue" buffers in stdin. After hook_start completes,
	// scrim reads "continue", fires UserPromptSubmit (re-arm → Active),
	// matches hook_continue, and replays (2s delay → text → stop).
	s.Type("start\n")
	s.Type("continue\n")

	s.Press("Escape")
	s.WaitForText("navigate", 10000)

	// Active — SessionStart hook has fired.
	if !waitForBadgeText(s, "active", 5000) {
		t.Fatalf("never observed Active badge text\nScreen:\n%s", s.Screenshot())
	}

	// Waiting — Notification hook fires during first scenario replay.
	// The 3s delay on turn 2 keeps the agent in Waiting long enough to poll.
	if !waitForBadgeText(s, "waiting", 10000) {
		t.Fatalf("never observed Waiting badge text\nScreen:\n%s", s.Screenshot())
	}

	// Idle — Stop fires at end of first scenario. Auto-promotion moves the
	// session from BUILDING to REVIEWING.
	if !waitForBadgeText(s, "REVIEWING", 10000) {
		t.Fatalf("never observed REVIEWING section after Stop\nScreen:\n%s", s.Screenshot())
	}

	// Active again — scrim reads buffered "continue", fires UserPromptSubmit
	// which re-arms the status indicator. The 2s delay in hook_continue gives
	// enough time for the test to poll and observe the Active badge.
	if !waitForBadgeText(s, "active", 10000) {
		t.Fatalf("never observed re-armed Active badge after UserPromptSubmit\nScreen:\n%s", s.Screenshot())
	}

	// Idle again — Stop fires at end of second scenario. Session stays in
	// REVIEWING.
	if !waitForBadgeText(s, "REVIEWING", 10000) {
		t.Fatalf("never observed REVIEWING section after second Stop\nScreen:\n%s", s.Screenshot())
	}
}

// waitForBadgeText polls Screenshot until the screen contains the given
// substring, or timeoutMs elapses.
func waitForBadgeText(s *Session, needle string, timeoutMs int) bool {
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for time.Now().Before(deadline) {
		if strings.Contains(s.Screenshot(), needle) {
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return strings.Contains(s.Screenshot(), needle)
}
