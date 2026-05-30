package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/github"
)

// renderShippingPanel renders the fullscreen shipping panel for a session.
// entry may be nil while the first PR poll is in flight (shows loading state).
// cursor and scroll control the feedback two-pane UI; triage holds user verdicts.
func renderShippingPanel(sess *agent.Session, entry *prCacheEntry, width, height, cursor, scroll int, triage map[string]*feedbackTriageEntry) string {
	var lines []string

	// ── Header ────────────────────────────────────────────────────────────────
	headerLeft := StyleHeading.Foreground(ColorShipping).Render("SHIPPING") +
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
	titleLine := "  " + StyleBold.Render(pr.Title)
	lines = append(lines, titleLine)

	var mergeableLabel string
	switch pr.MergeableState {
	case "clean":
		mergeableLabel = StyleSuccess.Render("✓ mergeable")
	case "dirty":
		mergeableLabel = StyleError.Render("✗ conflicts")
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

	// ── Review feedback (two-pane) ────────────────────────────────────────────
	lines = append(lines, StyleSubtle.Render("REVIEW FEEDBACK"))
	items := feedbackItems(entry.threads)
	if len(items) == 0 {
		lines = append(lines, StyleSubtle.Render("  no review feedback"))
		lines = append(lines, "")
	} else {
		// Reserve height for header+PR+CI+dividers already rendered (+ footer).
		usedAbove := len(lines) + 3 // +3 for footer sep + hints + blank before footer
		bodyH := height - usedAbove
		if bodyH < 4 {
			bodyH = 4
		}

		if width < 80 {
			// Narrow: stack list above detail.
			listH := bodyH / 2
			detailH := bodyH - listH - 1
			if listH < 2 {
				listH = 2
			}
			if detailH < 2 {
				detailH = 2
			}
			w := width - 2
			lines = append(lines, renderFeedbackList(items, cursor, triage, w, listH)...)
			lines = append(lines, StyleSubtle.Render(strings.Repeat("─", width-2)))
			lines = append(lines, renderFeedbackDetail(items, cursor, triage, scroll, w, detailH)...)
		} else {
			// Wide: side-by-side panes.
			leftW := width * 4 / 10
			if leftW < 30 {
				leftW = 30
			}
			rightW := width - leftW - 5
			if rightW < 20 {
				rightW = 20
			}
			leftLines := renderFeedbackList(items, cursor, triage, leftW, bodyH)
			rightLines := renderFeedbackDetail(items, cursor, triage, scroll, rightW, bodyH)
			gutter := " " + StyleSubtle.Render("│") + " "
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
				padW := leftW - ansi.StringWidth(l)
				if padW < 0 {
					padW = 0
				}
				lines = append(lines, l+strings.Repeat(" ", padW)+gutter+r)
			}
		}
	}

	// ── Footer ────────────────────────────────────────────────────────────────
	used := len(lines) + 2 // +2 for separator + hints
	if remaining := height - used; remaining > 0 {
		lines = append(lines, strings.Repeat("\n", remaining-1))
	}
	lines = append(lines, StyleSubtle.Render(strings.Repeat("─", width-2)))
	lines = append(lines, shippingHints(isMergeReady(entry)))

	return strings.Join(lines, "\n")
}

// verdictGlyph returns the colored one-character glyph for a verdict.
func verdictGlyph(v feedbackVerdict) string {
	switch v {
	case feedbackApproved:
		return StyleSuccess.Render("✓")
	case feedbackDisagreed:
		return StyleError.Render("✗")
	default:
		return StyleSubtle.Render("·")
	}
}

// renderFeedbackList renders the left-pane list of feedback items.
func renderFeedbackList(items []feedbackItem, cursor int, triage map[string]*feedbackTriageEntry, w, h int) []string {
	header := []string{StyleSubtle.Render("FEEDBACK"), ""}
	const headerLines = 2

	rowsH := h - headerLines
	if rowsH < 1 {
		rowsH = 1
	}
	offset := cursor - rowsH/2
	if offset < 0 {
		offset = 0
	}
	if offset+rowsH > len(items) {
		offset = len(items) - rowsH
		if offset < 0 {
			offset = 0
		}
	}

	cursorBar := StyleAccent.Render("│")

	end := offset + rowsH
	if end > len(items) {
		end = len(items)
	}
	lines := make([]string, 0, h)
	lines = append(lines, header...)

	for i := offset; i < end; i++ {
		item := items[i]
		selected := i == cursor
		key := feedbackItemKey(item)

		var verdict feedbackVerdict
		if triage != nil {
			if e := triage[key]; e != nil {
				verdict = e.Verdict
			}
		}
		glyph := verdictGlyph(verdict)

		// Note indicator.
		noteGlyph := "  "
		if triage != nil {
			if e := triage[key]; e != nil && strings.TrimSpace(e.Note) != "" {
				noteGlyph = " " + StyleSubtle.Render("✎")
			}
		}

		// Label: reviewer (for body items) or path:line (for inline).
		var label string
		if item.IsInline {
			if item.Line > 0 {
				label = fmt.Sprintf("%s:%d", item.Path, item.Line)
			} else {
				label = item.Path
			}
		} else {
			label = item.Reviewer
		}

		// Overhead: glyph(1) + space(1) + border×2(4) + note(2) + sep(2)
		overhead := 1 + 1 + 4 + 2 + 2
		maxLabelW := w - overhead - 1
		if maxLabelW < 4 {
			maxLabelW = 4
		}
		// Truncate label; show body preview inline after a colon.
		labelStr := truncateVisible(label, maxLabelW)
		rowText := glyph + " " + labelStr + noteGlyph

		if selected {
			contentW := w - 4
			if rw := ansi.StringWidth(rowText); rw < contentW {
				rowText += strings.Repeat(" ", contentW-rw)
			}
			lines = append(lines, " "+cursorBar+rowText+cursorBar+" ")
		} else {
			lines = append(lines, "  "+rowText)
		}
	}
	return lines
}

// renderFeedbackDetail renders the right-pane detail for the cursor-selected item.
func renderFeedbackDetail(items []feedbackItem, cursor int, triage map[string]*feedbackTriageEntry, scroll, w, h int) []string {
	if len(items) == 0 || cursor >= len(items) {
		return []string{StyleSubtle.Render("no feedback")}
	}
	item := items[cursor]
	key := feedbackItemKey(item)

	lines := make([]string, 0, h)

	// Heading: reviewer + state (or path:line for inline).
	var heading string
	stateStyle := StyleSubtle
	switch item.State {
	case "APPROVED":
		stateStyle = StyleSuccess
	case "CHANGES_REQUESTED":
		stateStyle = StyleError
	}
	if item.IsInline {
		loc := item.Path
		if item.Line > 0 {
			loc = fmt.Sprintf("%s:%d", item.Path, item.Line)
		}
		heading = StyleSubtle.Render(loc) + "  " + stateStyle.Render(strings.ToLower(strings.ReplaceAll(item.State, "_", " ")))
	} else {
		heading = StyleBold.Render(item.Reviewer) + "  " + stateStyle.Render(strings.ToLower(strings.ReplaceAll(item.State, "_", " ")))
	}
	lines = append(lines, heading, "")

	// Wrapped body.
	wrapped := wrapText(item.Body, w-2)
	// Scroll offset into wrapped lines.
	bodyH := h - 4 // leave room for heading + note section
	if bodyH < 2 {
		bodyH = 2
	}
	maxScroll := len(wrapped) - bodyH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}

	// Scroll affordance above.
	if scroll > 0 {
		lines = append(lines, StyleSubtle.Render(fmt.Sprintf("↑ %d lines above", scroll)))
	}

	end := scroll + bodyH
	if end > len(wrapped) {
		end = len(wrapped)
	}
	lines = append(lines, wrapped[scroll:end]...)

	// Scroll affordance below.
	remaining := len(wrapped) - end
	if remaining > 0 {
		lines = append(lines, StyleSubtle.Render(fmt.Sprintf("↓ %d below", remaining)))
	}

	// Note section.
	if triage != nil {
		if e := triage[key]; e != nil && strings.TrimSpace(e.Note) != "" {
			lines = append(lines, "")
			lines = append(lines, StyleSubtle.Render("Your note:"))
			for _, noteLine := range wrapText(e.Note, w-2) {
				lines = append(lines, "  "+noteLine)
			}
		}
	}

	return lines
}

// renderCheckRow renders one check-run line: status icon + name + duration.
func renderCheckRow(run github.CheckRun, width int) string {
	var icon string
	var iconStyle lipgloss.Style
	switch {
	case run.Status != "completed":
		icon = "○"
		iconStyle = StyleWarning
	case run.Conclusion == "success" || run.Conclusion == "skipped" || run.Conclusion == "neutral":
		icon = "✓"
		iconStyle = StyleSuccess
	default:
		icon = "✗"
		iconStyle = StyleError
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

// feedbackVerdict is the user's disposition on a single feedback item.
type feedbackVerdict int

const (
	feedbackNeutral   feedbackVerdict = iota
	feedbackApproved                  // user agreed; item will be addressed
	feedbackDisagreed                 // user disputes; item framed as advisory
)

// feedbackTriageEntry holds the verdict and optional guidance note for one item.
type feedbackTriageEntry struct {
	Verdict feedbackVerdict
	Note    string
}

// feedbackItem is a flattened view of a single piece of review feedback.
type feedbackItem struct {
	Reviewer  string
	State     string
	Path      string
	Line      int
	Body      string
	CommentID int64
	IsInline  bool
}

// feedbackItems flattens review threads into an ordered slice of feedback items.
// For each thread: one body item (when non-empty), then one item per inline comment.
func feedbackItems(threads []github.ReviewThread) []feedbackItem {
	var items []feedbackItem
	for _, t := range threads {
		if strings.TrimSpace(t.Body) != "" {
			items = append(items, feedbackItem{
				Reviewer: t.Reviewer,
				State:    t.State,
				Body:     t.Body,
				IsInline: false,
			})
		}
		for _, c := range t.Comments {
			items = append(items, feedbackItem{
				Reviewer:  t.Reviewer,
				State:     t.State,
				Path:      c.Path,
				Line:      c.Line,
				Body:      c.Body,
				CommentID: c.ID,
				IsInline:  true,
			})
		}
	}
	return items
}

// feedbackItemKey returns the stable triage map key for an item.
// Inline comments use "comment:<id>"; thread bodies use "thread:<reviewer>".
// The reviewer key is unique because GetReviewThreads produces at most one
// ReviewThread per reviewer (latest-review dedup + seen guard).
func feedbackItemKey(item feedbackItem) string {
	if item.IsInline {
		return fmt.Sprintf("comment:%d", item.CommentID)
	}
	return "thread:" + item.Reviewer
}

// shippingHints returns the action hint line for the shipping panel footer.
func shippingHints(mergeReady bool) string {
	mKey := StyleAccent.Foreground(ColorShipping).Render("m")
	mDesc := StyleSubtle.Render(" — merge")
	if !mergeReady {
		mKey = StyleSubtle.Render("m")
	}
	key := func(s string) string { return StyleAccent.Foreground(ColorBuilding).Render(s) }
	return "  " +
		mKey + mDesc +
		"   " + StyleSubtle.Render("M — force merge") +
		"   " + key("a") + StyleSubtle.Render(" — approve") +
		"   " + key("x") + StyleSubtle.Render(" — disagree") +
		"   " + key("u") + StyleSubtle.Render(" — clear") +
		"   " + key("n") + StyleSubtle.Render(" — note") +
		"   " + key("r") + StyleSubtle.Render(" — address feedback") +
		"   " + key("p") + StyleSubtle.Render(" — open PR") +
		"   " + key("t") + StyleSubtle.Render(" — terminal") +
		"   " + StyleSubtle.Render("ESC — back")
}
