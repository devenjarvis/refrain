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

	if !waitForProgress(s, 1, 3, 30000) {
		t.Fatalf("progress bar never showed 1/3 tasks\nScreen:\n%s", s.Screenshot())
	}

	s.Press("enter")
	s.WaitForText("back", 10000)
	s.Type("continue task 2\n")
	s.Type("continue task 3\n")
	s.Press("Escape")
	s.WaitForText("navigate", 10000)

	if !waitForProgress(s, 2, 3, 30000) {
		t.Errorf("progress bar never showed 2/3 tasks\nScreen:\n%s", s.Screenshot())
	}

	if !waitForBadgeText(s, "REVIEWING", 30000) {
		if !waitForProgress(s, 3, 3, 5000) {
			t.Errorf("progress bar never showed 3/3 tasks and session didn't promote to REVIEWING\nScreen:\n%s", s.Screenshot())
		}
	}
}

func TestTaskProgressBarStaysInBuilding(t *testing.T) {
	s := newPlanningSession(t, taskProgressDraftScenario, taskProgressBuildTask1)
	s.Start()
	s.WaitForText("FOCUS", 10000)

	submitPlanPrompt(t, s, "progress test")
	waitAndOpenEditor(t, s)

	s.Press("a")
	if !waitForBadgeText(s, "BUILDING", 15000) {
		t.Fatalf("session did not transition to BUILDING\nScreen:\n%s", s.Screenshot())
	}

	if !waitForProgress(s, 1, 3, 30000) {
		t.Errorf("progress bar never showed 1/3 tasks\nScreen:\n%s", s.Screenshot())
	}

	s.WaitStable(3000)
	s.AssertScreenContains("BUILDING")
	s.AssertScreenContains("1/3 tasks")
}
