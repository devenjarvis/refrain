package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/refrain/internal/diffmodel"
	tuidiff "github.com/devenjarvis/refrain/internal/tui/diff"
)

// focusedDiffModel returns the parsed diff model for the cursor-selected task,
// a pointer to the corresponding taskReviewGroup, and ok=true. Returns ok=false
// when the focused group has no rawDiff (right pane renders an empty state).
// Results are cached in m.parsedDiffs keyed by taskIndex so re-visits are
// instant and re-parses don't hit the render hot path.
func (m *reviewPanelModel) focusedDiffModel(entry *reviewDiffEntry) (*diffmodel.Model, *taskReviewGroup, bool) {
	group := reviewTaskGroupAtCursor(entry, m.taskCursor)
	if group == nil || group.rawDiff == "" {
		return nil, group, false
	}

	if m.parsedDiffs == nil {
		m.parsedDiffs = make(map[int]*diffmodel.Model)
	}
	if cached, ok := m.parsedDiffs[group.taskIndex]; ok {
		return cached, group, true
	}

	parsed, err := diffmodel.Parse(group.rawDiff)
	if err != nil || parsed == nil {
		return nil, group, false
	}
	m.parsedDiffs[group.taskIndex] = parsed
	return parsed, group, true
}

// refreshDiffPane updates the embedded viewport to show the focused task's diff
// at vpFileIdx. Safe to call from View() since it uses pointer receivers.
func (m *reviewPanelModel) refreshDiffPane(entry *reviewDiffEntry, paneW, paneH int) {
	m.vp.SetWidth(paneW)
	m.vp.SetHeight(paneH)

	parsed, _, ok := m.focusedDiffModel(entry)
	if !ok || parsed == nil || len(parsed.Files) == 0 {
		m.vp.SetContent("")
		return
	}

	// Clamp vpFileIdx to valid range.
	if m.vpFileIdx < 0 {
		m.vpFileIdx = 0
	}
	if m.vpFileIdx >= len(parsed.Files) {
		m.vpFileIdx = len(parsed.Files) - 1
	}
	f := &parsed.Files[m.vpFileIdx]

	if m.renderersByPath == nil {
		m.renderersByPath = make(map[string]*tuidiff.Renderer)
	}
	r, ok := m.renderersByPath[f.Path]
	if !ok {
		r = tuidiff.NewRenderer(f)
		m.renderersByPath[f.Path] = r
	}
	m.vp.SetContent(r.Render(paneW, m.sideBySide))
}

// renderTaskDiffPane renders the right-pane diff content: a single-line file
// header followed by the viewport's current content.
func (m *reviewPanelModel) renderTaskDiffPane(entry *reviewDiffEntry, width, height int) string {
	parsed, _, ok := m.focusedDiffModel(entry)

	// File-name header line.
	var headerLine string
	if ok && parsed != nil && len(parsed.Files) > 0 {
		if m.vpFileIdx < len(parsed.Files) {
			f := parsed.Files[m.vpFileIdx]
			stat := StyleSuccess.Render(fmt.Sprintf("+%d", f.Insertions)) +
				" " + StyleError.Render(fmt.Sprintf("-%d", f.Deletions))
			path := truncateVisible(f.Path, width-20)
			headerLine = StyleSubtle.Render(path) + " " + stat
			// File picker hint when multi-file.
			if len(parsed.Files) > 1 {
				hint := StyleSubtle.Render(fmt.Sprintf(" [%d/%d  [ ]", m.vpFileIdx+1, len(parsed.Files)))
				gap := width - ansi.StringWidth(headerLine) - ansi.StringWidth(hint)
				if gap < 1 {
					gap = 1
				}
				headerLine += strings.Repeat(" ", gap) + hint
			}
		}
	} else {
		headerLine = StyleSubtle.Render("(no diff)")
	}

	bodyH := height - 1
	if bodyH < 1 {
		bodyH = 1
	}
	m.vp.SetHeight(bodyH)

	vpContent := m.vp.View()
	return lipgloss.JoinVertical(lipgloss.Left, headerLine, vpContent)
}
