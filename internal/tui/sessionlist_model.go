package tui

import (
	"time"

	"github.com/devenjarvis/refrain/internal/agent"
)

// sessionListProps carries every value the session list renders from but does
// not own. App.sessionListProps() rebuilds it fresh each frame and threads it
// into sessionListModel.View, so the list reads live App state without
// mirroring it onto its own struct (CONVENTIONS.md §5/§6).
//
// Purity rule (§5): every time-derived string is computed against the
// tick-refreshed render clock (sessionListModel.now), never time.Now().
type sessionListProps struct {
	// items is the hierarchical repo/session/agent row list, rebuilt from the
	// managers each frame by App.listItems().
	items listItems

	// Per-session caches, owned by App: the PR badge cache and the session
	// closing-set, both keyed by cacheKey(repoPath, sessionID).
	prCache         map[string]*prCacheEntry
	closingSessions map[string]bool

	// activeRepoPath highlights the repo header that `n` targets when no
	// session row is selected.
	activeRepoPath string

	// PR draft-in-flight indicator, owned by App. The matching row renders a
	// "drafting PR…" badge while the push+draft pipeline runs.
	prDraftSessionID string
	prDraftRepoPath  string
}

// sessionRow is one selectable row of the flat session list.
type sessionRow struct {
	repoPath string
	repoName string
	session  *agent.Session
}

// sessionGroup is one repo's block of rows: an optional header line followed
// by rows[start : start+count] of the layout's flat row slice.
type sessionGroup struct {
	repoPath string
	repoName string
	header   bool // repo-header line rendered (multi-repo shape)
	start    int  // index of the group's first row in layout.rows
	count    int  // number of session rows in the group
}

// sessionListLayout is the flattened row/line model shared by rendering,
// scrolling, and mouse hit-testing so all three agree on vertical positions.
// Lines are content lines: a header is 1 line, a session card is
// sessionCardLines lines, an empty repo group renders 1 hint line.
type sessionListLayout struct {
	groups   []sessionGroup
	rows     []sessionRow
	rowStart []int // content-line offset of each row's first card line
	total    int   // total content lines
}

// sessionCardLines is the height of one session card in the list.
const sessionCardLines = 2

// buildSessionListLayout flattens the repo/session item list into the layout
// the session list renders. Sessions keep the order App.listItems() produced
// (repo-grouped, CreatedAt ascending) — attention never reorders the list.
// Sessions whose lifecycle reached Complete are hidden, matching the previous
// dashboard behavior of merged work leaving the screen.
func buildSessionListLayout(items listItems) sessionListLayout {
	var l sessionListLayout
	line := 0
	flushEmptyGroup := func() {
		if n := len(l.groups); n > 0 && l.groups[n-1].header && l.groups[n-1].count == 0 {
			line++ // the "no sessions" hint line under an empty repo header
		}
	}
	for _, item := range items {
		switch item.kind {
		case listItemRepo:
			flushEmptyGroup()
			l.groups = append(l.groups, sessionGroup{
				repoPath: item.repoPath,
				repoName: item.repoName,
				header:   true,
				start:    len(l.rows),
			})
			line++
		case listItemSession:
			if item.session == nil || item.session.LifecyclePhase() == agent.LifecycleComplete {
				continue
			}
			if len(l.groups) == 0 {
				// cfg==nil shape: sessions with no repo headers.
				l.groups = append(l.groups, sessionGroup{
					repoPath: item.repoPath,
					repoName: item.repoName,
					start:    len(l.rows),
				})
			}
			g := &l.groups[len(l.groups)-1]
			g.count++
			l.rows = append(l.rows, sessionRow{
				repoPath: item.repoPath,
				repoName: item.repoName,
				session:  item.session,
			})
			l.rowStart = append(l.rowStart, line)
			line += sessionCardLines
		}
	}
	flushEmptyGroup()
	l.total = line
	return l
}

// sessionListModel owns only the transient UI state of the root session list:
// its viewport size, the flat cursor, the scroll offset, and the render clock.
// Everything it renders is read live each frame from sessionListProps.
type sessionListModel struct {
	width  int
	height int
	cursor int // index into the layout's flat row slice
	scroll int // first visible content line

	// now is the render clock, refreshed from the app tick, so View derives
	// age strings without reading the wall clock (§5).
	now time.Time
}

func newSessionListModel() sessionListModel {
	return sessionListModel{now: time.Now()}
}

// SetSize informs the list of the space it has to render in.
func (m *sessionListModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// moveCursor shifts the cursor by delta, clamped to the row count, and keeps
// the selected card scrolled into view.
func (m *sessionListModel) moveCursor(delta int, layout sessionListLayout) {
	m.cursor = clampedMove(m.cursor, delta, len(layout.rows))
	m.ensureCursorVisible(layout)
}

// clamp keeps the cursor and scroll offset in range as the underlying row list
// changes (sessions created, killed, or completed).
func (m *sessionListModel) clamp(layout sessionListLayout) {
	if m.cursor >= len(layout.rows) {
		m.cursor = len(layout.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	maxScroll := layout.total - m.height
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
	m.ensureCursorVisible(layout)
}

// ensureCursorVisible adjusts the scroll offset so every line of the selected
// card is inside the viewport.
func (m *sessionListModel) ensureCursorVisible(layout sessionListLayout) {
	if m.height <= 0 || m.cursor < 0 || m.cursor >= len(layout.rowStart) {
		return
	}
	top := layout.rowStart[m.cursor]
	// Keep the repo header visible when the cursor is on a group's first row.
	for _, g := range layout.groups {
		if g.header && g.count > 0 && g.start == m.cursor {
			top--
			break
		}
	}
	bottom := layout.rowStart[m.cursor] + sessionCardLines - 1
	if top < m.scroll {
		m.scroll = top
	}
	if bottom >= m.scroll+m.height {
		m.scroll = bottom - m.height + 1
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

// selectSession moves the cursor to the row for (repoPath, sessionID).
// Returns false when the session has no row (e.g. Complete, or not yet in
// the manager list).
func (m *sessionListModel) selectSession(layout sessionListLayout, repoPath, sessionID string) bool {
	for i, row := range layout.rows {
		if row.repoPath == repoPath && row.session != nil && row.session.ID == sessionID {
			m.cursor = i
			m.ensureCursorVisible(layout)
			return true
		}
	}
	return false
}

// rowAt maps a content-relative Y coordinate (0 = first visible line) to the
// row index rendered there, plus whether the hit landed on the card's first
// line (where the PR indicator lives). ok is false for header lines, hint
// lines, and empty space.
func (m sessionListModel) rowAt(layout sessionListLayout, contentY int) (idx int, firstLine bool, ok bool) {
	line := contentY + m.scroll
	for i, start := range layout.rowStart {
		if line >= start && line < start+sessionCardLines {
			return i, line == start, true
		}
	}
	return 0, false, false
}
