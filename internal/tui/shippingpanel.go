package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/baton/internal/agent"
	"github.com/devenjarvis/baton/internal/github"
)

// renderShippingPanel renders the fullscreen shipping panel for a session.
// entry may be nil while the first PR poll is in flight (shows loading state).
func renderShippingPanel(sess *agent.Session, entry *prCacheEntry, width, height int) string {
	var lines []string

	// ── Header ────────────────────────────────────────────────────────────────
	headerLeft := lipgloss.NewStyle().Foreground(lipgloss.Color("#5ab58a")).Bold(true).Render("SHIPPING") +
		"  " + StyleSubtle.Render("›") +
		"  " + lipgloss.NewStyle().Render(sess.GetDisplayName())
	var headerRight string
	if entry != nil && entry.pr != nil {
		headerRight = StyleLink.Render(fmt.Sprintf("#%d", entry.pr.Number))
	}
	gap := width - ansi.StringWidth(headerLeft) - ansi.StringWidth(headerRight) - 4
	if gap < 1 {
		gap = 1
	}
	lines = append(lines, headerLeft+strings.Repeat(" ", gap)+headerRight)
	lines = append(lines, StyleSubtle.Render(strings.Repeat("─", width-2)))

	if entry == nil || entry.pr == nil {
		lines = append(lines, StyleSubtle.Render("  fetching PR status…"))
		lines = append(lines, "")
		lines = append(lines, StyleSubtle.Render(strings.Repeat("─", width-2)))
		lines = append(lines, shippingHints(false))
		return strings.Join(lines, "\n")
	}

	pr := entry.pr

	// ── PR summary ────────────────────────────────────────────────────────────
	titleLine := "  " + lipgloss.NewStyle().Bold(true).Render(pr.Title)
	lines = append(lines, titleLine)

	var mergeableLabel string
	switch pr.MergeableState {
	case "clean":
		mergeableLabel = lipgloss.NewStyle().Foreground(ColorSuccess).Render("✓ mergeable")
	case "dirty":
		mergeableLabel = lipgloss.NewStyle().Foreground(ColorError).Render("✗ conflicts")
	default:
		mergeableLabel = StyleSubtle.Render("⋯ checking")
	}
	baseLine := "  " + StyleSubtle.Render("base →") + " " + pr.BaseBranch +
		"   " + mergeableLabel
	if pr.Draft {
		baseLine += "   " + StyleSubtle.Render("(draft)")
	}
	if phrase := rowStatePhrase(entry); phrase != "" {
		baseLine += "   " + statePhraseStyle(phrase).Render(phrase)
	}
	lines = append(lines, baseLine)
	lines = append(lines, "")
	lines = append(lines, StyleSubtle.Render(strings.Repeat("─", width-2)))

	// ── CI checks ─────────────────────────────────────────────────────────────
	lines = append(lines, StyleSubtle.Render("CI CHECKS"))
	if entry.checks == nil || entry.checks.Total == 0 {
		lines = append(lines, StyleSubtle.Render("  no checks found"))
	} else {
		for _, run := range entry.checks.Runs {
			lines = append(lines, renderCheckRow(run, width))
		}
	}
	lines = append(lines, "")
	lines = append(lines, StyleSubtle.Render(strings.Repeat("─", width-2)))

	// ── Review threads ────────────────────────────────────────────────────────
	lines = append(lines, StyleSubtle.Render("REVIEW THREADS"))
	if len(entry.threads) == 0 {
		lines = append(lines, StyleSubtle.Render("  no review feedback"))
	} else {
		for _, thread := range entry.threads {
			lines = append(lines, renderReviewThread(thread, width)...)
		}
	}

	// ── Footer ────────────────────────────────────────────────────────────────
	// Pad remaining height so the footer always sits at the bottom.
	used := len(lines) + 2 // +2 for separator + hints
	if remaining := height - used; remaining > 0 {
		lines = append(lines, strings.Repeat("\n", remaining-1))
	}
	lines = append(lines, StyleSubtle.Render(strings.Repeat("─", width-2)))
	lines = append(lines, shippingHints(isMergeReady(entry)))

	return strings.Join(lines, "\n")
}

// renderCheckRow renders one check-run line: status icon + name + duration.
func renderCheckRow(run github.CheckRun, width int) string {
	var icon string
	var iconStyle lipgloss.Style
	switch {
	case run.Status != "completed":
		icon = "○"
		iconStyle = lipgloss.NewStyle().Foreground(ColorWarning)
	case run.Conclusion == "success" || run.Conclusion == "skipped" || run.Conclusion == "neutral":
		icon = "✓"
		iconStyle = lipgloss.NewStyle().Foreground(ColorSuccess)
	default:
		icon = "✗"
		iconStyle = lipgloss.NewStyle().Foreground(ColorError)
	}

	var dur string
	if !run.StartedAt.IsZero() && run.Duration > 0 {
		d := run.Duration.Round(time.Second)
		if d < time.Minute {
			dur = fmt.Sprintf("%ds", int(d.Seconds()))
		} else {
			dur = fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
		}
	}

	name := truncateVisible(run.Name, width-20)
	row := "  " + iconStyle.Render(icon) + "  " + name
	if dur != "" {
		padW := width - 4 - ansi.StringWidth(name) - ansi.StringWidth(dur) - 6
		if padW < 1 {
			padW = 1
		}
		row += strings.Repeat(" ", padW) + StyleSubtle.Render(dur)
	}
	return row
}

// renderReviewThread renders one reviewer block (state + body + inline comments).
func renderReviewThread(thread github.ReviewThread, width int) []string {
	lines := make([]string, 0, 2+len(thread.Comments)*2)

	stateStyle := StyleSubtle
	switch thread.State {
	case "APPROVED":
		stateStyle = lipgloss.NewStyle().Foreground(ColorSuccess)
	case "CHANGES_REQUESTED":
		stateStyle = lipgloss.NewStyle().Foreground(ColorError)
	}
	header := "  " + lipgloss.NewStyle().Bold(true).Render(thread.Reviewer) +
		"  " + stateStyle.Render(strings.ToLower(strings.ReplaceAll(thread.State, "_", " ")))
	lines = append(lines, header)

	if thread.Body != "" {
		body := truncateVisible(thread.Body, width-6)
		lines = append(lines, "    "+StyleSubtle.Render(body))
	}

	for _, c := range thread.Comments {
		fileLabel := StyleSubtle.Render(c.Path)
		if c.Line > 0 {
			fileLabel += StyleSubtle.Render(fmt.Sprintf(":%d", c.Line))
		}
		lines = append(lines, "    "+fileLabel)
		commentBody := truncateVisible(c.Body, width-8)
		lines = append(lines, "      "+commentBody)
	}
	lines = append(lines, "")
	return lines
}

// shippingHints returns the action hint line for the shipping panel footer.
func shippingHints(mergeReady bool) string {
	mKey := lipgloss.NewStyle().Foreground(lipgloss.Color("#5ab58a")).Render("m")
	mDesc := StyleSubtle.Render(" — merge")
	if !mergeReady {
		mKey = StyleSubtle.Render("m")
	}
	return "  " +
		mKey + mDesc +
		"   " + StyleSubtle.Render("M — force merge") +
		"   " + lipgloss.NewStyle().Foreground(lipgloss.Color("#7ec8e3")).Render("r") + StyleSubtle.Render(" — address feedback") +
		"   " + lipgloss.NewStyle().Foreground(lipgloss.Color("#7ec8e3")).Render("p") + StyleSubtle.Render(" — open PR") +
		"   " + lipgloss.NewStyle().Foreground(lipgloss.Color("#7ec8e3")).Render("t") + StyleSubtle.Render(" — terminal") +
		"   " + StyleSubtle.Render("ESC — back")
}
