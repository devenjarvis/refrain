package agent

import (
	"pgregory.net/rapid"
)

// genLifecyclePhase draws uniformly from all 7 lifecycle phases, including
// the transient LifecycleDrafting (-1).
func genLifecyclePhase(t *rapid.T) LifecyclePhase {
	v := rapid.IntRange(-1, 5).Draw(t, "phase")
	return LifecyclePhase(v)
}

// genForwardLifecyclePhase draws from the 6 forward (persistable) phases only.
func genForwardLifecyclePhase(t *rapid.T) LifecyclePhase {
	v := rapid.IntRange(0, 5).Draw(t, "forward_phase")
	return LifecyclePhase(v)
}

// genStatus draws uniformly from all 6 agent statuses.
func genStatus(t *rapid.T) Status {
	v := rapid.IntRange(0, 5).Draw(t, "status")
	return Status(v)
}

// genReviewableStatus draws from the 3 statuses that are considered reviewable.
func genReviewableStatus(t *rapid.T) Status {
	return rapid.SampledFrom([]Status{StatusIdle, StatusDone, StatusError}).Draw(t, "reviewable_status")
}

// genNonReviewableStatus draws from the 3 statuses that block reviewability.
func genNonReviewableStatus(t *rapid.T) Status {
	return rapid.SampledFrom([]Status{StatusStarting, StatusActive, StatusWaiting}).Draw(t, "non_reviewable_status")
}

// genLifecycleString generates strings that exercise LifecyclePhaseFromString:
// valid phase strings, "drafting", empty, near-misses, and arbitrary garbage.
func genLifecycleString(t *rapid.T) string {
	kind := rapid.IntRange(0, 3).Draw(t, "kind")
	switch kind {
	case 0:
		return rapid.SampledFrom([]string{
			"planning", "in_progress", "ready_for_review",
			"in_review", "shipping", "complete", "drafting",
		}).Draw(t, "valid")
	case 1:
		return rapid.SampledFrom([]string{
			"", "Planning", "IN_PROGRESS", "complete ",
			" shipping", "DRAFTING", "in-progress",
		}).Draw(t, "near_miss")
	case 2:
		return rapid.String().Draw(t, "garbage")
	default:
		return ""
	}
}
