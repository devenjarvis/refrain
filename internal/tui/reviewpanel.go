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

// renderReviewPanel renders the fullscreen review panel for a session.
// entry may be nil while diff stats are being fetched (shows loading placeholder).
func renderReviewPanel(sess *agent.Session, entry *reviewDiffEntry, width int) string {
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

	// Changes
	if entry == nil {
		lines = append(lines, StyleSubtle.Render("loading diff stats…"))
	} else {
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

	// Action footer
	lines = append(lines, "")
	lines = append(lines, StyleSubtle.Render(strings.Repeat("─", width-2)))
	hints := "  " +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#5ab58a")).Render("p") + StyleSubtle.Render(" — open PR in GitHub") +
		"   " + lipgloss.NewStyle().Foreground(lipgloss.Color("#7ec8e3")).Render("t") + StyleSubtle.Render(" — open agent terminal") +
		"   " + StyleSubtle.Render("c — mark complete") +
		"   " + StyleSubtle.Render("e — open in editor") +
		"   " + StyleSubtle.Render("d — defer") +
		"   " + StyleSubtle.Render("ESC — back to focus")
	lines = append(lines, hints)

	return strings.Join(lines, "\n")
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
