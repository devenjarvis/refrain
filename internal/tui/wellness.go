package tui

import "time"

// idleGrace is the keyboard/mouse inactivity window before the focus-block
// timer starts counting down. Kept unexported and non-configurable by design.
const idleGrace = 3 * time.Minute

// wellnessState owns the focus-block timer, the break overlay, and the
// session/agent counters that flush to the wellness log on quit. It was
// extracted from App so the 14 related fields stop sprawling at the top
// level — and so the break state machine has a single home.
//
// Field names match the originals from App to keep the sed-renamed call
// sites readable as `a.wellness.focusBreakMode`, etc. Encapsulation
// (StartBreak / EndBreak methods, Tick orchestration) is deferred to a
// follow-up so this commit stays a pure structural move with no behaviour
// change.
type wellnessState struct {
	// appStart is set once at init and never reset; powers the total session
	// duration line in the wellness log.
	appStart time.Time
	// sessionStart is the work-block start time; reset whenever a break ends.
	sessionStart time.Time

	// lastInputAt is the wall-clock time (monotonic stripped) of the most
	// recent keyboard or mouse event from the human. Used by EffectiveElapsed
	// to decay the timer during extended inactivity.
	lastInputAt time.Time
	// idleDebt is the cumulative time the user was idle beyond idleGrace in
	// prior idle intervals. Locked in by RecordInput and carried forward.
	idleDebt time.Duration

	// focusSessionMinutes and focusBreakMinutes mirror the resolved global
	// settings; cached so the per-tick comparison doesn't need to re-resolve.
	focusSessionMinutes int
	focusBreakMinutes   int

	// focusBreakMode is true while the break overlay is up.
	focusBreakMode bool
	// focusBreakStart is wall-clock (monotonic stripped) so suspend counts
	// toward the break duration.
	focusBreakStart time.Time
	// focusBreakShortWarning gates the "really cut it short?" double-press on b.
	focusBreakShortWarning bool
	// focusBreakTimerUp flips once the configured break duration has elapsed;
	// the overlay then waits for an explicit `b` to resume.
	focusBreakTimerUp bool
	// focusBreakAnimFrame advances every tick during break mode for the
	// overlay animation.
	focusBreakAnimFrame int

	// focusBlockCount is the number of completed focus blocks this session.
	focusBlockCount int

	// focusBacklogWarning is true after the first `n` press while the review
	// backlog cap is exceeded; a second press clears it and proceeds.
	focusBacklogWarning bool

	// agentsCreatedCount and sessionsCreatedCount count successful
	// CreateSession/AddAgent results; flushed to the wellness log on quit.
	agentsCreatedCount   int
	sessionsCreatedCount int
}

// newWellnessState initialises the timestamps that NewApp() previously set
// inline. Defaults for focus minutes are filled in by handleInit once the
// resolved settings are loaded.
func newWellnessState() wellnessState {
	now := time.Now()
	return wellnessState{
		appStart:     now,
		sessionStart: now,
		lastInputAt:  now,
	}
}

// RecordInput locks in the idle debt accumulated since the last input event
// and resets the inactivity clock. Call this at the top of every human
// keyboard and mouse handler so EffectiveElapsed reflects real active time.
func (w *wellnessState) RecordInput() {
	now := time.Now().Round(0)
	if !w.lastInputAt.IsZero() {
		gap := now.Sub(w.lastInputAt)
		if gap > idleGrace {
			w.idleDebt += gap - idleGrace
		}
	}
	w.lastInputAt = now
}

// EffectiveElapsed returns how much focus-block time has elapsed, excluding
// keyboard/mouse inactivity past idleGrace. The value is clamped to zero so
// the display never goes negative. If lastInputAt has not been seeded yet
// (tests or pre-init paths), falls back to raw time.Since(sessionStart).
func (w wellnessState) EffectiveElapsed() time.Duration {
	return w.EffectiveElapsedAt(time.Now())
}

// EffectiveElapsedAt is EffectiveElapsed against an explicit clock. The render
// path passes the tick-refreshed clock (dashboardModel.now) so building
// dashboardProps stays pure — no wall-clock read at render time (§5).
func (w wellnessState) EffectiveElapsedAt(now time.Time) time.Duration {
	if w.lastInputAt.IsZero() {
		return now.Sub(w.sessionStart)
	}
	currentExtendedIdle := now.Sub(w.lastInputAt) - idleGrace
	if currentExtendedIdle < 0 {
		currentExtendedIdle = 0
	}
	elapsed := now.Sub(w.sessionStart) - w.idleDebt - currentExtendedIdle
	if elapsed < 0 {
		return 0
	}
	return elapsed
}
