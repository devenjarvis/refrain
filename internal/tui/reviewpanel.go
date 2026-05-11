package tui

import (
	"fmt"
	"path/filepath"
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
			return "✓", "Pass", StyleSuccess
		case agent.VerdictConcerns:
			return "!", "Concerns", StyleWarning
		case agent.VerdictFail:
			return "✗", "Fail", StyleError
		}
	case verdictErr:
		return "✗", "Error", StyleError
	case verdictNoDiff:
		return "⊘", "No matching diff", StyleSubtle
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
	headerLeft := lipgloss.NewStyle().Foreground(lipgloss.Color("#9b7fdb")).Bold(true).Render("REVIEW") +
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
	var lines []string

	// Header
	age := ""
	if !sess.DoneAt().IsZero() {
		mins := int(time.Since(sess.DoneAt()).Minutes())
		age = fmt.Sprintf("done %dm ago", mins)
	}
	headerLeft := lipgloss.NewStyle().Foreground(lipgloss.Color("#9b7fdb")).Bold(true).Render("REVIEW") +
		"  " + StyleSubtle.Render("›") +
		"  " + lipgloss.NewStyle().Render(sess.GetDisplayName())
	headerRight := StyleSubtle.Render(age)
	gap := width - ansi.StringWidth(headerLeft) - ansi.StringWidth(headerRight) - 4
	if gap < 1 {
		gap = 1
	}
	lines = append(lines, headerLeft+strings.Repeat(" ", gap)+headerRight)
	lines = append(lines, StyleSubtle.Render(strings.Repeat("─", width-2)))

	// Original Intent
	lines = append(lines, StyleSubtle.Render("ORIGINAL INTENT"))
	prompt := sess.OriginalPrompt()
	if prompt == "" {
		prompt = "(no prompt recorded)"
	}
	intentLines := wrapText(prompt, width-6)
	if len(intentLines) > 6 {
		intentLines = append(intentLines[:5], StyleSubtle.Render("…"))
	}
	accentStyle := lipgloss.NewStyle().
		BorderLeft(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#9b7fdb")).
		PaddingLeft(1)
	lines = append(lines, accentStyle.Render(strings.Join(intentLines, "\n")))
	lines = append(lines, "")
	lines = append(lines, StyleSubtle.Render(strings.Repeat("─", width-2)))

	// Body: task list (if plan exists) or legacy file-centric view.
	if entry == nil {
		lines = append(lines, StyleSubtle.Render("loading diff stats…"))
	} else if len(entry.tasks) > 0 || len(entry.groups) > 0 {
		// Overhead: header(1) + divider(1) + "ORIGINAL INTENT"(1) + intent(≤6) +
		// blank(1) + divider(1) + blank(1) + divider(1) + hints(1) = 14 max.
		// +1 when prDraftInFlight adds a spinner line above the footer.
		overhead := 14
		if prDraftInFlight {
			overhead++
		}
		taskListHeight := height - overhead
		if taskListHeight < 4 {
			taskListHeight = 4
		}
		lines = append(lines, renderTaskList(entry, width, taskListHeight, cursor)...)
	} else {
		// No plan — fall back to the aggregate file view.
		leftWidth := (width - 4) / 2
		rightWidth := width - leftWidth - 4
		leftLines := renderFocusList(entry, leftWidth)
		rightLines := renderReviewShape(entry, rightWidth)
		maxRows := len(leftLines)
		if len(rightLines) > maxRows {
			maxRows = len(rightLines)
		}
		for i := 0; i < maxRows; i++ {
			l, r := "", ""
			if i < len(leftLines) {
				l = leftLines[i]
			}
			if i < len(rightLines) {
				r = rightLines[i]
			}
			pad := leftWidth - ansi.StringWidth(l)
			if pad < 0 {
				pad = 0
			}
			lines = append(lines, l+strings.Repeat(" ", pad+2)+r)
		}
	}

	// In-flight PR draft status line
	if prDraftInFlight {
		draftStatus := lipgloss.NewStyle().Foreground(ColorWarning).Render(reviewSpinnerFrame() + " Pushing branch and drafting PR…")
		lines = append(lines, draftStatus)
	}

	// Action footer
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
	subtleGreen := lipgloss.NewStyle().Foreground(lipgloss.Color("#7ed321"))
	subtleRed := lipgloss.NewStyle().Foreground(lipgloss.Color("#e74c3c"))

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

		// Stat string.
		statStr := ""
		if r.group != nil && r.group.stats != nil {
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

// renderTaskList renders the scrollable per-task review rows. availHeight
// controls the visible window; the list scrolls so the cursor stays visible.
func renderTaskList(entry *reviewDiffEntry, width, availHeight, cursor int) []string {
	const headerLines = 2 // "PLAN TASKS" + blank
	header := []string{StyleSubtle.Render("PLAN TASKS"), ""}

	// Build a merged view: one row per plan task, plus the "Other changes" group.
	type row struct {
		taskIndex int
		taskText  string
		group     *taskReviewGroup // may be nil if no commits for this task
	}

	// Index groups by taskIndex for O(1) lookup.
	groupByIdx := make(map[int]*taskReviewGroup, len(entry.groups))
	for i := range entry.groups {
		g := &entry.groups[i]
		groupByIdx[g.taskIndex] = g
	}

	rows := make([]row, 0, len(entry.tasks)+1)
	for _, t := range entry.tasks {
		g := groupByIdx[t.Index]
		rows = append(rows, row{taskIndex: t.Index, taskText: t.Text, group: g})
	}
	// Append "other" group if it exists.
	if other, ok := groupByIdx[0]; ok {
		rows = append(rows, row{taskIndex: 0, taskText: "Other changes", group: other})
	}

	// Compute visible window so the cursor stays centred.
	rowsH := availHeight - headerLines
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

	cursorStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#2a2a3a")).
		Bold(true)
	checkStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#5ab58a"))
	concernStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#f0c060"))
	failStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#e74c3c"))
	spinStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9b7fdb"))
	subtleGreen := lipgloss.NewStyle().Foreground(lipgloss.Color("#7ed321"))
	subtleRed := lipgloss.NewStyle().Foreground(lipgloss.Color("#e74c3c"))

	end := offset + rowsH
	if end > len(rows) {
		end = len(rows)
	}
	lines := make([]string, 0, headerLines+end-offset)
	lines = append(lines, header...)

	for i := offset; i < end; i++ {
		r := rows[i]
		selected := i == cursor

		// Task index label and text.
		label := fmt.Sprintf("[%d]", r.taskIndex)
		if r.taskIndex == 0 {
			label = "[?]"
		}
		labelW := 5
		labelPart := StyleSubtle.Render(fmt.Sprintf("%-*s", labelW, label))

		maxTextW := width - labelW - 30
		if maxTextW < 10 {
			maxTextW = 10
		}
		textPart := truncateVisible(r.taskText, maxTextW)

		// Commit count + stats.
		statPart := ""
		if r.group != nil && r.group.stats != nil {
			st := r.group.stats
			commitCount := len(r.group.commits)
			statPart = fmt.Sprintf("%d commit", commitCount)
			if commitCount != 1 {
				statPart += "s"
			}
			if st.Insertions > 0 || st.Deletions > 0 {
				statPart += "  " +
					subtleGreen.Render(fmt.Sprintf("+%d", st.Insertions)) +
					" " +
					subtleRed.Render(fmt.Sprintf("-%d", st.Deletions))
			}
		} else if r.group == nil {
			statPart = StyleSubtle.Render("no commits")
		}

		// Verdict badge.
		verdictPart := ""
		if entry.verdicts != nil {
			if v, ok := entry.verdicts[r.taskIndex]; ok {
				switch v.state {
				case verdictPending:
					verdictPart = StyleSubtle.Render("···")
				case verdictRunning:
					verdictPart = spinStyle.Render(reviewSpinnerFrame())
				case verdictDone:
					switch v.verdict.Kind {
					case agent.VerdictPass:
						verdictPart = checkStyle.Render("✓ pass")
					case agent.VerdictConcerns:
						verdictPart = concernStyle.Render("! concerns")
					case agent.VerdictFail:
						verdictPart = failStyle.Render("✗ fail")
					}
					if v.verdict.Rationale != "" {
						rationale := truncateVisible(v.verdict.Rationale, width-12)
						verdictPart += "  " + StyleSubtle.Render(rationale)
					}
				case verdictErr:
					errStr := "err"
					if v.err != nil {
						errStr = truncateVisible(v.err.Error(), 30)
					}
					verdictPart = failStyle.Render("✗ " + errStr)
				case verdictNoDiff:
					verdictPart = StyleSubtle.Render("no diff found")
					statPart = "" // "no diff found" already conveys this; avoid the duplicate "no commits" label
				}
			}
		}

		// Assemble the row.
		rowText := labelPart + " " + textPart
		if verdictPart != "" {
			// Right-align verdict badge in the remaining space.
			usedW := labelW + 1 + ansi.StringWidth(textPart)
			spaceW := width - 4 - usedW - ansi.StringWidth(statPart) - ansi.StringWidth(verdictPart) - 3
			if spaceW < 1 {
				spaceW = 1
			}
			rowText += strings.Repeat(" ", spaceW) + statPart + "  " + verdictPart
		} else if statPart != "" {
			usedW := labelW + 1 + ansi.StringWidth(textPart)
			spaceW := width - 4 - usedW - ansi.StringWidth(statPart)
			if spaceW < 1 {
				spaceW = 1
			}
			rowText += strings.Repeat(" ", spaceW) + statPart
		}

		if selected {
			// Pad to full width for background highlight.
			padW := width - 4 - ansi.StringWidth(rowText)
			if padW < 0 {
				padW = 0
			}
			rowText = cursorStyle.Render(rowText + strings.Repeat(" ", padW))
		}
		lines = append(lines, "  "+rowText)
	}

	return lines
}

// renderFocusList returns left-column lines: total + top files + also-changed.
func renderFocusList(entry *reviewDiffEntry, width int) []string {
	lines := make([]string, 0, len(entry.files)+6)
	agg := entry.aggregate
	lines = append(lines, StyleSubtle.Render("CHANGES"))
	totalLine := fmt.Sprintf("%d files · ", agg.Files) +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#7ed321")).Render(fmt.Sprintf("+%d", agg.Insertions)) +
		" " +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#e74c3c")).Render(fmt.Sprintf("-%d", agg.Deletions))
	lines = append(lines, totalLine)
	lines = append(lines, "")

	sorted := make([]git.FileStat, len(entry.files))
	copy(sorted, entry.files)
	sortFileStatsByChurn(sorted)

	lines = append(lines, StyleSubtle.Render("FOCUS HERE FIRST"))
	top := sorted
	if len(top) > 3 {
		top = sorted[:3]
	}
	for _, f := range top {
		cat := classifyFile(f.Path)
		name := truncateVisible(f.Path, width-20)
		stat := lipgloss.NewStyle().Foreground(lipgloss.Color("#7ed321")).Render(fmt.Sprintf("+%d", f.Insertions)) +
			" " + lipgloss.NewStyle().Foreground(lipgloss.Color("#e74c3c")).Render(fmt.Sprintf("-%d", f.Deletions)) +
			" · " + StyleSubtle.Render(cat)
		lines = append(lines, "  "+name+"  "+stat)
	}

	if len(sorted) > 3 {
		lines = append(lines, "")
		lines = append(lines, StyleSubtle.Render("ALSO CHANGED"))
		rest := sorted[3:]
		if len(rest) > 5 {
			rest = rest[:5]
		}
		for _, f := range rest {
			lines = append(lines, StyleSubtle.Render("  "+truncateVisible(f.Path, width-4)))
		}
		if len(sorted)-3 > 5 {
			lines = append(lines, StyleSubtle.Render(fmt.Sprintf("  … %d more files", len(sorted)-8)))
		}
	}
	return lines
}

// renderReviewShape returns right-column lines: logic/test/config bars.
func renderReviewShape(entry *reviewDiffEntry, width int) []string {
	var logicLines, testLines, configLines int
	for _, f := range entry.files {
		churn := f.Insertions + f.Deletions
		switch classifyFile(f.Path) {
		case "tests":
			testLines += churn
		case "config":
			configLines += churn
		default:
			logicLines += churn
		}
	}
	total := logicLines + testLines + configLines
	if total == 0 {
		total = 1
	}

	logicPct := float64(logicLines) / float64(total)
	testPct := float64(testLines) / float64(total)
	configPct := float64(configLines) / float64(total)

	barMax := width - 14
	if barMax < 4 {
		barMax = 4
	}

	bar := func(pct float64, color lipgloss.Color) string {
		filled := int(pct * float64(barMax))
		if filled > barMax {
			filled = barMax
		}
		b := strings.Repeat("█", filled) + strings.Repeat("░", barMax-filled)
		return lipgloss.NewStyle().Foreground(color).Render(b)
	}

	var lines []string
	lines = append(lines, StyleSubtle.Render("REVIEW SHAPE"))
	lines = append(lines, fmt.Sprintf("Logic   %3d%%  %s", int(logicPct*100), bar(logicPct, lipgloss.Color("#9b7fdb"))))
	lines = append(lines, fmt.Sprintf("Tests   %3d%%  %s", int(testPct*100), bar(testPct, lipgloss.Color("#7ec8e3"))))
	lines = append(lines, fmt.Sprintf("Config  %3d%%  %s", int(configPct*100), bar(configPct, lipgloss.Color("#555555"))))
	return lines
}

// classifyFile returns "tests", "config", or "logic" based on file path/extension.
func classifyFile(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(path)

	if strings.HasSuffix(base, "_test.go") ||
		strings.Contains(path, "/test/") ||
		strings.Contains(path, "/tests/") ||
		strings.Contains(path, "/spec/") ||
		strings.Contains(base, ".test.") ||
		strings.Contains(base, ".spec.") {
		return "tests"
	}

	switch ext {
	case ".json", ".yaml", ".yml", ".toml", ".env", ".ini", ".cfg":
		return "config"
	}
	switch base {
	case "Makefile", "Dockerfile", "docker-compose.yml", "docker-compose.yaml", ".gitignore", ".gitattributes":
		return "config"
	}

	return "logic"
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
