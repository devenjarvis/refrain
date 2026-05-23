package agent

import (
	"testing"

	"pgregory.net/rapid"
)

// Forward phases round-trip through String/FromString.
func TestLifecyclePhase_StringRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		p := genForwardLifecyclePhase(t)
		got := LifecyclePhaseFromString(p.String())
		if got != p {
			t.Fatalf("LifecyclePhaseFromString(%q) = %v, want %v", p.String(), got, p)
		}
	})
}

// LifecyclePhaseFromString never returns LifecycleDrafting for any input.
func TestLifecyclePhase_FromStringNeverReturnsDrafting(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := genLifecycleString(t)
		got := LifecyclePhaseFromString(s)
		if got == LifecycleDrafting {
			t.Fatalf("LifecyclePhaseFromString(%q) = LifecycleDrafting", s)
		}
	})
}

// LifecyclePhaseFromString always returns a valid forward phase (0..5).
func TestLifecyclePhase_FromStringAlwaysValid(t *testing.T) {
	valid := map[LifecyclePhase]bool{
		LifecyclePlanning:       true,
		LifecycleInProgress:     true,
		LifecycleReadyForReview: true,
		LifecycleInReview:       true,
		LifecycleShipping:       true,
		LifecycleComplete:       true,
	}
	rapid.Check(t, func(t *rapid.T) {
		s := genLifecycleString(t)
		got := LifecyclePhaseFromString(s)
		if !valid[got] {
			t.Fatalf("LifecyclePhaseFromString(%q) = %v, not a valid forward phase", s, got)
		}
	})
}

// Forward phases maintain strict numeric ordering matching pipeline order.
func TestLifecyclePhase_ForwardPhasesStrictlyOrdered(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := genForwardLifecyclePhase(t)
		b := genForwardLifecyclePhase(t)
		if a != b && a < b && int(a) >= int(b) {
			t.Fatalf("phase %v (%d) should be numerically < %v (%d)", a, a, b, b)
		}
		// Drafting is always less than any forward phase.
		if LifecycleDrafting >= a {
			t.Fatalf("LifecycleDrafting (%d) should be < %v (%d)", LifecycleDrafting, a, a)
		}
	})
}

// String() never returns empty for any valid phase.
func TestLifecyclePhase_StringNeverEmpty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		p := genLifecyclePhase(t)
		s := p.String()
		if s == "" {
			t.Fatalf("LifecyclePhase(%d).String() returned empty", p)
		}
	})
}

// Two round-trips through FromString/String always stabilize.
func TestLifecyclePhase_FromStringIdempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := genLifecycleString(t)
		once := LifecyclePhaseFromString(s).String()
		twice := LifecyclePhaseFromString(once).String()
		if once != twice {
			t.Fatalf("not idempotent: FromString(%q).String()=%q, FromString(%q).String()=%q",
				s, once, once, twice)
		}
	})
}
