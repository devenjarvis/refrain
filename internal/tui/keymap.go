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
	focusList   panelFocus = iota // pipeline: j/k navigate sessions
	focusConfig                   // overlay: per-repo config form
	focusReview                   // overlay: review panel
	focusLaunch                   // overlay: fullscreen agent terminal
)

// focusSection identifies which row group the fullscreen focus-mode cursor is
// currently on. The cursor traverses Active → Review Queue in render order,
// with j/k transitioning between sections at the boundaries.
type focusSection int

const (
	focusSectionActive focusSection = iota // in-progress sessions
	focusSectionReview                     // ready-for-review sessions
)
