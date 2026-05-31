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
	"github.com/devenjarvis/refrain/internal/config"
	"github.com/devenjarvis/refrain/internal/git"
	"github.com/devenjarvis/refrain/internal/tui/mdrender"
	"github.com/devenjarvis/refrain/internal/tui/theme"
)


// reviewSpinnerFrame returns the spinner character for the given render clock.
// now is the model's tick-refreshed timestamp (§5: no clock read at render
// time); deriving the frame from it keeps all running rows in sync. The frame
// set and derivation live in the design-system registry (theme.SpinnerFrame).
func reviewSpinnerFrame(now time.Time) string {
	return theme.SpinnerFrame(now)
}

// verdictBadge returns the icon, label, and lipgloss style for a task verdict record.
// rec may be nil (treated as verdictPending). now drives the running-state spinner.
func verdictBadge(rec *taskVerdictRecord, now time.Time) (icon, label string, style lipgloss.Style) {
	if rec == nil {
		return theme.GlyphPending, "Pending", StyleSubtle
	}
	// User flag wins over the AI verdict: the human reviewer is explicitly
	// asking for rework on this task.
	if rec.userFlagged {
		return theme.GlyphFlagged, "flagged", StyleWarning
	}
	switch rec.state {
	case verdictPending:
		return theme.GlyphPending, "Pending", StyleSubtle
	case verdictRunning:
		return reviewSpinnerFrame(now), "Reviewing…", StyleAccent
	case verdictDone:
		switch rec.verdict.Kind {
		case agent.VerdictPass:
			return theme.GlyphSuccess, "pass", StyleSuccess
		case agent.VerdictConcerns:
			return theme.GlyphConcerns, "concerns", StyleWarning
		case agent.VerdictFail:
			return theme.GlyphError, "fail", StyleError
		}
	case verdictErr:
		return theme.GlyphError, "error", StyleError
	case verdictNoDiff:
		return theme.GlyphNoDiff, "no diff found", StyleSubtle
	}
	return theme.GlyphPending, "Pending", StyleSubtle
}

// checkBadge returns the icon and lipgloss style for a validation check result.
// Modeled on verdictBadge to keep icon/style language consistent. now drives
// the running-state spinner.
func checkBadge(result validationCheckResult, now time.Time) (icon string, style lipgloss.Style) {
	switch result.state {
	case checkPending:
		return theme.GlyphPending, StyleSubtle
	case checkRunning:
		return reviewSpinnerFrame(now), StyleAccent
	case checkPassed:
		return theme.GlyphSuccess, StyleSuccess
	case checkFailed:
		return theme.GlyphError, StyleError
	case checkError:
		return theme.GlyphError, StyleError
	}
	return theme.GlyphPending, StyleSubtle
}

// renderChecksTab renders the Checks tab body: a compact check list on top and
// the selected check's combined output below. cs must not be nil.
func renderChecksTab(cs *checksTabState, width, height int, now time.Time) []string {
	if height < 4 {
		height = 4
	}

	listH := height * 2 / 5
	if listH < 2 {
		listH = 2
	}
	outputH := height - listH - 1
	if outputH < 2 {
		outputH = 2
	}

	// Build list pane.
	header := StyleHeading.Foreground(ColorSecondary).Render("CHECKS")
	listLines := make([]string, 0, len(cs.checks)+1)
	listLines = append(listLines, header)
	for i, ch := range cs.checks {
		var result validationCheckResult
		if i < len(cs.results) {
			result = cs.results[i]
		}
		icon, iconStyle := checkBadge(result, now)
		iconStr := iconStyle.Render(icon)

		duration := ""
		if result.state == checkPassed || result.state == checkFailed || result.state == checkError {
			if result.duration > 0 {
				duration = "  " + StyleSubtle.Render(result.duration.Round(time.Millisecond).String())
			}
		}

		cursor := " "
		nameStyle := StyleSubtle
		if i == cs.cursor {
			cursor = theme.GlyphCaret
			nameStyle = lipgloss.NewStyle()
		}

		line := fmt.Sprintf(
			"  %s %s %s%s%s",
			cursor,
			iconStr,
			nameStyle.Render(ch.Name),
			duration,
			"",
		)
		listLines = append(listLines, line)
	}
	listLines = capLines(listLines, listH)

	// Build output pane.
	var outputLines []string
	outputLines = append(outputLines, StyleSubtle.Render(strings.Repeat("─", innerWidth(width))))
	if cs.cursor < len(cs.results) {
		result := cs.results[cs.cursor]
		out := result.output
		if result.err != nil {
			out += "\n" + result.err.Error()
		}
		if out == "" {
			if result.state == checkRunning {
				out = "(running…)"
			} else {
				out = "(no output)"
			}
		}
		outLines := strings.Split(out, "\n")
		// Apply scroll offset.
		if cs.scroll > 0 && cs.scroll < len(outLines) {
			outLines = outLines[cs.scroll:]
		}
		outputLines = append(outputLines, capLines(outLines, outputH-1)...)
	}
	outputLines = capLines(outputLines, outputH)

	var lines []string
	lines = append(lines, listLines...)
	lines = append(lines, outputLines...)
	return lines
}

// tailLines returns the last n non-empty lines from s (split on "\n"),
// trimming a trailing empty element from strings that end with "\n".
func tailLines(s string, n int) []string {
	all := strings.Split(s, "\n")
	// Trim trailing empty element produced by a trailing newline.
	if len(all) > 0 && all[len(all)-1] == "" {
		all = all[:len(all)-1]
	}
	if len(all) <= n {
		return all
	}
	return all[len(all)-n:]
}

// renderChecksStrip renders the compact inline checks strip.
// When cs is nil or has no checks, returns an empty slice (strip is omitted).
// When all results are checkPassed, returns a single summary line.
// When any result is checkFailed/checkError, returns a 6-line expanded form:
// 1 summary, 4 tail lines of the first failed check's output, 1 divider.
func renderChecksStrip(cs *checksTabState, width int, now time.Time) []string {
	if cs == nil || len(cs.checks) == 0 {
		return nil
	}

	var passCount, failCount int
	var firstFailed *validationCheckResult
	var firstFailedName string
	var totalDuration time.Duration
	for i, result := range cs.results {
		switch result.state {
		case checkPassed:
			passCount++
			totalDuration += result.duration
		case checkFailed, checkError:
			failCount++
			if firstFailed == nil {
				r := result
				firstFailed = &r
				if i < len(cs.checks) {
					firstFailedName = cs.checks[i].Name
				}
			}
			totalDuration += result.duration
		}
	}

	passStr := StyleSuccess.Render(fmt.Sprintf("%d✓", passCount))

	if failCount == 0 {
		dur := totalDuration.Round(time.Second)
		summary := fmt.Sprintf("  Checks  %s  ran in %s", passStr, dur)
		return []string{summary}
	}

	// Expanded: summary + 4 tail output lines + divider.
	failStr := StyleError.Render(fmt.Sprintf("%d✗", failCount))
	nameStr := StyleSubtle.Render("❯ " + firstFailedName)
	summary := fmt.Sprintf("  Checks  %s %s  %s", passStr, failStr, nameStr)

	tail := tailLines(firstFailed.output, 4)
	var lines []string
	lines = append(lines, summary)
	for _, l := range tail {
		lines = append(lines, "  "+l)
	}
	// Pad to exactly 4 tail lines if there are fewer.
	for len(lines) < 5 {
		lines = append(lines, "")
	}
	lines = append(lines, StyleSubtle.Render(strings.Repeat("─", innerWidth(width))))
	return lines
}

// renderReviewHeader returns the 4-line collapsed header:
// [0] REVIEW › <name> [age], [1] prompt line 1, [2] prompt line 2 (with …), [3] divider.
// For short prompts that fit in one line, returns 3 lines: [0] title, [1] prompt, [2] divider.
func renderReviewHeader(sess *agent.Session, width int, now time.Time) []string {
	age := ""
	if !sess.DoneAt().IsZero() {
		mins := int(now.Sub(sess.DoneAt()).Minutes())
		age = fmt.Sprintf("done %dm ago", mins)
	}
	headerLeft := StyleHeading.Render("REVIEW") +
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
	lines = append(lines, StyleSubtle.Render(strings.Repeat("─", innerWidth(width))))
	return lines
}

// checksTabState carries the data needed to render the Checks tab body.
// checks and results are sourced from App-level validationRunState; cursor
// and scroll are panel-local since they don't need to survive panel close.
type checksTabState struct {
	checks  []config.ValidationCheck
	results []validationCheckResult
	cursor  int
	scroll  int
}

// renderReviewPanel renders the fullscreen review panel for a session.
// entry may be nil while diff stats are being fetched (shows loading placeholder).
// cursor is the currently selected task row index (0-based among all task rows).
// prDraftInFlight, when true, shows a spinner status line and disables the p hint.
// checkState is non-nil when validation checks are configured and their results
// are available; nil when no checks are configured or the run state is absent.
func renderReviewPanel(sess *agent.Session, entry *reviewDiffEntry, width, height, cursor int, prDraftInFlight bool, checkState *checksTabState, now time.Time) string {
	// Header (3–4 lines depending on prompt length).
	headerLines := renderReviewHeader(sess, width, now)

	// Checks strip: 0–6 lines depending on check state.
	checksStripLines := renderChecksStrip(checkState, width, now)

	// Footer: blank + divider + hints = 3 lines; +1 when draft in flight.
	footerLineCount := 3
	draftLineCount := 0
	if prDraftInFlight {
		draftLineCount = 1
	}
	bodyH := height - len(headerLines) - len(checksStripLines) - footerLineCount - draftLineCount
	if bodyH < 4 {
		bodyH = 4
	}

	// Body: task-card ledger (full height in narrow/stacked mode).
	var bodyLines []string
	if entry == nil {
		bodyLines = append(bodyLines, StyleSubtle.Render("loading diff stats…"))
	} else {
		bodyLines = renderTaskListPane(entry, innerWidth(width), bodyH, cursor, now)
	}

	// Pad body to exactly bodyH rows so the footer always pins to the bottom
	// regardless of how many items each tab body renders.
	paddedBody := fillHeight(strings.Join(bodyLines, "\n"), width, bodyH)
	bodyLines = strings.Split(paddedBody, "\n")

	// Assemble full panel.
	var lines []string
	lines = append(lines, headerLines...)
	lines = append(lines, checksStripLines...)
	lines = append(lines, bodyLines...)

	// In-flight PR draft status line.
	if prDraftInFlight {
		draftStatus := StyleWarning.Render(reviewSpinnerFrame(now) + " Pushing branch and drafting PR…")
		lines = append(lines, draftStatus)
	}

	// Action footer.
	lines = append(lines, "")
	lines = append(lines, StyleSubtle.Render(strings.Repeat("─", innerWidth(width))))
	lines = append(lines, reviewFooterHints(prDraftInFlight, checkState != nil))

	return strings.Join(lines, "\n")
}

// reviewFooterHints returns the unified single-line footer hint string.
// When checksConfigured is true, a "r rerun checks" hint is appended.
func reviewFooterHints(prDraftInFlight, checksConfigured bool) string {
	var pHint string
	if prDraftInFlight {
		pHint = StyleSubtle.Render("p — (in progress…)")
	} else {
		pHint = StyleActive.Render("p") + StyleSubtle.Render(" ship")
	}
	h := "  " + pHint +
		"  " + StyleWarning.Render("b") + StyleSubtle.Render(" rework") +
		"  " + StyleActive.Render("m") + StyleSubtle.Render(" approve") +
		"  " + StyleActive.Render("f") + StyleSubtle.Render(" flag") +
		"  " + StyleActive.Render("d") + StyleSubtle.Render(" defer") +
		"  " + StyleActive.Render("e") + StyleSubtle.Render(" editor") +
		"  " + StyleActive.Render("t") + StyleSubtle.Render(" terminal") +
		"  " + StyleActive.Render("enter") + StyleSubtle.Render(" expand") +
		"  " + StyleActive.Render("?") + StyleSubtle.Render(" spec") +
		"  " + StyleSubtle.Render("esc back")
	if checksConfigured {
		h += "  " + StyleActive.Render("r") + StyleSubtle.Render(" rerun checks")
	}
	return h
}

// reviewListPaneRowAt returns the 0-based task card index at (mouseX, mouseY)
// within the left task-list pane, or -1 if the click is outside the pane or
// in the PLAN TASKS header. paneTop is the Y coordinate of the first line of
// the pane (including the 2-line header). paneLeft/paneWidth define the
// horizontal bounds. Each task card occupies 4 lines; the returned index is
// the card index (one per task), not the line offset.
func reviewListPaneRowAt(entry *reviewDiffEntry, mouseX, mouseY, paneTop, paneLeft, paneWidth int) int {
	const listHeaderLines = 2 // PLAN TASKS + underline
	const cardH = 4
	if mouseX < paneLeft || mouseX >= paneLeft+paneWidth {
		return -1
	}
	taskRowStart := paneTop + listHeaderLines
	if mouseY < taskRowStart {
		return -1
	}
	cardIdx := (mouseY - taskRowStart) / cardH
	nRows := reviewTaskCount(entry)
	if cardIdx >= nRows {
		return -1
	}
	return cardIdx
}

// renderTaskListPane renders the task-card ledger.
// Each card occupies 4 lines:
//
//	line 1: <icon> [N] <text>   +X -Y
//	line 2:   <verdict_label> — <rationale>  (or  ⋯ Pending)
//	line 3:   <top file> +X -Y  (or blank)
//	line 4:   (blank separator)
//
// The selected card gets vertical-bar cursor stripes on the first line.
func renderTaskListPane(entry *reviewDiffEntry, width, height, cursor int, now time.Time) []string {
	const headerLines = 2
	const cardH = 4
	planTasksStyle := StyleHeading.Foreground(ColorSecondary)
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

	// How many cards fit in the available body height.
	cardsH := (height - headerLines) / cardH
	if cardsH < 1 {
		cardsH = 1
	}
	offset := cursor - cardsH/2
	if offset < 0 {
		offset = 0
	}
	if offset+cardsH > len(rows) {
		offset = len(rows) - cardsH
		if offset < 0 {
			offset = 0
		}
	}

	cursorBar := StyleAccent.Render("│")
	subtleGreen := StyleSuccess
	subtleRed := StyleError

	end := offset + cardsH
	if end > len(rows) {
		end = len(rows)
	}
	lines := make([]string, 0, headerLines+cardH*(end-offset))
	lines = append(lines, header...)

	for i := offset; i < end; i++ {
		r := rows[i]
		selected := i == cursor

		// Verdict record and icon.
		var rec *taskVerdictRecord
		if entry.verdicts != nil {
			rec = entry.verdicts[r.taskIndex]
		}
		icon, verdictLabel, iconStyle := verdictBadge(rec, now)
		if r.taskIndex == -1 {
			icon = "·"
			iconStyle = StyleSubtle
		}
		iconStr := iconStyle.Render(icon)

		// Index label.
		label := fmt.Sprintf("[%d]", r.taskIndex)
		switch r.taskIndex {
		case 0:
			label = "[?]"
		case -1:
			label = "   "
		}

		// Stat string for line 1 (aggregate +X -Y).
		statStr := ""
		if rec != nil && rec.state == verdictNoDiff {
			_, lbl, sty := verdictBadge(rec, now)
			statStr = sty.Render(lbl)
		} else if r.group != nil && r.group.stats != nil {
			st := r.group.stats
			if st.Insertions > 0 || st.Deletions > 0 {
				statStr = subtleGreen.Render(fmt.Sprintf("+%d", st.Insertions)) +
					" " + subtleRed.Render(fmt.Sprintf("-%d", st.Deletions))
			}
		}

		// Line 1: icon + label + text + stat, right-aligned.
		iconW := ansi.StringWidth(iconStr)
		labelW := len(label)
		statW := ansi.StringWidth(statStr)
		overhead := 4 + iconW + 1 + labelW + 1 + 2 + statW
		maxTextW := width - overhead
		if maxTextW < 4 {
			maxTextW = 4
		}
		textStr := truncateVisible(r.taskText, maxTextW)
		rowText := iconStr + " " + StyleSubtle.Render(label) + " " + textStr
		if statStr != "" {
			usedW := iconW + 1 + labelW + 1 + ansi.StringWidth(textStr)
			padW := modalContentWidth(width) - usedW - 2 - statW
			if padW < 1 {
				padW = 1
			}
			rowText += strings.Repeat(" ", padW) + statStr
		}

		var line1 string
		if selected {
			contentW := modalContentWidth(width)
			if w := ansi.StringWidth(rowText); w < contentW {
				rowText += strings.Repeat(" ", contentW-w)
			}
			line1 = " " + cursorBar + rowText + cursorBar + " "
		} else {
			line1 = "  " + rowText
		}

		// Line 2: verdict label — rationale (or ⋯ Pending).
		var line2 string
		if rec != nil && rec.state == verdictDone && rec.verdict.Rationale != "" {
			rationale := truncateVisible(rec.verdict.Rationale, width-len(verdictLabel)-7)
			_, _, vStyle := verdictBadge(rec, now)
			line2 = "  " + vStyle.Render(verdictLabel) + " — " + rationale
		} else {
			_, lbl, lstyle := verdictBadge(rec, now)
			line2 = "  " + lstyle.Render("⋯ "+lbl)
		}

		// Line 3: top file +X -Y, or commit count, or blank.
		var line3 string
		if r.group != nil && len(r.group.files) > 0 {
			sorted := make([]git.FileStat, len(r.group.files))
			copy(sorted, r.group.files)
			sortFileStatsByChurn(sorted)
			top := sorted[0]
			fileStat := subtleGreen.Render(fmt.Sprintf("+%d", top.Insertions)) +
				" " + subtleRed.Render(fmt.Sprintf("-%d", top.Deletions))
			fileStatW := ansi.StringWidth(fileStat)
			maxFileW := width - 4 - fileStatW - 2
			if maxFileW < 4 {
				maxFileW = 4
			}
			nameTrunc := truncateVisible(top.Path, maxFileW)
			suffix := ""
			if len(sorted) > 1 {
				suffix = StyleSubtle.Render(fmt.Sprintf(" … %d more", len(sorted)-1))
			}
			line3 = "  " + StyleSubtle.Render(nameTrunc) + " " + fileStat + suffix
		} else if r.group != nil && len(r.group.commits) > 0 {
			line3 = "  " + StyleSubtle.Render(fmt.Sprintf("%d commit(s)", len(r.group.commits)))
		}

		lines = append(lines, line1, line2, line3, "")
	}

	return lines
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
	titleStyle := StyleHeading
	title := titleStyle.Render("SPEC") + "  " + StyleSubtle.Render("›") + "  " + sess.GetDisplayName()
	header := title + "\n" + StyleSubtle.Render(strings.Repeat("─", innerWidth(width)))
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
	contentW := modalContentWidth(width)

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
	footer := "\n" + StyleSubtle.Render(strings.Repeat("─", innerWidth(width))) + "\n" +
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

// reviewLeftPaneWidth returns the task-ledger width for wide two-pane mode.
// Returns 0 when width < 120 (narrow: stacked layout).
func reviewLeftPaneWidth(width int) int {
	if width < 120 {
		return 0
	}
	w := width * 40 / 100
	if w < 38 {
		w = 38
	}
	if w > 52 {
		w = 52
	}
	return w
}

// View renders the review panel — either the spec overlay or the main panel.
// At width ≥ 120 the body is two-pane (task ledger left, diff right);
// at width < 120 the body falls back to the stacked layout via renderReviewPanel.
func (m *reviewPanelModel) View() string {
	if m == nil || m.session == nil {
		return ""
	}
	if m.specOverlay {
		plan, _ := m.session.CachedPlan()
		return renderReviewSpecOverlay(m.session, plan, m.specOverlayScroll, m.width, m.height)
	}
	entry := m.deps.ReviewCache(m.repoPath, m.session.ID)
	prDraftInFlight := m.drafting
	var checkState *checksTabState
	if m.deps.ValidationRuns != nil {
		if run := m.deps.ValidationRuns(m.repoPath, m.session.ID); run != nil {
			checkState = &checksTabState{
				checks:  run.checks,
				results: run.results,
				cursor:  m.checksCursor,
				scroll:  m.checksScroll,
			}
		}
	}

	leftW := reviewLeftPaneWidth(m.width)
	if leftW == 0 {
		// Narrow: stacked layout.
		return renderReviewPanel(m.session, entry, m.width, m.height, m.taskCursor, prDraftInFlight, checkState, m.now)
	}

	// Wide: two-pane layout.
	headerLines := renderReviewHeader(m.session, m.width, m.now)
	checksStripLines := renderChecksStrip(checkState, m.width, m.now)

	footerLineCount := 3
	draftLineCount := 0
	if prDraftInFlight {
		draftLineCount = 1
	}
	bodyH := m.height - len(headerLines) - len(checksStripLines) - footerLineCount - draftLineCount
	if bodyH < 4 {
		bodyH = 4
	}

	// Right pane gets remaining width after the separator.
	rightW := innerWidth(m.width) - leftW - 1
	if rightW < 20 {
		rightW = 20
	}

	var body string
	if entry == nil {
		body = padViewBody(StyleSubtle.Render("loading diff stats…"), innerWidth(m.width), bodyH)
	} else {
		// Refresh viewport with current focused task's diff.
		m.refreshDiffPane(entry, rightW, bodyH-1) // bodyH-1: 1 line for file header

		leftLines := renderTaskListPane(entry, leftW, bodyH, m.taskCursor, m.now)
		leftStr := padViewBody(strings.Join(leftLines, "\n"), leftW, bodyH)

		sep := renderVerticalSeparator(bodyH)
		rightStr := m.renderTaskDiffPane(entry, rightW, bodyH)

		body = lipgloss.JoinHorizontal(lipgloss.Top, leftStr, sep, rightStr)
	}

	// Build footer.
	hints := reviewFooterHints(prDraftInFlight, checkState != nil)

	var lines []string
	lines = append(lines, headerLines...)
	lines = append(lines, checksStripLines...)
	lines = append(lines, strings.Split(body, "\n")...)
	if prDraftInFlight {
		lines = append(lines, StyleWarning.Render(reviewSpinnerFrame(m.now)+" Pushing branch and drafting PR…"))
	}
	lines = append(lines, "")
	lines = append(lines, StyleSubtle.Render(strings.Repeat("─", innerWidth(m.width))))
	lines = append(lines, hints)

	return strings.Join(lines, "\n")
}
