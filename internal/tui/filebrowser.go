package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/devenjarvis/refrain/internal/git"
)

// fileBrowserSelectMsg is emitted when the user selects a git repo directory.
type fileBrowserSelectMsg struct{ path string }

// fileBrowserCancelMsg is emitted when the user cancels the file browser.
type fileBrowserCancelMsg struct{}

// fileBrowserModel is a sub-component for browsing and selecting git repo directories.
type fileBrowserModel struct {
	currentDir   string
	entries      []os.DirEntry // only dirs
	filtered     []int         // indices into entries
	selected     int           // index into filtered
	scrollOffset int
	filter       string
	showHidden   bool
	isGitRepo    bool   // cached git.IsRepo for selected entry
	gitBranch    string // cached branch name for selected entry
	width        int
	height       int
}

// newFileBrowserModel creates a fileBrowserModel starting at the user's home directory.
func newFileBrowserModel() fileBrowserModel {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	m := fileBrowserModel{
		currentDir: home,
	}
	m.entries = loadEntries(home, false)
	m.applyFilter()
	m.refreshGitStatus()
	return m
}

// loadEntries reads subdirectories from dir. Hidden dirs (starting with ".") are
// included only when showHidden is true.
func loadEntries(dir string, showHidden bool) []os.DirEntry {
	all, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	entries := make([]os.DirEntry, 0, len(all))
	for _, e := range all {
		if !e.IsDir() {
			continue
		}
		if !showHidden && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		entries = append(entries, e)
	}
	return entries
}

// Update handles key events for the file browser.
func (m fileBrowserModel) Update(msg tea.Msg) (fileBrowserModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		key := msg.String()
		switch key {
		case "j", "down":
			if m.selected < len(m.filtered)-1 {
				m.selected++
				m.refreshGitStatus()
			}
		case "k", "up":
			if m.selected > 0 {
				m.selected--
				m.refreshGitStatus()
			}
		case "enter":
			if len(m.filtered) == 0 {
				break
			}
			entry := m.entries[m.filtered[m.selected]]
			path := filepath.Join(m.currentDir, entry.Name())
			if m.isGitRepo {
				return m, func() tea.Msg { return fileBrowserSelectMsg{path: path} }
			}
			// Descend into the directory.
			m.currentDir = path
			m.entries = loadEntries(path, m.showHidden)
			m.filter = ""
			m.scrollOffset = 0
			m.applyFilter()
			m.selected = 0
			m.refreshGitStatus()
		case "backspace":
			if len(m.filter) > 0 {
				m.filter = m.filter[:len(m.filter)-1]
				m.applyFilter()
				m.refreshGitStatus()
			} else {
				parent := filepath.Dir(m.currentDir)
				if parent != m.currentDir {
					m.currentDir = parent
					m.entries = loadEntries(parent, m.showHidden)
					m.filter = ""
					m.scrollOffset = 0
					m.applyFilter()
					m.selected = 0
					m.refreshGitStatus()
				}
			}
		case "esc":
			return m, func() tea.Msg { return fileBrowserCancelMsg{} }
		case ".":
			// With a filter active, treat `.` as a normal filter character so that
			// names like "next.js" or "v2.0" are reachable. Toggle hidden only when
			// the filter is empty.
			if m.filter != "" {
				m.filter += "."
				m.applyFilter()
				m.refreshGitStatus()
				break
			}
			m.showHidden = !m.showHidden
			m.entries = loadEntries(m.currentDir, m.showHidden)
			m.scrollOffset = 0
			m.applyFilter()
			if m.selected >= len(m.filtered) {
				m.selected = max(0, len(m.filtered)-1)
			}
			m.refreshGitStatus()
		default:
			// Single printable character → add to filter.
			if len(key) == 1 && key[0] >= ' ' && key[0] <= '~' {
				m.filter += key
				m.applyFilter()
				m.refreshGitStatus()
			}
		}
	}
	m.ensureVisible()
	return m, nil
}

// View renders the two-panel file browser.
func (m fileBrowserModel) View() string {
	leftWidth, rightWidth := splitColumns(m.width, columnStrategy{num: 1, den: 3, min: 20}, separatorWidth)

	left := m.renderDirList(leftWidth)
	right := m.renderDetails(rightWidth)

	leftStyle := lipgloss.NewStyle().
		Width(leftWidth).
		Height(m.height).
		Border(lipgloss.NormalBorder(), false, true, false, false).
		BorderForeground(ColorMuted)

	rightStyle := lipgloss.NewStyle().
		Width(rightWidth).
		Height(m.height)

	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		leftStyle.Render(left),
		rightStyle.Render(right),
	)
}

// renderBreadcrumb renders the currentDir as a styled breadcrumb path.
// Substitutes "~" for the home directory prefix. Truncates from the left with "…"
// when the rendered width exceeds maxWidth.
func (m fileBrowserModel) renderBreadcrumb(maxWidth int) string {
	if maxWidth < 1 {
		maxWidth = 1
	}

	// Substitute home directory prefix with ~.
	display := m.currentDir
	leadingHome := false
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if display == home {
			display = "~"
			leadingHome = true
		} else if strings.HasPrefix(display, home+string(filepath.Separator)) {
			display = "~" + display[len(home):]
			leadingHome = true
		}
	}

	// Split into segments. Preserve a leading "/" for absolute paths that don't start with ~.
	leadingRoot := !leadingHome && strings.HasPrefix(display, string(filepath.Separator))
	trimmed := strings.Trim(display, string(filepath.Separator))
	var segments []string
	if trimmed != "" {
		segments = strings.Split(trimmed, string(filepath.Separator))
	}

	sep := StyleSubtle.Render(" / ")
	sepWidth := 3 // " / " is 3 ASCII cells

	// Build from the right, dropping leading segments until it fits.
	// Widths are measured in display cells via lipgloss.Width so that multi-byte
	// and wide (CJK) segment names are handled correctly.
	build := func(start int, elided bool) (string, int) {
		var styled strings.Builder
		width := 0

		if elided {
			styled.WriteString(StyleSubtle.Render("…"))
			width++
		} else if leadingRoot {
			styled.WriteString(StyleSubtle.Render("/"))
			width++
		}

		for i := start; i < len(segments); i++ {
			if i > start || elided || leadingRoot {
				styled.WriteString(sep)
				width += sepWidth
			}
			styled.WriteString(segments[i])
			width += lipgloss.Width(segments[i])
		}
		return styled.String(), width
	}

	// Handle empty (e.g. just "~" with no trailing segments — shouldn't happen normally).
	if len(segments) == 0 {
		if leadingHome {
			return "~"
		}
		if leadingRoot {
			return StyleSubtle.Render("/")
		}
		return ""
	}

	// Try full, then progressively elide from the left.
	for start := 0; start < len(segments); start++ {
		out, width := build(start, start > 0)
		if width <= maxWidth {
			return out
		}
	}

	// Even the last segment alone is too wide — fall back to a compact form.
	// If the panel is very narrow (maxWidth < "… / x" = 5 cells), just render "…".
	if maxWidth < sepWidth+2 {
		return StyleSubtle.Render("…")
	}
	last := segments[len(segments)-1]
	// Reserve space for "… / ".
	room := maxWidth - (1 + sepWidth) // 1 for "…"
	// Rune-safe truncation to avoid emitting invalid UTF-8 mid-codepoint.
	// This may slightly over-truncate for wide (CJK) chars but never under-truncates.
	if lipgloss.Width(last) > room {
		runes := []rune(last)
		for len(runes) > 0 && lipgloss.Width(string(runes)) > room {
			runes = runes[:len(runes)-1]
		}
		last = string(runes)
	}
	return StyleSubtle.Render("…") + sep + last
}

// renderDirList renders the left panel with the directory listing.
func (m fileBrowserModel) renderDirList(width int) string {
	title := StyleTitle.Render("DIRECTORIES")
	sepWidth := innerWidth(width)
	if sepWidth < 0 {
		sepWidth = 0
	}
	separator := StyleSubtle.Render(strings.Repeat("─", sepWidth))

	lines := make([]string, 0, 6+len(m.filtered))
	lines = append(lines, title, separator)
	lines = append(lines, m.renderBreadcrumb(innerWidth(width)))
	if m.showHidden {
		lines = append(lines, StyleSubtle.Render("(showing hidden)"))
	}

	// Filter indicator.
	if m.filter != "" {
		lines = append(lines, StyleSubtle.Render("filter: ")+m.filter)
	}
	lines = append(lines, "")

	if len(m.filtered) == 0 {
		if m.filter != "" {
			lines = append(lines, StyleSubtle.Render("  No matches"))
		} else {
			lines = append(lines, StyleSubtle.Render("  (empty)"))
		}
		return strings.Join(lines, "\n")
	}

	// Use the same visible-lines budget as ensureVisible so the selected entry
	// is never scrolled off-screen. This reserves space for both scroll indicators.
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

		name := m.entries[idx].Name()
		maxLen := modalContentWidth(width)
		if len(name) > maxLen {
			name = name[:maxLen-1] + "…"
		}

		lines = append(lines, prefix+name)
	}

	if below > 0 {
		lines = append(lines, StyleSubtle.Render(fmt.Sprintf("  ↓ %d more", below)))
	}

	return strings.Join(lines, "\n")
}

// renderDetails renders the right panel with info about the selected directory.
func (m fileBrowserModel) renderDetails(width int) string {
	title := StyleTitle.Render("DETAILS")
	sepWidth := width - 1
	if sepWidth < 0 {
		sepWidth = 0
	}
	separator := StyleSubtle.Render(strings.Repeat("─", sepWidth))

	var lines []string
	lines = append(lines, title, separator)

	if len(m.filtered) == 0 {
		lines = append(lines, StyleSubtle.Render("No subdirectories"))
		return strings.Join(lines, "\n")
	}

	entry := m.entries[m.filtered[m.selected]]
	path := filepath.Join(m.currentDir, entry.Name())

	lines = append(lines, "")
	lines = append(lines, StyleTitle.Render(entry.Name()))
	lines = append(lines, StyleSubtle.Render(path))
	lines = append(lines, "")

	if m.isGitRepo {
		lines = append(lines, StyleSuccess.Render("git repo"))
		lines = append(lines, StyleSubtle.Render("branch: ")+m.gitBranch)
		lines = append(lines, "")
		lines = append(lines, StyleSubtle.Render("Press enter to select"))
	} else {
		lines = append(lines, StyleSubtle.Render("not a git repo"))
		lines = append(lines, "")
		lines = append(lines, StyleSubtle.Render("Press enter to open"))
	}

	return strings.Join(lines, "\n")
}

// ensureVisible adjusts scrollOffset so that selected is within the visible window.
func (m *fileBrowserModel) ensureVisible() {
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
func (m fileBrowserModel) visibleEntryLines() int {
	// Header: title + separator + breadcrumb + optional hidden + optional filter + blank.
	headerLines := 4
	if m.showHidden {
		headerLines++
	}
	if m.filter != "" {
		headerLines++
	}
	// Reserve space for both scroll indicators conservatively. This may scroll one
	// row earlier than strictly necessary, but guarantees selected is never clipped.
	visible := m.height - headerLines - 2
	if visible < 1 {
		visible = 1
	}
	return visible
}

// applyFilter updates the filtered indices based on the current filter string.
func (m *fileBrowserModel) applyFilter() {
	m.filtered = nil
	lower := strings.ToLower(m.filter)
	for i, e := range m.entries {
		if lower == "" || strings.Contains(strings.ToLower(e.Name()), lower) {
			m.filtered = append(m.filtered, i)
		}
	}
	if m.selected >= len(m.filtered) {
		m.selected = max(0, len(m.filtered)-1)
	}
}

// refreshGitStatus updates the cached isGitRepo and gitBranch for the selected entry.
func (m *fileBrowserModel) refreshGitStatus() {
	if len(m.filtered) == 0 {
		m.isGitRepo = false
		m.gitBranch = ""
		return
	}
	path := filepath.Join(m.currentDir, m.entries[m.filtered[m.selected]].Name())
	m.isGitRepo = git.IsRepo(path)
	if m.isGitRepo {
		branch, err := git.BaseBranch(path)
		if err != nil {
			branch = "(unknown)"
		}
		m.gitBranch = branch
	} else {
		m.gitBranch = ""
	}
}
