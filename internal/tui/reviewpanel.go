package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/diffmodel"
	"github.com/devenjarvis/refrain/internal/git"
	"github.com/devenjarvis/refrain/internal/tui/diff"
	"github.com/devenjarvis/refrain/internal/tui/mdrender"
)

// spinnerFrames is the braille spinner sequence used while a verdict is running.
var spinnerFrames = []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}

// reviewSpinnerFrame returns the current spinner character based on wall time.
// Using time.Now() keeps all running rows in sync without needing a tick counter.
func reviewSpinnerFrame() string {
	frame := int(time.Now().UnixMilli()/100) % len(spinnerFrames)
	return spinnerFrames[frame]
}

// verdictBadge returns the icon, label, and lipgloss style for a task verdict record.
// rec may be nil (treated as verdictPending).
func verdictBadge(rec *taskVerdictRecord) (icon, label string, style lipgloss.Style) {
	if rec == nil {
		return "⋯", "Pending", StyleSubtle
	}
	// User flag wins over the AI verdict: the human reviewer is explicitly
	// asking for rework on this task.
	if rec.userFlagged {
		return "⚑", "flagged", lipgloss.NewStyle().Foreground(ColorWarning)
	}
	switch rec.state {
	case verdictPending:
		return "⋯", "Pending", StyleSubtle
	case verdictRunning:
		return reviewSpinnerFrame(), "Reviewing…", lipgloss.NewStyle().Foreground(ColorPrimary)
	case verdictDone:
		switch rec.verdict.Kind {
		case agent.VerdictPass:
			return "✓", "pass", StyleSuccess
		case agent.VerdictConcerns:
			return "!", "concerns", StyleWarning
		case agent.VerdictFail:
			return "✗", "fail", StyleError
		}
	case verdictErr:
		return "✗", "error", StyleError
	case verdictNoDiff:
		return "⊘", "no diff found", StyleSubtle
	}
	return "⋯", "Pending", StyleSubtle
}

// renderReviewHeader returns the 4-line collapsed header:
// [0] REVIEW › <name> [age], [1] prompt line 1, [2] prompt line 2 (with …), [3] divider.
// For short prompts that fit in one line, returns 3 lines: [0] title, [1] prompt, [2] divider.
func renderReviewHeader(sess *agent.Session, width int) []string {
	age := ""
	if !sess.DoneAt().IsZero() {
		mins := int(time.Since(sess.DoneAt()).Minutes())
		age = fmt.Sprintf("done %dm ago", mins)
	}
	headerLeft := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("REVIEW") +
		"  " + StyleSubtle.Render("›") +
		"  " + lipgloss.NewStyle().Render(sess.GetDisplayName())
	headerRight := StyleSubtle.Render(age)
	gap := width - ansi.StringWidth(headerLeft) - ansi.StringWidth(headerRight) - 4
	if gap < 1 {
		gap = 1
	}
	titleRow := headerLeft + strings.Repeat(" ", gap) + headerRight

	prompt := sess.OriginalPrompt()
	if prompt == "" {
		prompt = "(no prompt recorded)"
	}
	intentLines := wrapText(prompt, width-10)
	if len(intentLines) > 2 {
		line2 := intentLines[1]
		// Trim line2 to fit with the ellipsis appended.
		maxLine2 := width - 10 - 2 // leave room for " …"
		if maxLine2 < 4 {
			maxLine2 = 4
		}
		if utf8.RuneCountInString(line2) > maxLine2 {
			runes := []rune(line2)
			line2 = string(runes[:maxLine2])
		}
		intentLines = []string{intentLines[0], line2 + " …"}
	}

	lines := make([]string, 0, 3+len(intentLines)+1)
	lines = append(lines, titleRow)

	// Goal line from plan, if present.
	if plan, ok := sess.CachedPlan(); ok {
		sections := agent.ParsePlanSections(plan)
		// Use the first non-empty line of the Goal section.
		goalText := ""
		for _, l := range strings.Split(sections.Goal, "\n") {
			if t := strings.TrimSpace(l); t != "" {
				goalText = t
				break
			}
		}
		if goalText != "" {
			maxGoal := width - 10
			if maxGoal < 10 {
				maxGoal = 10
			}
			goalText = truncateVisible(goalText, maxGoal)
			goalLine := "  " + StyleSubtle.Render("Goal:") + " " + goalText
			lines = append(lines, goalLine)
		}
	}

	for _, l := range intentLines {
		lines = append(lines, "  "+l)
	}
	lines = append(lines, StyleSubtle.Render(strings.Repeat("─", width-2)))
	return lines
}

// renderReviewPanel renders the fullscreen review panel for a session.
// entry may be nil while diff stats are being fetched (shows loading placeholder).
// cursor is the currently selected task row index (0-based among all task rows).
// prDraftInFlight, when true, shows a spinner status line and disables the p hint.
// vpView is the pre-rendered viewport.View() string for the inline diff. When
// non-empty it is used directly; when empty (tests / no data) renderInlineDiffPane
// is called as a fallback with scrollOffset=0.
func renderReviewPanel(sess *agent.Session, entry *reviewDiffEntry, width, height, cursor int, prDraftInFlight bool, vpView string) string {
	// Header (3–4 lines depending on prompt length).
	headerLines := renderReviewHeader(sess, width)

	// Footer: blank + divider + hints = 3 lines; +1 when draft in flight.
	footerLineCount := 3
	draftLineCount := 0
	if prDraftInFlight {
		draftLineCount = 1
	}
	bodyH := height - len(headerLines) - footerLineCount - draftLineCount
	if bodyH < 4 {
		bodyH = 4
	}

	// Build body lines.
	var bodyLines []string
	if entry == nil {
		bodyLines = append(bodyLines, StyleSubtle.Render("loading diff stats…"))
	} else if width < 80 {
		// Narrow: stack list above detail with a divider.
		listH := bodyH / 2
		detailH := bodyH - listH - 1
		if listH < 2 {
			listH = 2
		}
		if detailH < 2 {
			detailH = 2
		}
		leftW := width - 2
		bodyLines = append(bodyLines, renderTaskListPane(entry, leftW, listH, cursor)...)
		bodyLines = append(bodyLines, StyleSubtle.Render(strings.Repeat("─", width-2)))
		bodyLines = append(bodyLines, renderTaskDetailPane(entry, cursor, leftW, detailH)...)
	} else {
		// Wide: side-by-side panes with a │ gutter.
		leftW := width * 4 / 10
		if leftW < 32 {
			leftW = 32
		}
		// gutter: " │ " = 3 chars, but we also have 2 leading spaces on the outer,
		// so effective layout: 2sp + leftW + " │ " + rightW
		rightW := width - leftW - 5
		if rightW < 20 {
			rightW = 20
		}
		leftPaneLines := renderTaskListPane(entry, leftW, bodyH, cursor)
		rightPaneLines := buildRightPane(entry, cursor, rightW, bodyH, vpView)

		gutter := " " + StyleSubtle.Render("│") + " "
		maxRows := len(leftPaneLines)
		if len(rightPaneLines) > maxRows {
			maxRows = len(rightPaneLines)
		}
		for i := 0; i < maxRows; i++ {
			l, r := "", ""
			if i < len(leftPaneLines) {
				l = leftPaneLines[i]
			}
			if i < len(rightPaneLines) {
				r = rightPaneLines[i]
			}
			// Pad left cell to leftW visible columns.
			padW := leftW - ansi.StringWidth(l)
			if padW < 0 {
				padW = 0
			}
			bodyLines = append(bodyLines, l+strings.Repeat(" ", padW)+gutter+r)
		}
	}

	// Assemble full panel.
	var lines []string
	lines = append(lines, headerLines...)
	lines = append(lines, bodyLines...)

	// In-flight PR draft status line.
	if prDraftInFlight {
		draftStatus := lipgloss.NewStyle().Foreground(ColorWarning).Render(reviewSpinnerFrame() + " Pushing branch and drafting PR…")
		lines = append(lines, draftStatus)
	}

	// Action footer.
	lines = append(lines, "")
	lines = append(lines, StyleSubtle.Render(strings.Repeat("─", width-2)))
	var pHint string
	if prDraftInFlight {
		pHint = StyleSubtle.Render("p — (in progress…)")
	} else {
		pHint = lipgloss.NewStyle().Foreground(lipgloss.Color("#5ab58a")).Render("p") + StyleSubtle.Render(" — create or open PR")
	}
	hints := "  " +
		pHint +
		"   " + lipgloss.NewStyle().Foreground(lipgloss.Color("#7ec8e3")).Render("t") + StyleSubtle.Render(" — open agent terminal") +
		"   " + lipgloss.NewStyle().Foreground(ColorWarning).Render("b") + StyleSubtle.Render(" — back to build") +
		"   " + StyleSubtle.Render("f — flag task") +
		"   " + StyleSubtle.Render("c — mark complete") +
		"   " + StyleSubtle.Render("e — open in editor") +
		"   " + StyleSubtle.Render("d — defer") +
		"   " + StyleSubtle.Render("pgdn") + StyleSubtle.Render("/pgup") + StyleSubtle.Render(" — scroll diff") +
		"   " + lipgloss.NewStyle().Foreground(lipgloss.Color("#f0c060")).Render("?") + StyleSubtle.Render(" — spec") +
		"   " + StyleSubtle.Render("ESC — back to focus")
	lines = append(lines, hints)

	return strings.Join(lines, "\n")
}

// reviewListPaneRowAt returns the 0-based task row index at (mouseX, mouseY) within the
// left task-list pane, or -1 if the click is outside the pane or in the PLAN TASKS header.
// paneTop is the Y coordinate of the first line of the pane (including the 2-line header).
// paneLeft/paneWidth define the horizontal bounds.
func reviewListPaneRowAt(entry *reviewDiffEntry, mouseX, mouseY, paneTop, paneLeft, paneWidth int) int {
	const listHeaderLines = 2 // PLAN TASKS + blank
	if mouseX < paneLeft || mouseX >= paneLeft+paneWidth {
		return -1
	}
	taskRowStart := paneTop + listHeaderLines
	if mouseY < taskRowStart {
		return -1
	}
	rowIdx := mouseY - taskRowStart
	nRows := reviewTaskCount(entry)
	if rowIdx >= nRows {
		return -1
	}
	return rowIdx
}

// renderTaskListPane renders the left-pane compact task list with icon, index, text, stat.
// Row format: <icon> [N] <truncated text>  +X -Y
func renderTaskListPane(entry *reviewDiffEntry, width, height, cursor int) []string {
	const headerLines = 2
	header := []string{StyleSubtle.Render("PLAN TASKS"), ""}

	type row struct {
		taskIndex int
		taskText  string
		group     *taskReviewGroup
	}

	groupByIdx := make(map[int]*taskReviewGroup, len(entry.groups))
	for i := range entry.groups {
		g := &entry.groups[i]
		groupByIdx[g.taskIndex] = g
	}

	// Build rows: no-plan synthetic row, plan tasks, then "Other changes".
	var rows []row
	if len(entry.tasks) == 0 && len(entry.groups) == 0 {
		rows = append(rows, row{taskIndex: -1, taskText: "Overview"})
	} else {
		for _, t := range entry.tasks {
			g := groupByIdx[t.Index]
			rows = append(rows, row{taskIndex: t.Index, taskText: t.Text, group: g})
		}
		if other, ok := groupByIdx[0]; ok {
			rows = append(rows, row{taskIndex: 0, taskText: "Other changes", group: other})
		}
	}

	rowsH := height - headerLines
	if rowsH < 1 {
		rowsH = 1
	}
	offset := cursor - rowsH/2
	if offset < 0 {
		offset = 0
	}
	if offset+rowsH > len(rows) {
		offset = len(rows) - rowsH
		if offset < 0 {
			offset = 0
		}
	}

	// Selection affordance: a vertical bar in primary color on each side of the
	// cursor row. We reserve 2 columns globally (one per side) so moving the
	// cursor never reflows text — both selected and unselected rows have the
	// same content budget.
	cursorBar := lipgloss.NewStyle().Foreground(ColorPrimary).Render("│")
	subtleGreen := StyleSuccess
	subtleRed := StyleError

	end := offset + rowsH
	if end > len(rows) {
		end = len(rows)
	}
	lines := make([]string, 0, headerLines+end-offset)
	lines = append(lines, header...)

	for i := offset; i < end; i++ {
		r := rows[i]
		selected := i == cursor

		// Icon from verdict badge.
		var rec *taskVerdictRecord
		if entry.verdicts != nil {
			rec = entry.verdicts[r.taskIndex]
		}
		icon, _, style := verdictBadge(rec)
		// For the synthetic overview row, use a dot.
		if r.taskIndex == -1 {
			icon = "·"
			style = StyleSubtle
		}
		iconStr := style.Render(icon)

		// Index label.
		label := fmt.Sprintf("[%d]", r.taskIndex)
		switch r.taskIndex {
		case 0:
			label = "[?]"
		case -1:
			label = "   "
		}

		// Stat string — for verdictNoDiff, show the label as the stat.
		statStr := ""
		if rec != nil && rec.state == verdictNoDiff {
			_, lbl, sty := verdictBadge(rec)
			statStr = sty.Render(lbl)
		} else if r.group != nil && r.group.stats != nil {
			st := r.group.stats
			if st.Insertions > 0 || st.Deletions > 0 {
				statStr = subtleGreen.Render(fmt.Sprintf("+%d", st.Insertions)) +
					" " + subtleRed.Render(fmt.Sprintf("-%d", st.Deletions))
			}
		}

		// Text: truncate to fit remaining width.
		iconW := ansi.StringWidth(iconStr)
		labelW := len(label)
		statW := ansi.StringWidth(statStr)
		// 1 outer space + 1 border + icon + space + label + space + ... + 2 sep
		// + stat + 1 border + 1 outer space. The +4 over the prior layout
		// reserves columns for the cursor border on both sides.
		overhead := 4 + iconW + 1 + labelW + 1 + 2 + statW
		maxTextW := width - overhead
		if maxTextW < 4 {
			maxTextW = 4
		}
		textStr := truncateVisible(r.taskText, maxTextW)

		// Assemble row.
		rowText := iconStr + " " + StyleSubtle.Render(label) + " " + textStr
		if statStr != "" {
			usedW := iconW + 1 + labelW + 1 + ansi.StringWidth(textStr)
			padW := width - 4 - usedW - 2 - statW
			if padW < 1 {
				padW = 1
			}
			rowText += strings.Repeat(" ", padW) + statStr
		}

		if selected {
			contentW := width - 4
			if w := ansi.StringWidth(rowText); w < contentW {
				rowText += strings.Repeat(" ", contentW-w)
			}
			lines = append(lines, " "+cursorBar+rowText+cursorBar+" ")
		} else {
			lines = append(lines, "  "+rowText)
		}
	}

	return lines
}

// renderTaskDetailPane renders the right-pane detail for the cursor-selected task.
// Sections: task heading, verdict badge, rationale, changed files, commits.
func renderTaskDetailPane(entry *reviewDiffEntry, cursor, width, height int) []string {
	if entry == nil {
		return []string{StyleSubtle.Render("loading…")}
	}

	// No-plan overview path.
	if len(entry.tasks) == 0 && len(entry.groups) == 0 {
		var lines []string
		if entry.aggregate != nil {
			agg := entry.aggregate
			subtleGreen := StyleSuccess
			subtleRed := StyleError
			summary := fmt.Sprintf("%d files · ", agg.Files) +
				subtleGreen.Render(fmt.Sprintf("+%d", agg.Insertions)) +
				" " + subtleRed.Render(fmt.Sprintf("-%d", agg.Deletions))
			lines = append(lines, summary, "")
			sorted := make([]git.FileStat, len(entry.files))
			copy(sorted, entry.files)
			sortFileStatsByChurn(sorted)
			top := sorted
			if len(top) > 8 {
				top = sorted[:8]
			}
			for _, f := range top {
				name := truncateVisible(f.Path, width-14)
				stat := subtleGreen.Render(fmt.Sprintf("+%d", f.Insertions)) +
					" " + subtleRed.Render(fmt.Sprintf("-%d", f.Deletions))
				lines = append(lines, "  "+name+"  "+stat)
			}
			if len(sorted) > 8 {
				lines = append(lines, StyleSubtle.Render(fmt.Sprintf("  … %d more files", len(sorted)-8)))
			}
		}
		return capLines(lines, height)
	}

	// Resolve selected task and group using same row order as renderTaskListPane.
	groupByIdx := make(map[int]*taskReviewGroup, len(entry.groups))
	for i := range entry.groups {
		g := &entry.groups[i]
		groupByIdx[g.taskIndex] = g
	}

	var selectedTask *agent.PlanTask
	var group *taskReviewGroup
	row := 0
	for i := range entry.tasks {
		if row == cursor {
			t := entry.tasks[i]
			selectedTask = &t
			group = groupByIdx[t.Index]
			break
		}
		row++
	}
	if selectedTask == nil {
		if other, ok := groupByIdx[0]; ok && row == cursor {
			group = other
		}
	}

	var lines []string
	maxW := width - 2

	// (1) Task heading.
	heading := ""
	if selectedTask != nil {
		heading = fmt.Sprintf("Task %d: %s", selectedTask.Index, selectedTask.Text)
	} else if group != nil {
		heading = "Other changes"
	}
	if heading != "" {
		lines = append(lines, wrapText(heading, maxW)...)
		lines = append(lines, "")
	}

	// (2) Verdict badge.
	var rec *taskVerdictRecord
	if entry.verdicts != nil && selectedTask != nil {
		rec = entry.verdicts[selectedTask.Index]
	} else if entry.verdicts != nil && group != nil {
		rec = entry.verdicts[group.taskIndex]
	}
	icon, label, style := verdictBadge(rec)
	lines = append(lines, style.Render(icon+" "+label))

	// (3) Rationale.
	if rec != nil && rec.state == verdictDone && rec.verdict.Rationale != "" {
		lines = append(lines, "")
		lines = append(lines, wrapText(rec.verdict.Rationale, maxW)...)
	}

	if group == nil {
		lines = append(lines, "", StyleSubtle.Render("(no commits matched this task)"))
		return capLines(lines, height)
	}

	// (4) Changed files.
	if len(group.files) > 0 {
		lines = append(lines, "", StyleSubtle.Render("Changed:"))
		sorted := make([]git.FileStat, len(group.files))
		copy(sorted, group.files)
		sortFileStatsByChurn(sorted)
		subtleGreen := StyleSuccess
		subtleRed := StyleError
		const maxFiles = 8
		top := sorted
		if len(top) > maxFiles {
			top = sorted[:maxFiles]
		}
		for _, f := range top {
			stat := subtleGreen.Render(fmt.Sprintf("+%d", f.Insertions)) +
				" " + subtleRed.Render(fmt.Sprintf("-%d", f.Deletions))
			statW := ansi.StringWidth(stat)
			nameMax := maxW - 2 - 2 - statW
			if nameMax < 4 {
				nameMax = 4
			}
			name := truncateVisible(f.Path, nameMax)
			lines = append(lines, "  "+name+"  "+stat)
		}
		if len(sorted) > maxFiles {
			lines = append(lines, StyleSubtle.Render(fmt.Sprintf("  … %d more", len(sorted)-maxFiles)))
		}
	}

	// (5) Commits.
	if len(group.commits) > 0 {
		lines = append(lines, "", StyleSubtle.Render("Commits:"))
		for _, c := range group.commits {
			hash := c.Hash
			if len(hash) > 7 {
				hash = hash[:7]
			}
			subject := truncateVisible(c.Subject, maxW-2-8)
			lines = append(lines, "  "+StyleSubtle.Render(hash)+" "+subject)
		}
	}

	return capLines(lines, height)
}

// renderTaskSummaryPane renders the summary portion of the right pane (verdict,
// rationale, files, commits) capped to summaryH lines. It is the upper half of
// the right pane in wide mode; the inline diff viewport occupies the lower half.
// This is identical to renderTaskDetailPane but without the "enter — open task diff" hint.
func renderTaskSummaryPane(entry *reviewDiffEntry, cursor, width, summaryH int) []string {
	return renderTaskDetailPane(entry, cursor, width, summaryH)
}

// renderAllFilesUnified renders every file in a diffmodel.Model as unified
// (non-side-by-side) text at the given width, with files joined by newlines.
func renderAllFilesUnified(m *diffmodel.Model, width int) string {
	if m == nil {
		return ""
	}
	parts := make([]string, 0, len(m.Files))
	for i := range m.Files {
		r := diff.NewRenderer(&m.Files[i])
		parts = append(parts, r.Render(width, false))
	}
	return strings.Join(parts, "\n")
}

// renderInlineDiffPane renders the diff body for the cursor task's rawDiff,
// windowed by scrollOffset and capped to height. Returns a placeholder line
// when the diff is empty.
func renderInlineDiffPane(entry *reviewDiffEntry, cursor, width, height, scrollOffset int) []string {
	rawDiff := ""
	if entry != nil {
		group := reviewTaskGroupAtCursor(entry, cursor)
		// For the no-plan overview path (no tasks), fall back to the first group.
		if group == nil && len(entry.groups) > 0 && len(entry.tasks) == 0 {
			group = &entry.groups[0]
		}
		if group != nil {
			rawDiff = group.rawDiff
		}
	}

	const placeholder = "(no diff for this task)"
	if rawDiff == "" {
		if height <= 0 {
			return []string{StyleSubtle.Render(placeholder)}
		}
		out := make([]string, height)
		out[0] = StyleSubtle.Render(placeholder)
		return out
	}

	m, err := diffmodel.Parse(rawDiff)
	if err != nil || m == nil || len(m.Files) == 0 {
		out := make([]string, max(1, height))
		out[0] = StyleSubtle.Render(placeholder)
		return out
	}

	content := renderAllFilesUnified(m, width)
	allLines := strings.Split(content, "\n")

	if scrollOffset < 0 {
		scrollOffset = 0
	}
	if scrollOffset >= len(allLines) {
		scrollOffset = max(0, len(allLines)-1)
	}
	end := scrollOffset + height
	if end > len(allLines) {
		end = len(allLines)
	}
	result := allLines[scrollOffset:end]
	// Pad to height so the pane always occupies its allocated rows.
	for len(result) < height {
		result = append(result, "")
	}
	return result
}

// buildRightPane composes the right pane for wide mode: summary on top,
// a horizontal rule, then the inline diff below.
// vpView is the pre-rendered output from viewport.View(). When non-empty it is
// used directly for the diff section (no re-parsing); when empty renderInlineDiffPane
// is called as a fallback (used by tests that don't wire up a viewport).
func buildRightPane(entry *reviewDiffEntry, cursor, width, bodyH int, vpView string) []string {
	if entry == nil {
		return []string{StyleSubtle.Render("loading…")}
	}
	maxSummaryH := bodyH / 3
	if maxSummaryH < 4 {
		maxSummaryH = 4
	}
	summaryLines := renderTaskSummaryPane(entry, cursor, width, maxSummaryH)
	divider := StyleSubtle.Render(strings.Repeat("─", width))
	diffH := bodyH - len(summaryLines) - 1
	if diffH < 1 {
		diffH = 1
	}
	var diffLines []string
	if vpView != "" {
		// Use the viewport's pre-rendered content (scroll already applied, no re-parse).
		diffLines = strings.Split(vpView, "\n")
	} else {
		diffLines = renderInlineDiffPane(entry, cursor, width, diffH, 0)
	}
	result := make([]string, 0, len(summaryLines)+1+len(diffLines))
	result = append(result, summaryLines...)
	result = append(result, divider)
	result = append(result, diffLines...)
	return result
}

// capLines returns lines capped to height, with a trailing truncation note if needed.
func capLines(lines []string, height int) []string {
	if height <= 0 || len(lines) <= height {
		return lines
	}
	return append(lines[:height-1], StyleSubtle.Render(fmt.Sprintf("… %d more", len(lines)-height+1)))
}

// sortFileStatsByChurn sorts files by total insertions+deletions descending.
func sortFileStatsByChurn(files []git.FileStat) {
	sort.Slice(files, func(i, j int) bool {
		return (files[i].Insertions + files[i].Deletions) > (files[j].Insertions + files[j].Deletions)
	})
}

// renderReviewSpecOverlay renders the full-screen Spec overlay for the review
// panel. It shows the Goal, Spec, Verification, and Not in scope sections from
// the plan markdown, rendered via mdrender and scrolled by scrollOffset.
// sess and plan are passed so callers can cache plan reads; if plan is empty
// or the session has no plan, shows a brief "no plan available" message.
func renderReviewSpecOverlay(sess *agent.Session, plan string, scrollOffset, width, height int) string {
	titleStyle := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)
	title := titleStyle.Render("SPEC") + "  " + StyleSubtle.Render("›") + "  " + sess.GetDisplayName()
	header := title + "\n" + StyleSubtle.Render(strings.Repeat("─", width-2))
	bodyH := height - 2 // title + divider
	if bodyH < 1 {
		bodyH = 1
	}

	if plan == "" {
		msg := StyleSubtle.Render("(no plan available for this session)")
		return header + "\n" + msg
	}

	sections := agent.ParsePlanSections(plan)
	r := mdrender.New(planEditorChromaStyle)
	contentW := width - 4

	type namedSection struct {
		heading string
		body    string
	}
	ordered := []namedSection{
		{"# Goal", sections.Goal},
		{"## Spec", sections.Spec},
		{"## Verification", sections.Verification},
		{"## Not in scope", sections.NotInScope},
	}

	allLines := make([]string, 0, len(ordered)*8)
	for _, sec := range ordered {
		if sec.body == "" {
			continue
		}
		allLines = append(allLines, StyleSubtle.Render(sec.heading), "")
		rendered := r.RenderLines(sec.body, contentW)
		for _, l := range rendered {
			allLines = append(allLines, "  "+l)
		}
		allLines = append(allLines, "")
	}

	if len(allLines) == 0 {
		allLines = []string{StyleSubtle.Render("(no content)")}
	}

	// Clamp scroll offset.
	if scrollOffset < 0 {
		scrollOffset = 0
	}
	if scrollOffset >= len(allLines) {
		scrollOffset = max(0, len(allLines)-1)
	}

	end := scrollOffset + bodyH
	if end > len(allLines) {
		end = len(allLines)
	}
	window := allLines[scrollOffset:end]
	body := strings.Join(window, "\n")

	// Footer hint.
	footer := "\n" + StyleSubtle.Render(strings.Repeat("─", width-2)) + "\n" +
		"  " + StyleSubtle.Render("pgdn/pgup") + " scroll  " +
		StyleSubtle.Render("g/G") + " top/bottom  " +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#f0c060")).Render("esc") + StyleSubtle.Render(" — back to review")

	return header + "\n" + body + footer
}

// wrapText wraps s to maxWidth columns.
func wrapText(s string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{s}
	}
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		var line strings.Builder
		for _, w := range words {
			if line.Len() > 0 && line.Len()+1+utf8.RuneCountInString(w) > maxWidth {
				lines = append(lines, line.String())
				line.Reset()
			}
			if line.Len() > 0 {
				line.WriteByte(' ')
			}
			line.WriteString(w)
		}
		if line.Len() > 0 {
			lines = append(lines, line.String())
		}
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}
