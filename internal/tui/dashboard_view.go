package tui

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/tui/theme"
	"github.com/devenjarvis/refrain/internal/vt"
)

// truncateVisible returns s truncated to n display cells with an ellipsis.
// ANSI-aware; avoids the naive byte-slice truncation that can cut multi-byte
// runes in half or miscount ANSI escape sequences.
func truncateVisible(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= n {
		return s
	}
	return ansi.Truncate(s, n, "…")
}

func (d dashboardModel) View(props dashboardProps) string {
	var out string
	switch {
	case len(props.items) == 0:
		out = d.emptyView()
	case props.panelFocus == focusLaunch:
		out = d.renderFocusLaunchView(props, d.width, d.height)
	case props.panelFocus == focusConfig && props.repoConfigForm != nil:
		out = d.renderRepoConfigOverlay(props, d.width, d.height)
	case props.panelFocus == focusRepoChecks && props.repoChecksEditor != nil:
		out = d.renderRepoChecksOverlay(props, d.width, d.height)
	default:
		out = d.renderFullscreenFocus(props, d.width, d.height)
	}
	return out
}

// renderRepoConfigOverlay renders the per-repo settings form full-screen. It's
// reached from the pipeline by selecting a repo header (when there are
// multiple repos) and pressing enter, or from the cross-repo summary header.
// The form mirrors the global settings overlay's centered-box layout.
func (d dashboardModel) renderRepoConfigOverlay(props dashboardProps, width, height int) string {
	if props.repoConfigForm == nil {
		return d.emptyView()
	}

	var repoName, repoPath string
	for _, item := range props.items {
		if item.kind == listItemRepo && item.repoPath == props.configRepoPath {
			repoName = item.repoName
			repoPath = item.repoPath
			break
		}
	}
	if repoName == "" {
		repoName = props.configRepoPath
	}

	title := StyleTitle.Render(repoName + " Settings")
	pathLine := StyleSubtle.Render(repoPath)
	hint := StyleSubtle.Render("j/k navigate  ←/→ select  enter edit/toggle  ctrl+s save  esc cancel")

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		pathLine, "",
		props.repoConfigForm.View(), "",
		hint,
	)

	box := modalBoxStyle(64).Render(content)
	return placeCentered(width, height, box)
}

// renderRepoChecksOverlay renders the validation-checks list editor as a
// centered modal box, styled to match renderRepoConfigOverlay so the user
// perceives it as a sub-form of the repo settings overlay.
func (d dashboardModel) renderRepoChecksOverlay(props dashboardProps, width, height int) string {
	if props.repoChecksEditor == nil {
		return d.emptyView()
	}

	repoName := props.repoChecksEditor.repoName
	if repoName == "" {
		repoName = props.repoChecksRepoPath
	}

	title := StyleTitle.Render(repoName + " · Validation Checks")
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		title, "",
		props.repoChecksEditor.View(),
	)

	box := modalBoxStyle(72).Render(content)
	return placeCentered(width, height, box)
}

func (d dashboardModel) emptyView() string {
	title := StyleTitle.Render("Refrain")
	subtitle := StyleSubtle.Render("No agents running")
	hint := StyleSubtle.Render("Press n to create a new session")

	content := lipgloss.JoinVertical(lipgloss.Center, title, "", subtitle, "", hint)
	return placeCentered(d.width, d.contentHeight(), content)
}

// renderCardProgressBar returns a progress bar + muted "done/total" suffix
// right-padded to exactly width display cells. Returns "" when total == 0.
// At 100% the bar is colored ColorSuccess; otherwise it uses primary.
func renderCardProgressBar(done, total, width int, primary lipgloss.Color) string {
	if total == 0 {
		return ""
	}
	pct := float64(done) / float64(total)
	suffix := StyleSubtle.Render(fmt.Sprintf("%d/%d tasks", done, total))
	suffixWidth := ansi.StringWidth(suffix)
	// Reserve at least 1 cell for the bar (plus a separating space).
	barWidth := width - suffixWidth - 1
	if barWidth < 1 {
		barWidth = 1
	}
	bar := progress.New(
		progress.WithoutPercentage(),
		progress.WithColorFunc(func(_, _ float64) color.Color {
			if done == total && total > 0 {
				return ColorSuccess
			}
			return primary
		}),
	)
	bar.SetWidth(barWidth)
	rendered := bar.ViewAs(pct) + " " + suffix
	// Pad or trim to exactly width cells so callers can right-align predictably.
	got := ansi.StringWidth(rendered)
	if got < width {
		rendered += strings.Repeat(" ", width-got)
	}
	return rendered
}

// sessionFocusStatus returns a styled inline status badge for a session row in
// the unified SESSIONS list. Priority: Error > Waiting > May Need Input >
// idle-but-reviewable (only when no active agents) > finished (DoneAt set) > normal (N active, M idle).
func (items listItems) sessionFocusStatus(sess *agent.Session) string {
	var waitingCount, activeCount, idleCount, idleAskingCount int
	var firstWaitingReason string
	var hasError bool
	for _, item := range items {
		if item.kind != listItemAgent || item.agent == nil || item.agent.IsShell || item.session != sess {
			continue
		}
		switch item.agent.Status() {
		case agent.StatusError:
			hasError = true
		case agent.StatusWaiting:
			waitingCount++
			if firstWaitingReason == "" {
				firstWaitingReason = item.agent.WaitingReason()
			}
		case agent.StatusActive:
			activeCount++
		case agent.StatusIdle:
			idleCount++
			if item.agent.AskingQuestion() {
				idleAskingCount++
			}
		}
	}
	if hasError {
		return StyleError.Render(theme.GlyphError + " error")
	}
	if waitingCount > 0 {
		badge := fmt.Sprintf("%s %d waiting", theme.GlyphWaiting, waitingCount)
		if firstWaitingReason != "" {
			badge += " — " + truncateVisible(firstWaitingReason, 40)
		}
		return StyleWaiting.Render(badge)
	}
	if idleAskingCount > 0 {
		return StyleWarning.Render(fmt.Sprintf("%s %d idle — may need input", theme.GlyphQuestion, idleAskingCount))
	}
	// Progress bar takes priority over the idle/reviewable badge so it
	// stays visible while the agent pauses between tasks. Once all tasks
	// are complete (done == total), fall through to the review badge.
	if sess.LifecyclePhase() == agent.LifecycleInProgress {
		const barWidth = 20
		var planTotal, planDone int
		if plan, present := sess.CachedPlan(); present {
			planTotal, planDone = planTaskCounts(plan)
		}
		commitDone, commitMax := sess.CommitTaskCount()
		total := max(planTotal, commitMax)
		done := max(planDone, commitDone)
		if done > total {
			done = total
		}
		if total > 0 && done < total {
			return renderCardProgressBar(done, total, barWidth, ColorPrimary)
		}
		if total == 0 {
			if ag := sess.PrimaryAgent(); ag != nil {
				todos := ag.Todos()
				if len(todos) > 0 {
					todoTotal := len(todos)
					todoDone := 0
					for _, t := range todos {
						if t.Status == "completed" {
							todoDone++
						}
					}
					if todoDone < todoTotal {
						return renderCardProgressBar(todoDone, todoTotal, barWidth, ColorPrimary)
					}
				}
			}
		}
	}
	if sess.IsReviewable() && sess.DoneAt().IsZero() && activeCount == 0 {
		return StyleSuccess.Render("✓ idle — press m to review")
	}
	if !sess.DoneAt().IsZero() {
		return StyleSuccess.Render("✓ finished — awaiting prompt")
	}
	return StyleSubtle.Render(fmt.Sprintf("%d active, %d idle", activeCount, idleCount))
}

// sessionFocusStripeColor returns the accent color for a session's left stripe
// in the focus mode SESSIONS list. Mirrors the priority order in
// sessionFocusStatus so the stripe and the badge agree on the dominant
// condition. Selection highlight is applied by the caller.
func (items listItems) sessionFocusStripeColor(sess *agent.Session) lipgloss.Color {
	var hasError, hasWaiting, hasIdleAsking bool
	for _, item := range items {
		if item.kind != listItemAgent || item.agent == nil || item.agent.IsShell || item.session != sess {
			continue
		}
		switch item.agent.Status() {
		case agent.StatusError:
			hasError = true
		case agent.StatusWaiting:
			hasWaiting = true
		case agent.StatusIdle:
			if item.agent.AskingQuestion() {
				hasIdleAsking = true
			}
		}
	}
	switch {
	case hasError:
		return ColorError
	case hasWaiting:
		return ColorWaiting
	case hasIdleAsking:
		return ColorWarning
	case sess.IsReviewable() && sess.DoneAt().IsZero():
		return ColorSuccess
	case !sess.DoneAt().IsZero():
		return ColorSuccess
	default:
		return ColorMuted
	}
}

// sessionStatusGlyph returns a single-rune glyph and color that mirrors the
// dominant session state. Used on line 1 of the card, between the stripe and
// the session name, so the reader can identify state at a glance even when ANSI
// colors are unavailable. planningPhase callers pass true to suppress the glyph
// when the right-side badge already begins with one.
func (items listItems) sessionStatusGlyph(sess *agent.Session) (glyph string, col lipgloss.Color) {
	var hasError, hasWaiting, hasIdleAsking, hasActive bool
	for _, item := range items {
		if item.kind != listItemAgent || item.agent == nil || item.agent.IsShell || item.session != sess {
			continue
		}
		switch item.agent.Status() {
		case agent.StatusError:
			hasError = true
		case agent.StatusWaiting:
			hasWaiting = true
		case agent.StatusActive:
			hasActive = true
		case agent.StatusIdle:
			if item.agent.AskingQuestion() {
				hasIdleAsking = true
			}
		}
	}
	switch {
	case hasError:
		return theme.GlyphError, ColorError
	case hasWaiting:
		return theme.GlyphWaiting, ColorWaiting
	case hasIdleAsking:
		return theme.GlyphQuestion, ColorWarning
	case sess.IsReviewable() || !sess.DoneAt().IsZero():
		return theme.GlyphSuccess, ColorSuccess
	case hasActive:
		return theme.GlyphActive, ColorSecondary
	default:
		return theme.GlyphIdle, ColorMuted
	}
}

// renderFocusSessionCard returns exactly 4 lines for a session card in focus mode.
// Each line begins with a colored vertical stripe (▎) whose color encodes the
// dominant session state via sessionFocusStripeColor; the selected card
// brightens the stripe to ColorSecondary.
//
// Line 1: <stripe> <glyph> <name (bold, ColorText)>   ... right-aligned <status badge/progress bar>
// Line 2: <stripe>   <active task (bold)>   (building) | <description (muted/italic)> (planning/other)
// Line 3: <stripe>   next: <next task (muted-italic)>   (building) | <description line 2 or empty>
// Line 4: <stripe>   [⎇ branch] [· detail]      ... right-aligned ⏱ <elapsed>
func (d dashboardModel) renderFocusSessionCard(props dashboardProps, sess *agent.Session, repoName string, selected bool, width int) []string {
	stripeColor := props.items.sessionFocusStripeColor(sess)
	if selected {
		stripeColor = ColorSecondary
	}
	stripe := lipgloss.NewStyle().Foreground(stripeColor).Render(theme.GlyphStripe)
	const indent = "   "
	const stripeIndentWidth = 4 // stripe (1) + 3 spaces

	// --- Line 1: stripe + repo prefix + name, right-aligned status badge ---
	// A "> " prefix marks the selected card unambiguously even when stdout
	// strips ANSI (e2e screenshots, terminal recordings). The "> " stays
	// leftmost so the muted repo prefix never gets between the cursor and the
	// stripe.
	nameStyled := StyleCardTitle.Render(sess.GetDisplayName())
	if repoName != "" {
		nameStyled = StyleSubtle.Render(repoName+" › ") + nameStyled
	}
	if selected {
		nameStyled = "> " + nameStyled
	}
	// Planning + Drafting phases get a dedicated badge + description because
	// they have no agent yet — the regular sessionFocusStatus path is keyed
	// off agent.Status and would render "0 active, 0 idle" for these rows.
	phase := sess.LifecyclePhase()
	planningPhase := phase == agent.LifecyclePlanning || phase == agent.LifecycleDrafting
	var badge string
	if planningPhase {
		badge = planningStatusBadge(sess)
	} else {
		badge = props.items.sessionFocusStatus(sess)
	}
	// Status glyph: prepend between stripe and name for non-planning cards.
	// Planning cards suppress the glyph because the badge already leads with ✎/✗/○.
	if !planningPhase {
		glyph, glyphColor := props.items.sessionStatusGlyph(sess)
		glyphStyled := lipgloss.NewStyle().Foreground(glyphColor).Render(glyph)
		nameStyled = glyphStyled + " " + nameStyled
	}
	line1 := rightAlign(stripe+" "+nameStyled, badge, width)

	// --- Lines 2 and 3: description (always two lines) ---
	descBudget := width - stripeIndentWidth
	if descBudget < 1 {
		descBudget = 1
	}
	var descLine1, descLine2 string
	var descPending bool
	if planningPhase {
		var descText string
		descText, descPending = planningDescription(sess)
		descLine1, descLine2 = wrapTwoLines(descText, descBudget)
	} else {
		descLine1, descLine2, descPending = focusSessionDescription(sess, descBudget)
	}
	var line2, line3 string
	buildingPhase := phase == agent.LifecycleInProgress && !sess.IsReviewable()
	if planningPhase {
		descStyle := StyleSubtle
		if descPending {
			descStyle = StyleMutedItalic
		}
		line2 = stripe + indent + descStyle.Render(descLine1)
		line3 = stripe + indent
		if descLine2 != "" {
			line3 = stripe + indent + descStyle.Render(descLine2)
		}
	} else if buildingPhase {
		// Line 2: session description — StyleSubtle or muted-italic when pending.
		descStyle := StyleSubtle
		if descPending {
			descStyle = StyleMutedItalic
		}
		line2 = stripe + indent + descStyle.Render(descLine1)
		// Line 3: current task (label muted, name bold) or description overflow.
		task := buildingCurrentTask(sess)
		line3 = stripe + indent
		if task != "" {
			taskBudget := descBudget - lipgloss.Width("current task: ")
			if taskBudget < 0 {
				taskBudget = 0
			}
			taskLabel := StyleSubtle.Render("current task: ")
			taskName := StyleBold.Render(truncateVisible(task, taskBudget))
			line3 = stripe + indent + taskLabel + taskName
		} else if descLine2 != "" {
			line3 = stripe + indent + descStyle.Render(descLine2)
		}
	} else {
		line2 = stripe + indent + StyleSubtle.Render(descLine1)
		line3 = stripe + indent
		if descLine2 != "" {
			line3 = stripe + indent + StyleSubtle.Render(descLine2)
		}
	}

	// --- Line 4: branch [· detail], right-aligned elapsed ---
	branch := ""
	if sess.Worktree != nil {
		branch = sess.Branch()
	}
	var waitingReason string
	allIdle := true
	anyAgent := false
	for _, item := range props.items {
		if item.kind != listItemAgent || item.agent == nil || item.agent.IsShell || item.session != sess {
			continue
		}
		anyAgent = true
		st := item.agent.Status()
		if st == agent.StatusActive || st == agent.StatusWaiting {
			allIdle = false
		}
		if st == agent.StatusWaiting && waitingReason == "" {
			waitingReason = item.agent.WaitingReason()
		}
	}
	// Build the detail string (idle time or waiting reason) shown after the chip.
	var detailStr string
	switch {
	case waitingReason != "":
		detailStr = waitingReason
	case anyAgent && allIdle && !sess.LastOutputTime().IsZero():
		detailStr = fmt.Sprintf("idle %dm", int(d.now.Sub(sess.LastOutputTime()).Minutes()))
	}

	totalMins := int(d.now.Sub(sess.CreatedAt).Minutes())
	var elapsedStr string
	if totalMins >= 60 {
		elapsedStr = fmt.Sprintf("%dh %dm", totalMins/60, totalMins%60)
	} else {
		elapsedStr = fmt.Sprintf("%dm", totalMins)
	}

	// Build the left part of line 4: ⎇ branch [ · detail], right side ⏱ elapsed.
	elapsedBudget := lipgloss.Width("⏱ "+elapsedStr) + 1
	leftBudget := width - stripeIndentWidth - elapsedBudget
	if leftBudget < 0 {
		leftBudget = 0
	}
	var bottomLeft string
	branchLabel := renderBranchLabel(branch)
	if branchLabel != "" {
		branchWidth := lipgloss.Width(branchLabel)
		var detailRendered string
		if detailStr != "" {
			remaining := leftBudget - branchWidth - 3 // 3 = " · "
			if remaining > 0 {
				detailRendered = StyleSubtle.Render(" · " + truncateVisible(detailStr, remaining))
			}
		}
		bottomLeft = stripe + indent + branchLabel + detailRendered
	} else {
		var detailRendered string
		if detailStr != "" {
			detailRendered = StyleSubtle.Render(truncateVisible(detailStr, leftBudget))
		}
		bottomLeft = stripe + indent + detailRendered
	}

	line4 := rightAlign(bottomLeft, StyleSubtle.Render("⏱ "+elapsedStr), width)

	return []string{line1, line2, line3, line4}
}

// renderBranchLabel returns a muted "⎇ <branch>" label with no background fill.
// Returns "" for an empty branch so callers can omit it cleanly.
func renderBranchLabel(branch string) string {
	if branch == "" {
		return ""
	}
	return StyleSubtle.Render(theme.GlyphBranch + " " + branch)
}

// planningStatusBadge renders the right-aligned status badge for a Planning
// or Drafting card. Priority: Drafting > Revising > DraftError > plan task
// summary > "no plan yet". Drafting and revising are mutually exclusive at
// the session level, so the fall-through ordering is unambiguous.
//
// Reads plan content via Session.CachedPlan to keep the per-render hot path
// off os.Stat + os.ReadFile. Cache is invalidated by WritePlan, which the
// drafter and revise paths both go through.
func planningStatusBadge(sess *agent.Session) string {
	if sess.IsDrafting() {
		if cur, max := sess.DraftAttempt(); cur > 1 && max > 0 {
			return StyleWarning.Render(
				fmt.Sprintf("✎ retrying… (%d/%d)", cur, max),
			)
		}
		return StyleWarning.Render("✎ drafting…")
	}
	if sess.IsRevising() {
		return StyleWarning.Render("✎ revising…")
	}
	if err := sess.DraftError(); err != nil {
		return StyleError.Render("✗ draft failed")
	}
	plan, present := sess.CachedPlan()
	if !present {
		return StyleSubtle.Render("○ no plan yet")
	}
	total, done := planTaskCounts(plan)
	if total == 0 {
		return StyleAccent.Render("✎ plan ready")
	}
	return StyleAccent.Render(
		fmt.Sprintf("✎ %d/%d tasks", done, total),
	)
}

// planningDescription chooses the description text for a Planning or Drafting
// card. Drafting shows the prompt italicized; a successful draft surfaces the
// first uncompleted task; a failed draft surfaces the error excerpt; an
// orphan Planning row with no plan falls back to the original prompt.
func planningDescription(sess *agent.Session) (string, bool) {
	if sess.IsDrafting() {
		if p := sess.OriginalPrompt(); p != "" {
			return p, true
		}
		return "drafting plan…", true
	}
	if sess.IsRevising() {
		// Match the badge ("✎ revising…") so the card reads as a unit.
		// Without this branch the description would fall through to the
		// pre-revise plan's first uncompleted task, which is technically
		// correct but visually inconsistent with the badge.
		return "revising plan…", true
	}
	if err := sess.DraftError(); err != nil {
		return "draft failed: " + err.Error(), false
	}
	plan, present := sess.CachedPlan()
	if !present {
		if p := sess.OriginalPrompt(); p != "" {
			return p, true
		}
		return "no plan yet — press space to write one", true
	}
	if next := firstUncompletedTask(plan); next != "" {
		return "next: " + next, false
	}
	if p := sess.OriginalPrompt(); p != "" {
		return p, false
	}
	return "plan ready — press a to approve", false
}

// planTaskCounts returns (total, done) for "- [ ]" / "- [x]" task list items
// inside the plan's "## Tasks" section. The review panel maps commits to tasks
// via Plan-Task: N body trailers, where N is the 1-based checkbox position
// inside this section; stray "- [ ]" lines elsewhere are cosmetic but best
// avoided. Plans without a "## Tasks" heading fall back to whole-document
// scope so freeform plans still get a count.
// Tolerant of leading whitespace and either case for the completion marker.
func planTaskCounts(plan string) (total, done int) {
	for _, raw := range agent.ScanTaskLines(plan) {
		line := strings.TrimLeft(raw, " \t")
		if !strings.HasPrefix(line, "- [") {
			continue
		}
		// Need at least "- [x] " to be a task line.
		if len(line) < 6 || line[4] != ']' {
			continue
		}
		total++
		marker := line[3]
		if marker == 'x' || marker == 'X' {
			done++
		}
	}
	return total, done
}

// firstUncompletedTask returns the text of the first "- [ ]" line in plan, or
// "" if every task is done (or the plan has no task lines). Used by the
// Planning card description so the user sees what's outstanding without
// opening the editor.
//
// Scopes to the "## Tasks" section via agent.ScanTaskLines so a stray "- [ ]"
// in Spec/Goal/Verification cannot surface as the "current task" while
// planTaskCounts (which uses the same scope) ignores it — the two would
// otherwise disagree on which item is outstanding.
func firstUncompletedTask(plan string) string {
	for _, raw := range agent.ScanTaskLines(plan) {
		line := strings.TrimLeft(raw, " \t")
		if !strings.HasPrefix(line, "- [ ]") {
			continue
		}
		text := strings.TrimSpace(strings.TrimPrefix(line, "- [ ]"))
		if text == "" {
			continue
		}
		return text
	}
	return ""
}

// buildingCurrentTask returns the name of the task currently in progress for
// the session. Priority: in_progress TodoItem ActiveForm → Content; plan
// firstUncompletedTask (when plan has open checkboxes); first pending TodoItem
// Content (only when no plan with checkboxes exists); "". Mirrors the
// plan-first ordering in sessionFocusStatus and focusTaskDescription.
func buildingCurrentTask(sess *agent.Session) string {
	if ag := sess.PrimaryAgent(); ag != nil {
		todos := ag.Todos()
		for _, t := range todos {
			if t.Status == "in_progress" {
				if t.ActiveForm != "" {
					return t.ActiveForm
				}
				return t.Content
			}
		}
	}
	// Plan checkboxes win over stale pending todos when a plan exists.
	if plan, present := sess.CachedPlan(); present {
		if first := firstUncompletedTask(plan); first != "" {
			return first
		}
		// Plan exists but all checkboxes done: don't surface stale todos.
		if total, _ := planTaskCounts(plan); total > 0 {
			return ""
		}
	}
	// No plan (or plan with no checkboxes): fall back to first pending todo.
	if ag := sess.PrimaryAgent(); ag != nil {
		for _, t := range ag.Todos() {
			if t.Status == "pending" {
				return t.Content
			}
		}
	}
	return ""
}

// focusSessionDescription chooses the description lines for a session card in
// focus mode and reports whether they should render in pending (italic) style.
// Priority: TaskSummary → OriginalPrompt → "…". Building-phase todo/plan task
// text is intentionally excluded — current-task signal belongs on line 3 via
// buildingCurrentTask, not here.
func focusSessionDescription(sess *agent.Session, budget int) (line1, line2 string, pending bool) {
	origPrompt := sess.OriginalPrompt()
	var text string
	var pend bool
	switch {
	case sess.HasTaskSummary() && sess.TaskSummary() != "":
		text, pend = sess.TaskSummary(), false
	case sess.HasTaskSummary() && sess.TaskSummary() == "":
		text, pend = origPrompt, false
	case !sess.HasTaskSummary() && origPrompt != "":
		text, pend = origPrompt, true
	default:
		text, pend = "…", false
	}
	l1, l2 := wrapTwoLines(text, budget)
	return l1, l2, pend
}

// wrapTwoLines greedily word-wraps s into at most two lines of `budget`
// display cells. If only one line is needed, line2 is empty. If a third line
// would have been required, the second line is truncated with an ellipsis.
func wrapTwoLines(s string, budget int) (string, string) {
	if budget <= 0 {
		return "", ""
	}
	if s == "" {
		return "", ""
	}
	if ansi.StringWidth(s) <= budget {
		return s, ""
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return truncateVisible(s, budget), ""
	}
	var line1Words []string
	used := 0
	i := 0
	for i < len(words) {
		w := words[i]
		ww := ansi.StringWidth(w)
		extra := ww
		if used > 0 {
			extra++ // separator space
		}
		if used > 0 && used+extra > budget {
			break
		}
		line1Words = append(line1Words, w)
		used += extra
		i++
		if used >= budget {
			// First word filled or overflowed the line; stop here.
			break
		}
	}
	line1 := strings.Join(line1Words, " ")
	if ansi.StringWidth(line1) > budget {
		line1 = truncateVisible(line1, budget)
	}
	if i >= len(words) {
		return line1, ""
	}
	rest := strings.Join(words[i:], " ")
	if ansi.StringWidth(rest) <= budget {
		return line1, rest
	}
	return line1, truncateVisible(rest, budget)
}

// rightAlign places left and right on the same line with padding so the total
// visible width equals width. ANSI-escape-aware via lipgloss.Width.
func rightAlign(left, right string, width int) string {
	total := lipgloss.Width(left) + lipgloss.Width(right)
	pad := width - total
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + right
}

// renderPipelineWidget renders the 4-cell pipeline row, one cell per phase:
// PLANNING → BUILDING → REVIEWING → SHIPPING. Counts mirror the section lists.
func (d dashboardModel) renderPipelineWidget(props dashboardProps, width int) string {
	var planning, building, reviewing, shipping int
	for _, item := range props.items {
		if item.kind != listItemSession || item.session == nil {
			continue
		}
		if props.activeRepoPath != "" && item.repoPath != props.activeRepoPath {
			continue
		}
		switch item.session.LifecyclePhase() {
		case agent.LifecyclePlanning, agent.LifecycleDrafting:
			planning++
		case agent.LifecycleInProgress:
			building++
		case agent.LifecycleReadyForReview, agent.LifecycleInReview:
			reviewing++
		case agent.LifecycleShipping:
			shipping++
		}
	}

	cellWidth := (width - 8) / 4
	if cellWidth < 18 {
		cellWidth = 18
	}

	cell := func(label string, count int, color lipgloss.Color, highlight bool) string {
		cnt := lipgloss.NewStyle().Foreground(color).Bold(true).Render(fmt.Sprintf("%d", count))
		lbl := StyleSubtle.Render(truncateVisible(label, cellWidth-2))
		inner := fmt.Sprintf("%s\n%s", lbl, cnt)
		style := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(theme.PadCell[0], theme.PadCell[1]).Width(cellWidth)
		if highlight {
			style = style.BorderForeground(color)
		}
		return style.Render(inner)
	}

	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		cell("PLANNING", planning, ColorMuted, false),
		cell("BUILDING", building, ColorBuilding, false),
		cell("REVIEWING", reviewing, ColorWarning, reviewing > 0),
		cell("SHIPPING", shipping, ColorShipping, false),
	)
}

// focusLaunchTabDot returns the status indicator character for a tab.
func focusLaunchTabDot(ag *agent.Agent) string {
	switch ag.Status() {
	case agent.StatusActive, agent.StatusWaiting:
		return theme.GlyphActive
	case agent.StatusError, agent.StatusDone:
		return theme.GlyphCross
	default:
		return theme.GlyphIdle
	}
}

// focusLaunchTabText returns the plain (unstyled) text for a tab label,
// used by both rendering and click-hit detection so they stay in sync.
func focusLaunchTabText(ag *agent.Agent) string {
	name := truncateVisible(ag.GetDisplayName(), 18)
	return "[" + focusLaunchTabDot(ag) + " " + name + "]"
}

// renderFocusLaunchTabBar renders the tab strip row for focusLaunch. Returns
// the styled tab bar string and, as side-data for click handling, the starting
// column of each tab (returned to avoid duplicating layout math in App).
func (d dashboardModel) renderFocusLaunchTabBar(props dashboardProps, width int) string {
	if props.focusLaunchSession == nil || props.focusLaunchAgent == nil {
		return ""
	}
	agents := props.focusLaunchSession.Agents()
	if len(agents) == 0 {
		return ""
	}

	var parts []string
	for _, ag := range agents {
		text := focusLaunchTabText(ag)
		if ag.ID == props.focusLaunchAgent.ID {
			parts = append(parts, StyleTitle.Render(text))
		} else {
			parts = append(parts, StyleSubtle.Render(text))
		}
	}
	bar := strings.Join(parts, "  ")
	// Truncate if it exceeds width (no wrapping).
	if ansi.StringWidth(bar) > width {
		bar = ansi.Truncate(bar, width, "")
	}
	return bar
}

// renderFocusLaunchView renders the "focus mode paused" view with a single agent terminal.
func (d dashboardModel) renderFocusLaunchView(props dashboardProps, width, height int) string {
	ag := props.focusLaunchAgent
	if ag == nil {
		return d.renderFullscreenFocus(props, width, height)
	}

	agentName := ag.GetDisplayName()
	headerParts := []string{fmt.Sprintf("agent: %s", agentName)}
	if props.focusLaunchSession != nil {
		if branch := props.focusLaunchSession.Branch(); branch != "" {
			headerParts = append(headerParts, fmt.Sprintf("branch: %s", branch))
		}
	}
	header := StyleSubtle.Render(strings.Join(headerParts, "  "))

	tabBar := d.renderFocusLaunchTabBar(props, width)

	vpWidth := width
	vpHeight := height - 2
	var render string
	if d.scrollOffset > 0 {
		sbLines, viewport := ag.Snapshot(vpWidth, vpHeight)
		vpLines := strings.Split(viewport, "\n")
		allLines := append(sbLines, vpLines...)

		end := len(allLines) - d.scrollOffset
		if end < 0 {
			end = 0
		}
		start := end - vpHeight
		if start < 0 {
			start = 0
		}
		visibleLines := vt.PadLines(allLines[start:end], vpWidth)
		if d.selection.active && d.selection.dragSeen && d.selection.agentID == ag.ID {
			sx, sy, ex, ey, _ := d.selectionRect()
			render = applySelectionHighlight(visibleLines, vt.SelectionRect{
				StartX: sx, StartY: sy, EndX: ex, EndY: ey, Active: true,
			})
		} else {
			render = strings.Join(visibleLines, "\n")
		}
	} else if d.selection.active && d.selection.dragSeen && d.selection.agentID == ag.ID {
		sx, sy, ex, ey, _ := d.selectionRect()
		render = ag.RenderPaddedWithSelection(vpWidth, vpHeight, vt.SelectionRect{
			StartX: sx, StartY: sy, EndX: ex, EndY: ey, Active: true,
		})
	} else {
		render = ag.RenderPadded(vpWidth, vpHeight)
	}

	if tabBar == "" {
		return header + "\n" + render
	}
	return header + "\n" + tabBar + "\n" + render
}

// breatheFrames is built at package init via generateBreatheFrames so the
// animation stays smooth across many frames without hand-tuning each one.
var breatheFrames = generateBreatheFrames()

// breatheFramesCompact is a smaller fallback for terminals that can't fit
// the full-size breath. Mirrors the original three-row design.
var breatheFramesCompact = [12][3]string{
	{"         ", "    ·    ", "         "},
	{"   · · · ", "  · · ·  ", " · · ·   "},
	{"  ○○○○○  ", "  ○   ○  ", "  ○○○○○  "},
	{" ○○○○○○○ ", " ○     ○ ", " ○○○○○○○ "},
	{"○○○○○○○○○", "○○  ◎  ○○", "○○○○○○○○○"},
	{"○○○○○○○○○", "○○  ◉  ○○", "○○○○○○○○○"},
	{"○○○○○○○○○", "○○  ●  ○○", "○○○○○○○○○"},
	{"○○○○○○○○○", "○○  ◉  ○○", "○○○○○○○○○"},
	{"○○○○○○○○○", "○○  ◎  ○○", "○○○○○○○○○"},
	{" ○○○○○○○ ", " ○     ○ ", " ○○○○○○○ "},
	{"  ○○○○○  ", "  ○   ○  ", "  ○○○○○  "},
	{"   · · · ", "  · · ·  ", " · · ·   "},
}

// breatheColors and completeColors are the wellness-overlay animation palettes,
// sourced from the design-system registry (internal/tui/theme).
var (
	breatheColors  = theme.BreatheColors
	completeColors = theme.CompleteColors
)

// generateBreatheFrames builds a smooth concentric breath cycle. The first
// half of the cycle inhales (radius grows), the second half exhales. A
// cosine easing keeps motion organic; an aspect-ratio correction keeps the
// shape feeling round despite character cells being roughly twice as tall
// as they are wide.
func generateBreatheFrames() [breathFrameCount][breathHeight]string {
	var out [breathFrameCount][breathHeight]string
	cx := float64(breathWidth-1) / 2.0
	cy := float64(breathHeight-1) / 2.0
	const aspect = 2.3
	maxR := cy + 0.6

	for f := 0; f < breathFrameCount; f++ {
		half := breathFrameCount / 2
		var phase float64
		if f < half {
			phase = float64(f) / float64(half-1)
		} else {
			phase = float64(breathFrameCount-1-f) / float64(half-1)
		}
		eased := 0.5 - 0.5*math.Cos(phase*math.Pi)
		radius := eased * maxR

		for r := 0; r < breathHeight; r++ {
			row := make([]rune, breathWidth)
			for c := 0; c < breathWidth; c++ {
				dx := (float64(c) - cx) / aspect
				dy := float64(r) - cy
				dist := math.Sqrt(dx*dx + dy*dy)
				ch := ' '
				switch {
				case radius < 0.35:
					if dist < 0.6 {
						ch = '·'
					}
				case dist < radius-1.0:
					ch = '●'
				case dist < radius-0.45:
					ch = '◉'
				case dist < radius+0.15:
					ch = '○'
				case dist < radius+0.55 && phase > 0.85:
					// Sparkle ring at the top of the inhale gives the
					// peak a held, alive feeling.
					ch = '·'
				}
				row[c] = ch
			}
			out[f][r] = string(row)
		}
	}
	return out
}

// renderBreatheBlock returns the current breath frame as a colored block.
// Falls back to the compact frames when the terminal can't fit the bigger
// canvas.
func (d dashboardModel) renderBreatheBlock(props dashboardProps, width, height int) string {
	if width < breathWidth+4 || height < breathHeight+8 {
		cycle := props.focusBreakAnimFrame % breathFrameCount
		frame := cycle * len(breatheFramesCompact) / breathFrameCount
		animColor := breatheColors[(props.focusBreakAnimFrame/breathFrameCount)%len(breatheColors)]
		animStyle := lipgloss.NewStyle().Foreground(animColor)
		rows := breatheFramesCompact[frame]
		return animStyle.Render(rows[0]) + "\n" +
			animStyle.Render(rows[1]) + "\n" +
			animStyle.Render(rows[2])
	}
	frame := props.focusBreakAnimFrame % breathFrameCount
	// Color rotates per breath cycle, not per frame, so the eye gets a
	// stable hue to settle on for the duration of one breath.
	cycle := props.focusBreakAnimFrame / breathFrameCount
	animColor := breatheColors[cycle%len(breatheColors)]
	animStyle := lipgloss.NewStyle().Foreground(animColor)

	rows := breatheFrames[frame]
	out := make([]string, breathHeight)
	for i, row := range rows {
		out[i] = animStyle.Render(row)
	}
	return strings.Join(out, "\n")
}

// breathPhaseLabel returns a one-word cue ("inhale" / "exhale") matching
// the current breath phase. No hold phase — the sparkle ring in
// generateBreatheFrames provides the held feeling visually at the peak.
func (d dashboardModel) breathPhaseLabel(props dashboardProps) string {
	frame := props.focusBreakAnimFrame % breathFrameCount
	half := breathFrameCount / 2
	if frame < half {
		return "inhale"
	}
	return "exhale"
}

// renderBreakOverlay returns a fullscreen centered break screen. Behaviour
// depends on whether the configured break duration has elapsed:
//   - Active break: large breath animation, countdown, exit-early hint.
//   - Timer up: warm "BREAK COMPLETE" panel that waits for the user to
//     explicitly opt back in (no auto-resume).
func (d dashboardModel) renderBreakOverlay(props dashboardProps, width, height int) string {
	if props.focusBreakTimerUp {
		return d.renderBreakCompleteOverlay(props, width, height)
	}

	animBlock := d.renderBreatheBlock(props, width, height)

	titleStyle := StyleHeading.Foreground(ColorBreakTitle)
	title := titleStyle.Render("BREAK")

	var blockLine string
	if props.focusBlockCount > 0 {
		blockLine = StyleSubtle.Render(fmt.Sprintf("Block %d", props.focusBlockCount))
	}

	phaseLine := StyleSubtle.Render(d.breathPhaseLabel(props))

	var countdownLine string
	if props.focusBreakMinutes > 0 {
		totalSecs := props.focusBreakMinutes * 60
		remainSecs := totalSecs - int(props.focusBreakElapsed.Seconds())
		if remainSecs < 0 {
			remainSecs = 0
		}
		mins := remainSecs / 60
		secs := remainSecs % 60
		countdownLine = StyleSubtle.Render(fmt.Sprintf("%dm %02ds remaining", mins, secs))
	}

	var actionLine string
	if props.focusBreakShortWarning {
		actionLine = StyleWarning.Render("break too short — press b again to override")
	} else {
		actionLine = StyleSubtle.Render("[b] return early")
	}

	var parts []string
	parts = append(parts, title)
	if blockLine != "" {
		parts = append(parts, blockLine)
	}
	parts = append(parts, "")
	parts = append(parts, animBlock)
	parts = append(parts, "")
	parts = append(parts, phaseLine)
	if countdownLine != "" {
		parts = append(parts, countdownLine)
	}
	parts = append(parts, "")
	parts = append(parts, actionLine)

	inner := strings.Join(parts, "\n")
	return placeCentered(width, height, inner)
}

// renderBreakCompleteOverlay is shown once the break timer has elapsed.
// The visual is intentionally loud: warm bordered banner, pulsing colour,
// over-time counter. The user must press b to leave — we never advance on
// their behalf.
func (d dashboardModel) renderBreakCompleteOverlay(props dashboardProps, width, height int) string {
	pulse := completeColors[(props.focusBreakAnimFrame/3)%len(completeColors)]
	bannerStyle := lipgloss.NewStyle().
		Foreground(pulse).
		Bold(true).
		Border(lipgloss.DoubleBorder()).
		BorderForeground(pulse).
		Padding(1, 4)
	banner := bannerStyle.Render("⏰  B R E A K   C O M P L E T E  ⏰")

	subStyle := lipgloss.NewStyle().Foreground(pulse).Bold(true)
	subhead := subStyle.Render("ready when you are")

	var stats string
	if props.focusBreakMinutes > 0 {
		breakSecs := int(props.focusBreakElapsed.Seconds())
		bm, bs := breakSecs/60, breakSecs%60
		over := breakSecs - props.focusBreakMinutes*60
		if over < 0 {
			over = 0
		}
		om, os := over/60, over%60
		if over > 0 {
			stats = StyleSubtle.Render(fmt.Sprintf("on break %dm %02ds · %dm %02ds past timer", bm, bs, om, os))
		} else {
			stats = StyleSubtle.Render(fmt.Sprintf("on break %dm %02ds", bm, bs))
		}
	}

	var blockLine string
	if props.focusBlockCount > 0 {
		blockLine = StyleSubtle.Render(fmt.Sprintf("Block %d so far", props.focusBlockCount))
	}

	prompt := StyleBold.
		Foreground(ColorBreakAccent).
		Render("[b] resume focus session")
	hint := StyleSubtle.Render("(no rush — the timer won't drag you back in)")

	parts := []string{banner, "", subhead}
	if stats != "" {
		parts = append(parts, "", stats)
	}
	if blockLine != "" {
		parts = append(parts, blockLine)
	}
	parts = append(parts, "", prompt, hint)

	inner := lipgloss.JoinVertical(lipgloss.Center, parts...)
	return placeCentered(width, height, inner)
}

// renderFullscreenFocus renders the pipeline dashboard: header, pipeline
// widget, SESSIONS section, and REVIEW QUEUE section.
func (d dashboardModel) renderFullscreenFocus(props dashboardProps, width, height int) string {
	if props.focusBreakMode {
		return d.renderBreakOverlay(props, width, height)
	}

	var lines []string

	// Header: title + timer
	title := StyleTitle.Render("FOCUS")
	if props.focusBlockCount > 0 {
		title += "  " + StyleSubtle.Render(fmt.Sprintf("Block %d", props.focusBlockCount))
	}
	timerStr := ""
	if props.focusSessionMinutes > 0 {
		threshold := time.Duration(props.focusSessionMinutes) * time.Minute
		elapsed := props.sessionElapsed
		if elapsed > threshold {
			elapsed = threshold
		}
		pct := float64(elapsed) / float64(threshold)
		barWidth := width - 30
		if barWidth < 5 {
			barWidth = 5
		} else if barWidth > 20 {
			barWidth = 20
		}

		var barColor lipgloss.Color
		switch {
		case pct >= 1.0:
			barColor = ColorError
		case pct >= 0.75:
			barColor = ColorWarning
		default:
			barColor = ColorMuted
		}
		barModel := progress.New(
			progress.WithoutPercentage(),
			progress.WithColorFunc(func(_, _ float64) color.Color { return barColor }),
		)
		barModel.SetWidth(barWidth)

		elapsedMin := int(props.sessionElapsed.Minutes())
		timerStr = barModel.ViewAs(pct) + " " + fmt.Sprintf("%dm/%dm", elapsedMin, props.focusSessionMinutes)
	}
	headerLine := title
	if props.activeRepoName != "" {
		headerLine += "  " + StyleSubtle.Render(props.activeRepoName)
	}
	headerLine += "  " + timerStr
	lines = append(lines, headerLine)
	// width can be 0 on the very first frame, before the initial
	// WindowSizeMsg arrives: props.items is non-empty immediately (cfg repo
	// headers), so View() reaches this path before a size is known. Clamp so
	// strings.Repeat never gets a negative count.
	lines = append(lines, StyleSubtle.Render(strings.Repeat("─", max(0, width-2))))

	// Pipeline widget
	lines = append(lines, d.renderPipelineWidget(props, width))
	lines = append(lines, "")

	// Section render order matches focusSectionsInOrder() so navigation walks
	// the same sequence the user reads top-to-bottom.
	planningItems := props.items.planningSessions()
	if len(planningItems) > 0 {
		lines = append(lines, StyleSubtle.Render("PLANNING"))
		for i, item := range planningItems {
			selected := props.cursor.Section() == focusSectionPlanning && i == props.cursor.Index(focusSectionPlanning)
			card := d.renderFocusSessionCard(props, item.session, item.repoName, selected, width)
			lines = append(lines, card...)
			if i < len(planningItems)-1 {
				lines = append(lines, "")
			}
		}
		lines = append(lines, "")
	}

	buildingItems := props.items.buildingSessions()
	if len(buildingItems) > 0 {
		lines = append(lines, StyleSubtle.Render("BUILDING"))
		for i, item := range buildingItems {
			selected := props.cursor.Section() == focusSectionBuilding && i == props.cursor.Index(focusSectionBuilding)
			card := d.renderFocusSessionCard(props, item.session, item.repoName, selected, width)
			lines = append(lines, card...)
			if i < len(buildingItems)-1 {
				lines = append(lines, "")
			}
		}
		lines = append(lines, "")
	}

	reviewSessions := props.items.reviewQueueSessions()
	if len(reviewSessions) > 0 {
		lines = append(lines, StyleSubtle.Render("REVIEWING"))
		for i, item := range reviewSessions {
			selected := props.cursor.Section() == focusSectionReview && i == props.cursor.Index(focusSectionReview)
			row := d.renderQueueRow(props, item.session, item.repoName, item.repoPath, selected, ColorWarning, width)
			lines = append(lines, row...)
			if i < len(reviewSessions)-1 {
				lines = append(lines, "")
			}
		}
		lines = append(lines, "")
	}

	shippingItems := props.items.shippingSessions()
	if len(shippingItems) > 0 {
		lines = append(lines, StyleSubtle.Render("SHIPPING"))
		for i, item := range shippingItems {
			selected := props.cursor.Section() == focusSectionShipping && i == props.cursor.Index(focusSectionShipping)
			row := d.renderQueueRow(props, item.session, item.repoName, item.repoPath, selected, ColorShipping, width)
			lines = append(lines, row...)
			if i < len(shippingItems)-1 {
				lines = append(lines, "")
			}
		}
		lines = append(lines, "")
	}

	return lipgloss.Place(width, height, lipgloss.Left, lipgloss.Top, strings.Join(lines, "\n"))
}

// renderQueueRow renders a 2-line review/shipping row. selectedColor is the
// accent for the cursor stripe and prefix when selected (warning for review,
// success for shipping). Used by both the REVIEWING and SHIPPING sections so
// they share layout/age/prIndicator handling.
func (d dashboardModel) renderQueueRow(props dashboardProps, sess *agent.Session, repoName, repoPath string, selected bool, selectedColor lipgloss.Color, width int) []string {
	name := sess.GetDisplayName()
	age := ""
	if !sess.DoneAt().IsZero() {
		mins := int(d.now.Sub(sess.DoneAt()).Minutes())
		age = fmt.Sprintf("done %dm ago", mins)
	}
	var cardStyle lipgloss.Style
	if selected {
		cardStyle = lipgloss.NewStyle().Foreground(selectedColor)
	} else {
		cardStyle = StyleSubtle
	}
	prefix := "  "
	if selected {
		prefix = cardStyle.Render("> ")
	}

	nameRendered := cardStyle.Render(name)
	if repoName != "" {
		nameRendered = StyleSubtle.Render(repoName+" › ") + nameRendered
	}
	// The (reviewing) tag distinguishes InReview rows in the merged Reviewing
	// section so the user can tell which session is open in the panel.
	if sess.LifecyclePhase() == agent.LifecycleInReview {
		tag := StyleAccent.Foreground(ColorReviewing).Render(" (reviewing)")
		nameRendered += tag
	}
	line1 := prefix + nameRendered
	prIndSet := false
	if prEntry := props.prCache[cacheKey(repoPath, sess.ID)]; prEntry != nil {
		if prInd := prIndicator(prEntry); prInd != "" {
			line1 = rightAlign(prefix+nameRendered, prInd, width)
			prIndSet = true
		}
	}
	if !prIndSet && !sess.IsReviewable() {
		badge := props.items.sessionFocusStatus(sess)
		line1 = rightAlign(prefix+nameRendered, badge, width)
	}

	var taskDisplay string
	if props.prDraftSessionID != "" && sess.ID == props.prDraftSessionID && repoPath == props.prDraftRepoPath {
		taskDisplay = StyleWarning.Render(reviewSpinnerFrame(d.now) + " drafting PR…")
	} else {
		origPrompt := sess.OriginalPrompt()
		switch {
		case sess.HasTaskSummary() && sess.TaskSummary() != "":
			taskDisplay = cardStyle.Render(truncateVisible(sess.TaskSummary(), width-30))
		case origPrompt != "":
			taskDisplay = cardStyle.Render(truncateVisible(origPrompt, width-30))
		default:
			taskDisplay = cardStyle.Render("…")
		}
	}
	left2 := "  " + taskDisplay
	line2 := left2
	if age != "" {
		line2 = rightAlign(left2, StyleSubtle.Render(age), width)
	}

	return []string{line1, line2}
}

// applySelectionHighlight inserts reverse-video SGR codes around selected column
// ranges in each line of lines. ANSI is stripped first so column positions are
// reliable; the non-selected text is emitted as plain. Active must be true on
// rect for any highlight to be applied.
func applySelectionHighlight(lines []string, rect vt.SelectionRect) string {
	inSel := func(x, y int) bool {
		if !rect.Active || y < rect.StartY || y > rect.EndY {
			return false
		}
		if y > rect.StartY && y < rect.EndY {
			return true
		}
		if rect.StartY == rect.EndY {
			return x >= rect.StartX && x <= rect.EndX
		}
		if y == rect.StartY {
			return x >= rect.StartX
		}
		return x <= rect.EndX
	}
	result := make([]string, len(lines))
	for y, line := range lines {
		if !rect.Active || y < rect.StartY || y > rect.EndY {
			result[y] = line
			continue
		}
		stripped := ansi.Strip(line)
		var b strings.Builder
		col := 0
		inRev := false
		for _, r := range stripped {
			rw := ansi.StringWidth(string(r))
			if rw == 0 {
				continue // combining marks / zero-width chars have no column position
			}
			sel := false
			for c := col; c < col+rw; c++ {
				if inSel(c, y) {
					sel = true
					break
				}
			}
			if sel && !inRev {
				b.WriteString("\x1b[7m")
				inRev = true
			} else if !sel && inRev {
				b.WriteString("\x1b[27m")
				inRev = false
			}
			b.WriteRune(r)
			col += rw
		}
		if inRev {
			b.WriteString("\x1b[27m")
		}
		result[y] = b.String()
	}
	return strings.Join(result, "\n")
}
