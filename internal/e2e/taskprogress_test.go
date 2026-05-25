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
var taskProgressBuildTask1 = scenarioFile{
	name: "tp_build1.yaml",
	content: `name: tp_build1
match:
  prompt: "execute the plan"
session:
  id: "e2e-tp-build"
  model: "claude-sonnet-4-6"
turns:
  - delay: "2s"
    exec: "echo 'feature A' > featureA.txt && git add featureA.txt && git commit -m '[task 1] Create feature A' && sed -i 's/- \\[ \\] Create feature A/- [x] Create feature A/' .claude/plan.md"
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
  - delay: "2s"
    exec: "echo 'feature B' > featureB.txt && git add featureB.txt && git commit -m '[task 2] Create feature B' && sed -i 's/- \\[ \\] Create feature B/- [x] Create feature B/' .claude/plan.md"
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
  - delay: "2s"
    exec: "echo 'feature C' > featureC.txt && git add featureC.txt && git commit -m '[task 3] Create feature C' && sed -i 's/- \\[ \\] Create feature C/- [x] Create feature C/' .claude/plan.md"
    assistant:
      - type: text
        text: "Completed task 3."
`,
}

// taskProgressBuildNoCheckbox scenarios create commits with [task N] prefixes
// but do NOT toggle plan checkboxes. Tests the commit-based fallback.
var taskProgressBuildNoCheckbox1 = scenarioFile{
	name: "tp_nc_build1.yaml",
	content: `name: tp_nc_build1
match:
  prompt: "execute the plan"
session:
  id: "e2e-tp-nc-build"
  model: "claude-sonnet-4-6"
turns:
  - delay: "2s"
    exec: "echo 'feature A' > featureA.txt && git add featureA.txt && git commit -m '[task 1] Create feature A'"
    assistant:
      - type: text
        text: "Done with task 1."
`,
}

var taskProgressBuildNoCheckbox2 = scenarioFile{
	name: "tp_nc_build2.yaml",
	content: `name: tp_nc_build2
match:
  prompt: "continue task 2"
session:
  id: "e2e-tp-nc-build"
  model: "claude-sonnet-4-6"
turns:
  - delay: "2s"
    exec: "echo 'feature B' > featureB.txt && git add featureB.txt && git commit -m '[task 2] Create feature B'"
    assistant:
      - type: text
        text: "Done with task 2."
`,
}

var taskProgressBuildNoCheckbox3 = scenarioFile{
	name: "tp_nc_build3.yaml",
	content: `name: tp_nc_build3
match:
  prompt: "continue task 3"
session:
  id: "e2e-tp-nc-build"
  model: "claude-sonnet-4-6"
turns:
  - delay: "2s"
    exec: "echo 'feature C' > featureC.txt && git add featureC.txt && git commit -m '[task 3] Create feature C'"
    assistant:
      - type: text
        text: "Done with task 3."
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

func allTaskProgressNoCheckboxScenarios() []scenarioFile {
	return []scenarioFile{
		taskProgressDraftScenario,
		taskProgressBuildNoCheckbox1,
		taskProgressBuildNoCheckbox2,
		taskProgressBuildNoCheckbox3,
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
// both the plan-checkbox path (mtime-detected edits to plan.md) and the
// commit-based path (RefreshCommitTaskCount on KindStop).
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
	if !waitForProgress(s, 1, 3, 25000) {
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
	if !waitForProgress(s, 2, 3, 25000) {
		t.Errorf("progress bar never showed 2/3 tasks\nScreen:\n%s", s.Screenshot())
	}

	// Task 3: all tasks complete. The session should auto-promote to
	// REVIEWING since all tasks are done.
	if !waitForBadgeText(s, "REVIEWING", 25000) {
		// If not promoted, check if 3/3 is visible in BUILDING.
		if !waitForProgress(s, 3, 3, 5000) {
			t.Errorf("progress bar never showed 3/3 tasks and session didn't promote to REVIEWING\nScreen:\n%s", s.Screenshot())
		}
	}
}

// TestTaskProgressBarCommitOnly verifies that the progress bar works even
// when the agent does not toggle plan checkboxes — relying solely on
// [task N] commit prefixes detected via RefreshCommitTaskCount.
func TestTaskProgressBarCommitOnly(t *testing.T) {
	s := newPlanningSession(t, allTaskProgressNoCheckboxScenarios()...)
	s.Start()
	s.WaitForText("FOCUS", 10000)

	submitPlanPrompt(t, s, "progress test")
	waitAndOpenEditor(t, s)

	s.Press("a")
	if !waitForBadgeText(s, "BUILDING", 15000) {
		t.Fatalf("session did not transition to BUILDING\nScreen:\n%s", s.Screenshot())
	}

	// Without checkbox toggling, only commit-based counts drive progress.
	// After each stop event, RefreshCommitTaskCount runs and updates the
	// cached count. Wait for the first task to complete before buffering.
	if !waitForProgress(s, 1, 3, 25000) {
		t.Fatalf("commit-only progress never showed 1/3 tasks\nScreen:\n%s", s.Screenshot())
	}

	// Enter focusLaunch to buffer follow-up prompts.
	s.Press("enter")
	s.WaitForText("back", 10000)
	s.Type("continue task 2\n")
	s.Type("continue task 3\n")
	s.Press("Escape")
	s.WaitForText("navigate", 10000)

	if !waitForProgress(s, 2, 3, 25000) {
		t.Errorf("commit-only progress never showed 2/3 tasks\nScreen:\n%s", s.Screenshot())
	}

	// After task 3, all commit indices are present. Session should
	// auto-promote since commitDone == commitMax == planTotal.
	if !waitForBadgeText(s, "REVIEWING", 25000) {
		if !waitForProgress(s, 3, 3, 5000) {
			t.Errorf("commit-only progress never reached 3/3 and session didn't promote\nScreen:\n%s", s.Screenshot())
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
	if !waitForProgress(s, 1, 3, 25000) {
		t.Errorf("progress bar never showed 1/3 tasks\nScreen:\n%s", s.Screenshot())
	}

	// Give auto-promotion a chance to fire (it shouldn't).
	s.WaitStable(3000)

	// Session should still be in BUILDING, NOT promoted to REVIEWING.
	s.AssertScreenContains("BUILDING")
	s.AssertScreenContains("1/3 tasks")
}
