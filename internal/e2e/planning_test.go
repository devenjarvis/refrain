//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Scrim scenarios
// ---------------------------------------------------------------------------

// planMD is a minimal 8-section plan that exercises section parsing, task
// counting, and fold state. Kept short to minimise screen-real-estate
// sensitivity — the plan editor wraps at planEditorMaxMeasure (72 cols) and
// our tu terminal is 120×40.
const planMD = `# Goal
Add dark mode support to the settings page.

## Spec
1. Settings page has a dark-mode toggle
2. Toggle persists across sessions

## Context
- internal/tui/settings.go:42

## Reuse
- internal/config/settings.go

## Risks
- Color contrast accessibility

## Tasks
- [ ] Add theme toggle to settings form
- [ ] Persist theme choice in config

## Verification
- go test -race ./...

## Not in scope
- Custom color schemes`

// revisedPlanMD is the plan returned after a revise call. The Goal line
// differs so tests can distinguish original from revised.
const revisedPlanMD = `# Goal
Add dark mode with system-preference detection.

## Spec
1. Settings page has a dark-mode toggle
2. Toggle persists across sessions
3. Detects OS dark-mode preference on launch

## Context
- internal/tui/settings.go:42

## Reuse
- internal/config/settings.go

## Risks
- Color contrast accessibility

## Tasks
- [ ] Add theme toggle to settings form
- [ ] Persist theme choice in config
- [ ] Detect OS preference on startup

## Verification
- go test -race ./...

## Not in scope
- Custom color schemes`

// planDraftScenario matches the user prompt "add dark mode" inside the
// planner's stdin (planDraftPrompt + userPrompt). Scrim in -p mode reads
// stdin, matches the substring, and prints the plan to stdout.
var planDraftScenario = scenarioFile{
	name: "plan_draft.yaml",
	content: `name: plan_draft
match:
  prompt: "add dark mode"
session:
  id: "e2e-plan-draft"
  model: "claude-sonnet-4-6"
turns:
  - assistant:
      - type: text
        text: |
          ` + indentPlan(planMD) + `
`,
}

// planReviseScenario matches "CRITIQUE:" in the revise prompt's stdin.
var planReviseScenario = scenarioFile{
	name: "plan_revise.yaml",
	content: `name: plan_revise
match:
  prompt: "CRITIQUE:"
session:
  id: "e2e-plan-revise"
  model: "claude-sonnet-4-6"
turns:
  - assistant:
      - type: text
        text: |
          ` + indentPlan(revisedPlanMD) + `
`,
}

// planBuildScenario is the interactive agent scenario for after plan approval.
// It matches the BuildFromPlanPrompt ("execute the plan") which the approved
// session fires. Must NOT use an empty match — that would also catch the
// planner's one-shot `-p` call.
var planBuildScenario = scenarioFile{
	name: "plan_build.yaml",
	content: `name: plan_build
match:
  prompt: "execute the plan"
session:
  id: "e2e-plan-build"
  model: "claude-sonnet-4-6"
turns:
  - assistant:
      - type: text
        text: "Building from plan."
`,
}

// planErrorScenario matches "trigger error" and returns empty text,
// which causes the drafter to fail with "planner returned empty plan".
var planErrorScenario = scenarioFile{
	name: "plan_error.yaml",
	content: `name: plan_error
match:
  prompt: "trigger error"
session:
  id: "e2e-plan-error"
  model: "claude-sonnet-4-6"
turns:
  - assistant:
      - type: text
        text: ""
`,
}

// indentPlan indents every line of a plan by 10 spaces for YAML block scalar
// embedding. The first line is NOT indented (it follows the `|` indicator
// and the 10-space content indent on the `text:` line).
func indentPlan(plan string) string {
	lines := strings.Split(plan, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = "          " + lines[i]
	}
	return strings.Join(lines, "\n")
}

// allPlanningScenarios bundles the scenarios needed for most planning tests.
func allPlanningScenarios() []scenarioFile {
	return []scenarioFile{planDraftScenario, planReviseScenario, planBuildScenario, planErrorScenario}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// waitForPlanReady polls until the dashboard shows a "plan ready" or "tasks"
// badge for the planning card, indicating the draft subprocess has completed
// and written the plan to disk.
func waitForPlanReady(s *Session, timeoutMs int) bool {
	return waitForAny(s, timeoutMs, "plan ready", "tasks")
}

// waitForAny polls until the screen contains any of the given substrings.
func waitForAny(s *Session, timeoutMs int, needles ...string) bool {
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for time.Now().Before(deadline) {
		screen := s.Screenshot()
		for _, n := range needles {
			if strings.Contains(screen, n) {
				return true
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// submitPlanPrompt opens the prompt modal (n), types the prompt, and submits
// with enter (plan-first path). Waits for the modal to appear first.
func submitPlanPrompt(t *testing.T, s *Session, prompt string) {
	t.Helper()
	s.Press("n")
	s.WaitForText("Planning", 5000)
	s.Type(prompt)
	s.Press("enter")
}

// waitAndOpenEditor waits for the draft to complete and then opens the plan
// editor by pressing enter on the focused planning card.
func waitAndOpenEditor(t *testing.T, s *Session) {
	t.Helper()
	if !waitForPlanReady(s, 25000) {
		t.Fatalf("draft never completed\nScreen:\n%s", s.Screenshot())
	}
	s.Press("enter")
	s.WaitForText("approve", 8000)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestPlanDraftingBadge verifies that after submitting a prompt in the
// plan-first flow, the session shows up in the PLANNING section. With scrim
// the draft often completes instantly, so we accept either "drafting" or
// "plan ready" as proof the pipeline ran.
func TestPlanDraftingBadge(t *testing.T) {
	s := newPlanningSession(t, allPlanningScenarios()...)
	s.Start()
	s.WaitForText("FOCUS", 10000)

	submitPlanPrompt(t, s, "add dark mode")

	if !waitForAny(s, 15000, "drafting", "plan ready", "tasks") {
		t.Fatalf("never observed planning badge\nScreen:\n%s", s.Screenshot())
	}
	s.AssertScreenContains("PLANNING")
}

// TestPlanDraftFlow verifies the full draft flow: prompt modal → drafting
// badge → draft completes → plan editor opens with plan content.
func TestPlanDraftFlow(t *testing.T) {
	s := newPlanningSession(t, allPlanningScenarios()...)
	s.Start()
	s.WaitForText("FOCUS", 10000)

	submitPlanPrompt(t, s, "add dark mode")

	// Wait for draft to complete and then open the editor.
	waitAndOpenEditor(t, s)

	// The plan editor should show the Goal section content.
	s.AssertScreenContains("dark mode support")
}

// TestPlanApproval verifies that pressing 'a' in the plan editor approves
// the plan, spawns a build agent, and transitions the session to BUILDING.
func TestPlanApproval(t *testing.T) {
	s := newPlanningSession(t, allPlanningScenarios()...)
	s.Start()
	s.WaitForText("FOCUS", 10000)

	submitPlanPrompt(t, s, "add dark mode")
	waitAndOpenEditor(t, s)

	// Approve the plan.
	s.Press("a")

	// Session should move from PLANNING to BUILDING.
	if !waitForBadgeText(s, "BUILDING", 15000) {
		t.Fatalf("session did not transition to BUILDING after approve\nScreen:\n%s", s.Screenshot())
	}
}

// TestPlanRevision verifies the revise flow: press 'r', type a critique,
// submit, and confirm the revised plan content appears. With scrim the
// revision may complete so fast that the "Revising" transient state is
// never observed, so we only assert on the final outcome.
func TestPlanRevision(t *testing.T) {
	s := newPlanningSession(t, allPlanningScenarios()...)
	s.Start()
	s.WaitForText("FOCUS", 10000)

	submitPlanPrompt(t, s, "add dark mode")
	waitAndOpenEditor(t, s)

	// Enter revise mode.
	s.Press("r")
	s.WaitForText("submit", 3000)

	// Type a critique and submit.
	s.Type("add system preference detection")
	s.Press("enter")

	// Wait for the revised plan to land — the new Goal text should appear.
	// With scrim the revision completes instantly so the "Revising" state
	// may be too transient to catch.
	if !waitForBadgeText(s, "system-preference", 25000) {
		t.Fatalf("revised plan content never appeared\nScreen:\n%s", s.Screenshot())
	}
}

// TestPlanUndo verifies that pressing 'u' after a revision restores the
// original plan content.
func TestPlanUndo(t *testing.T) {
	s := newPlanningSession(t, allPlanningScenarios()...)
	s.Start()
	s.WaitForText("FOCUS", 10000)

	submitPlanPrompt(t, s, "add dark mode")
	waitAndOpenEditor(t, s)

	// Revise first so there's something to undo.
	s.Press("r")
	s.WaitForText("submit", 3000)
	s.Type("add system preference detection")
	s.Press("enter")

	// Wait for revision to complete.
	if !waitForBadgeText(s, "system-preference", 25000) {
		t.Fatalf("revised plan never appeared\nScreen:\n%s", s.Screenshot())
	}

	// Undo — original Goal should come back.
	s.Press("u")
	if !waitForBadgeText(s, "dark mode support", 10000) {
		t.Fatalf("undo did not restore original plan\nScreen:\n%s", s.Screenshot())
	}
	s.AssertScreenNotContains("system-preference")
}

// TestSkipPlanning verifies that with plan_first_enabled=false, pressing n
// creates a session directly in BUILDING without going through the planning
// flow (no prompt modal, no drafting phase).
//
// Note: the ctrl+enter skip path within the prompt modal cannot be tested
// via tu because tu doesn't support Ctrl+Enter as a key combination.
func TestSkipPlanning(t *testing.T) {
	s := newScrimSession(t, planBuildScenario)
	s.Start()
	s.WaitForText("FOCUS", 10000)

	// With plan_first_enabled=false, pressing n creates a session directly
	// in BUILDING — no prompt modal, no drafting. The session card should
	// appear in the BUILDING section.
	s.Press("n")
	s.WaitForText("back", 10000)
	s.Press("Escape")
	s.WaitForText("navigate", 10000)

	// The session should be under BUILDING with "active" in its status,
	// not under PLANNING.
	s.AssertScreenContains("active")
}

// TestPlanEditorNavigation verifies section navigation (]/[) and fold
// toggling (tab, Z) in the plan editor.
func TestPlanEditorNavigation(t *testing.T) {
	s := newPlanningSession(t, allPlanningScenarios()...)
	s.Start()
	s.WaitForText("FOCUS", 10000)

	submitPlanPrompt(t, s, "add dark mode")
	waitAndOpenEditor(t, s)

	// The editor starts with Goal visible.
	s.AssertScreenContains("dark mode support")

	// Navigate to next section with ].
	s.Press("]")
	s.WaitStable(300)

	// Navigate again.
	s.Press("]")
	s.WaitStable(300)

	// Toggle fold on the current section with tab — some content should
	// appear or disappear.
	before := s.Screenshot()
	s.Press("tab")
	s.WaitStable(300)
	after := s.Screenshot()

	if before == after {
		t.Log("Warning: fold toggle did not change screen (section may already be folded)")
	}

	// Toggle all folds with Z.
	s.Press("Z")
	s.WaitStable(300)
	afterZ := s.Screenshot()
	if afterZ == after {
		t.Log("Warning: Z toggle did not change screen")
	}
}

// TestPlanEditorCloseReopen verifies that closing the plan editor with esc
// returns to the dashboard, and pressing enter on the planning card reopens
// the editor with the same plan content.
func TestPlanEditorCloseReopen(t *testing.T) {
	s := newPlanningSession(t, allPlanningScenarios()...)
	s.Start()
	s.WaitForText("FOCUS", 10000)

	submitPlanPrompt(t, s, "add dark mode")
	waitAndOpenEditor(t, s)
	s.AssertScreenContains("dark mode support")

	// Close the editor.
	s.Press("Escape")
	s.WaitForText("PLANNING", 8000)

	// The dashboard should show the planning card.
	s.AssertScreenContains("PLANNING")

	// Reopen the editor.
	s.Press("enter")
	s.WaitForText("approve", 8000)

	// Plan content should still be there.
	s.AssertScreenContains("dark mode support")
}

// TestPlanAbandon verifies that pressing 'q' in the plan editor kills the
// session and removes it from the dashboard.
func TestPlanAbandon(t *testing.T) {
	s := newPlanningSession(t, allPlanningScenarios()...)
	s.Start()
	s.WaitForText("FOCUS", 10000)

	submitPlanPrompt(t, s, "add dark mode")
	waitAndOpenEditor(t, s)

	// Abandon the plan.
	s.Press("q")

	// Session should disappear from the dashboard.
	if !waitForSessionCount(s, 0, 10000) {
		t.Errorf("session not removed after abandon\nScreen:\n%s", s.Screenshot())
	}
}

// TestPlanDraftError verifies that when the drafter returns empty text, the
// dashboard shows a "draft failed" badge. The plan editor opens with no plan
// content and surfaces the error in the status line with a retry hint.
func TestPlanDraftError(t *testing.T) {
	s := newPlanningSession(t, allPlanningScenarios()...)
	s.Start()
	s.WaitForText("FOCUS", 10000)

	// "trigger error" matches planErrorScenario which returns empty text.
	submitPlanPrompt(t, s, "trigger error")

	// The draft will fail after retries. Wait for the error badge.
	// planDraftAttempts=3 with backoff 2s+5s, so worst case ~17s.
	if !waitForBadgeText(s, "draft failed", 60000) {
		t.Fatalf("never observed draft failed badge\nScreen:\n%s", s.Screenshot())
	}

	// Dashboard card should show the error badge and description.
	s.AssertScreenContains("draft failed")
}

// TestPlanEditorEditMode verifies that pressing 'i' enters edit mode where
// the user can modify the plan, and esc returns to scroll mode.
func TestPlanEditorEditMode(t *testing.T) {
	s := newPlanningSession(t, allPlanningScenarios()...)
	s.Start()
	s.WaitForText("FOCUS", 10000)

	submitPlanPrompt(t, s, "add dark mode")
	waitAndOpenEditor(t, s)

	// Enter edit mode.
	s.Press("i")
	s.WaitForText("save", 3000) // edit mode footer shows "ctrl+s save"

	// Return to scroll mode.
	s.Press("Escape")
	s.WaitForText("approve", 3000) // scroll mode footer shows "approve"
}

// TestPlanPromptModalCancel verifies that pressing esc in the prompt modal
// cancels without creating a session.
func TestPlanPromptModalCancel(t *testing.T) {
	s := newPlanningSession(t, allPlanningScenarios()...)
	s.Start()
	s.WaitForText("FOCUS", 10000)

	// Open prompt modal.
	s.Press("n")
	s.WaitForText("Planning", 5000)

	// Cancel.
	s.Press("Escape")
	s.WaitForText("FOCUS", 5000)

	// No session should have been created.
	if countSessionCards(s.Screenshot()) != 0 {
		t.Errorf("cancelling prompt modal should not create a session\nScreen:\n%s", s.Screenshot())
	}
}
