package tui

import "time"

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
	// lastReviewAt is the wall-clock time of the most recent review-panel
	// open. Surfaced as a comfort metric in the wellness log.
	lastReviewAt time.Time

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
	}
}
