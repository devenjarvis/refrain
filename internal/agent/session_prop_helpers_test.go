package agent

import (
	"fmt"

	"github.com/devenjarvis/refrain/internal/git"
	"pgregory.net/rapid"
)

type agentSpec struct {
	IsShell bool
	Status  Status
}

// genAgentSpecs generates a slice of 0..8 agent specifications with random
// shell/status combinations.
func genAgentSpecs(t *rapid.T) []agentSpec {
	n := rapid.IntRange(0, 8).Draw(t, "agent_count")
	specs := make([]agentSpec, n)
	for i := range specs {
		specs[i] = agentSpec{
			IsShell: rapid.Bool().Draw(t, fmt.Sprintf("shell_%d", i)),
			Status:  genStatus(t),
		}
	}
	return specs
}

// genNonShellSpecs generates 1..5 non-shell agent specs with random statuses.
func genNonShellSpecs(t *rapid.T) []agentSpec {
	n := rapid.IntRange(1, 5).Draw(t, "non_shell_count")
	specs := make([]agentSpec, n)
	for i := range specs {
		specs[i] = agentSpec{
			IsShell: false,
			Status:  genStatus(t),
		}
	}
	return specs
}

// buildSession creates a Session and populates it with agents matching the specs.
func buildSession(specs []agentSpec) *Session {
	s := newSession("prop-test", "prop", &git.WorktreeInfo{})
	for i, spec := range specs {
		s.AddTestAgent(fmt.Sprintf("agent-%d", i), spec.IsShell, spec.Status)
	}
	return s
}

// oracleSessionStatus re-implements the Status() priority logic as an
// independent oracle for property testing.
func oracleSessionStatus(specs []agentSpec) Status {
	hasStarting := false
	hasIdle := false
	hasError := false
	allDone := true
	nonShellCount := 0

	for _, spec := range specs {
		if spec.IsShell {
			continue
		}
		nonShellCount++
		switch spec.Status {
		case StatusActive, StatusWaiting:
			return StatusActive
		case StatusStarting:
			hasStarting = true
			allDone = false
		case StatusIdle:
			hasIdle = true
			allDone = false
		case StatusError:
			hasError = true
			allDone = false
		case StatusDone:
			// continue
		default:
			allDone = false
		}
	}

	if nonShellCount == 0 {
		return StatusIdle
	}
	if hasStarting {
		return StatusStarting
	}
	if hasIdle {
		return StatusIdle
	}
	if hasError {
		return StatusError
	}
	if allDone {
		return StatusDone
	}
	return StatusIdle
}

// oracleIsReviewable re-implements the IsReviewable() logic as an independent
// oracle for property testing.
func oracleIsReviewable(specs []agentSpec) bool {
	nonShell := 0
	for _, spec := range specs {
		if spec.IsShell {
			continue
		}
		nonShell++
		switch spec.Status {
		case StatusIdle, StatusDone, StatusError:
			// reviewable
		default:
			return false
		}
	}
	return nonShell > 0
}
