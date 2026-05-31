package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/devenjarvis/refrain/internal/config"
	"github.com/devenjarvis/refrain/internal/tui/theme"
)

// repoPickerAddRepoIdx is the sentinel value stored in repoPickerModel.filtered
// to mark the synthetic "+ add new repo…" row, distinct from any real index
// into repoPickerModel.repos.
const repoPickerAddRepoIdx = -1

// repoPickerMode controls which action enter/s/d perform.
type repoPickerMode int

const (
	repoPickerModeSession repoPickerMode = iota // default: enter starts a session
	repoPickerModeManage                        // enter switches active repo, s/d manage
)

// repoPickerSelectMsg is emitted when the user picks a registered repo.
type repoPickerSelectMsg struct{ path string }

// repoPickerSwitchActiveMsg is emitted in manage mode when the user presses enter on a repo.
type repoPickerSwitchActiveMsg struct{ path string }

// repoPickerEditSettingsMsg is emitted in manage mode when the user presses s.
type repoPickerEditSettingsMsg struct{ path string }

// repoPickerRemoveMsg is emitted in manage mode when the user confirms removal with d+d.
type repoPickerRemoveMsg struct{ path string }

// repoPickerAddRepoMsg is emitted when the user picks the add-repo entry or
// presses the `a` shortcut.
type repoPickerAddRepoMsg struct{}

// repoPickerCancelMsg is emitted when the user presses esc.
type repoPickerCancelMsg struct{}

// repoPickerModel is a sub-component for picking a registered repo to start a
// session in, with type-to-filter and an inline add-repo entry.
type repoPickerModel struct {
	repos        []config.Repo
	counts       map[string]int // keyed by repo path; missing → 0
	filtered     []int          // indices into repos; repoPickerAddRepoIdx marks the add-repo entry
	selected     int            // index into filtered
	scrollOffset int
	filter       string

	mode             repoPickerMode
	pendingRemoveIdx int // filtered-row index awaiting d+d confirm; -1 means none

	width  int
	height int
}

// newRepoPickerModel returns an empty picker. Call setRepos before use.
func newRepoPickerModel() repoPickerModel {
	return repoPickerModel{pendingRemoveIdx: -1}
}

// SetMode sets the picker mode and resets any pending-confirm state.
func (m *repoPickerModel) SetMode(mode repoPickerMode) {
	m.mode = mode
	m.pendingRemoveIdx = -1
}

// setRepos populates the picker's data and selects initialPath if present.
// Safe to call repeatedly to refresh after an add-repo round-trip.
func (m *repoPickerModel) setRepos(repos []config.Repo, counts map[string]int, initialPath string) {
	m.repos = repos
	m.counts = counts
	if m.counts == nil {
		m.counts = map[string]int{}
	}
	m.filter = ""
	m.scrollOffset = 0
	m.selected = 0
	m.pendingRemoveIdx = -1
	m.applyFilter()
	if initialPath != "" {
		for i, idx := range m.filtered {
			if idx >= 0 && idx < len(m.repos) && m.repos[idx].Path == initialPath {
				m.selected = i
				break
			}
		}
	}
	m.ensureVisible()
}

// Update handles key events for the picker.
func (m repoPickerModel) Update(msg tea.Msg) (repoPickerModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		key := msg.String()
		switch key {
		case "esc":
			return m, func() tea.Msg { return repoPickerCancelMsg{} }
		case "up", "k":
			m.pendingRemoveIdx = -1
			m.selected = clampedMove(m.selected, -1, len(m.filtered))
		case "down", "j":
			m.pendingRemoveIdx = -1
			m.selected = clampedMove(m.selected, 1, len(m.filtered))
		case "enter":
			if len(m.filtered) == 0 || m.selected >= len(m.filtered) {
				break
			}
			idx := m.filtered[m.selected]
			if idx == repoPickerAddRepoIdx {
				return m, func() tea.Msg { return repoPickerAddRepoMsg{} }
			}
			path := m.repos[idx].Path
			if m.mode == repoPickerModeManage {
				return m, func() tea.Msg { return repoPickerSwitchActiveMsg{path: path} }
			}
			return m, func() tea.Msg { return repoPickerSelectMsg{path: path} }
		case "a":
			// Mirror filebrowser's `.` pattern: when the filter is active,
			// treat `a` as a normal filter character so repo names like "alpha"
			// stay reachable. Only triggers add-repo when the filter is empty.
			if m.filter == "" {
				m.pendingRemoveIdx = -1
				return m, func() tea.Msg { return repoPickerAddRepoMsg{} }
			}
			m.filter += key
			m.applyFilter()
		case "s":
			if m.mode == repoPickerModeManage && m.filter == "" {
				if len(m.filtered) == 0 || m.selected >= len(m.filtered) {
					break
				}
				idx := m.filtered[m.selected]
				if idx == repoPickerAddRepoIdx {
					break
				}
				path := m.repos[idx].Path
				m.pendingRemoveIdx = -1
				return m, func() tea.Msg { return repoPickerEditSettingsMsg{path: path} }
			}
			m.pendingRemoveIdx = -1
			m.filter += key
			m.applyFilter()
		case "d":
			if m.mode == repoPickerModeManage && m.filter == "" {
				if len(m.filtered) == 0 || m.selected >= len(m.filtered) {
					break
				}
				idx := m.filtered[m.selected]
				if idx == repoPickerAddRepoIdx {
					break
				}
				if m.pendingRemoveIdx == m.selected {
					// Second d on the same row: emit remove.
					path := m.repos[idx].Path
					m.pendingRemoveIdx = -1
					return m, func() tea.Msg { return repoPickerRemoveMsg{path: path} }
				}
				// First d: mark for confirm.
				m.pendingRemoveIdx = m.selected
				break
			}
			m.pendingRemoveIdx = -1
			m.filter += key
			m.applyFilter()
		case "backspace":
			m.pendingRemoveIdx = -1
			if len(m.filter) > 0 {
				m.filter = m.filter[:len(m.filter)-1]
				m.applyFilter()
			}
		default:
			m.pendingRemoveIdx = -1
			if len(key) == 1 && key[0] >= ' ' && key[0] <= '~' {
				m.filter += key
				m.applyFilter()
			}
		}
	}
	m.ensureVisible()
	return m, nil
}

// applyFilter rebuilds the filtered slice and always appends the add-repo
// sentinel as the final entry.
func (m *repoPickerModel) applyFilter() {
	m.filtered = m.filtered[:0]
	lower := strings.ToLower(m.filter)
	for i, r := range m.repos {
		if lower == "" {
			m.filtered = append(m.filtered, i)
			continue
		}
		if strings.Contains(strings.ToLower(r.DisplayName()), lower) ||
			strings.Contains(strings.ToLower(r.Path), lower) {
			m.filtered = append(m.filtered, i)
		}
	}
	m.filtered = append(m.filtered, repoPickerAddRepoIdx)
	if m.selected >= len(m.filtered) {
		m.selected = max(0, len(m.filtered)-1)
	}
}

// View renders the two-panel picker. The App wraps this with a statusbar.
func (m repoPickerModel) View() string {
	leftWidth, rightWidth := splitColumns(m.width, columnStrategy{num: 1, den: 2, min: 30, reserve: 20}, separatorWidth)

	left := m.renderList(leftWidth)
	right := m.renderDetails(rightWidth)

	leftStyle := theme.BorderPaneLeft().
		Width(leftWidth).
		Height(m.height)

	rightStyle := lipgloss.NewStyle().
		Width(rightWidth).
		Height(m.height)

	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		leftStyle.Render(left),
		rightStyle.Render(right),
	)
}

// renderList renders the left panel with the filtered repo list.
func (m repoPickerModel) renderList(width int) string {
	titleText := "NEW SESSION IN…"
	if m.mode == repoPickerModeManage {
		titleText = "MANAGE REPOS"
	}
	title := StyleTitle.Render(titleText)
	sepWidth := innerWidth(width)
	if sepWidth < 0 {
		sepWidth = 0
	}
	separator := StyleSubtle.Render(strings.Repeat("─", sepWidth))

	lines := make([]string, 0, len(m.filtered)+6)
	lines = append(lines, title, separator)

	if m.filter != "" {
		lines = append(lines, StyleSubtle.Render("filter: ")+m.filter)
	}
	lines = append(lines, "")

	if len(m.filtered) == 0 {
		// applyFilter always appends the add-repo entry, so this is a defensive guard.
		lines = append(lines, StyleSubtle.Render("  (no repos)"))
		return strings.Join(lines, "\n")
	}

	visibleLines := m.visibleEntryLines()
	total := len(m.filtered)
	above := m.scrollOffset
	end := m.scrollOffset + visibleLines
	if end > total {
		end = total
	}
	below := total - end

	if above > 0 {
		lines = append(lines, StyleSubtle.Render(fmt.Sprintf("  ↑ %d more", above)))
	}

	for fi := m.scrollOffset; fi < end; fi++ {
		idx := m.filtered[fi]
		prefix := "  "
		if fi == m.selected {
			prefix = StyleActive.Render("▸ ")
		}

		if idx == repoPickerAddRepoIdx {
			label := "+ add new repo…"
			lines = append(lines, prefix+StyleActive.Render(truncateVisible(label, modalContentWidth(width))))
			continue
		}

		repo := m.repos[idx]
		count := m.counts[repo.Path]
		countLabel := "—"
		if count > 0 {
			countLabel = fmt.Sprintf("%d active", count)
		}

		// Reserve room: prefix (2) + countLabel + 1 space gap.
		countWidth := lipgloss.Width(countLabel)
		// Inner width available for name + path; allow a couple cells of padding.
		nameRoom := innerWidth(width) - countWidth - 2
		if nameRoom < 4 {
			nameRoom = 4
		}

		name := repo.DisplayName()
		shortPath := compactHomePath(repo.Path)

		// Layout: "name  shortPath ……  countLabel"
		// Compute how much space the name+path block can use, then truncate.
		nameW := lipgloss.Width(name)
		gap := 2
		pathRoom := nameRoom - nameW - gap
		var leftBlock string
		if pathRoom > 1 {
			truncatedPath := StyleSubtle.Render(truncateVisible(shortPath, pathRoom))
			leftBlock = name + strings.Repeat(" ", gap) + truncatedPath
		} else {
			leftBlock = truncateVisible(name, nameRoom)
		}

		// Pad leftBlock so countLabel sits at the right edge.
		leftBlockW := lipgloss.Width(leftBlock)
		padding := nameRoom - leftBlockW
		if padding < 1 {
			padding = 1
		}
		row := prefix + leftBlock + strings.Repeat(" ", padding) + StyleSubtle.Render(countLabel)
		lines = append(lines, row)
	}

	if below > 0 {
		lines = append(lines, StyleSubtle.Render(fmt.Sprintf("  ↓ %d more", below)))
	}

	return strings.Join(lines, "\n")
}

// renderDetails renders the right panel with details about the selected entry.
func (m repoPickerModel) renderDetails(width int) string {
	title := StyleTitle.Render("DETAILS")
	sepWidth := width - 1
	if sepWidth < 0 {
		sepWidth = 0
	}
	separator := StyleSubtle.Render(strings.Repeat("─", sepWidth))

	var lines []string
	lines = append(lines, title, separator, "")

	if len(m.filtered) == 0 || m.selected >= len(m.filtered) {
		lines = append(lines, StyleSubtle.Render("No item selected"))
		return strings.Join(lines, "\n")
	}

	idx := m.filtered[m.selected]
	if idx == repoPickerAddRepoIdx {
		lines = append(lines, StyleTitle.Render("Add new repo"))
		lines = append(lines, "")
		lines = append(lines, StyleSubtle.Render("Opens the file browser to register a new repo."))
		lines = append(lines, "")
		lines = append(lines, StyleSubtle.Render("Press enter to continue"))
		return strings.Join(lines, "\n")
	}

	repo := m.repos[idx]
	count := m.counts[repo.Path]
	countLabel := "no active sessions"
	if count == 1 {
		countLabel = "1 active session"
	} else if count > 1 {
		countLabel = fmt.Sprintf("%d active sessions", count)
	}

	lines = append(lines, StyleTitle.Render(repo.DisplayName()))
	lines = append(lines, StyleSubtle.Render(repo.Path))
	lines = append(lines, "")
	lines = append(lines, StyleSubtle.Render(countLabel))
	lines = append(lines, "")

	if m.mode == repoPickerModeManage {
		if m.pendingRemoveIdx == m.selected {
			lines = append(lines, StyleWarning.Render("delete "+repo.DisplayName()+"?"))
			lines = append(lines, StyleSubtle.Render("d again to confirm · any other key to cancel"))
		} else {
			lines = append(lines, StyleSubtle.Render("enter switch · s settings · d remove"))
		}
	} else {
		lines = append(lines, StyleSubtle.Render("Press enter to start a session in this repo"))
	}
	return strings.Join(lines, "\n")
}

// ensureVisible adjusts scrollOffset so that selected is within the visible window.
func (m *repoPickerModel) ensureVisible() {
	visibleLines := m.visibleEntryLines()
	if m.selected < m.scrollOffset {
		m.scrollOffset = m.selected
	}
	if m.selected >= m.scrollOffset+visibleLines {
		m.scrollOffset = m.selected - visibleLines + 1
	}
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
}

// visibleEntryLines returns the number of entry rows that fit in the left panel,
// reserving space for header lines and (conservatively) both scroll indicators.
func (m repoPickerModel) visibleEntryLines() int {
	headerLines := 3 // title + separator + blank
	if m.filter != "" {
		headerLines++
	}
	visible := m.height - headerLines - 2
	if visible < 1 {
		visible = 1
	}
	return visible
}

// compactHomePath returns p with the user's home directory replaced by "~".
// Used to keep repo paths short in the picker rows.
func compactHomePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}
