package tui

import tea "charm.land/bubbletea/v2"

// KeyMap groups action-keyed bindings for the session list. Each field lists
// the `tea.KeyPressMsg.String()` values that trigger the action. Mapping by
// action (not by key) keeps the dispatch switches readable, makes per-action
// testing possible, and is the seam for future user-configurable rebindings.
type KeyMap struct {
	Quit         []string // detach and exit
	Up           []string // list cursor up
	Down         []string // list cursor down
	Activate     []string // space/enter: open the row's terminal
	NextRepo     []string // cycle active repo
	NewSession   []string // create a new session
	AddAgent     []string // add an agent to the cursor's session
	OpenReview   []string // open the review panel
	OpenTerminal []string // open or focus shell terminal
	OpenIDE      []string // open worktree in IDE
	OpenPR       []string // open the session's PR panel (or push+draft)
	OpenBranch   []string // open branch/PR picker
	ManageRepos  []string // open repo picker in manage mode
	AddRepo      []string // add a repo via filesystem browser
	Settings     []string // open global settings overlay
	OpenDiff     []string // open the diff view
	KillAgent    []string // kill cursor session's primary agent
	KillSession  []string // kill cursor session entirely
}

// DefaultKeyMap returns the production binding set. Keep this aligned with the
// keybinding tables documented in README.md and CLAUDE.md.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Quit:         []string{"q", "ctrl+c"},
		Up:           []string{"up", "k"},
		Down:         []string{"down", "j"},
		Activate:     []string{"space", "enter"},
		NextRepo:     []string{"N"},
		NewSession:   []string{"n"},
		AddAgent:     []string{"c"},
		OpenReview:   []string{"r"},
		OpenTerminal: []string{"t"},
		OpenIDE:      []string{"e"},
		OpenPR:       []string{"p"},
		OpenBranch:   []string{"o"},
		ManageRepos:  []string{"R"},
		AddRepo:      []string{"a"},
		Settings:     []string{"s"},
		OpenDiff:     []string{"d"},
		KillAgent:    []string{"x"},
		KillSession:  []string{"X"},
	}
}

// Match reports whether msg matches any of the strings bound to action.
func (k KeyMap) Match(msg tea.KeyPressMsg, action []string) bool {
	s := msg.String()
	for _, b := range action {
		if b == s {
			return true
		}
	}
	return false
}

// ViewMode represents the current TUI view.
type ViewMode int

const (
	ViewDashboard ViewMode = iota
	ViewDiff
	ViewFileBrowser  // overlay: browse filesystem to add a repo
	ViewGlobalConfig // overlay: edit global settings
	ViewBranchPicker // overlay: pick branch/PR to open session on
	ViewRepoPicker   // overlay: pick a registered repo to start a session in
	ViewNewSession   // full-viewport new-session composition screen
)

// panelFocus tracks which dashboard surface has keyboard focus.
type panelFocus int

const (
	focusList       panelFocus = iota // pipeline: j/k navigate sessions
	focusConfig                       // overlay: per-repo config form
	focusRepoChecks                   // overlay: validation checks editor (sub-form of focusConfig)
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
