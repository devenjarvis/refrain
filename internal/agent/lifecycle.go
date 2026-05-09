package agent

// LifecyclePhase describes where the developer is in the workflow for a session.
// It is orthogonal to agent.Status, which describes the agent process state.
type LifecyclePhase int

const (
	LifecyclePlanning       LifecyclePhase = iota // plan exists, user editing/approving
	LifecycleInProgress                           // Building: agent running or done, not yet marked ready
	LifecycleReadyForReview                       // developer marked it ready
	LifecycleInReview                             // developer committed to reviewing
	LifecycleShipping                             // PR open, waiting for CI/team
	LifecycleComplete                             // PR merged or manually marked done
	LifecycleDrafting                             // claude -p plan subprocess is running; transitions to Planning on completion
)

func (p LifecyclePhase) String() string {
	switch p {
	case LifecyclePlanning:
		return "planning"
	case LifecycleInProgress:
		return "in_progress"
	case LifecycleReadyForReview:
		return "ready_for_review"
	case LifecycleInReview:
		return "in_review"
	case LifecycleShipping:
		return "shipping"
	case LifecycleComplete:
		return "complete"
	case LifecycleDrafting:
		return "drafting"
	default:
		return "in_progress"
	}
}

// LifecyclePhaseFromString parses a string produced by String(). Unknown/empty → InProgress.
// "drafting" is treated as Planning on restore: a draft cannot survive a
// detach (the subprocess is gone), so the user lands in Planning where they
// can edit the partial plan or restart the draft.
func LifecyclePhaseFromString(s string) LifecyclePhase {
	switch s {
	case "planning":
		return LifecyclePlanning
	case "drafting":
		return LifecyclePlanning
	case "in_progress":
		return LifecycleInProgress
	case "ready_for_review":
		return LifecycleReadyForReview
	case "in_review":
		return LifecycleInReview
	case "shipping":
		return LifecycleShipping
	case "complete":
		return LifecycleComplete
	default:
		return LifecycleInProgress
	}
}
