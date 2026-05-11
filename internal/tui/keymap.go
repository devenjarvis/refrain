package tui

// ViewMode represents the current TUI view.
type ViewMode int

const (
	ViewDashboard ViewMode = iota
	ViewDiff
	ViewFileBrowser  // overlay: browse filesystem to add a repo
	ViewGlobalConfig // overlay: edit global settings
	ViewBranchPicker // overlay: pick branch/PR to open session on
	ViewRepoPicker   // overlay: pick a registered repo to start a session in
)

// panelFocus tracks which dashboard surface has keyboard focus.
type panelFocus int

const (
	focusList       panelFocus = iota // pipeline: j/k navigate sessions
	focusConfig                       // overlay: per-repo config form
	focusReview                       // overlay: review panel
	focusLaunch                       // overlay: fullscreen agent terminal
	focusPlanEditor                   // overlay: full-page plan editor (.claude/plan.md)
	focusShipping                     // overlay: shipping panel (CI + review threads + merge)
)

// focusSection identifies which row group the fullscreen focus-mode cursor is
// currently on. The cursor traverses Planning → Building → Reviewing → Shipping
// in render order, with j/k transitioning between sections at the boundaries.
type focusSection int

const (
	focusSectionPlanning focusSection = iota // sessions the user is still scoping (LifecyclePlanning)
	focusSectionBuilding                     // sessions actively running (LifecycleInProgress)
	focusSectionReview                       // ReadyForReview + InReview
	focusSectionShipping                     // PR open, awaiting CI/merge (LifecycleShipping)
)

// focusSectionsInOrder lists the sections in the order they render on the
// pipeline. Navigation, hit-testing, and clamp logic walk this list rather
// than hard-coding section transitions.
func focusSectionsInOrder() []focusSection {
	return []focusSection{
		focusSectionPlanning,
		focusSectionBuilding,
		focusSectionReview,
		focusSectionShipping,
	}
}
