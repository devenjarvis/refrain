//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Plan markdown
// ---------------------------------------------------------------------------

const taskProgressPlanMD = `# Goal
Test progress meter.

## Tasks
- [ ] Create feature A
- [ ] Create feature B
- [ ] Create feature C

## Verification
- All tests pass`

// ---------------------------------------------------------------------------
// Scrim scenarios
// ---------------------------------------------------------------------------

// taskProgressDraftScenario matches the planning prompt and returns a plan
// with 3 unchecked tasks under ## Tasks.
var taskProgressDraftScenario = scenarioFile{
	name: "tp_draft.yaml",
	content: `name: tp_draft
match:
  prompt: "progress test"
session:
  id: "e2e-tp-draft"
  model: "claude-sonnet-4-6"
turns:
  - assistant:
      - type: text
        text: |
          ` + indentPlan(taskProgressPlanMD) + `
`,
}

// taskProgressBuildTask1 matches "execute the plan" (the BuildFromPlanPrompt).
// It creates a [task 1] commit and toggles the first checkbox via exec.
// The sed uses '||true' as a safety net so exec succeeds even if sed fails.
var taskProgressBuildTask1 = scenarioFile{
	name: "tp_build1.yaml",
	content: `name: tp_build1
match:
  prompt: "execute the plan"
session:
  id: "e2e-tp-build"
  model: "claude-sonnet-4-6"
turns:
  - exec: "echo featureA > featureA.txt && git add featureA.txt && git commit -m '[task 1] Create feature A'"
    assistant:
      - type: text
        text: "Completed task 1."
`,
}

// taskProgressBuildTask2 matches the follow-up prompt "continue task 2".
var taskProgressBuildTask2 = scenarioFile{
	name: "tp_build2.yaml",
	content: `name: tp_build2
match:
  prompt: "continue task 2"
session:
  id: "e2e-tp-build"
  model: "claude-sonnet-4-6"
turns:
  - exec: "echo featureB > featureB.txt && git add featureB.txt && git commit -m '[task 2] Create feature B'"
    assistant:
      - type: text
        text: "Completed task 2."
`,
}

// taskProgressBuildTask3 matches the follow-up prompt "continue task 3".
var taskProgressBuildTask3 = scenarioFile{
	name: "tp_build3.yaml",
	content: `name: tp_build3
match:
  prompt: "continue task 3"
session:
  id: "e2e-tp-build"
  model: "claude-sonnet-4-6"
turns:
  - exec: "echo featureC > featureC.txt && git add featureC.txt && git commit -m '[task 3] Create feature C'"
    assistant:
      - type: text
        text: "Completed task 3."
`,
}

func allTaskProgressScenarios() []scenarioFile {
	return []scenarioFile{
		taskProgressDraftScenario,
		taskProgressBuildTask1,
		taskProgressBuildTask2,
		taskProgressBuildTask3,
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// waitForProgress polls until the screen contains "done/total tasks" where
// done matches the expected value.
func waitForProgress(s *Session, done, total int, timeoutMs int) bool {
	needle := fmt.Sprintf("%d/%d tasks", done, total)
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for time.Now().Before(deadline) {
		if strings.Contains(s.Screenshot(), needle) {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return strings.Contains(s.Screenshot(), needle)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestTaskProgressBarUpdates verifies that the progress bar on a BUILDING
// session card updates incrementally as the agent completes tasks. Each task
// is a separate scrim scenario so a stop event fires between them, exercising
// the commit-based path (RefreshCommitTaskCount on KindStop).
//
// The scenarios only create commits (no plan checkbox toggling) so the test
// validates the commit-count fallback.
func TestTaskProgressBarUpdates(t *testing.T) {
	s := newPlanningSession(t, allTaskProgressScenarios()...)
	s.Start()
	s.WaitForText("FOCUS", 10000)

	// Submit prompt to trigger plan drafting.
	submitPlanPrompt(t, s, "progress test")

	// Wait for draft and open editor.
	waitAndOpenEditor(t, s)

	// Approve the plan → session enters BUILDING.
	s.Press("a")
	if !waitForBadgeText(s, "BUILDING", 15000) {
		t.Fatalf("session did not transition to BUILDING after approve\nScreen:\n%s", s.Screenshot())
	}

	// The first build scenario fires automatically from the BuildFromPlanPrompt.
	// Wait for the first task's progress to appear before entering focusLaunch
	// to buffer the follow-up prompts.
	if !waitForProgress(s, 1, 3, 30000) {
		t.Fatalf("progress bar never showed 1/3 tasks\nScreen:\n%s", s.Screenshot())
	}

	// Enter the agent terminal to type follow-up prompts. Press enter on
	// the building session card to open focusLaunch.
	s.Press("enter")
	s.WaitForText("back", 10000)

	// Buffer follow-up prompts for scenarios 2 and 3. Scrim reads them
	// from stdin after the current scenario completes.
	s.Type("continue task 2\n")
	s.Type("continue task 3\n")

	// Return to the dashboard to observe the progress bar.
	s.Press("Escape")
	s.WaitForText("navigate", 10000)

	// Task 2: second scenario fires after scrim reads "continue task 2".
	if !waitForProgress(s, 2, 3, 30000) {
		t.Errorf("progress bar never showed 2/3 tasks\nScreen:\n%s", s.Screenshot())
	}

	// Task 3: all tasks complete. The session should auto-promote to
	// REVIEWING since commitDone == commitMax == planTotal.
	if !waitForBadgeText(s, "REVIEWING", 30000) {
		// If not promoted, check if 3/3 is visible in BUILDING.
		if !waitForProgress(s, 3, 3, 5000) {
			t.Errorf("progress bar never showed 3/3 tasks and session didn't promote to REVIEWING\nScreen:\n%s", s.Screenshot())
		}
	}
}

// TestTaskProgressBarStaysInBuilding verifies that a session with uncompleted
// plan tasks is NOT auto-promoted to REVIEWING when the agent goes idle.
// The session should stay in BUILDING with the progress bar visible.
func TestTaskProgressBarStaysInBuilding(t *testing.T) {
	// Use only task 1 scenario — agent completes 1 of 3 tasks then stops.
	s := newPlanningSession(t, taskProgressDraftScenario, taskProgressBuildTask1)
	s.Start()
	s.WaitForText("FOCUS", 10000)

	submitPlanPrompt(t, s, "progress test")
	waitAndOpenEditor(t, s)

	s.Press("a")
	if !waitForBadgeText(s, "BUILDING", 15000) {
		t.Fatalf("session did not transition to BUILDING\nScreen:\n%s", s.Screenshot())
	}

	// Wait for task 1 to complete.
	if !waitForProgress(s, 1, 3, 30000) {
		t.Errorf("progress bar never showed 1/3 tasks\nScreen:\n%s", s.Screenshot())
	}

	// Give auto-promotion a chance to fire (it shouldn't).
	s.WaitStable(3000)

	// Session should still be in BUILDING, NOT promoted to REVIEWING.
	s.AssertScreenContains("BUILDING")
	s.AssertScreenContains("1/3 tasks")
}
