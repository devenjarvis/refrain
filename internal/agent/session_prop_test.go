package agent

import (
	"fmt"
	"sync"
	"testing"

	"github.com/devenjarvis/refrain/internal/git"
	"pgregory.net/rapid"
)

var validStatuses = map[Status]bool{
	StatusStarting: true,
	StatusActive:   true,
	StatusWaiting:  true,
	StatusIdle:     true,
	StatusDone:     true,
	StatusError:    true,
}

// Status() always returns one of the 6 defined Status constants.
func TestSession_StatusAlwaysValid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		specs := genAgentSpecs(t)
		s := buildSession(specs)
		got := s.Status()
		if !validStatuses[got] {
			t.Fatalf("Status() = %v, not a valid Status constant", got)
		}
	})
}

// If any non-shell agent is Active or Waiting, session status is Active.
func TestSession_StatusActiveWhenAnyNonShellActiveOrWaiting(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		specs := genAgentSpecs(t)
		hotStatus := rapid.SampledFrom([]Status{StatusActive, StatusWaiting}).Draw(t, "hot")
		specs = append(specs, agentSpec{IsShell: false, Status: hotStatus})
		s := buildSession(specs)
		if got := s.Status(); got != StatusActive {
			t.Fatalf("Status() = %v, want Active (has non-shell %v agent)", got, hotStatus)
		}
	})
}

// With no non-shell agents (0 agents or all shells), Status() is Idle.
func TestSession_StatusIdleWhenNoNonShellAgents(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 5).Draw(t, "shell_count")
		specs := make([]agentSpec, n)
		for i := range specs {
			specs[i] = agentSpec{IsShell: true, Status: genStatus(t)}
		}
		s := buildSession(specs)
		if got := s.Status(); got != StatusIdle {
			t.Fatalf("Status() = %v, want Idle (no non-shell agents)", got)
		}
	})
}

// When all non-shell agents are Done (and there is at least one), Status() is Done.
func TestSession_StatusDoneWhenAllNonShellDone(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		nonShellCount := rapid.IntRange(1, 5).Draw(t, "non_shell_count")
		shellCount := rapid.IntRange(0, 3).Draw(t, "shell_count")
		specs := make([]agentSpec, 0, nonShellCount+shellCount)
		for range nonShellCount {
			specs = append(specs, agentSpec{IsShell: false, Status: StatusDone})
		}
		for range shellCount {
			specs = append(specs, agentSpec{IsShell: true, Status: genStatus(t)})
		}
		s := buildSession(specs)
		if got := s.Status(); got != StatusDone {
			t.Fatalf("Status() = %v, want Done (all non-shell agents Done)", got)
		}
	})
}

// Model-based: oracle re-derives expected Status() from agent specs.
func TestSession_StatusPriorityOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		specs := genAgentSpecs(t)
		s := buildSession(specs)
		got := s.Status()
		want := oracleSessionStatus(specs)
		if got != want {
			t.Fatalf("Status() = %v, oracle says %v; specs=%v", got, want, specs)
		}
	})
}

// Oracle-based equivalence for IsReviewable().
func TestSession_IsReviewableEquivalence(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		specs := genAgentSpecs(t)
		s := buildSession(specs)
		got := s.IsReviewable()
		want := oracleIsReviewable(specs)
		if got != want {
			t.Fatalf("IsReviewable() = %v, oracle says %v; specs=%v", got, want, specs)
		}
	})
}

// Adding/removing shell agents never changes IsReviewable().
func TestSession_IsReviewableIndependentOfShell(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		specs := genNonShellSpecs(t)
		s1 := buildSession(specs)
		base := s1.IsReviewable()

		shellStatus := genStatus(t)
		withShell := append(append([]agentSpec{}, specs...), agentSpec{IsShell: true, Status: shellStatus})
		s2 := buildSession(withShell)
		got := s2.IsReviewable()
		if got != base {
			t.Fatalf("IsReviewable changed from %v to %v after adding shell (status=%v)", base, got, shellStatus)
		}
	})
}

// Sessions with no non-shell agents are never reviewable.
func TestSession_IsReviewableFalseIfNoNonShellAgents(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 5).Draw(t, "shell_count")
		specs := make([]agentSpec, n)
		for i := range specs {
			specs[i] = agentSpec{IsShell: true, Status: genStatus(t)}
		}
		s := buildSession(specs)
		if s.IsReviewable() {
			t.Fatalf("IsReviewable() = true with no non-shell agents")
		}
	})
}

// Any Active/Waiting/Starting non-shell agent blocks reviewability.
func TestSession_IsReviewableFalseIfAnyNonShellBlocking(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 4).Draw(t, "extra_count")
		specs := make([]agentSpec, 0, n+1)
		for range n {
			specs = append(specs, agentSpec{IsShell: false, Status: genReviewableStatus(t)})
		}
		blocker := genNonReviewableStatus(t)
		specs = append(specs, agentSpec{IsShell: false, Status: blocker})
		s := buildSession(specs)
		if s.IsReviewable() {
			t.Fatalf("IsReviewable() = true with blocking agent (status=%v)", blocker)
		}
	})
}

// All non-shell agents in {Idle, Done, Error} with at least one → reviewable.
func TestSession_IsReviewableTrueWhenAllSettled(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 5).Draw(t, "count")
		shellCount := rapid.IntRange(0, 3).Draw(t, "shell_count")
		specs := make([]agentSpec, 0, n+shellCount)
		for range n {
			specs = append(specs, agentSpec{IsShell: false, Status: genReviewableStatus(t)})
		}
		for range shellCount {
			specs = append(specs, agentSpec{IsShell: true, Status: genStatus(t)})
		}
		s := buildSession(specs)
		if !s.IsReviewable() {
			t.Fatalf("IsReviewable() = false, but all non-shell agents are settled; specs=%v", specs)
		}
	})
}

// SetLifecyclePhase/LifecyclePhase round-trips for any phase including Drafting.
func TestSession_SetLifecycleRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		p := genLifecyclePhase(t)
		s := newSession("id", "name", &git.WorktreeInfo{})
		s.SetLifecyclePhase(p)
		if got := s.LifecyclePhase(); got != p {
			t.Fatalf("LifecyclePhase() = %v after Set(%v)", got, p)
		}
	})
}

// MarkDone is idempotent: the timestamp never changes after the first call.
func TestSession_MarkDoneIdempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 10).Draw(t, "call_count")
		s := newSession("id", "name", &git.WorktreeInfo{})
		s.MarkDone()
		first := s.DoneAt()
		if first.IsZero() {
			t.Fatal("DoneAt() is zero after MarkDone()")
		}
		for range n {
			s.MarkDone()
			if got := s.DoneAt(); got != first {
				t.Fatalf("DoneAt() changed from %v to %v on subsequent MarkDone()", first, got)
			}
		}
	})
}

// SetOriginalPrompt only stores the first non-empty value.
func TestSession_OriginalPromptSetOnce(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 5).Draw(t, "prompt_count")
		prompts := make([]string, n)
		for i := range prompts {
			prompts[i] = rapid.String().Draw(t, fmt.Sprintf("prompt_%d", i))
		}

		s := newSession("id", "name", &git.WorktreeInfo{})
		for _, p := range prompts {
			s.SetOriginalPrompt(p)
		}

		got := s.OriginalPrompt()

		// The stored value should be the first non-empty prompt, or empty
		// if all prompts were empty.
		var want string
		for _, p := range prompts {
			if p != "" {
				want = p
				break
			}
		}
		if got != want {
			t.Fatalf("OriginalPrompt() = %q, want %q (first non-empty from %v)", got, want, prompts)
		}
	})
}

// Concurrent SetLifecyclePhase does not race; final value is one of the written phases.
func TestSession_ConcurrentSetLifecyclePhase(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(2, 8).Draw(t, "goroutine_count")
		phases := make([]LifecyclePhase, n)
		for i := range phases {
			phases[i] = genLifecyclePhase(t)
		}

		s := newSession("id", "name", &git.WorktreeInfo{})
		var wg sync.WaitGroup
		wg.Add(n)
		for _, p := range phases {
			go func() {
				defer wg.Done()
				s.SetLifecyclePhase(p)
			}()
		}
		wg.Wait()

		got := s.LifecyclePhase()
		found := false
		for _, p := range phases {
			if got == p {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("LifecyclePhase() = %v, not among written phases %v", got, phases)
		}
	})
}
