package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	xvt "github.com/charmbracelet/x/vt"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/tui/theme"
	"github.com/devenjarvis/refrain/internal/vt"
)

// launchModel owns the transient UI state of the fullscreen agent terminal
// (focusLaunch): its viewport size, the scrollback offset, and the in-flight
// mouse text selection. The agent/session/repo it renders live in App.modals;
// this model is lifted out of the retired dashboard component so focusLaunch
// opens from any session-list row (rollback design §4.2).
type launchModel struct {
	width  int // full content width
	height int // content height (excludes the status bar)

	scrollOffset int

	// selection tracks a mouse drag selection in VT-cell coordinates, bound
	// to a specific agent so a tab switch clears it cleanly.
	selection selection
}

// SetSize informs the launch view of the space it has to render in.
func (m *launchModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// termHeight is the agent terminal's row count inside the launch view:
// the content height minus the header row and the tab bar row.
func (m launchModel) termHeight() int {
	return m.height - 2
}

// clearSelection resets the mouse text-selection state.
func (m *launchModel) clearSelection() {
	m.selection = selection{}
}

// selection tracks an in-progress or completed mouse drag selection inside the
// agent VT viewport. Coordinates are zero-based cell indices within the
// agent's viewport.
type selection struct {
	anchorX, anchorY int
	cursorX, cursorY int
	active           bool   // a click has seeded an in-flight or completed selection
	dragSeen         bool   // mouse moved away from the anchor; distinguishes drag from plain click
	agentID          string // agent.Agent.ID() the selection is bound to
}

// selectionRect returns the active selection as a normalized rectangle in
// VT-cell coordinates. Normalization is by row first, so for a multi-row
// reverse drag (anchor row > cursor row) the returned startX/endX may be
// "out of order" relative to a Cartesian rect — that asymmetry is intentional
// and matches the per-line membership rule in vt.SelectionRect.inSelection:
// startX picks where the start row begins, endX picks where the end row ends,
// and the X axis is independent on each row. ok is false when there is no
// drag-confirmed selection to render or copy from.
func (m launchModel) selectionRect() (startX, startY, endX, endY int, ok bool) {
	if !m.selection.active || !m.selection.dragSeen {
		return 0, 0, 0, 0, false
	}
	startX, endX = m.selection.anchorX, m.selection.cursorX
	startY, endY = m.selection.anchorY, m.selection.cursorY
	if startY > endY || (startY == endY && startX > endX) {
		startX, endX = endX, startX
		startY, endY = endY, startY
	}
	return startX, startY, endX, endY, true
}

// renderTabBar renders the agent tab strip. Uses focusLaunchTabText for the
// labels so rendering and click-hit detection stay in sync.
func (m launchModel) renderTabBar(sess *agent.Session, active *agent.Agent) string {
	if sess == nil || active == nil {
		return ""
	}
	agents := sess.Agents()
	if len(agents) == 0 {
		return ""
	}
	var parts []string
	for _, ag := range agents {
		text := focusLaunchTabText(ag)
		if ag.ID == active.ID {
			parts = append(parts, StyleTitle.Render(text))
		} else {
			parts = append(parts, StyleSubtle.Render(text))
		}
	}
	bar := strings.Join(parts, "  ")
	if ansi.StringWidth(bar) > m.width {
		bar = ansi.Truncate(bar, m.width, "")
	}
	return bar
}

// tabIndexAt returns the agent index in sess for a tab-bar click at column x,
// or -1 when x lands on no tab.
func (m launchModel) tabIndexAt(sess *agent.Session, x int) int {
	if sess == nil {
		return -1
	}
	col := 0
	for i, ag := range sess.Agents() {
		w := ansi.StringWidth(focusLaunchTabText(ag))
		if x >= col && x < col+w {
			return i
		}
		col += w + 2 // 2-space separator between tabs
	}
	return -1
}

// View renders the fullscreen agent terminal: a muted header (agent + branch),
// the tab bar, and the terminal viewport with scrollback and selection
// highlighting. Pure w.r.t. the model; the agent's screen is the live source.
func (m launchModel) View(sess *agent.Session, ag *agent.Agent) string {
	if ag == nil {
		return ""
	}
	headerParts := []string{fmt.Sprintf("agent: %s", ag.GetDisplayName())}
	if sess != nil {
		if branch := sess.Branch(); branch != "" {
			headerParts = append(headerParts, fmt.Sprintf("branch: %s", branch))
		}
		if sess.Kind() == agent.KindCheckout {
			headerParts = append(headerParts, "checkout session")
		}
	}
	header := StyleSubtle.Render(strings.Join(headerParts, "  "))

	tabBar := m.renderTabBar(sess, ag)

	vpWidth := m.width
	vpHeight := m.termHeight()
	var render string
	switch {
	case m.scrollOffset > 0:
		sbLines, viewport := ag.Snapshot(vpWidth, vpHeight)
		vpLines := strings.Split(viewport, "\n")
		allLines := append(sbLines, vpLines...)

		end := len(allLines) - m.scrollOffset
		if end < 0 {
			end = 0
		}
		start := end - vpHeight
		if start < 0 {
			start = 0
		}
		visibleLines := vt.PadLines(allLines[start:end], vpWidth)
		if m.selection.active && m.selection.dragSeen && m.selection.agentID == ag.ID {
			sx, sy, ex, ey, _ := m.selectionRect()
			render = applySelectionHighlight(visibleLines, vt.SelectionRect{
				StartX: sx, StartY: sy, EndX: ex, EndY: ey, Active: true,
			})
		} else {
			render = strings.Join(visibleLines, "\n")
		}
	case m.selection.active && m.selection.dragSeen && m.selection.agentID == ag.ID:
		sx, sy, ex, ey, _ := m.selectionRect()
		render = ag.RenderPaddedWithSelection(vpWidth, vpHeight, vt.SelectionRect{
			StartX: sx, StartY: sy, EndX: ex, EndY: ey, Active: true,
		})
	default:
		render = ag.RenderPadded(vpWidth, vpHeight)
	}

	if tabBar == "" {
		return header + "\n" + render
	}
	return header + "\n" + tabBar + "\n" + render
}

// launchTermCellAt converts a screen-space mouse coordinate to a VT cell
// coordinate inside the launch terminal viewport.
func (a *App) launchTermCellAt(screenX, screenY int) (termX, termY int, inViewport bool) {
	termX = screenX
	termY = screenY - a.dashboardTopY() - 2 // header row + tab bar row
	w := a.launch.width
	h := a.launch.termHeight()
	inViewport = termX >= 0 && termX < w && termY >= 0 && termY < h
	return termX, termY, inViewport
}

// handleLaunchKeys handles all keypresses while panelFocus == focusLaunch.
// The fullscreen agent terminal owns the keyboard: most keys forward to the
// underlying PTY, with a small set of escape hatches — esc/ctrl+e back to the
// list, alt+[ / alt+] cycle agents, ctrl+t / ctrl+n add a shell / agent,
// ctrl+w close the current agent, pgup/pgdn/home scrollback.
func (a App) handleLaunchKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	a.confirmQuit = false
	ag := a.modals.LaunchAgent()
	sess := a.modals.LaunchSession()
	if ag == nil {
		a.closeModal()
		a.launch.scrollOffset = 0
		return a, nil
	}
	switch msg.String() {
	case "esc", "ctrl+e":
		a.closeModal()
		a.launch.scrollOffset = 0
		a.launch.clearSelection()
	case "shift+esc":
		ag.SendKey(xvt.KeyPressEvent{Code: tea.KeyEscape})
	case "pgup":
		maxScroll := len(ag.ScrollbackLines())
		a.launch.scrollOffset += a.launch.height / 2
		if a.launch.scrollOffset > maxScroll {
			a.launch.scrollOffset = maxScroll
		}
	case "pgdn":
		a.launch.scrollOffset -= a.launch.height / 2
		if a.launch.scrollOffset < 0 {
			a.launch.scrollOffset = 0
		}
	case "home":
		a.launch.scrollOffset = 0
	case "alt+]", "alt+[":
		if sess != nil {
			agents := sess.Agents()
			idx := 0
			for i, candidate := range agents {
				if candidate.ID == ag.ID {
					idx = i
					break
				}
			}
			if msg.String() == "alt+]" {
				idx = (idx + 1) % len(agents)
			} else {
				idx = (idx - 1 + len(agents)) % len(agents)
			}
			a.modals.SetLaunchAgent(agents[idx])
			a.launch.scrollOffset = 0
			agents[idx].Resize(a.agentTermRows(), a.agentTermCols())
		}
	case "ctrl+t":
		if sess != nil {
			repoPath := a.modals.LaunchRepoPath()
			mgr := a.managers[repoPath]
			if mgr != nil {
				resolved := a.resolvedCache[repoPath]
				cfg := agent.Config{
					Rows:              a.agentTermRows(),
					Cols:              a.agentTermCols(),
					BypassPermissions: resolved.BypassPermissions,
				}
				if newAg, err := mgr.AddShell(sess.ID, cfg); err == nil {
					a.modals.SetLaunchAgent(newAg)
					a.launch.scrollOffset = 0
				} else {
					a.setError(err.Error())
				}
			}
		}
	case "ctrl+n":
		if sess != nil {
			repoPath := a.modals.LaunchRepoPath()
			mgr := a.managers[repoPath]
			if mgr != nil {
				resolved := a.resolvedCache[repoPath]
				cfg := agent.Config{
					Rows:              a.agentTermRows(),
					Cols:              a.agentTermCols(),
					BypassPermissions: resolved.BypassPermissions,
					AgentProgram:      resolved.AgentProgram,
					AgentModel:        resolved.AgentModel,
					BuildSystemPrompt: resolved.BuildSystemPrompt,
				}
				if newAg, err := mgr.AddAgent(sess.ID, cfg); err == nil {
					a.modals.SetLaunchAgent(newAg)
					a.launch.scrollOffset = 0
				} else {
					a.setError(err.Error())
				}
			}
		}
	case "ctrl+w":
		return a.closeLaunchAgent()
	default:
		if msg.Text != "" {
			ag.SendText(msg.Text)
		} else {
			ag.SendKey(xvt.KeyPressEvent(msg))
		}
	}
	return a, nil
}

// closeLaunchAgent kills the currently-focused agent inside the launch view.
// The last agent in a session collapses the view back to the session list;
// otherwise focus moves to a neighbor while the kill runs asynchronously.
func (a App) closeLaunchAgent() (tea.Model, tea.Cmd) {
	sess := a.modals.LaunchSession()
	ag := a.modals.LaunchAgent()
	if sess == nil || ag == nil {
		return a, nil
	}
	agents := sess.Agents()
	if len(agents) == 0 {
		a.closeModal()
		a.launch.scrollOffset = 0
		return a, nil
	}
	oldID := ag.ID
	sessionID := sess.ID
	currentIdx := 0
	for i, candidate := range agents {
		if candidate.ID == oldID {
			currentIdx = i
			break
		}
	}
	if len(agents) == 1 {
		a.closeModal()
		a.launch.scrollOffset = 0
	} else {
		nextIdx := currentIdx + 1
		if currentIdx == len(agents)-1 {
			nextIdx = currentIdx - 1
		}
		a.modals.SetLaunchAgent(agents[nextIdx])
		agents[nextIdx].Resize(a.agentTermRows(), a.agentTermCols())
		a.launch.scrollOffset = 0
	}
	repoPath := a.modals.LaunchRepoPath()
	agentKey := agentCacheKey(repoPath, oldID)
	if a.closingAgents[agentKey] {
		return a, nil
	}
	mgr := a.managers[repoPath]
	if mgr == nil {
		return a, nil
	}
	a.closingAgents[agentKey] = true
	return a, func() tea.Msg {
		err := mgr.KillAgent(sessionID, oldID)
		return killResultMsg{
			scope:     killScopeAgent,
			repoPath:  repoPath,
			sessionID: sessionID,
			agentID:   oldID,
			err:       err,
		}
	}
}

// handleLaunchMouseClick routes a left-click inside the launch view: tab bar
// clicks switch the active agent; clicks inside the terminal seed a text
// selection.
func (a App) handleLaunchMouseClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	sess := a.modals.LaunchSession()
	if sess == nil {
		return a, nil
	}
	tabBarY := a.dashboardTopY() + 1
	if msg.Y == tabBarY {
		if idx := a.launch.tabIndexAt(sess, msg.X); idx >= 0 {
			agents := sess.Agents()
			a.modals.SetLaunchAgent(agents[idx])
			a.launch.scrollOffset = 0
			agents[idx].Resize(a.agentTermRows(), a.agentTermCols())
		}
		return a, nil
	}
	if ag := a.modals.LaunchAgent(); ag != nil {
		if termX, termY, inVP := a.launchTermCellAt(msg.X, msg.Y); inVP {
			a.launch.selection = selection{
				anchorX: termX,
				anchorY: termY,
				cursorX: termX,
				cursorY: termY,
				active:  true,
				agentID: ag.ID,
			}
		} else {
			a.launch.clearSelection()
		}
	}
	return a, nil
}

// handleLaunchMouseMotion extends an in-flight terminal text selection.
func (a App) handleLaunchMouseMotion(msg tea.MouseMotionMsg) (tea.Model, tea.Cmd) {
	if a.launch.selection.active && msg.Button == tea.MouseLeft &&
		a.modals.LaunchAgent() != nil {
		tx, ty, inVP := a.launchTermCellAt(msg.X, msg.Y)
		if inVP {
			a.launch.selection.cursorX = tx
			a.launch.selection.cursorY = ty
			a.launch.selection.dragSeen = true
		}
	}
	return a, nil
}

// handleLaunchMouseRelease finalises a drag-to-select (copying the highlighted
// region to the clipboard) or drops a seeded selection on a plain click.
func (a App) handleLaunchMouseRelease() (tea.Model, tea.Cmd) {
	if !a.launch.selection.active {
		return a, nil
	}
	if !a.launch.selection.dragSeen {
		a.launch.clearSelection()
		return a, nil
	}
	ag := a.modals.LaunchAgent()
	if ag == nil || ag.ID != a.launch.selection.agentID {
		return a, nil
	}
	sx, sy, ex, ey, ok := a.launch.selectionRect()
	if !ok {
		return a, nil
	}
	rect := vt.SelectionRect{StartX: sx, StartY: sy, EndX: ex, EndY: ey, Active: true}
	var text string
	if a.launch.scrollOffset > 0 {
		text = ag.ExtractTextFromSnapshot(a.launch.width, a.launch.termHeight(), a.launch.scrollOffset, rect)
	} else {
		text = ag.ExtractText(rect)
	}
	if text != "" {
		return a, tea.SetClipboard(text)
	}
	return a, nil
}

// handleLaunchMouseWheel scrolls the launch terminal: in alt-screen mode the
// wheel forwards to the PTY as a mouse event; otherwise it adjusts the
// scrollback offset.
func (a App) handleLaunchMouseWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	ag := a.modals.LaunchAgent()
	if ag == nil {
		return a, nil
	}
	if ag.IsAltScreen() {
		termX, termY, _ := a.launchTermCellAt(msg.X, msg.Y)
		if termX < 0 {
			termX = 0
		}
		if termX >= a.launch.width {
			termX = a.launch.width - 1
		}
		if termY < 0 {
			termY = 0
		}
		if termY >= a.launch.termHeight() {
			termY = a.launch.termHeight() - 1
		}
		ag.SendMouse(xvt.MouseWheel{
			X:      termX,
			Y:      termY,
			Button: xvt.MouseButton(msg.Button),
			Mod:    xvt.KeyMod(msg.Mod),
		})
		return a, nil
	}
	if msg.Button == tea.MouseWheelUp {
		maxScroll := len(ag.ScrollbackLines())
		a.launch.scrollOffset += 3
		if a.launch.scrollOffset > maxScroll {
			a.launch.scrollOffset = maxScroll
		}
	} else {
		a.launch.scrollOffset -= 3
		if a.launch.scrollOffset < 0 {
			a.launch.scrollOffset = 0
		}
	}
	return a, nil
}

// focusLaunchTabDot returns the status indicator character for a tab.
func focusLaunchTabDot(ag *agent.Agent) string {
	switch ag.Status() {
	case agent.StatusActive, agent.StatusWaiting:
		return theme.GlyphActive
	case agent.StatusError, agent.StatusDone:
		return theme.GlyphCross
	default:
		return theme.GlyphIdle
	}
}

// focusLaunchTabText returns the plain (unstyled) text for a tab label,
// used by both rendering and click-hit detection so they stay in sync.
func focusLaunchTabText(ag *agent.Agent) string {
	name := truncateVisible(ag.GetDisplayName(), 18)
	return "[" + focusLaunchTabDot(ag) + " " + name + "]"
}

// applySelectionHighlight re-renders lines with reverse-video over the cells
// covered by rect. Styling from the underlying terminal content is stripped on
// selected rows so the highlight is unambiguous.
func applySelectionHighlight(lines []string, rect vt.SelectionRect) string {
	inSel := func(x, y int) bool {
		if !rect.Active || y < rect.StartY || y > rect.EndY {
			return false
		}
		if y > rect.StartY && y < rect.EndY {
			return true
		}
		if rect.StartY == rect.EndY {
			return x >= rect.StartX && x <= rect.EndX
		}
		if y == rect.StartY {
			return x >= rect.StartX
		}
		return x <= rect.EndX
	}
	result := make([]string, len(lines))
	for y, line := range lines {
		if !rect.Active || y < rect.StartY || y > rect.EndY {
			result[y] = line
			continue
		}
		stripped := ansi.Strip(line)
		var b strings.Builder
		col := 0
		inRev := false
		for _, r := range stripped {
			rw := ansi.StringWidth(string(r))
			if rw == 0 {
				continue // combining marks / zero-width chars have no column position
			}
			sel := false
			for c := col; c < col+rw; c++ {
				if inSel(c, y) {
					sel = true
					break
				}
			}
			if sel && !inRev {
				b.WriteString("\x1b[7m")
				inRev = true
			} else if !sel && inRev {
				b.WriteString("\x1b[27m")
				inRev = false
			}
			b.WriteRune(r)
			col += rw
		}
		if inRev {
			b.WriteString("\x1b[27m")
		}
		result[y] = b.String()
	}
	return strings.Join(result, "\n")
}
