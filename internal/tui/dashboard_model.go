package tui

import (
	"fmt"
	"sort"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/refrain/internal/agent"
)

// clampedMove shifts a list cursor at cur by delta and clamps the result to
// [0, n-1], where n is the item count. A non-positive n yields 0. Shared by
// the list pickers for up/down (k/j) navigation.
func clampedMove(cur, delta, n int) int {
	cur += delta
	if cur < 0 {
		return 0
	}
	if n <= 0 {
		return 0
	}
	if cur > n-1 {
		return n - 1
	}
	return cur
}

// Timing constants for the sidebar session-name marquee ticker.
const (
	tickerPauseStart = 2 * time.Second
	tickerPauseEnd   = time.Second
	tickerInterval   = 250 * time.Millisecond
)

// sessionTicker tracks the scroll state for one session's name in the sidebar.
type sessionTicker struct {
	offset      int
	pauseUntil  time.Time
	nextAdvance time.Time
	atEnd       bool
}

// listItemKind distinguishes repo headers, session rows, and agent rows in the dashboard list.
type listItemKind int

const (
	listItemRepo listItemKind = iota
	listItemSession
	listItemAgent
)

// listItem represents one row in the hierarchical dashboard list.
type listItem struct {
	kind     listItemKind
	repoPath string
	repoName string         // set for repo header items
	session  *agent.Session // set for session and agent items
	agent    *agent.Agent   // set for agent items
}

// listItems is the hierarchical repo/session/agent row list. App.listItems()
// rebuilds it from the managers each frame; the phase-filter / section
// accessors and the per-session agent-status helpers hang off it so the
// dashboard can derive everything it renders from props.items without the
// model owning a mirrored copy (CONVENTIONS.md §5/§6).
type listItems []listItem

// agents returns every agent row in the list (for resize operations).
func (items listItems) agents() []*agent.Agent {
	var result []*agent.Agent
	for _, item := range items {
		if item.kind == listItemAgent {
			result = append(result, item.agent)
		}
	}
	return result
}

// dashboardModel owns only the transient UI state the dashboard genuinely
// holds across frames: its viewport size, scroll offset, marquee tickers, the
// in-flight mouse text selection, and the tick-refreshed render clock.
// Everything else it renders (modal state, the wellness snapshot, the pipeline
// cursor, caches, the active repo, and the item list) is read live each frame
// from a fresh dashboardProps — see CONVENTIONS.md §5/§6.
type dashboardModel struct {
	width        int
	height       int
	sidebarWidth int // resolved global SidebarWidth; 0 means use DefaultSidebarWidth
	scrollOffset int

	// tickers tracks marquee scroll state for session names that overflow the sidebar.
	tickers map[string]*sessionTicker

	// now is the render clock, refreshed from the app tick. View derives the
	// "idle Nm" / "done Nm ago" / elapsed strings and the review spinner from
	// it so rendering stays pure (§5: no clock read at render time).
	now time.Time

	// Mouse text selection state in VT-cell coordinates, bound to a specific
	// agent so a sidebar selection change clears it cleanly.
	selection selection
}

// selection tracks an in-progress or completed mouse drag selection inside the
// agent VT viewport. Coordinates are zero-based cell indices within the
// agent's viewport (0..fixedTermWidth, 0..fixedTermHeight).
type selection struct {
	anchorX, anchorY int
	cursorX, cursorY int
	active           bool   // a click has seeded an in-flight or completed selection
	dragSeen         bool   // mouse moved away from the anchor; distinguishes drag from plain click
	agentID          string // agent.Agent.ID() the selection is bound to
}

func newDashboardModel() dashboardModel {
	return dashboardModel{tickers: make(map[string]*sessionTicker), now: time.Now()}
}

// advanceTickers steps the marquee scroll state for all sessions whose names
// overflow the sidebar. Must be called once per tick before rendering.
func (d *dashboardModel) advanceTickers(now time.Time, props dashboardProps) {
	width := d.listWidth()
	for _, item := range props.items {
		if item.kind != listItemSession || item.session == nil {
			continue
		}
		sess := item.session
		displayName := sess.GetDisplayName()

		sessKey := cacheKey(item.repoPath, sess.ID)
		closing := props.closingSessions != nil && props.closingSessions[sessKey]
		prSuffixLen := 0
		if !closing {
			if entry := props.prCache[sessKey]; entry != nil && entry.pr != nil {
				prSuffixLen = 1 + prIndicatorWidth(entry) // 1 for leading space
			}
		}
		closingTagLen := 0
		if closing {
			closingTagLen = 9
		}
		maxNameLen := width - 10 - prSuffixLen - closingTagLen
		if maxNameLen < 5 {
			maxNameLen = 5
		}

		if ansi.StringWidth(displayName) <= maxNameLen {
			delete(d.tickers, sess.ID)
			continue
		}

		fullName := displayName + " ·"
		fullRunes := []rune(fullName)

		t, exists := d.tickers[sess.ID]
		if !exists {
			t = &sessionTicker{pauseUntil: now.Add(tickerPauseStart)}
			d.tickers[sess.ID] = t
		}

		if now.Before(t.pauseUntil) {
			continue
		}
		if now.Before(t.nextAdvance) {
			continue
		}

		t.offset++
		t.nextAdvance = now.Add(tickerInterval)

		if ansi.StringWidth(string(fullRunes[t.offset:])) <= maxNameLen {
			if !t.atEnd {
				// First time reaching the end: brief pause before snapping.
				t.atEnd = true
				t.pauseUntil = now.Add(tickerPauseEnd)
				t.nextAdvance = time.Time{}
			} else {
				// End pause expired: snap back to start.
				t.offset = 0
				t.atEnd = false
				t.pauseUntil = now.Add(tickerPauseStart)
				t.nextAdvance = time.Time{}
			}
		}
	}
}

// listWidth returns the configured sidebar width, falling back to the default
// when sidebarWidth has not yet been plumbed in. Both View() and
// fixedTermWidth() must return the same value on any given frame, otherwise
// the sidebar and the agent VT will disagree about column counts.
func (d dashboardModel) listWidth() int {
	return resolveSidebarWidth(d.sidebarWidth)
}

func (d dashboardModel) contentHeight() int {
	return d.height - statusBarHeight - titleHeight
}

// sessionsInPhase returns listItems for sessions whose lifecycle phase matches
// any of the given phases. Repo filtering is intentionally NOT applied here
// because the pipeline shows cross-repo work; callers that want a per-repo
// view should filter the result themselves.
func (items listItems) sessionsInPhase(phases ...agent.LifecyclePhase) []listItem {
	var result []listItem
	for _, item := range items {
		if item.kind != listItemSession || item.session == nil {
			continue
		}
		phase := item.session.LifecyclePhase()
		for _, p := range phases {
			if phase == p {
				result = append(result, item)
				break
			}
		}
	}
	return result
}

// planningSessions returns the sessions the user is still scoping. Planning is
// the entry point for new work — sessions advance to Building (InProgress) once
// the user presses 'b'. Drafting sessions (LifecycleDrafting) are included
// here so they're visible from the dashboard while the background draft runs;
// the card renderer detects the sub-phase and shows a "drafting…" badge.
func (items listItems) planningSessions() []listItem {
	return items.sessionsInPhase(agent.LifecyclePlanning, agent.LifecycleDrafting)
}

// reviewQueueSessions returns listItems for sessions in ReadyForReview or
// InReview phase. InReview sessions are kept in the queue so that an ESC out
// of the review panel never orphans them — without this, a session whose user
// peeked at the review panel and backed out (or hit "open PR" with no PR
// cached) would disappear from both BUILDING (InProgress only) and the queue,
// even though the pipeline IN REVIEW count showed it was still there.
func (items listItems) reviewQueueSessions() []listItem {
	return items.sessionsInPhase(agent.LifecycleReadyForReview, agent.LifecycleInReview)
}

// shippingSessions returns sessions whose PR is open and awaiting CI/merge.
// They leave this list automatically when polling detects a merge (transition
// to LifecycleComplete) or when the user presses 'c' in the review panel.
func (items listItems) shippingSessions() []listItem {
	return items.sessionsInPhase(agent.LifecycleShipping)
}

// buildingSessions returns listItems for all sessions in InProgress phase,
// including those whose process has finished (DoneAt set) but have not yet been
// moved to ReadyForReview. This is the "active work" section — agents are
// running, the user is interacting with them, and the work has moved past the
// scoping done in Planning.
func (items listItems) buildingSessions() []listItem {
	result := items.sessionsInPhase(agent.LifecycleInProgress)
	sort.SliceStable(result, func(i, j int) bool {
		pi := items.sessionFocusPriority(result[i].session)
		pj := items.sessionFocusPriority(result[j].session)
		if pi != pj {
			return pi < pj
		}
		// Same priority: stable order by CreatedAt ASC. Using a time-of-output
		// key here would re-sort the list on every burst of agent output,
		// causing visible flicker. The order should only change when a session
		// crosses a priority boundary (i.e., a real state change).
		return result[i].session.CreatedAt.Before(result[j].session.CreatedAt)
	})
	return result
}

// sectionItems returns the listItem slice that backs the given fullscreen-focus
// section. The panic case enforces the focusSection enum invariant: a new
// section added without updating this switch fails loudly at the first call
// rather than silently rendering empty.
func (items listItems) sectionItems(s focusSection) []listItem {
	switch s {
	case focusSectionPlanning:
		return items.planningSessions()
	case focusSectionBuilding:
		return items.buildingSessions()
	case focusSectionReview:
		return items.reviewQueueSessions()
	case focusSectionShipping:
		return items.shippingSessions()
	}
	panic(fmt.Sprintf("listItems.sectionItems: unknown focusSection %d", s))
}

// sessionFocusPriority returns an integer priority for sorting sessions in the
// focus mode SESSIONS list. Lower values surface first (needs attention first).
// 0=error, 1=waiting, 2=active, 3=idle/other.
func (items listItems) sessionFocusPriority(sess *agent.Session) int {
	var hasError, hasWaiting, hasActive bool
	for _, item := range items {
		if item.kind != listItemAgent || item.agent == nil || item.agent.IsShell || item.session != sess {
			continue
		}
		switch item.agent.Status() {
		case agent.StatusError:
			hasError = true
		case agent.StatusWaiting:
			hasWaiting = true
		case agent.StatusActive:
			hasActive = true
		}
	}
	if hasError {
		return 0
	}
	if hasWaiting {
		return 1
	}
	if hasActive {
		return 2
	}
	return 3
}

// Breath animation tuning. 100 frames at the 100ms tick = a 10s cycle
// (5.5 BPM), matching coherent/resonance breathing — the rate that
// maximally increases HRV and activates the parasympathetic nervous system
// (PMID 24380741, PMC5575449). Equal 5s inhale / 5s exhale.
const (
	breathFrameCount = 100
	breathWidth      = 27
	breathHeight     = 9
)

// clearSelection resets the mouse text-selection state. Safe to call when no
// selection is active.
func (d *dashboardModel) clearSelection() {
	d.selection = selection{}
}

// selectionRect returns the active selection as a normalized rectangle in
// VT-cell coordinates. Normalization is by row first, so for a multi-row
// reverse drag (anchor row > cursor row) the returned startX/endX may be
// "out of order" relative to a Cartesian rect — that asymmetry is intentional
// and matches the per-line membership rule in vt.SelectionRect.inSelection:
// startX picks where the start row begins, endX picks where the end row ends,
// and the X axis is independent on each row. ok is false when there is no
// drag-confirmed selection to render or copy from.
func (d dashboardModel) selectionRect() (startX, startY, endX, endY int, ok bool) {
	if !d.selection.active || !d.selection.dragSeen {
		return 0, 0, 0, 0, false
	}
	startX, endX = d.selection.anchorX, d.selection.cursorX
	startY, endY = d.selection.anchorY, d.selection.cursorY
	if startY > endY || (startY == endY && startX > endX) {
		startX, endX = endX, startX
		startY, endY = endY, startY
	}
	return startX, startY, endX, endY, true
}
