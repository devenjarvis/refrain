package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/tui/theme"
)

// View renders the root session list: repo-grouped 2-line session cards under
// muted repo headers, with an empty-state hint block when nothing is running.
// Pure: deterministic from (m, props), no mutation, no I/O (§5).
func (m sessionListModel) View(props sessionListProps) string {
	layout := buildSessionListLayout(props.items)
	if len(layout.groups) == 0 {
		return m.emptyView()
	}

	lines := make([]string, 0, layout.total)
	for _, g := range layout.groups {
		if g.header {
			lines = append(lines, m.renderRepoHeader(g, props))
		}
		if g.header && g.count == 0 {
			lines = append(lines, StyleSubtle.Render("   no sessions"))
		}
		for i := g.start; i < g.start+g.count; i++ {
			lines = append(lines, m.renderSessionCard(layout.rows[i], i == m.cursor, props)...)
		}
	}
	if len(layout.rows) == 0 {
		lines = append(lines, "", m.hintBlock())
	}

	// Scroll window.
	start := m.scroll
	if start > len(lines) {
		start = len(lines)
	}
	end := start + m.height
	if m.height <= 0 || end > len(lines) {
		end = len(lines)
	}
	body := strings.Join(lines[start:end], "\n")
	return lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Top, body)
}

// emptyView is the zero-repo state (cfg has no repos and no manager was wired).
func (m sessionListModel) emptyView() string {
	title := StyleTitle.Render("Refrain")
	subtitle := StyleSubtle.Render("No sessions")
	content := lipgloss.JoinVertical(lipgloss.Center, title, "", subtitle, "", m.hintBlock())
	return placeCentered(m.width, m.height, content)
}

// hintBlock lists the three ways to get work onto the screen.
func (m sessionListModel) hintBlock() string {
	key := StyleBold.Foreground(ColorText)
	return lipgloss.JoinVertical(
		lipgloss.Left,
		key.Render("n")+StyleSubtle.Render("  new session"),
		key.Render("o")+StyleSubtle.Render("  open a branch or PR"),
		key.Render("a")+StyleSubtle.Render("  add repo"),
	)
}

// renderRepoHeader renders " repoName ────" with the active repo's name in
// body-text color so the user can see which repo `n` targets by default.
func (m sessionListModel) renderRepoHeader(g sessionGroup, props sessionListProps) string {
	nameStyle := StyleSubtle
	if g.repoPath == props.activeRepoPath {
		nameStyle = lipgloss.NewStyle().Bold(true).Foreground(ColorText)
	}
	name := nameStyle.Render(truncateVisible(g.repoName, m.width-4))
	ruleW := m.width - lipgloss.Width(name) - 3 // leading space + space each side of rule
	if ruleW < 0 {
		ruleW = 0
	}
	return " " + name + " " + StyleSubtle.Render(strings.Repeat(theme.GlyphRuleThin, ruleW))
}

// sessionListStatus returns the glyph, word, and color for a session's
// aggregate status: the max-severity across its non-shell agents, per the
// rollback design §4.1 — Error > Waiting > Active > Starting > Idle > Done.
// ok is false when the session has no non-shell agents (plan-only sessions);
// those rows let the badges carry the state instead.
func sessionListStatus(sess *agent.Session) (glyph, word string, col lipgloss.Color, ok bool) {
	var hasError, hasWaiting, hasActive, hasStarting, hasIdle, any bool
	for _, ag := range sess.Agents() {
		if ag.IsShell {
			continue
		}
		any = true
		switch ag.Status() {
		case agent.StatusError:
			hasError = true
		case agent.StatusWaiting:
			hasWaiting = true
		case agent.StatusActive:
			hasActive = true
		case agent.StatusStarting:
			hasStarting = true
		case agent.StatusIdle:
			hasIdle = true
		}
	}
	if !any {
		return "", "", "", false
	}
	switch {
	case hasError:
		return theme.GlyphError, "error", ColorError, true
	case hasWaiting:
		return theme.GlyphWaiting, "waiting", ColorWaiting, true
	case hasActive:
		return theme.GlyphActive, "active", ColorSecondary, true
	case hasStarting:
		return theme.GlyphActive, "starting", ColorMuted, true
	case hasIdle:
		return theme.GlyphIdle, "idle", ColorMuted, true
	default:
		return theme.GlyphSuccess, "done", ColorSuccess, true
	}
}

// sessionAttention reports whether a session row warrants the accent stripe:
// an agent is waiting for input or errored. Attention colors the stripe but
// never reorders the list (§4.1 — the signal budget survives the pipeline).
func sessionAttention(sess *agent.Session) (lipgloss.Color, bool) {
	var hasError, hasWaiting bool
	for _, ag := range sess.Agents() {
		if ag.IsShell {
			continue
		}
		switch ag.Status() {
		case agent.StatusError:
			hasError = true
		case agent.StatusWaiting:
			hasWaiting = true
		}
	}
	switch {
	case hasError:
		return ColorError, true
	case hasWaiting:
		return ColorWaiting, true
	}
	return ColorMuted, false
}

// renderSessionCard returns exactly sessionCardLines lines for one session.
//
//	Line 1: <stripe> <name>  <status glyph+word>      …right: badges
//	Line 2: <stripe>   <branch> · <context tag>       …right: age · N agents
//
// The stripe cell is the cursor bar (selected), the attention accent
// (waiting/error), or blank — one column, three meanings, matching the
// StatusWaiting accent semantics the dashboard used.
func (m sessionListModel) renderSessionCard(row sessionRow, selected bool, props sessionListProps) []string {
	sess := row.session
	sessKey := cacheKey(row.repoPath, sess.ID)

	// Stripe cell (2 cols: glyph + space).
	stripe := "  "
	if attnColor, attn := sessionAttention(sess); attn {
		stripe = lipgloss.NewStyle().Foreground(attnColor).Render(theme.GlyphStripe) + " "
	}
	if selected {
		stripe = lipgloss.NewStyle().Foreground(ColorSecondary).Render(theme.GlyphCursor) + " "
	}

	// --- Line 1: name + status, right-aligned badges ---
	nameBudget := m.width / 3
	if nameBudget < 12 {
		nameBudget = 12
	}
	name := StyleCardTitle.Render(truncateVisible(sess.GetDisplayName(), nameBudget))
	left1 := " " + stripe + name
	if glyph, word, col, ok := sessionListStatus(sess); ok {
		left1 += "  " + lipgloss.NewStyle().Foreground(col).Render(glyph+" "+word)
	}
	line1 := rightAlign(left1, m.renderBadges(row, sessKey, props), m.width)

	// --- Line 2: branch + context tag, right-aligned age + agent count ---
	branch := sess.Branch()
	var ctx string
	if sess.Kind() == agent.KindCheckout {
		// Distinct accent: an agent is loose in the user's real working tree.
		ctx = StyleWarning.Render("checkout @ " + branch)
	} else {
		ctx = StyleSubtle.Render("worktree")
	}
	left2 := " " + stripe + "  " + StyleSubtle.Render(truncateVisible(branch, m.width/2)) + StyleSubtle.Render(" · ") + ctx

	right2 := shortDuration(m.now.Sub(sess.CreatedAt))
	if n := sess.AgentCount(); n == 1 {
		right2 += " · 1 agent"
	} else if n > 1 {
		right2 += fmt.Sprintf(" · %d agents", n)
	}
	line2 := rightAlign(left2, StyleSubtle.Render(right2), m.width)

	return []string{line1, line2}
}

// renderBadges assembles the right-aligned badge cluster on a card's first
// line. Badges are derived session facts (§4.1): plan presence, draft in
// flight, PR state, teardown in flight. Empty when nothing applies.
func (m sessionListModel) renderBadges(row sessionRow, sessKey string, props sessionListProps) string {
	sess := row.session
	if props.closingSessions[sessKey] {
		return StyleSubtle.Render("closing…")
	}
	var badges []string
	switch {
	case sess.IsDrafting():
		badges = append(badges, StyleWarning.Render("✎ drafting…"))
	case sess.IsRevising():
		badges = append(badges, StyleWarning.Render("✎ revising…"))
	case sess.DraftError() != nil:
		badges = append(badges, StyleError.Render("✗ draft failed"))
	default:
		if plan, present := sess.CachedPlan(); present {
			// Fold task progress into the plan badge: commit trailers
			// (Plan-Task: N) and checked boxes both count, matching the old
			// building-card progress semantics.
			label := "plan"
			planTotal, planDone := planTaskCounts(plan)
			commitDone, commitMax := sess.CommitTaskCount()
			total := max(planTotal, commitMax)
			done := min(max(planDone, commitDone), total)
			if total > 0 {
				label = fmt.Sprintf("plan %d/%d", done, total)
			}
			badges = append(badges, StyleAccent.Render(label))
		}
	}
	if props.prDraftSessionID != "" && sess.ID == props.prDraftSessionID && row.repoPath == props.prDraftRepoPath {
		badges = append(badges, StyleWarning.Render(reviewSpinnerFrame(m.now)+" drafting PR…"))
	} else if entry := props.prCache[sessKey]; entry != nil {
		if ind := prIndicator(entry); ind != "" {
			badges = append(badges, ind)
		}
	}
	return strings.Join(badges, StyleSubtle.Render(" · "))
}

// renderRepoConfigModal renders the per-repo settings form as a centered
// modal box over the root view.
func renderRepoConfigModal(form *configForm, repoName, repoPath string, width, height int) string {
	if form == nil {
		return ""
	}
	if repoName == "" {
		repoName = repoPath
	}
	title := StyleTitle.Render(repoName + " Settings")
	pathLine := StyleSubtle.Render(repoPath)
	hint := StyleSubtle.Render("j/k navigate  ←/→ select  enter edit/toggle  ctrl+s save  esc cancel")
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		pathLine, "",
		form.View(), "",
		hint,
	)
	return placeCentered(width, height, modalBoxStyle(64).Render(content))
}

// renderRepoChecksModal renders the validation-checks sub-editor as a centered
// modal box, styled to match renderRepoConfigModal.
func renderRepoChecksModal(editor *repoChecksModel, repoPath string, width, height int) string {
	if editor == nil {
		return ""
	}
	repoName := editor.repoName
	if repoName == "" {
		repoName = repoPath
	}
	title := StyleTitle.Render(repoName + " · Validation Checks")
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		title, "",
		editor.View(),
	)
	return placeCentered(width, height, modalBoxStyle(72).Render(content))
}

// shortDuration formats an age as "12m" or "1h 5m" for the card's right edge.
func shortDuration(d time.Duration) string {
	mins := int(d.Minutes())
	if mins < 0 {
		mins = 0
	}
	if mins >= 60 {
		return fmt.Sprintf("%dh %dm", mins/60, mins%60)
	}
	return fmt.Sprintf("%dm", mins)
}
