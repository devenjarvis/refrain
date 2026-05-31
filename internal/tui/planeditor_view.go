package tui

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/refrain/internal/tui/mdrender"
)

// displayLines returns the fold-aware, post-wrap, post-style ANSI display lines
// for the current textarea content at the current width. Collapsed sections
// appear as a single line with ▶ glyph, heading text, and hidden-line count.
// Expanded sections prepend ▼ to the heading line. Preamble lines (before the
// first H1/H2) are always shown.
//
// Result is cached on (width, valueHash, foldsHash); the renderer itself caches
// at a coarser grain so cross-frame reuse is cheap.
//
// sectionDisplayStart is populated as a side-effect of building the cache so
// navigation ([ and ]) and tab-fold detection can look up section positions
// without re-scanning.
func (m *planEditorModel) displayLines() []string {
	v := m.doc.Value()
	if v == "" {
		return nil
	}
	w := m.doc.ContentWidth()
	key := displayCacheKey{
		width:         w,
		valueHash:     sha256.Sum256([]byte(v)),
		foldsHash:     m.foldsHashFor(),
		sectionCursor: m.sectionCursor,
	}
	if m.displayCache != nil && m.displayCacheKey == key {
		return m.displayCache
	}

	// If there are no sections at all, fall back to the plain renderer path.
	if len(m.sections) == 0 {
		out := m.doc.renderer.RenderLines(v, w)
		m.displayCache = out
		m.displayCacheKey = key
		m.sectionDisplayStart = nil
		return out
	}

	srcLines := splitPlanLines(v)
	ctxs := m.doc.renderer.LineContexts(v)
	sectionStarts := make([]int, len(m.sections))

	out := make([]string, 0, len(srcLines))

	// Preamble: source lines before the first section heading.
	preambleEnd := m.sections[0].headingLine
	for i := 0; i < preambleEnd && i < len(srcLines); i++ {
		var ctx mdrender.LineCtx
		if i < len(ctxs) {
			ctx = ctxs[i]
		}
		out = append(out, m.doc.StyledScrollLines(srcLines[i], ctx, w)...)
	}

	// Sections.
	for si, s := range m.sections {
		sectionStarts[si] = len(out)
		folded := m.folds[s.heading]

		// Render the heading line with ▼/▶ glyph.
		var headingCtx mdrender.LineCtx
		if s.headingLine < len(ctxs) {
			headingCtx = ctxs[s.headingLine]
		}
		headingSegs := m.doc.renderer.StyleLine(srcLines[s.headingLine], headingCtx, w)
		if len(headingSegs) == 0 {
			headingSegs = []string{""}
		}
		glyphStyle := StyleSubtle
		if si == m.sectionCursor {
			glyphStyle = StyleActive
		}
		glyph := glyphStyle.Render("▼ ")
		if folded {
			glyph = glyphStyle.Render("▶ ")
		}
		headingLine := glyph + headingSegs[0]
		if folded {
			// Count hidden source lines. Only strip a trailing "" for the last
			// section (where strings.Split produces a spurious empty entry from
			// the final \n). For mid-plan sections the final "" is a real blank
			// line the user typed before the next heading and should be counted.
			hiddenSlice := srcLines[s.headingLine+1 : s.nextLine]
			hiddenCount := len(hiddenSlice)
			if s.nextLine == len(srcLines) && hiddenCount > 0 && hiddenSlice[hiddenCount-1] == "" {
				hiddenCount--
			}
			headingLine += StyleSubtle.Render(fmt.Sprintf("  · %d lines", hiddenCount))
		}
		out = append(out, headingLine)
		// Additional wrapped heading segments (rare but possible on narrow terminals).
		out = append(out, headingSegs[1:]...)

		// Inject underline decoration for H1/H2 headings when expanded, matching
		// what RenderLines produces so scroll-mode line counts stay in sync.
		if !folded && headingCtx.HeadingLevel >= 1 && headingCtx.HeadingLevel <= 2 {
			headingTextWidth := ansi.StringWidth(srcLines[s.headingLine])
			out = append(out, m.doc.renderer.HeadingUnderline(headingCtx.HeadingLevel, headingTextWidth, w))
		}

		if folded {
			continue
		}

		// Expanded: emit all content lines within this section.
		for i := s.headingLine + 1; i < s.nextLine && i < len(srcLines); i++ {
			var ctx mdrender.LineCtx
			if i < len(ctxs) {
				ctx = ctxs[i]
			}
			out = append(out, m.doc.StyledScrollLines(srcLines[i], ctx, w)...)
		}
	}

	// Apply centering: prepend left-margin padding after all fold glyphs so
	// the glyph stays at the content edge, not pushed into the margin.
	if leftPad := m.doc.DisplayLeftPad(); leftPad > 0 {
		pad := strings.Repeat(" ", leftPad)
		for i, line := range out {
			out[i] = pad + line
		}
	}

	m.displayCache = out
	m.displayCacheKey = key
	m.sectionDisplayStart = sectionStarts
	return out
}

// View renders the full-page plan editor.
func (m planEditorModel) View() string {
	var lines []string
	lines = append(lines, m.renderHeader())
	lines = append(lines, StyleSubtle.Render(strings.Repeat("─", max(1, innerWidth(m.doc.width)))))

	statusLine := m.renderStatusLine()
	if statusLine != "" {
		lines = append(lines, statusLine)
	}
	statusLines := 0
	if statusLine != "" {
		statusLines = 1
	}

	const footerLineCount = 2 // divider + hints
	bodyH := m.doc.height - 2 - statusLines - footerLineCount
	if bodyH < 1 {
		bodyH = 1
	}

	var body string
	switch m.mode {
	case planEditorModeEdit:
		body = m.doc.CenteredBlock(m.doc.textarea.View())
	case planEditorModeReviseInput:
		// Reserve 2 rows (blank + revise: input) for the input affordance so the
		// plan preview doesn't push the revise: line below the footer.
		previewH := bodyH - 2
		if previewH < 1 {
			previewH = 1
		}
		rawPreview := m.renderBody()
		previewLines := strings.Split(rawPreview, "\n")
		if len(previewLines) > previewH {
			previewLines = previewLines[:previewH]
		}
		body = strings.Join(previewLines, "\n") + "\n\n" + StyleActive.Render("revise:") + " " + m.reviseInput.View()
	case planEditorModeQuestion:
		body = m.renderQuestionBody()
	default:
		body = m.renderBody()
	}
	body = fillHeight(body, m.doc.width, bodyH)
	lines = append(lines, body)

	lines = append(lines, m.renderFooter())
	return strings.Join(lines, "\n")
}

func (m *planEditorModel) renderHeader() string {
	title := StyleTitle.Render("PLAN")
	if m.sess == nil {
		return title
	}
	name := m.sess.GetDisplayName()
	left := title + "  " + StyleSubtle.Render("›") + "  " + name
	rightLabel := ""
	switch {
	case m.drafting:
		rightLabel = StyleActive.Render("drafting…")
	case m.revising:
		rightLabel = StyleActive.Render("revising… (" + fmtSeconds(m.revisingElapsed) + ")")
	case m.dirty:
		rightLabel = StyleWarning.Render("● unsaved")
	case m.saveNoteVisible:
		rightLabel = StyleSuccess.Render(m.saveNote)
	}
	gap := m.doc.width - ansi.StringWidth(left) - ansi.StringWidth(rightLabel) - 4
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + rightLabel
}

func (m *planEditorModel) renderStatusLine() string {
	if m.errMsg != "" {
		msg := m.errMsg
		if m.sess != nil && m.sess.DraftError() != nil && m.sess.OriginalPrompt() != "" {
			msg += " — press R to retry"
		}
		return StyleError.Render(msg)
	}
	if m.statusMsg != "" {
		return StyleSubtle.Render(m.statusMsg)
	}
	return ""
}

func (m *planEditorModel) renderBody() string {
	if m.drafting {
		return StyleSubtle.Render("Drafting plan with claude -p… (esc to cancel)")
	}
	if m.revising {
		// Show the current plan greyed out so the user has context for the
		// in-flight critique, plus a status line at the top. Cleaner than a
		// blank "Revising…" screen and lets the user keep reading. The grey
		// wrapper drops the inline syntax highlighting on purpose; revising
		// is a transient state and a uniform muted body cues "this is the
		// previous plan, not the current one".
		all := m.planLinesPlain()
		body := m.doc.BodyHeight(5) - 1
		if body < 1 {
			body = 1
		}
		end := m.doc.scrollOff + body
		if end > len(all) {
			end = len(all)
		}
		var rendered string
		if len(all) == 0 {
			rendered = StyleSubtle.Render("(no plan content)")
		} else {
			start := m.doc.scrollOff
			if start > len(all) {
				start = len(all)
			}
			if start < 0 {
				start = 0
			}
			rendered = strings.Join(all[start:end], "\n")
		}
		return StyleActive.Render("Revising plan with claude -p…") + "\n" + StyleSubtle.Render(rendered)
	}
	all := m.displayLines()
	if len(all) == 0 {
		return StyleSubtle.Render("(no plan content yet — press i to start writing or r to revise)")
	}
	// Use a local start so View() stays pure — never mutate m.doc.scrollOff
	// from the render path. Update()/SetSize keep m.doc.scrollOff in range via
	// clampScroll; this local clamp guards a stale scrollOff in the render
	// frame that races a textarea shrink (e.g. plan reload after revise).
	return m.doc.ScrollWindow(all, m.doc.BodyHeight(5))
}

// planLinesPlain returns the textarea's value as raw source lines. Used by
// the revising-mode preview, which intentionally shows un-styled muted text.
func (m *planEditorModel) planLinesPlain() []string {
	v := m.doc.Value()
	if v == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(v, "\n"), "\n")
}

func (m *planEditorModel) renderFooter() string {
	var hints string
	switch m.mode {
	case planEditorModeEdit:
		hints = StyleActive.Render("ctrl+s") + StyleSubtle.Render(" save  ") +
			StyleActive.Render("esc") + StyleSubtle.Render(" cancel edit")
	case planEditorModeReviseInput:
		hints = StyleActive.Render("enter") + StyleSubtle.Render(" submit  ") +
			StyleActive.Render("esc") + StyleSubtle.Render(" cancel")
	case planEditorModeQuestion:
		hints = StyleActive.Render("enter") + StyleSubtle.Render(" answer  ") +
			StyleActive.Render("esc") + StyleSubtle.Render(" skip")
	default:
		hints = StyleActive.Render("j/k") + StyleSubtle.Render(" navigate  ") +
			StyleActive.Render("tab") + StyleSubtle.Render(" fold  ") +
			StyleActive.Render("Z") + StyleSubtle.Render(" toggle all  ") +
			StyleActive.Render("i") + StyleSubtle.Render(" edit  ") +
			StyleActive.Render("r") + StyleSubtle.Render(" revise  ")
		if m.sess != nil && m.sess.HasPrevPlan() {
			hints += StyleActive.Render("u") + StyleSubtle.Render(" undo  ")
		}
		hints += StyleActive.Render("a") + StyleSubtle.Render(" approve  ") +
			StyleActive.Render("q") + StyleSubtle.Render(" abandon  ") +
			StyleActive.Render("esc") + StyleSubtle.Render(" back")
	}
	divider := StyleSubtle.Render(strings.Repeat("─", max(1, innerWidth(m.doc.width))))
	return divider + "\n" + hints
}

// renderQuestionBody renders the planner-question card: an "ask_user" badge,
// the question text wrapped to width, a blank line, and the answer input.
// Kept deliberately minimal — the goal is to make it impossible to miss the
// question, not to entertain. The plan content is intentionally hidden to
// keep the user's focus on the one decision the planner is blocking on.
func (m *planEditorModel) renderQuestionBody() string {
	var b strings.Builder
	b.WriteString(StyleActive.Render("planner is asking:"))
	b.WriteString("\n\n")
	b.WriteString(ansi.Wrap(m.questionText, max(20, modalContentWidth(m.doc.width)), ""))
	b.WriteString("\n\n")
	b.WriteString(StyleActive.Render("answer:") + " " + m.questionInput.View())
	return b.String()
}
