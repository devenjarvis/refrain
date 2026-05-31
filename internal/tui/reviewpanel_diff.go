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
// when the focused group has no rawDiff. Lazily parses and caches the model
// in m.parsedDiffs; must only be called from Update paths (not from View).
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

// parsedDiffForCursor returns the already-parsed diff model for the
// cursor-selected task without any side effects. Returns nil when the model
// has not yet been parsed (caller should show empty state). Safe to call from
// View because it never mutates m.parsedDiffs.
func (m *reviewPanelModel) parsedDiffForCursor(entry *reviewDiffEntry) (*diffmodel.Model, *taskReviewGroup) {
	group := reviewTaskGroupAtCursor(entry, m.taskCursor)
	if group == nil {
		return nil, nil
	}
	return m.parsedDiffs[group.taskIndex], group
}

// refreshDiffPane updates the embedded viewport to show the focused task's diff
// at vpFileIdx. Must be called from Update() paths only — it mutates m.vp,
// m.vpFileIdx, m.parsedDiffs, and m.renderersByPath.
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

// syncDiffPane computes the right-pane dimensions from the current model state
// and calls refreshDiffPane. Call this from Update() whenever the diff pane
// content needs to change: cursor move, file cycle, sideBySide toggle, or size
// change. No-op in narrow mode (width < 120).
func (m *reviewPanelModel) syncDiffPane() {
	leftW := reviewLeftPaneWidth(m.width)
	if leftW == 0 {
		return // narrow mode: no right pane
	}

	headerH := len(renderReviewHeader(m.session, m.width, m.now))

	// Checks strip height: use actual run state so body height matches View().
	var checksH int
	if m.deps.ValidationRuns != nil {
		if run := m.deps.ValidationRuns(m.repoPath, m.session.ID); run != nil {
			cs := &checksTabState{
				checks:  run.checks,
				results: run.results,
				cursor:  m.checksCursor,
				scroll:  m.checksScroll,
			}
			checksH = len(renderChecksStrip(cs, m.width, m.now))
		}
	}

	footerLineCount := 3
	draftLineCount := 0
	if m.drafting {
		draftLineCount = 1
	}
	bodyH := m.height - headerH - checksH - footerLineCount - draftLineCount
	if bodyH < 4 {
		bodyH = 4
	}

	rightW := innerWidth(m.width) - leftW - 1
	if rightW < 20 {
		rightW = 20
	}

	entry := m.deps.ReviewCache(m.repoPath, m.session.ID)
	m.refreshDiffPane(entry, rightW, bodyH-1) // bodyH-1: 1 line for file header
}

// renderTaskDiffPane renders the right-pane diff content: a single-line file
// header followed by the viewport's current content. Pure — reads model state
// only; all mutations must have been performed by syncDiffPane() in Update().
func (m *reviewPanelModel) renderTaskDiffPane(entry *reviewDiffEntry, width, height int) string {
	_ = height // height was used when SetHeight was called here; now done in refreshDiffPane
	parsed, _ := m.parsedDiffForCursor(entry)

	// File-name header line (read-only).
	var headerLine string
	if parsed != nil && len(parsed.Files) > 0 {
		vpFileIdx := m.vpFileIdx
		if vpFileIdx < 0 {
			vpFileIdx = 0
		}
		if vpFileIdx >= len(parsed.Files) {
			vpFileIdx = len(parsed.Files) - 1
		}
		f := parsed.Files[vpFileIdx]
		stat := StyleSuccess.Render(fmt.Sprintf("+%d", f.Insertions)) +
			" " + StyleError.Render(fmt.Sprintf("-%d", f.Deletions))
		path := truncateVisible(f.Path, width-20)
		headerLine = StyleSubtle.Render(path) + " " + stat
		// File picker hint when multi-file.
		if len(parsed.Files) > 1 {
			hint := StyleSubtle.Render(fmt.Sprintf("[%d/%d] [/]", vpFileIdx+1, len(parsed.Files)))
			gap := width - ansi.StringWidth(headerLine) - ansi.StringWidth(hint)
			if gap < 1 {
				gap = 1
			}
			headerLine += strings.Repeat(" ", gap) + hint
		}
	} else {
		headerLine = StyleSubtle.Render("(no diff)")
	}

	vpContent := m.vp.View()
	return lipgloss.JoinVertical(lipgloss.Left, headerLine, vpContent)
}
