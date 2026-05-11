package tui

import (
	"crypto/sha256"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/baton/internal/agent"
	"github.com/devenjarvis/baton/internal/tui/mdrender"
	"github.com/devenjarvis/baton/internal/tui/mdtextarea"
)

// planEditorChromaStyle is the chroma style used by the markdown renderer.
// Hardcoded for now — a follow-up will plumb this through config.Settings.
const planEditorChromaStyle = "monokai"

// planEditorMode is the editor's current input mode. The default is
// scroll-mode (read-only navigation); `i` enters edit-mode and `r` enters
// revise-input-mode. planEditorModeQuestion is entered automatically when
// the planner subprocess raises an ask_user clarifying question; it
// supersedes whatever mode was active and returns to scroll on submit.
type planEditorMode int

const (
	planEditorModeScroll planEditorMode = iota
	planEditorModeEdit
	planEditorModeReviseInput
	planEditorModeQuestion
)

// planEditorModel renders a session's .claude/plan.md and lets the user
// scroll, edit it directly via textarea, or kick off a revise loop. The
// model owns its own textarea/textinput components and reports user actions
// back to the App via tea.Msg values declared below.
type planEditorModel struct {
	sess     *agent.Session
	repoPath string // repo this session belongs to; set at construction to avoid ambiguous cross-repo ID lookup

	mode      planEditorMode
	plan      string // last-loaded plan content; used in scroll mode
	scrollOff int    // top line offset in scroll mode
	dirty     bool   // textarea has unsaved edits relative to file
	saveNote  string // transient confirmation ("saved") or error
	saveAt    time.Time

	textarea    mdtextarea.Model
	reviseInput textinput.Model
	width       int
	height      int
	drafting    bool // session is currently in LifecycleDrafting; show placeholder
	revising    bool // a revise call is in flight; lock i/a/ctrl+s
	revisingFor time.Time
	statusMsg   string // generic status line under the header (e.g. "Drafting…")
	errMsg      string // inline error message; cleared on next interaction

	renderer *mdrender.Renderer

	// displayCache memoises the post-wrap, post-style display lines for the
	// current textarea value at the current width. Invalidated by content
	// hash so edits always re-derive. The renderer itself caches at a coarser
	// grain (sha256(plan), styleName, width) — this avoids the per-frame map
	// lookup when nothing changed between renders.
	displayCache    []string
	displayCacheKey displayCacheKey

	// Planner question state. When questionAnswerCh is non-nil, the editor is
	// in planEditorModeQuestion and mirrors the question text + a single-line
	// input for the answer. Submitting (or skipping) the question writes a
	// value to questionAnswerCh exactly once and clears these fields. Clearing
	// without answering is a bug — the planner subprocess will hang.
	questionText     string
	questionInput    textinput.Model
	questionAnswerCh chan<- string
	priorMode        planEditorMode // mode to restore after the question is answered
}

// displayCacheKey gates the editor-local cache of display lines. width matters
// because re-wrap depends on it; valueHash matters because edits change the
// content. styleName isn't part of the key — the renderer is reused per
// editor and the style is fixed at construction.
type displayCacheKey struct {
	width     int
	valueHash [32]byte
}

// planEditorApproveMsg is emitted when the user approves the plan (`a`).
// The App spawns the real agent in response. The plan text itself isn't
// carried on the message — by the time approve fires, the editor has
// already written any pending textarea content to disk via Session.WritePlan,
// so the spawned agent reads .claude/plan.md directly.
type planEditorApproveMsg struct {
	sessionID string
}

// planEditorReviseMsg is emitted when the user submits a revise critique.
type planEditorReviseMsg struct {
	sessionID string
	critique  string
}

// planEditorAbandonMsg is emitted on `q` in scroll mode to abandon the
// planning session entirely.
type planEditorAbandonMsg struct {
	sessionID string
}

// planEditorCloseMsg is emitted on `esc` to close the editor and return to
// the dashboard without changing session state.
type planEditorCloseMsg struct {
	sessionID string
}

// planEditorRestoreMsg is emitted on `u` in scroll mode to restore the
// previous plan from .claude/plan.prev.md (single-step undo). The App
// handler delegates to Session.RestorePrevPlan and reloads the editor.
type planEditorRestoreMsg struct {
	sessionID string
}

// planEditorSavedMsg is emitted when ctrl+s completes; the App typically
// just clears any pending error state.
type planEditorSavedMsg struct {
	sessionID string
}

// newPlanEditor constructs a fresh editor model bound to sess. Plan content
// is loaded from disk on construction; if the session is currently drafting
// the model renders a "Drafting…" placeholder and locks input.
func newPlanEditor(sess *agent.Session, repoPath string, width, height int) planEditorModel {
	m := planEditorModel{
		sess:     sess,
		repoPath: repoPath,
		mode:     planEditorModeScroll,
		width:    width,
		height:   height,
	}

	ta := mdtextarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.SetWidth(textareaWidth(width))
	ta.SetHeight(textareaHeight(height))
	m.renderer = mdrender.New(planEditorChromaStyle)
	ta.SetMarkdownRenderer(m.renderer)
	m.textarea = ta

	ti := textinput.New()
	ti.Placeholder = "What should change?"
	ti.CharLimit = 512
	ti.SetWidth(width - 4)
	m.reviseInput = ti

	qi := textinput.New()
	qi.Placeholder = "Type an answer (enter to submit, esc to skip)"
	qi.CharLimit = 1024
	qi.SetWidth(width - 4)
	m.questionInput = qi

	m.reload()
	return m
}

// SetSize updates internal width/height and resizes the textarea. Called by
// the App on tea.WindowSizeMsg while the editor is focused. We clamp scroll
// here because shrinking the height also shrinks bodyHeight, which can leave
// scrollOff above its new max — keeping the clamp out of the View() path is
// what lets renderBody stay pure.
func (m *planEditorModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.textarea.SetWidth(textareaWidth(w))
	m.textarea.SetHeight(textareaHeight(h))
	m.reviseInput.SetWidth(w - 4)
	m.questionInput.SetWidth(w - 4)
	m.clampScroll()
}

// SetDrafting toggles the drafting placeholder. While drafting, scroll-mode
// hints render a spinner and `i`/`a`/`r` are no-ops.
func (m *planEditorModel) SetDrafting(v bool) {
	m.drafting = v
	if v {
		m.statusMsg = "Drafting…"
	} else if m.statusMsg == "Drafting…" {
		m.statusMsg = ""
	}
}

// SetRevising toggles the revising lock. The editor renders "Revising…
// (Ns)" until cleared.
func (m *planEditorModel) SetRevising(v bool) {
	m.revising = v
	if v {
		m.revisingFor = time.Now()
		m.statusMsg = "Revising…"
	} else if m.statusMsg == "Revising…" {
		m.statusMsg = ""
	}
}

// SetError sets a one-shot error message rendered below the header until
// the next interaction.
func (m *planEditorModel) SetError(s string) { m.errMsg = s }

// AskQuestion puts the editor into planEditorModeQuestion with the given
// question text and answer channel. It supersedes whatever mode was active
// (the prior mode is restored on submit). If a question is already pending,
// the new one is queued by replacing it — but in practice the planner is
// configured to ask one question at a time, so this is a defensive guard,
// not a routine code path: the previous answer channel gets the empty
// answer so the in-flight handler unblocks.
func (m *planEditorModel) AskQuestion(question string, answerCh chan<- string) tea.Cmd {
	if m.questionAnswerCh != nil {
		select {
		case m.questionAnswerCh <- "":
		default:
		}
	}
	m.questionText = question
	m.questionAnswerCh = answerCh
	m.questionInput.SetValue("")
	if m.mode != planEditorModeQuestion {
		m.priorMode = m.mode
	}
	m.mode = planEditorModeQuestion
	// Blur other inputs so keystrokes route to the question input only.
	m.textarea.Blur()
	m.reviseInput.Blur()
	return m.questionInput.Focus()
}

// HasPendingQuestion reports whether the editor is currently waiting on the
// user to answer (or skip) a planner question. The App uses this to suppress
// duplicate dispatches and to decide whether esc should close the editor or
// resolve the question.
func (m *planEditorModel) HasPendingQuestion() bool { return m.questionAnswerCh != nil }

// resolveQuestion sends answer to the planner, clears question state, and
// restores the prior mode. Safe to call only when a question is pending.
func (m *planEditorModel) resolveQuestion(answer string) {
	if m.questionAnswerCh == nil {
		return
	}
	// Non-blocking send: AnswerCh is buffered (cap 1) by the IPC server.
	select {
	case m.questionAnswerCh <- answer:
	default:
	}
	m.questionAnswerCh = nil
	m.questionText = ""
	m.questionInput.Blur()
	m.questionInput.SetValue("")
	m.mode = m.priorMode
	if m.mode == planEditorModeQuestion {
		m.mode = planEditorModeScroll
	}
}

// reload rereads the plan file from disk and resets dirty/scroll state.
func (m *planEditorModel) reload() {
	if m.sess == nil {
		return
	}
	plan, err := m.sess.ReadPlan()
	if err != nil {
		m.errMsg = "read plan: " + err.Error()
		return
	}
	m.plan = plan
	m.textarea.SetValue(plan)
	m.dirty = false
	m.scrollOff = 0
}

// Reload is the exported version called by the App when a draft completes
// or a revise lands.
func (m *planEditorModel) Reload() { m.reload() }

// Update routes a key event. The caller should already have dispatched
// other tea.Msg types (resize, ticks).
func (m *planEditorModel) Update(msg tea.Msg) tea.Cmd {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		// Forward non-key events to whichever component is active.
		if m.mode == planEditorModeEdit {
			var cmd tea.Cmd
			m.textarea, cmd = m.textarea.Update(msg)
			return cmd
		}
		if m.mode == planEditorModeReviseInput {
			var cmd tea.Cmd
			m.reviseInput, cmd = m.reviseInput.Update(msg)
			return cmd
		}
		if m.mode == planEditorModeQuestion {
			var cmd tea.Cmd
			m.questionInput, cmd = m.questionInput.Update(msg)
			return cmd
		}
		return nil
	}
	m.errMsg = ""

	switch m.mode {
	case planEditorModeEdit:
		return m.updateEdit(keyMsg)
	case planEditorModeReviseInput:
		return m.updateReviseInput(keyMsg)
	case planEditorModeQuestion:
		return m.updateQuestion(keyMsg)
	default:
		return m.updateScroll(keyMsg)
	}
}

func (m *planEditorModel) updateScroll(msg tea.KeyPressMsg) tea.Cmd {
	if m.drafting {
		// Only esc/q work during drafting; everything else is a no-op so the
		// user can't approve a half-written plan.
		switch msg.String() {
		case "esc":
			return m.emitClose()
		case "q":
			return m.emitAbandon()
		}
		return nil
	}

	switch msg.String() {
	case "esc":
		return m.emitClose()
	case "q":
		return m.emitAbandon()
	case "j", "down":
		m.scrollOff++
		m.clampScroll()
		return nil
	case "k", "up":
		if m.scrollOff > 0 {
			m.scrollOff--
		}
		return nil
	case "ctrl+d", "pgdown":
		m.scrollOff += m.bodyHeight() / 2
		m.clampScroll()
		return nil
	case "ctrl+u", "pgup":
		m.scrollOff -= m.bodyHeight() / 2
		if m.scrollOff < 0 {
			m.scrollOff = 0
		}
		return nil
	case "g", "home":
		m.scrollOff = 0
		return nil
	case "G", "end":
		m.scrollOff = len(m.displayLines())
		m.clampScroll()
		return nil
	case "i":
		if m.revising {
			return nil
		}
		m.mode = planEditorModeEdit
		return m.textarea.Focus()
	case "r":
		if m.revising {
			return nil
		}
		m.mode = planEditorModeReviseInput
		m.reviseInput.SetValue("")
		return m.reviseInput.Focus()
	case "u":
		if m.revising || m.sess == nil {
			return nil
		}
		// Surface a friendly inline message instead of routing through the
		// App when there's nothing to undo — saves a round-trip and keeps
		// the no-op key press from looking broken.
		if !m.sess.HasPrevPlan() {
			m.errMsg = "nothing to undo"
			return nil
		}
		sessID := m.sess.ID
		return func() tea.Msg { return planEditorRestoreMsg{sessionID: sessID} }
	case "a":
		if m.revising || m.drafting || m.sess == nil {
			return nil
		}
		// Persist any pending textarea edits before approving so the spawned
		// agent reads exactly what the user saw on screen. Approve is a
		// no-op on an empty plan; the editor surfaces an inline error and
		// stays put.
		val := m.textarea.Value()
		if strings.TrimSpace(val) == "" {
			m.errMsg = "Plan is empty — edit or revise first."
			return nil
		}
		if m.dirty {
			if err := m.sess.WritePlan(val); err != nil {
				m.errMsg = "save plan: " + err.Error()
				return nil
			}
			m.plan = val
			m.dirty = false
		}
		return m.emitApprove()
	}
	return nil
}

func (m *planEditorModel) updateEdit(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		// Preserve any in-progress edits — esc only blurs the textarea so
		// the user can scroll and approve without losing typed content. The
		// dirty indicator stays visible until ctrl+s or `a` writes to disk.
		m.textarea.Blur()
		m.mode = planEditorModeScroll
		return nil
	case "ctrl+s":
		if m.sess == nil {
			return nil
		}
		val := m.textarea.Value()
		if err := m.sess.WritePlan(val); err != nil {
			m.errMsg = "save plan: " + err.Error()
			return nil
		}
		m.plan = val
		m.dirty = false
		m.saveNote = "saved"
		m.saveAt = time.Now()
		sessID := m.sess.ID
		return func() tea.Msg { return planEditorSavedMsg{sessionID: sessID} }
	}
	prev := m.textarea.Value()
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	if m.textarea.Value() != prev {
		m.dirty = true
	}
	return cmd
}

// updateQuestion handles input while the editor is parked on a planner
// ask_user question. Enter submits the typed answer; esc submits an empty
// answer (the agreed "skip / no answer" signal so the planner unblocks
// rather than deadlocking). Answering also restores the prior input mode
// so the user can continue scrolling/editing without re-entering it.
func (m *planEditorModel) updateQuestion(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.resolveQuestion("")
		return nil
	case "enter":
		answer := strings.TrimSpace(m.questionInput.Value())
		m.resolveQuestion(answer)
		return nil
	}
	var cmd tea.Cmd
	m.questionInput, cmd = m.questionInput.Update(msg)
	return cmd
}

func (m *planEditorModel) updateReviseInput(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.reviseInput.Blur()
		m.mode = planEditorModeScroll
		return nil
	case "enter":
		critique := strings.TrimSpace(m.reviseInput.Value())
		if critique == "" {
			m.errMsg = "Critique is empty — describe what should change."
			return nil
		}
		m.reviseInput.Blur()
		m.mode = planEditorModeScroll
		sessID := m.sess.ID
		return func() tea.Msg {
			return planEditorReviseMsg{sessionID: sessID, critique: critique}
		}
	}
	var cmd tea.Cmd
	m.reviseInput, cmd = m.reviseInput.Update(msg)
	return cmd
}

func (m *planEditorModel) emitApprove() tea.Cmd {
	if m.sess == nil {
		return nil
	}
	sessID := m.sess.ID
	return func() tea.Msg { return planEditorApproveMsg{sessionID: sessID} }
}

func (m *planEditorModel) emitAbandon() tea.Cmd {
	if m.sess == nil {
		return nil
	}
	sessID := m.sess.ID
	return func() tea.Msg { return planEditorAbandonMsg{sessionID: sessID} }
}

func (m *planEditorModel) emitClose() tea.Cmd {
	if m.sess == nil {
		return nil
	}
	sessID := m.sess.ID
	return func() tea.Msg { return planEditorCloseMsg{sessionID: sessID} }
}

// displayLines returns the post-wrap, post-style ANSI display lines for the
// current textarea content at the current width. Result is cached on the
// model keyed by (width, valueHash); the renderer itself caches at a coarser
// grain so cross-frame reuse is cheap even when the editor is reconstructed.
func (m *planEditorModel) displayLines() []string {
	v := m.textarea.Value()
	if v == "" {
		return nil
	}
	w := m.contentWidth()
	key := displayCacheKey{width: w, valueHash: sha256.Sum256([]byte(v))}
	if m.displayCache != nil && m.displayCacheKey == key {
		return m.displayCache
	}
	out := m.renderer.RenderLines(v, w)
	m.displayCache = out
	m.displayCacheKey = key
	return out
}

// contentWidth is the column width used for both wrap and styling. Match
// textareaWidth so scroll-mode wraps line up with edit-mode wraps.
func (m *planEditorModel) contentWidth() int {
	return textareaWidth(m.width)
}

// bodyHeight is the number of lines available for plan content.
// Subtract: header (2) + status line (1) + footer hints (2) = 5.
func (m *planEditorModel) bodyHeight() int {
	h := m.height - 5
	if h < 1 {
		return 1
	}
	return h
}

func (m *planEditorModel) clampScroll() {
	max := len(m.displayLines()) - m.bodyHeight()
	if max < 0 {
		max = 0
	}
	if m.scrollOff > max {
		m.scrollOff = max
	}
	if m.scrollOff < 0 {
		m.scrollOff = 0
	}
}

// View renders the full-page plan editor.
func (m *planEditorModel) View() string {
	var lines []string
	lines = append(lines, m.renderHeader())
	lines = append(lines, StyleSubtle.Render(strings.Repeat("─", max(1, m.width-2))))

	if status := m.renderStatusLine(); status != "" {
		lines = append(lines, status)
	}

	switch m.mode {
	case planEditorModeEdit:
		lines = append(lines, m.textarea.View())
	case planEditorModeReviseInput:
		lines = append(lines, m.renderBody())
		lines = append(lines, "")
		lines = append(lines, StyleActive.Render("revise:")+" "+m.reviseInput.View())
	case planEditorModeQuestion:
		lines = append(lines, m.renderQuestionBody())
	default:
		lines = append(lines, m.renderBody())
	}

	lines = append(lines, m.renderFooter())
	return strings.Join(lines, "\n")
}

func (m *planEditorModel) renderHeader() string {
	title := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("PLAN")
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
		secs := int(time.Since(m.revisingFor).Seconds())
		rightLabel = StyleActive.Render("revising… (" + fmtSeconds(secs) + ")")
	case m.dirty:
		rightLabel = StyleWarning.Render("● unsaved")
	case m.saveNote != "" && time.Since(m.saveAt) < 3*time.Second:
		rightLabel = StyleSuccess.Render(m.saveNote)
	}
	gap := m.width - ansi.StringWidth(left) - ansi.StringWidth(rightLabel) - 4
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + rightLabel
}

func (m *planEditorModel) renderStatusLine() string {
	if m.errMsg != "" {
		return StyleError.Render(m.errMsg)
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
		body := m.bodyHeight() - 1
		if body < 1 {
			body = 1
		}
		end := m.scrollOff + body
		if end > len(all) {
			end = len(all)
		}
		var rendered string
		if len(all) == 0 {
			rendered = StyleSubtle.Render("(no plan content)")
		} else {
			start := m.scrollOff
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
	body := m.bodyHeight()
	// Use a local start so View() stays pure — never mutate m.scrollOff
	// from the render path. Update()/SetSize keep m.scrollOff in range via
	// clampScroll; this local clamp guards a stale scrollOff in the render
	// frame that races a textarea shrink (e.g. plan reload after revise).
	start := m.scrollOff
	if start > len(all)-body {
		start = len(all) - body
	}
	if start < 0 {
		start = 0
	}
	end := start + body
	if end > len(all) {
		end = len(all)
	}
	return strings.Join(all[start:end], "\n")
}

// planLinesPlain returns the textarea's value as raw source lines. Used by
// the revising-mode preview, which intentionally shows un-styled muted text.
func (m *planEditorModel) planLinesPlain() []string {
	v := m.textarea.Value()
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
		hints = StyleActive.Render("i") + StyleSubtle.Render(" edit  ") +
			StyleActive.Render("r") + StyleSubtle.Render(" revise  ")
		if m.sess != nil && m.sess.HasPrevPlan() {
			hints += StyleActive.Render("u") + StyleSubtle.Render(" undo  ")
		}
		hints += StyleActive.Render("a") + StyleSubtle.Render(" approve  ") +
			StyleActive.Render("q") + StyleSubtle.Render(" abandon  ") +
			StyleActive.Render("esc") + StyleSubtle.Render(" back")
	}
	divider := StyleSubtle.Render(strings.Repeat("─", max(1, m.width-2)))
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
	b.WriteString(ansi.Wrap(m.questionText, max(20, m.width-4), ""))
	b.WriteString("\n\n")
	b.WriteString(StyleActive.Render("answer:") + " " + m.questionInput.View())
	return b.String()
}

// textareaWidth/textareaHeight reserve space for header, divider, status,
// and footer when sizing the embedded textarea.
func textareaWidth(w int) int {
	if w < 8 {
		return 8
	}
	return w - 2
}

func textareaHeight(h int) int {
	if h < 6 {
		return 1
	}
	return h - 5
}

func fmtSeconds(s int) string {
	if s < 0 {
		s = 0
	}
	if s < 60 {
		return strconv.Itoa(s) + "s"
	}
	return strconv.Itoa(s/60) + "m" + strconv.Itoa(s%60) + "s"
}
