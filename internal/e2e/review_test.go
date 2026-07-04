//go:build e2e

package e2e

import "testing"

// TestReviewPlanlessBranch drives review generalization (rollback design
// §4.6 mode 2): a raw session that commits without a plan opens the review
// panel with one card per commit — titled by commit subject under a COMMITS
// header — instead of the plan-task ledger.
func TestReviewPlanlessBranch(t *testing.T) {
	s := newScrimSession(t, diffScenario)
	s.Start()

	s.WaitForText(listAnchor, 10000)

	createBlankSession(t, s)

	s.Type("go\n")
	s.WaitForText("COMMIT_DONE", 10000)

	s.Press("Escape")
	s.WaitForText(listAnchor, 5000)

	s.Press("r")
	s.WaitForText("COMMITS", 5000)
	// The commit subject from the scenario stands in for the task text.
	s.AssertScreenContains("add file")
	// No plan-task ledger on a plan-less branch.
	s.AssertScreenNotContains("PLAN TASKS")

	s.Press("Escape")
	s.WaitForText(listAnchor, 5000)
}
