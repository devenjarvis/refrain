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
// status from UserPromptSubmit before Stop fires.
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
// asserts the session-list status word transitions Active → Waiting → Idle →
// Active → Idle. This is the end-to-end check that hooks-file wiring, socket
// forwarding, agent status transitions, and list rendering all work in
// concert.
//
// Scrim fires hook events through the same settings-file mechanism as real
// Claude Code: refrain writes hooks.json, passes --settings to scrim, and
// scrim executes `refrain hook <event>` at each lifecycle point.
//
// Both inputs ("start" and "continue") are typed while in the fullscreen
// terminal; scrim buffers the second line in stdin and reads it after the
// first scenario completes.
func TestHookPipeline(t *testing.T) {
	s := newScrimSession(t, hookStartScenario, hookContinueScenario)
	s.Start()

	s.WaitForText(listAnchor, 10000)

	// Create a blank session. Scrim starts in interactive mode and fires
	// SessionStart immediately → agent marked Active.
	createBlankSession(t, s)

	// Type "start" in the terminal: scrim fires UserPromptSubmit, matches
	// hook_start, and replays two turns — turn 1 (1s delay → notification →
	// text) then turn 2 (3s delay → text → stop).
	s.Type("start\n")

	s.Press("Escape")
	s.WaitForText(listAnchor, 10000)

	// Active — SessionStart hook has fired.
	if !waitForBadgeText(s, "active", 5000) {
		t.Fatalf("never observed Active status\nScreen:\n%s", s.Screenshot())
	}

	// Waiting — Notification hook fires during first scenario replay.
	// The 3s delay on turn 2 keeps the agent in Waiting long enough to poll.
	if !waitForBadgeText(s, "waiting", 10000) {
		t.Fatalf("never observed Waiting status\nScreen:\n%s", s.Screenshot())
	}

	// Idle — Stop fires at end of first scenario.
	if !waitForBadgeText(s, "idle", 10000) {
		t.Fatalf("never observed Idle status after Stop\nScreen:\n%s", s.Screenshot())
	}

	// Re-enter the terminal from the row (enter always opens the terminal on
	// the new home screen) and send the second prompt: UserPromptSubmit
	// re-arms the status indicator. The 2s delay in hook_continue gives the
	// poll loop time to observe the Active status.
	s.Press("enter")
	s.WaitForText(launchAnchor, 10000)
	s.Type("continue\n")
	s.Press("Escape")
	s.WaitForText(listAnchor, 10000)

	if !waitForBadgeText(s, "active", 10000) {
		t.Fatalf("never observed re-armed Active status after UserPromptSubmit\nScreen:\n%s", s.Screenshot())
	}

	// Idle again — Stop fires at end of second scenario.
	if !waitForBadgeText(s, "idle", 10000) {
		t.Fatalf("never observed Idle status after second Stop\nScreen:\n%s", s.Screenshot())
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
