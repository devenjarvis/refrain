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
)

// LifecycleDrafting is a transient pre-Planning sub-phase: a `claude -p`
// plan subprocess is running and will transition the session to
// LifecyclePlanning on completion. It is declared in a separate const
// block with an explicit negative value so any future range check
// (e.g. phase >= LifecycleComplete) classifies it as "earlier than
// Planning", matching its position in the visual pipeline. Keeping it
// outside the main iota block also preserves the 0..5 numeric ordering
// of the forward phases. Serialization is string-based (see String /
// LifecyclePhaseFromString) so the numeric value is never persisted.
const LifecycleDrafting LifecyclePhase = -1

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
