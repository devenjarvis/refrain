package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/devenjarvis/refrain/internal/git"
	"github.com/devenjarvis/refrain/internal/github"
	"github.com/devenjarvis/refrain/internal/tui/theme"
)

// branchPickerItem represents a single entry in the branch picker.
type branchPickerItem struct {
	kind       string // "pr", "local", "remote"
	branch     string
	baseBranch string // only for PRs
	prNumber   int    // only for PRs
	prTitle    string // only for PRs
}

// branchPickerDataMsg carries the async-loaded picker data.
type branchPickerDataMsg struct {
	prs    []branchPickerItem
	local  []branchPickerItem
	remote []branchPickerItem
	err    error
}

// branchPickerSelectMsg is emitted when the user picks an item.
type branchPickerSelectMsg struct {
	item branchPickerItem
}

// branchPickerCancelMsg is emitted when the user cancels.
type branchPickerCancelMsg struct{}

// branchPickerModel is a sub-component for selecting a branch or PR to open.
type branchPickerModel struct {
	items    []branchPickerItem
	filtered []int // indices into items
	selected int   // index into filtered
	filter   string
	loading  bool
	loadErr  string

	width  int
	height int
}

// newBranchPickerModel creates a new branch picker in loading state.
func newBranchPickerModel() branchPickerModel {
	return branchPickerModel{
		loading: true,
	}
}

// loadBranchPickerData returns a tea.Cmd that loads PRs and branches concurrently.
func loadBranchPickerData(repoPath string, ghClient *github.Client, activeBranches map[string]bool) tea.Cmd {
	return func() tea.Msg {
		// Detect the repo's current HEAD branch (e.g. "main") to exclude it from the list.
		headBranch, _ := git.BaseBranch(repoPath)
		var prs []branchPickerItem
		var local []branchPickerItem
		var remote []branchPickerItem
		var prErr error

		// Load PRs if GitHub client is available.
		if ghClient != nil {
			rawURL, err := git.GetRemoteURL(repoPath)
			if err == nil {
				owner, repo, err := github.ParseRemoteURL(rawURL)
				if err == nil {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					prList, err := ghClient.ListPRs(ctx, owner, repo)
					if err != nil {
						prErr = err
					} else {
						for _, pr := range prList {
							if activeBranches[pr.HeadBranch] {
								continue
							}
							prs = append(prs, branchPickerItem{
								kind:       "pr",
								branch:     pr.HeadBranch,
								baseBranch: pr.BaseBranch,
								prNumber:   pr.Number,
								prTitle:    pr.Title,
							})
						}
					}
				}
			}
		}

		// Load local branches.
		localBranches, err := git.ListLocalBranches(repoPath)
		if err == nil {
			for _, b := range localBranches {
				if activeBranches[b] || b == headBranch {
					continue
				}
				local = append(local, branchPickerItem{
					kind:   "local",
					branch: b,
				})
			}
		}

		// Load remote branches.
		remoteBranches, err := git.ListRemoteBranches(repoPath)
		if err == nil {
			for _, b := range remoteBranches {
				if activeBranches[b] || b == headBranch {
					continue
				}
				// Skip remote branches that are also local (avoid duplicates).
				isLocal := false
				for _, lb := range localBranches {
					if lb == b {
						isLocal = true
						break
					}
				}
				// Also skip branches that appear as PR head branches.
				isPR := false
				for _, pr := range prs {
					if pr.branch == b {
						isPR = true
						break
					}
				}
				if !isLocal && !isPR {
					remote = append(remote, branchPickerItem{
						kind:   "remote",
						branch: b,
					})
				}
			}
		}

		// If PR loading failed but we got branches, just return what we have.
		if len(local) == 0 && len(remote) == 0 && prErr != nil {
			return branchPickerDataMsg{err: prErr}
		}

		return branchPickerDataMsg{
			prs:    prs,
			local:  local,
			remote: remote,
		}
	}
}

// Update handles key events for the branch picker.
func (m branchPickerModel) Update(msg tea.Msg) (branchPickerModel, tea.Cmd) {
	switch msg := msg.(type) {
	case branchPickerDataMsg:
		m.loading = false
		if msg.err != nil {
			m.loadErr = msg.err.Error()
			return m, nil
		}
		// Combine items: PRs first, then local, then remote.
		m.items = nil
		m.items = append(m.items, msg.prs...)
		m.items = append(m.items, msg.local...)
		m.items = append(m.items, msg.remote...)
		m.applyFilter()
		return m, nil

	case tea.KeyPressMsg:
		key := msg.String()
		switch key {
		case "esc":
			return m, func() tea.Msg { return branchPickerCancelMsg{} }
		case "up", "k":
			m.selected = clampedMove(m.selected, -1, len(m.filtered))
		case "down", "j":
			m.selected = clampedMove(m.selected, 1, len(m.filtered))
		case "enter":
			if len(m.filtered) > 0 && m.selected < len(m.filtered) {
				item := m.items[m.filtered[m.selected]]
				return m, func() tea.Msg { return branchPickerSelectMsg{item: item} }
			}
		case "backspace":
			if len(m.filter) > 0 {
				m.filter = m.filter[:len(m.filter)-1]
				m.applyFilter()
			}
		default:
			// Single printable character → add to filter.
			if len(key) == 1 && key[0] >= ' ' && key[0] <= '~' {
				m.filter += key
				m.applyFilter()
			}
		}
	}
	return m, nil
}

// applyFilter updates the filtered indices based on the current filter string.
func (m *branchPickerModel) applyFilter() {
	m.filtered = nil
	lower := strings.ToLower(m.filter)
	for i, item := range m.items {
		if lower == "" {
			m.filtered = append(m.filtered, i)
			continue
		}
		if strings.Contains(strings.ToLower(item.branch), lower) {
			m.filtered = append(m.filtered, i)
			continue
		}
		if item.prTitle != "" && strings.Contains(strings.ToLower(item.prTitle), lower) {
			m.filtered = append(m.filtered, i)
		}
	}
	if m.selected >= len(m.filtered) {
		m.selected = max(0, len(m.filtered)-1)
	}
}

// View renders the branch picker as a two-panel overlay.
func (m branchPickerModel) View() string {
	leftWidth, rightWidth := splitColumns(m.width, columnStrategy{num: 1, den: 3, min: 25}, separatorWidth)

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

// renderList renders the left panel with the grouped branch list.
func (m branchPickerModel) renderList(width int) string {
	title := StyleTitle.Render("OPEN BRANCH")
	sepWidth := innerWidth(width)
	if sepWidth < 0 {
		sepWidth = 0
	}
	separator := StyleSubtle.Render(strings.Repeat("─", sepWidth))

	lines := make([]string, 0, len(m.filtered)+6)
	lines = append(lines, title, separator)

	// Filter indicator.
	if m.filter != "" {
		lines = append(lines, StyleSubtle.Render("filter: ")+m.filter)
	}
	lines = append(lines, "")

	if m.loading {
		lines = append(lines, StyleSubtle.Render("  Loading..."))
		return strings.Join(lines, "\n")
	}

	if m.loadErr != "" {
		lines = append(lines, StyleWarning.Render("  Error: "+m.loadErr))
		return strings.Join(lines, "\n")
	}

	if len(m.filtered) == 0 {
		if m.filter != "" {
			lines = append(lines, StyleSubtle.Render("  No matches"))
		} else {
			lines = append(lines, StyleSubtle.Render("  No branches available"))
		}
		return strings.Join(lines, "\n")
	}

	// Group items by kind for display.
	lastKind := ""
	for fi, idx := range m.filtered {
		item := m.items[idx]

		// Section headers.
		if item.kind != lastKind {
			if lastKind != "" {
				lines = append(lines, "")
			}
			switch item.kind {
			case "pr":
				lines = append(lines, StyleTitle.Render("Pull Requests"))
			case "local":
				lines = append(lines, StyleTitle.Render("Local Branches"))
			case "remote":
				lines = append(lines, StyleTitle.Render("Remote Branches"))
			}
			lastKind = item.kind
		}

		prefix := "  "
		if fi == m.selected {
			prefix = StyleActive.Render("▸ ")
		}

		label := item.branch
		if item.kind == "pr" {
			label = fmt.Sprintf("#%d %s", item.prNumber, item.branch)
		}
		maxLen := modalContentWidth(width)
		if len(label) > maxLen && maxLen > 3 {
			label = label[:maxLen-1] + "…"
		}
		lines = append(lines, prefix+label)
	}

	return strings.Join(lines, "\n")
}

// renderDetails renders the right panel with details about the selected item.
func (m branchPickerModel) renderDetails(width int) string {
	title := StyleTitle.Render("DETAILS")
	sepWidth := width - 1
	if sepWidth < 0 {
		sepWidth = 0
	}
	separator := StyleSubtle.Render(strings.Repeat("─", sepWidth))

	var lines []string
	lines = append(lines, title, separator)

	if m.loading {
		lines = append(lines, "", StyleSubtle.Render("Loading branch data..."))
		return strings.Join(lines, "\n")
	}

	if len(m.filtered) == 0 || m.selected >= len(m.filtered) {
		lines = append(lines, "", StyleSubtle.Render("No item selected"))
		return strings.Join(lines, "\n")
	}

	item := m.items[m.filtered[m.selected]]
	lines = append(lines, "")

	switch item.kind {
	case "pr":
		lines = append(lines, StyleTitle.Render(fmt.Sprintf("PR #%d", item.prNumber)))
		lines = append(lines, "")
		lines = append(lines, StyleSubtle.Render("Title: ")+item.prTitle)
		lines = append(lines, StyleSubtle.Render("Branch: ")+item.branch)
		lines = append(lines, StyleSubtle.Render("Base: ")+item.baseBranch)
		lines = append(lines, "")
		lines = append(lines, StyleSubtle.Render("Press enter to open session on this PR's branch"))
	case "local":
		lines = append(lines, StyleTitle.Render(item.branch))
		lines = append(lines, "")
		lines = append(lines, StyleSuccess.Render("local branch"))
		lines = append(lines, "")
		lines = append(lines, StyleSubtle.Render("Press enter to open session on this branch"))
	case "remote":
		lines = append(lines, StyleTitle.Render(item.branch))
		lines = append(lines, "")
		lines = append(lines, StyleSubtle.Render("remote branch (will be fetched)"))
		lines = append(lines, "")
		lines = append(lines, StyleSubtle.Render("Press enter to open session on this branch"))
	}

	return strings.Join(lines, "\n")
}
