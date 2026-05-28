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
	"github.com/devenjarvis/refrain/internal/git"
	"github.com/devenjarvis/refrain/internal/tui/mdrender"
)

// reviewDetailMaxMeasure is the maximum content column width for the right detail
// pane. Matches docEditorMaxMeasure so typography is consistent across panels.
const reviewDetailMaxMeasure = docEditorMaxMeasure

// reviewRenderer is the markdown renderer used for rationale text in the detail pane.
var reviewRenderer = mdrender.New(docEditorChromaStyle)

// detailContentMeasure returns the effective content width and left-margin padding
// for centering the detail pane content. Thin wrapper around mdrender.ContentMeasure.
func detailContentMeasure(paneWidth, maxMeasure int) (measure, leftPad int) {
	return mdrender.ContentMeasure(paneWidth, maxMeasure)
}

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

// renderReviewPlaceholderTab renders a height-line placeholder body for tabs
// that haven't been implemented yet. The label is horizontally centered on
// the first line; remaining lines are empty strings.
func renderReviewPlaceholderTab(label string, width, height int) []string {
	lines := make([]string, height)
	text := StyleSubtle.Render("(" + label + ")")
	pad := (width - ansi.StringWidth(text)) / 2
	if pad < 0 {
		pad = 0
	}
	lines[0] = strings.Repeat(" ", pad) + text
	return lines
}

// renderReviewTabBar renders the 2-line tab bar for the review panel.
// Line 0: tab labels separated by two spaces; active tab in ColorSecondary bold,
// inactive tabs in StyleSubtle. Line 1: a subtle horizontal divider.
func renderReviewTabBar(activeTab, width int) []string {
	labels := []string{"Tasks", "Diff", "Checks", "Validate"}
	activeStyle := lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true)
	var parts []string
	for i, label := range labels {
		if i == activeTab {
			parts = append(parts, activeStyle.Render(label))
		} else {
			parts = append(parts, StyleSubtle.Render(label))
		}
	}
	labelLine := "  " + strings.Join(parts, "  ")
	divider := StyleSubtle.Render(strings.Repeat("─", width-2))
	return []string{labelLine, divider}
}

// renderReviewPanel renders the fullscreen review panel for a session.
// entry may be nil while diff stats are being fetched (shows loading placeholder).
// cursor is the currently selected task row index (0-based among all task rows).
// prDraftInFlight, when true, shows a spinner status line and disables the p hint.
// activeTab selects which tab body to render (0=Tasks, 1=Diff, 2=Checks, 3=Validate).
func renderReviewPanel(sess *agent.Session, entry *reviewDiffEntry, width, height, cursor int, prDraftInFlight bool, activeTab int) string {
	// Header (3–4 lines depending on prompt length).
	headerLines := renderReviewHeader(sess, width)

	// Tab bar: 2 lines (labels + divider).
	const tabBarH = 2

	// Footer: blank + divider + hints = 3 lines; +1 when draft in flight.
	footerLineCount := 3
	draftLineCount := 0
	if prDraftInFlight {
		draftLineCount = 1
	}
	bodyH := height - len(headerLines) - tabBarH - footerLineCount - draftLineCount
	if bodyH < 4 {
		bodyH = 4
	}

	// Build body lines based on active tab.
	var bodyLines []string
	switch {
	case activeTab != reviewTabTasks:
		// Non-Tasks tabs: placeholder.
		var label string
		switch activeTab {
		case reviewTabDiff:
			label = "full diff browser coming soon"
		case reviewTabChecks:
			label = "local checks coming soon"
		case reviewTabValidate:
			label = "manual validation coming soon"
		}
		bodyLines = renderReviewPlaceholderTab(label, width, bodyH)
	case entry == nil:
		bodyLines = append(bodyLines, StyleSubtle.Render("loading diff stats…"))
	default:
		// Tasks tab: full-width stacked layout.
		listH := bodyH * 2 / 5
		detailH := bodyH - listH - 1
		if listH < 2 {
			listH = 2
		}
		if detailH < 2 {
			detailH = 2
		}
		paneW := width - 2
		bodyLines = append(bodyLines, renderTaskListPane(entry, paneW, listH, cursor)...)
		bodyLines = append(bodyLines, StyleSubtle.Render(strings.Repeat("─", width-2)))
		bodyLines = append(bodyLines, renderTaskDetailPane(entry, cursor, paneW, detailH)...)
	}

	// Assemble full panel.
	var lines []string
	lines = append(lines, headerLines...)
	lines = append(lines, renderReviewTabBar(activeTab, width)...)
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
		pHint = StyleActive.Render("p") + StyleSubtle.Render(" — create or open PR")
	}
	hints := "  " +
		pHint +
		"  " + StyleActive.Render("t") + StyleSubtle.Render(" — open agent terminal") +
		"  " + StyleWarning.Render("b") + StyleSubtle.Render(" — back to build") +
		"  " + StyleActive.Render("f") + StyleSubtle.Render(" — flag task") +
		"  " + StyleActive.Render("c") + StyleSubtle.Render(" — mark complete") +
		"  " + StyleActive.Render("e") + StyleSubtle.Render(" — open in editor") +
		"  " + StyleActive.Render("d") + StyleSubtle.Render(" — defer") +
		"  " + StyleActive.Render("enter") + StyleSubtle.Render(" — open task diff") +
		"  " + StyleActive.Render("?") + StyleSubtle.Render(" — spec") +
		"  " + StyleSubtle.Render("ESC — back to focus")
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
	planTasksStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#06B6D4")).Bold(true)
	header := []string{
		planTasksStyle.Render("PLAN TASKS"),
		StyleSubtle.Render(strings.Repeat("─", ansi.StringWidth("PLAN TASKS"))),
	}

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

	measure, leftPad := detailContentMeasure(width, reviewDetailMaxMeasure)

	// capAndCenter caps lines to height then prepends centering padding to each
	// non-empty line. Applied at every return point in the function.
	capAndCenter := func(lines []string) []string {
		capped := capLines(lines, height)
		if leftPad > 0 {
			pad := strings.Repeat(" ", leftPad)
			for i, l := range capped {
				if l != "" {
					capped[i] = pad + l
				}
			}
		}
		return capped
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
				name := truncateVisible(f.Path, measure-14)
				stat := subtleGreen.Render(fmt.Sprintf("+%d", f.Insertions)) +
					" " + subtleRed.Render(fmt.Sprintf("-%d", f.Deletions))
				lines = append(lines, "  "+name+"  "+stat)
			}
			if len(sorted) > 8 {
				lines = append(lines, StyleSubtle.Render(fmt.Sprintf("  … %d more files", len(sorted)-8)))
			}
		}
		return capAndCenter(lines)
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
	maxW := measure - 2

	// H2 heading style: matches colHeading2 / styleH2 in mdrender.
	headingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#06B6D4")).Bold(true)

	// (1) Task heading with H2 color and thin underline.
	heading := ""
	if selectedTask != nil {
		heading = fmt.Sprintf("Task %d: %s", selectedTask.Index, selectedTask.Text)
	} else if group != nil {
		heading = "Other changes"
	}
	if heading != "" {
		displayHeading := truncateVisible(heading, maxW)
		lines = append(lines, headingStyle.Render(displayHeading))
		lines = append(lines, reviewRenderer.HeadingUnderline(2, ansi.StringWidth(displayHeading), measure))
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

	// (3) Rationale rendered through mdrender for inline bold/italic/code styling.
	if rec != nil && rec.state == verdictDone && rec.verdict.Rationale != "" {
		lines = append(lines, "")
		lines = append(lines, reviewRenderer.RenderLines(rec.verdict.Rationale, measure)...)
	}

	if group == nil {
		lines = append(lines, "", StyleSubtle.Render("(no commits matched this task)"))
		return capAndCenter(lines)
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

	return capAndCenter(lines)
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
	r := mdrender.New(docEditorChromaStyle)
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
		"  " + StyleActive.Render("pgdn") + StyleSubtle.Render("/") + StyleActive.Render("pgup") + StyleSubtle.Render(" — scroll") +
		"  " + StyleActive.Render("g") + StyleSubtle.Render("/") + StyleActive.Render("G") + StyleSubtle.Render(" — top/bottom") +
		"  " + StyleActive.Render("esc") + StyleSubtle.Render(" — back to review")

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
