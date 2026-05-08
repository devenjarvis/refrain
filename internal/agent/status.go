package agent

// Status represents the current state of an agent.
type Status int

const (
	StatusStarting Status = iota
	StatusActive
	StatusWaiting
	StatusIdle
	StatusDone
	StatusError
)

func (s Status) String() string {
	switch s {
	case StatusStarting:
		return "Starting"
	case StatusActive:
		return "Active"
	case StatusWaiting:
		return "Waiting"
	case StatusIdle:
		return "Idle"
	case StatusDone:
		return "Done"
	case StatusError:
		return "Error"
	default:
		return "Unknown"
	}
}
