package agent

import "testing"

func TestLifecyclePhase_String(t *testing.T) {
	cases := []struct {
		phase LifecyclePhase
		want  string
	}{
		{LifecyclePlanning, "planning"},
		{LifecycleInProgress, "in_progress"},
		{LifecycleReadyForReview, "ready_for_review"},
		{LifecycleInReview, "in_review"},
		{LifecycleShipping, "shipping"},
		{LifecycleComplete, "complete"},
		{LifecycleDrafting, "drafting"},
	}
	for _, tc := range cases {
		if got := tc.phase.String(); got != tc.want {
			t.Errorf("LifecyclePhase(%d).String() = %q, want %q", tc.phase, got, tc.want)
		}
	}
}

func TestLifecycleDrafting_OrdersBeforeForwardPhases(t *testing.T) {
	// Drafting must sort numerically before every forward phase so any
	// future range check (`phase >= LifecycleComplete`, etc.) classifies
	// it as "earlier than Planning". Locks in the explicit -1 assignment.
	forward := []LifecyclePhase{
		LifecyclePlanning,
		LifecycleInProgress,
		LifecycleReadyForReview,
		LifecycleInReview,
		LifecycleShipping,
		LifecycleComplete,
	}
	for _, phase := range forward {
		if LifecycleDrafting >= phase {
			t.Errorf("LifecycleDrafting (%d) should be < %s (%d)", LifecycleDrafting, phase, phase)
		}
	}
}

func TestLifecyclePhaseFromString(t *testing.T) {
	cases := []struct {
		s    string
		want LifecyclePhase
	}{
		{"planning", LifecyclePlanning},
		{"in_progress", LifecycleInProgress},
		{"ready_for_review", LifecycleReadyForReview},
		{"in_review", LifecycleInReview},
		{"shipping", LifecycleShipping},
		{"complete", LifecycleComplete},
		{"drafting", LifecyclePlanning},  // drafting cannot survive detach; restore as Planning
		{"", LifecycleInProgress},        // empty → default
		{"unknown", LifecycleInProgress}, // unknown → default
	}
	for _, tc := range cases {
		if got := LifecyclePhaseFromString(tc.s); got != tc.want {
			t.Errorf("LifecyclePhaseFromString(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}
