package tui

import "time"

// Polling and adaptive-interval timing for the GitHub PR poller. The poller
// runs from App.Update on every tick (DashboardTickInterval); these constants
// are the wall-clock thresholds it consults to decide what to do for each
// session. See prPollInterval in update_pr.go for the adaptive ladder.
const (
	// PRPollBurstAfterCreate is the post-create burst window: after refrain
	// creates or detects a new PR, poll aggressively for this long so the
	// PR's `unknown` mergeable state resolves quickly.
	PRPollBurstAfterCreate = 60 * time.Second

	// PRPollBurstUnknownMergeable is a shorter burst armed each time the
	// poller observes a `""` / `"unknown"` mergeable state, so the next
	// poll picks up the resolved state within ~2s.
	PRPollBurstUnknownMergeable = 15 * time.Second

	// PRPollDuringBurst is the in-burst interval (2s -> visible transitions
	// within one tick of arming the burst).
	PRPollDuringBurst = 2 * time.Second

	// PRPollAfterPush is the interval used after a branch push has been
	// detected but no PR exists yet — fast enough to catch the PR landing
	// without hammering the API.
	PRPollAfterPush = 10 * time.Second

	// PRPollCIPending is the interval used while at least one check run is
	// in `pending` so success/failure transitions render promptly.
	PRPollCIPending = 5 * time.Second

	// PRPollStable is the steady-state interval — no active burst, no
	// pending CI, no recent push. Same value is used both for "no PR yet"
	// and "PR exists with terminal CI state" because the cost of the API
	// call is the same and both want the same baseline freshness.
	PRPollStable = 30 * time.Second

	// PRSHACheckInterval throttles the git rev-parse origin/<branch> probe
	// the poller uses to detect external pushes. Keeping this tight (2s)
	// makes pushes visible quickly without blocking the Bubble Tea main
	// goroutine on every tick.
	PRSHACheckInterval = 2 * time.Second

	// PRCIFlashDuration is how long a session row "flashes" green/red
	// after a CI state transition lands.
	PRCIFlashDuration = 2 * time.Second

	// MaxConcurrentPRPolls caps the number of in-flight gh API calls so
	// the poller can't saturate a tight rate limit.
	MaxConcurrentPRPolls = 3

	// PRPollClientTimeout is the per-call context timeout for the GitHub
	// client. Bounds individual API calls; the poller's overall budget is
	// the surrounding tick cycle.
	PRPollClientTimeout = 20 * time.Second

	// PRPollParentTimeout is the parent context budget for a full
	// session-poll round (PR + checks + reviews + threads + stack).
	PRPollParentTimeout = 90 * time.Second
)

// Dashboard UI cadence.
const (
	// DashboardTickInterval is the Bubble Tea tick cadence driving the
	// dashboard ticker, PR poller, wellness timer, and error-overlay
	// countdown. 100ms keeps the marquee and timer responsive without
	// burning CPU on idle sessions.
	DashboardTickInterval = 100 * time.Millisecond

	// PipelineDoubleClickWindow is the maximum gap between two clicks on
	// the same pipeline row that still counts as a double-click activation.
	PipelineDoubleClickWindow = 500 * time.Millisecond

	// DiffStatsCacheTTL is how long App.diffStatsCache entries are
	// considered fresh before the dashboard triggers a background refresh.
	DiffStatsCacheTTL = 5 * time.Second

	// ErrorOverlayTicks is the number of dashboard ticks the bottom-row
	// error message stays visible after setError() (3 seconds at the
	// 100ms tick interval).
	ErrorOverlayTicks = 30
)
