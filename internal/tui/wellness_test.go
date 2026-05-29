package tui

import (
	"testing"
	"time"
)

func TestWellnessEffectiveElapsed(t *testing.T) {
	const tol = 500 * time.Millisecond

	t.Run("fresh state (zero lastInputAt) returns time.Since(sessionStart)", func(t *testing.T) {
		w := wellnessState{
			sessionStart: time.Now().Add(-5 * time.Minute),
			// lastInputAt is zero — guard must return raw time.Since(sessionStart)
		}
		got := w.EffectiveElapsed()
		want := 5 * time.Minute
		if got < want-tol || got > want+tol {
			t.Errorf("EffectiveElapsed() = %v, want ~%v", got, want)
		}
	})

	t.Run("gap < grace returns full elapsed", func(t *testing.T) {
		w := wellnessState{
			sessionStart: time.Now().Add(-5 * time.Minute),
			lastInputAt:  time.Now().Add(-1 * time.Minute), // 1 min idle, within 3-min grace
		}
		got := w.EffectiveElapsed()
		want := 5 * time.Minute
		if got < want-tol || got > want+tol {
			t.Errorf("EffectiveElapsed() = %v, want ~%v (gap < grace so no decay)", got, want)
		}
	})

	t.Run("gap > grace returns elapsed minus excess", func(t *testing.T) {
		// sessionStart 10 min ago, lastInputAt 5 min ago
		// currentExtendedIdle = 5min - 3min = 2min
		// EffectiveElapsed = 10min - 0 - 2min = 8min
		w := wellnessState{
			sessionStart: time.Now().Add(-10 * time.Minute),
			lastInputAt:  time.Now().Add(-5 * time.Minute),
		}
		got := w.EffectiveElapsed()
		want := 8 * time.Minute
		if got < want-tol || got > want+tol {
			t.Errorf("EffectiveElapsed() = %v, want ~%v (5min gap - 3min grace = 2min decay)", got, want)
		}
	})

	t.Run("RecordInput accumulates idleDebt and EffectiveElapsed stays stable", func(t *testing.T) {
		// lastInputAt 5 min ago; after RecordInput:
		//   idleDebt ≈ 2min, lastInputAt ≈ now
		//   EffectiveElapsed ≈ 10min - 2min - 0 = 8min
		w := wellnessState{
			sessionStart: time.Now().Add(-10 * time.Minute),
			lastInputAt:  time.Now().Add(-5 * time.Minute),
		}
		w.RecordInput()

		if w.idleDebt < 2*time.Minute-tol || w.idleDebt > 2*time.Minute+tol {
			t.Errorf("idleDebt = %v, want ~2m after RecordInput on a 5-min gap", w.idleDebt)
		}

		got := w.EffectiveElapsed()
		want := 8 * time.Minute
		if got < want-tol || got > want+tol {
			t.Errorf("EffectiveElapsed() = %v, want ~%v after RecordInput", got, want)
		}

		// Short gap after RecordInput (< grace) — EffectiveElapsed should not decay further.
		got2 := w.EffectiveElapsed()
		if got2 < got-tol || got2 > got+tol {
			t.Errorf("second EffectiveElapsed() = %v, want same as first %v (no extra decay for short gap)", got2, got)
		}
	})

	t.Run("floor at zero", func(t *testing.T) {
		// session just started, but lastInputAt was 10 min ago
		// currentExtendedIdle = 10min - 3min = 7min >> sessionElapsed ≈ 0
		w := wellnessState{
			sessionStart: time.Now(),
			lastInputAt:  time.Now().Add(-10 * time.Minute),
		}
		got := w.EffectiveElapsed()
		if got != 0 {
			t.Errorf("EffectiveElapsed() = %v, want 0 (floor)", got)
		}
	})
}
