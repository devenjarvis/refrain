//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const taskProgressPlanMD = `# Goal
Test progress meter.

## Tasks
- [ ] Create feature A
- [ ] Create feature B
- [ ] Create feature C

## Verification
- All tests pass`

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

// taskProgressBranchNameScenario absorbs the background haiku branch-namer
// subprocess that fires on the first UserPromptSubmit. The DefaultBranchNamePrompt
// contains "Respond with ONLY the slug" which is unique to the haiku instruction
// and not present in any build or draft prompt. Returning empty text causes the
// namer to see an empty slug (ErrEmptySlug) and skip the rename, which eliminates
// the race between "git branch -m" and "git commit" in the exec scenario.
var taskProgressBranchNameScenario = scenarioFile{
	name: "tp_branchname.yaml",
	content: `name: tp_branchname
match:
  prompt: "Respond with ONLY the slug"
session:
  id: "e2e-tp-branchname"
  model: "claude-haiku-4-5-20251001"
turns:
  - assistant:
      - type: text
        text: ""
`,
}

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
		taskProgressBranchNameScenario,
		taskProgressBuildTask1,
		taskProgressBuildTask2,
		taskProgressBuildTask3,
	}
}

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

func TestTaskProgressBarUpdates(t *testing.T) {
	s := newPlanningSession(t, allTaskProgressScenarios()...)
	s.Start()
	s.WaitForText("FOCUS", 10000)

	submitPlanPrompt(t, s, "progress test")
	waitAndOpenEditor(t, s)

	s.Press("a")
	if !waitForBadgeText(s, "BUILDING", 15000) {
		t.Fatalf("session did not transition to BUILDING after approve\nScreen:\n%s", s.Screenshot())
	}

	// Open the agent terminal and send the build prompt to trigger task 1.
	// scrim starts in interactive mode and waits for prompts via stdin.
	s.Press("enter")
	s.WaitForText("back", 10000)
	s.Type("execute the plan\n")
	s.WaitForText("Completed task 1", 15000)
	s.Press("Escape")
	s.WaitForText("navigate", 10000)

	if !waitForProgress(s, 1, 3, 30000) {
		t.Fatalf("progress bar never showed 1/3 tasks\nScreen:\n%s", s.Screenshot())
	}

	// Send task 2, escape back to dashboard to observe 2/3 progress.
	s.Press("enter")
	s.WaitForText("back", 10000)
	s.Type("continue task 2\n")
	s.WaitForText("Completed task 2", 15000)
	s.Press("Escape")
	s.WaitForText("navigate", 10000)

	if !waitForProgress(s, 2, 3, 30000) {
		t.Errorf("progress bar never showed 2/3 tasks\nScreen:\n%s", s.Screenshot())
	}

	// Send task 3, then wait for either 3/3 tasks or auto-promotion to REVIEWING.
	s.Press("enter")
	s.WaitForText("back", 10000)
	s.Type("continue task 3\n")
	s.Press("Escape")
	s.WaitForText("navigate", 10000)

	if !waitForBadgeText(s, "REVIEWING", 30000) {
		if !waitForProgress(s, 3, 3, 5000) {
			t.Errorf("progress bar never showed 3/3 tasks and session didn't promote to REVIEWING\nScreen:\n%s", s.Screenshot())
		}
	}
}

func TestTaskProgressBarStaysInBuilding(t *testing.T) {
	s := newPlanningSession(t, taskProgressDraftScenario, taskProgressBranchNameScenario, taskProgressBuildTask1)
	s.Start()
	s.WaitForText("FOCUS", 10000)

	submitPlanPrompt(t, s, "progress test")
	waitAndOpenEditor(t, s)

	s.Press("a")
	if !waitForBadgeText(s, "BUILDING", 15000) {
		t.Fatalf("session did not transition to BUILDING\nScreen:\n%s", s.Screenshot())
	}

	// Open the agent terminal and trigger task 1.
	s.Press("enter")
	s.WaitForText("back", 10000)
	s.Type("execute the plan\n")
	s.WaitForText("Completed task 1", 15000)
	s.Press("Escape")
	s.WaitForText("navigate", 10000)

	if !waitForProgress(s, 1, 3, 30000) {
		t.Errorf("progress bar never showed 1/3 tasks\nScreen:\n%s", s.Screenshot())
	}

	s.WaitStable(3000)
	s.AssertScreenContains("BUILDING")
	s.AssertScreenContains("1/3 tasks")
}
