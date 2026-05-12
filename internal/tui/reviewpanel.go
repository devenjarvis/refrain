package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/baton/internal/agent"
	"github.com/devenjarvis/baton/internal/git"
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
func verdictBadge(rec *taskVerdictRecord) (icon string, label string, style lipgloss.Style) {
	if rec == nil {
		return "⋯", "Pending", StyleSubtle
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

	lines := make([]string, 0, 2+len(intentLines)+1)
	lines = append(lines, titleRow)
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
func renderReviewPanel(sess *agent.Session, entry *reviewDiffEntry, width, height, cursor int, prDraftInFlight bool) string {
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
		rightPaneLines := renderTaskDetailPane(entry, cursor, rightW, bodyH)

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
	taskHint := ""
	if len(entry.getGroups()) > 0 {
		taskHint = "   " + lipgloss.NewStyle().Foreground(lipgloss.Color("#f0c060")).Render("enter") + StyleSubtle.Render(" — view task diff")
	}
	var pHint string
	if prDraftInFlight {
		pHint = StyleSubtle.Render("p — (in progress…)")
	} else {
		pHint = lipgloss.NewStyle().Foreground(lipgloss.Color("#5ab58a")).Render("p") + StyleSubtle.Render(" — create or open PR")
	}
	hints := "  " +
		pHint +
		"   " + lipgloss.NewStyle().Foreground(lipgloss.Color("#7ec8e3")).Render("t") + StyleSubtle.Render(" — open agent terminal") +
		"   " + StyleSubtle.Render("c — mark complete") +
		"   " + StyleSubtle.Render("e — open in editor") +
		"   " + StyleSubtle.Render("d — defer") +
		taskHint +
		"   " + StyleSubtle.Render("ESC — back to focus")
	lines = append(lines, hints)

	return strings.Join(lines, "\n")
}

// getGroups safely returns groups, even on a nil entry.
func (e *reviewDiffEntry) getGroups() []taskReviewGroup {
	if e == nil {
		return nil
	}
	return e.groups
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

	cursorStyle := lipgloss.NewStyle().Background(lipgloss.Color("#2a2a3a")).Bold(true)
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
		// 2 prefix spaces + icon + space + label + space + ... + 2 sep + stat
		overhead := 2 + iconW + 1 + labelW + 1 + 2 + statW
		maxTextW := width - overhead
		if maxTextW < 4 {
			maxTextW = 4
		}
		textStr := truncateVisible(r.taskText, maxTextW)

		// Assemble row.
		rowText := iconStr + " " + StyleSubtle.Render(label) + " " + textStr
		if statStr != "" {
			usedW := iconW + 1 + labelW + 1 + ansi.StringWidth(textStr)
			padW := width - 2 - usedW - 2 - statW
			if padW < 1 {
				padW = 1
			}
			rowText += strings.Repeat(" ", padW) + statStr
		}

		if selected {
			padW := width - 2 - ansi.StringWidth(rowText)
			if padW < 0 {
				padW = 0
			}
			rowText = cursorStyle.Render(rowText + strings.Repeat(" ", padW))
		}
		lines = append(lines, "  "+rowText)
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
		for _, l := range wrapText(heading, maxW) {
			lines = append(lines, l)
		}
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
		for _, l := range wrapText(rec.verdict.Rationale, maxW) {
			lines = append(lines, l)
		}
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

	// (6) Diff hint.
	if group.rawDiff != "" {
		lines = append(lines, "", StyleSubtle.Render("enter — open task diff"))
	}

	return capLines(lines, height)
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
