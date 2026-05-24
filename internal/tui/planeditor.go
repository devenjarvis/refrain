package tui

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/tui/mdrender"
	"github.com/devenjarvis/refrain/internal/tui/mdtextarea"
)

// planEditorChromaStyle is the chroma style used by the markdown renderer.
// Hardcoded for now — a follow-up will plumb this through config.Settings.
const planEditorChromaStyle = "monokai"

// planEditorMaxMeasure is the maximum content column width for plan text.
// On wider terminals the plan is centered with equal left/right margins.
const planEditorMaxMeasure = 72

// planSection represents one H1 or H2 ATX section in the plan. headingLine is
// the 0-based source-line index of the `#`/`##` line; nextLine is the index of
// the next sibling-or-ancestor heading (or end-of-lines). level is 1 or 2.
// heading is the trimmed text after the `#` characters.
type planSection struct {
	headingLine int
	nextLine    int
	level       int
	heading     string
}

// parsePlanSections walks LineContexts and builds the ordered section list.
// preamble lines before the first H1/H2 are not represented here; callers
// handle them as source lines [0, sections[0].headingLine).
func parsePlanSections(srcLines []string, ctxs []mdrender.LineCtx) []planSection {
	var sections []planSection
	for i, ctx := range ctxs {
		if ctx.Kind == mdrender.LineHeading && ctx.HeadingLevel <= 2 {
			heading := strings.TrimSpace(strings.TrimLeft(srcLines[i], "#\t "))
			sections = append(sections, planSection{
				headingLine: i,
				level:       ctx.HeadingLevel,
				heading:     heading,
			})
		}
	}
	// Fill nextLine: each section ends where the next one begins (or at len(srcLines)).
	for i := range sections {
		if i+1 < len(sections) {
			sections[i].nextLine = sections[i+1].headingLine
		} else {
			sections[i].nextLine = len(srcLines)
		}
	}
	return sections
}

// defaultSectionFolded returns the initial fold state for a section.
// H1 sections and H2 Spec/Tasks/Goal are expanded; every other H2 is collapsed.
func defaultSectionFolded(heading string, level int) bool {
	if level == 1 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(heading)) {
	case "spec", "tasks", "goal":
		return false
	}
	return true
}

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

	// sections is the ordered list of H1/H2 sections parsed from the current
	// plan. folds maps section heading name to its current fold state (true =
	// collapsed). Both are repopulated by reload() and preserved across Reload()
	// for headings that survive the edit.
	sections      []planSection
	folds         map[string]bool
	sectionCursor int // index into sections of the cursor-selected section

	// displayCache memoises the post-wrap, post-style display lines for the
	// current textarea value at the current width. Invalidated by content
	// hash so edits always re-derive. The renderer itself caches at a coarser
	// grain (sha256(plan), styleName, width) — this avoids the per-frame map
	// lookup when nothing changed between renders.
	//
	// foldsHash is included in the key because a fold toggle changes the
	// displayed lines without changing the plan content; missing this would
	// return stale lines from cache after a toggle.
	//
	// sectionDisplayStart[i] is the display-line index where sections[i]'s
	// heading line appears. Recomputed with displayCache.
	displayCache        []string
	displayCacheKey     displayCacheKey
	sectionDisplayStart []int

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
// content; foldsHash captures the current fold state so toggling a fold
// invalidates the cache even when the plan content is unchanged.
type displayCacheKey struct {
	width         int
	valueHash     [32]byte
	foldsHash     [32]byte
	sectionCursor int
}

// planEditorApproveMsg is emitted when the user approves the plan (`a`).
// The App spawns the real agent in response. The plan text itself isn't
// carried on the message — by the time approve fires, the editor has
// already written any pending textarea content to disk via Session.WritePlan,
// so the spawned agent reads .claude/plan.md directly.
type planEditorApproveMsg struct {
	sessionID string
	repoPath  string
}

// planEditorReviseMsg is emitted when the user submits a revise critique.
type planEditorReviseMsg struct {
	sessionID string
	repoPath  string
	critique  string
}

// planEditorRetryMsg is emitted when the user presses R in scroll mode while
// a draft error is set and the original prompt is available.
type planEditorRetryMsg struct {
	sessionID string
	repoPath  string
}

// planEditorAbandonMsg is emitted on `q` in scroll mode to abandon the
// planning session entirely.
type planEditorAbandonMsg struct {
	sessionID string
	repoPath  string
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
// hints render a spinner and `i`/`a`/`r` are no-ops. When the session is on
// a retry attempt, the status line reflects the current attempt counter.
func (m *planEditorModel) SetDrafting(v bool) {
	m.drafting = v
	if v {
		if m.sess != nil {
			if cur, max := m.sess.DraftAttempt(); cur > 1 && max > 0 {
				m.statusMsg = fmt.Sprintf("Drafting… (retry %d/%d)", cur, max)
				return
			}
		}
		m.statusMsg = "Drafting…"
	} else if m.statusMsg == "Drafting…" || strings.HasPrefix(m.statusMsg, "Drafting… (retry") {
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
// It repopulates sections and seeds folds with default values; existing fold
// state for unchanged heading names is preserved in Reload() by the caller.
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
	m.rebuildSections()
}

// rebuildSections parses the current plan into sections and applies the
// default fold policy to every heading. It does NOT preserve existing fold
// state — callers that need preservation (see Reload) must do so explicitly.
//
// Fold state is keyed by heading text, not by position. If two H2s share the
// same name (non-canonical but user-editable), they collapse/expand together —
// accepted rather than introducing a positional disambiguator.
func (m *planEditorModel) rebuildSections() {
	v := m.textarea.Value()
	srcLines := splitPlanLines(v)
	ctxs := m.renderer.LineContexts(v)
	newSections := parsePlanSections(srcLines, ctxs)
	newFolds := make(map[string]bool, len(newSections))
	for _, s := range newSections {
		newFolds[s.heading] = defaultSectionFolded(s.heading, s.level)
	}
	m.sections = newSections
	m.folds = newFolds
	m.clampCursor()
}

// rebuildSectionsPreservingFolds rebuilds section boundaries from the current
// textarea value, restoring fold state for any heading that survives the edit.
// New headings receive the default fold policy; vanished headings are dropped.
func (m *planEditorModel) rebuildSectionsPreservingFolds() {
	savedFolds := make(map[string]bool, len(m.folds))
	for heading, folded := range m.folds {
		savedFolds[heading] = folded
	}
	m.rebuildSections()
	for _, s := range m.sections {
		if saved, ok := savedFolds[s.heading]; ok {
			m.folds[s.heading] = saved
		}
	}
}

// splitPlanLines splits plan content on "\n", matching mdrender's convention.
func splitPlanLines(plan string) []string {
	if plan == "" {
		return nil
	}
	return strings.Split(plan, "\n")
}

// Reload is called by the App when a draft completes or a revise lands. It
// re-reads the plan from disk and preserves fold state for every heading name
// that still exists in the new content. New headings receive the default fold
// policy (spec item 2); vanished headings are dropped silently.
func (m *planEditorModel) Reload() {
	// Snapshot current fold state before reload resets it to defaults.
	savedFolds := make(map[string]bool, len(m.folds))
	for heading, folded := range m.folds {
		savedFolds[heading] = folded
	}
	m.reload()
	// Overwrite default folds with saved state for any heading that survived.
	for _, s := range m.sections {
		if saved, ok := savedFolds[s.heading]; ok {
			m.folds[s.heading] = saved
		}
	}
}

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

// activeSectionIndex returns the index into m.sections of the section that
// contains the current viewport top (m.scrollOff). Returns -1 if there are no
// sections or if scrollOff is in the preamble before any section heading.
func (m *planEditorModel) activeSectionIndex() int {
	// Ensure sectionDisplayStart is populated.
	m.displayLines()
	if len(m.sectionDisplayStart) == 0 {
		return -1
	}
	// Walk backward to find the last section whose display start <= scrollOff.
	result := -1
	for i, start := range m.sectionDisplayStart {
		if start <= m.scrollOff {
			result = i
		}
	}
	return result
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
		if len(m.sections) > 0 {
			m.sectionCursor++
			m.clampCursor()
			m.scrollToCursor()
		}
		return nil
	case "k", "up":
		if len(m.sections) > 0 {
			m.sectionCursor--
			m.clampCursor()
			m.scrollToCursor()
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
	case "tab":
		idx := m.activeSectionIndex()
		if idx >= 0 {
			heading := m.sections[idx].heading
			m.folds[heading] = !m.folds[heading]
			m.invalidateDisplayCache()
			m.clampScroll()
		}
		return nil
	case "]":
		m.displayLines() // ensure sectionDisplayStart is populated
		for _, start := range m.sectionDisplayStart {
			// Strict > so that pressing ] from exactly on a heading advances
			// to the *next* heading. Two presses from the same heading are a
			// no-op on the second press, which is intentional.
			if start > m.scrollOff {
				m.scrollOff = start
				m.clampScroll()
				return nil
			}
		}
		return nil
	case "[":
		m.displayLines() // ensure sectionDisplayStart is populated
		best := -1
		for _, start := range m.sectionDisplayStart {
			if start < m.scrollOff {
				best = start
			}
		}
		if best >= 0 {
			m.scrollOff = best
			m.clampScroll()
		}
		return nil
	case "Z":
		// If any section is expanded, collapse all; otherwise expand all.
		anyExpanded := false
		for _, s := range m.sections {
			if !m.folds[s.heading] {
				anyExpanded = true
				break
			}
		}
		for _, s := range m.sections {
			m.folds[s.heading] = anyExpanded
		}
		m.invalidateDisplayCache()
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
	case "R":
		if m.drafting || m.revising {
			return nil
		}
		if m.sess == nil || m.sess.DraftError() == nil || m.sess.OriginalPrompt() == "" {
			return nil
		}
		sessID, repoPath := m.sess.ID, m.repoPath
		return func() tea.Msg { return planEditorRetryMsg{sessionID: sessID, repoPath: repoPath} }
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
		m.rebuildSectionsPreservingFolds()
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
		m.rebuildSectionsPreservingFolds()
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
		repoPath := m.repoPath
		return func() tea.Msg {
			return planEditorReviseMsg{sessionID: sessID, repoPath: repoPath, critique: critique}
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
	repoPath := m.repoPath
	return func() tea.Msg { return planEditorApproveMsg{sessionID: sessID, repoPath: repoPath} }
}

func (m *planEditorModel) emitAbandon() tea.Cmd {
	if m.sess == nil {
		return nil
	}
	sessID := m.sess.ID
	repoPath := m.repoPath
	return func() tea.Msg { return planEditorAbandonMsg{sessionID: sessID, repoPath: repoPath} }
}

func (m *planEditorModel) emitClose() tea.Cmd {
	if m.sess == nil {
		return nil
	}
	sessID := m.sess.ID
	return func() tea.Msg { return planEditorCloseMsg{sessionID: sessID} }
}

// foldsHashFor computes a stable hash over the current fold state so it can be
// included in displayCacheKey. The hash is built from "heading|folded\n" pairs
// in section order so that reordering is detectable (though the canonical plan
// format has unique names, reordering is still a content change).
func (m *planEditorModel) foldsHashFor() [32]byte {
	var sb strings.Builder
	for _, s := range m.sections {
		folded := m.folds[s.heading]
		if folded {
			fmt.Fprintf(&sb, "%s|1\n", s.heading)
		} else {
			fmt.Fprintf(&sb, "%s|0\n", s.heading)
		}
	}
	return sha256.Sum256([]byte(sb.String()))
}

// invalidateDisplayCache zeroes the display cache key so the next displayLines
// call rebuilds from scratch.
func (m *planEditorModel) invalidateDisplayCache() {
	m.displayCacheKey = displayCacheKey{}
	m.displayCache = nil
	m.sectionDisplayStart = nil
}

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
	v := m.textarea.Value()
	if v == "" {
		return nil
	}
	w := m.contentWidth()
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
		out := m.renderer.RenderLines(v, w)
		m.displayCache = out
		m.displayCacheKey = key
		m.sectionDisplayStart = nil
		return out
	}

	srcLines := splitPlanLines(v)
	ctxs := m.renderer.LineContexts(v)
	sectionStarts := make([]int, len(m.sections))

	out := make([]string, 0, len(srcLines))

	// Preamble: source lines before the first section heading.
	preambleEnd := m.sections[0].headingLine
	for i := 0; i < preambleEnd && i < len(srcLines); i++ {
		var ctx mdrender.LineCtx
		if i < len(ctxs) {
			ctx = ctxs[i]
		}
		out = append(out, m.styledScrollLines(srcLines[i], ctx, w)...)
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
		headingSegs := m.renderer.StyleLine(srcLines[s.headingLine], headingCtx, w)
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
			out = append(out, m.renderer.HeadingUnderline(headingCtx.HeadingLevel, headingTextWidth, w))
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
			out = append(out, m.styledScrollLines(srcLines[i], ctx, w)...)
		}
	}

	// Apply centering: prepend left-margin padding after all fold glyphs so
	// the glyph stays at the content edge, not pushed into the margin.
	if leftPad := m.displayLeftPad(); leftPad > 0 {
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

// contentWidth returns the effective column width for wrap and styling.
// Capped at planEditorMaxMeasure so wide terminals produce comfortable margins.
func (m *planEditorModel) contentWidth() int {
	measure, _ := mdrender.ContentMeasure(textareaWidth(m.width), planEditorMaxMeasure)
	return measure
}

// displayLeftPad returns the left-margin padding to center content on wide terminals.
func (m *planEditorModel) displayLeftPad() int {
	_, pad := mdrender.ContentMeasure(textareaWidth(m.width), planEditorMaxMeasure)
	return pad
}

// styledScrollLines wraps and styles a source line for scroll-mode display,
// applying fence-block bar prefixes that are omitted in edit mode to keep
// mdtextarea cursor-splice math unaffected.
func (m *planEditorModel) styledScrollLines(src string, ctx mdrender.LineCtx, width int) []string {
	isFenceLine := ctx.Kind == mdrender.LineFenceContent || ctx.Kind == mdrender.LineFenceOpen || ctx.Kind == mdrender.LineFenceClose
	lineWidth := width
	if isFenceLine && width > 2 {
		lineWidth = width - 2
	}
	lines := m.renderer.StyleLine(src, ctx, lineWidth)
	if isFenceLine {
		bar := StyleSubtle.Render("│") + " "
		for i, l := range lines {
			lines[i] = bar + l
		}
	}
	return lines
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

// scrollToCursor adjusts scrollOff so the heading of sectionCursor's section
// is visible within the body viewport. Scrolls up if the heading is above the
// current viewport; scrolls down if it's below.
func (m *planEditorModel) scrollToCursor() {
	if len(m.sectionDisplayStart) == 0 {
		m.displayLines()
	}
	if m.sectionCursor < 0 || m.sectionCursor >= len(m.sectionDisplayStart) {
		return
	}
	headingLine := m.sectionDisplayStart[m.sectionCursor]
	body := m.bodyHeight()
	if headingLine < m.scrollOff {
		m.scrollOff = headingLine
	} else if headingLine >= m.scrollOff+body {
		m.scrollOff = headingLine - body + 1
	}
	m.clampScroll()
}

func (m *planEditorModel) clampCursor() {
	if len(m.sections) == 0 {
		m.sectionCursor = 0
		return
	}
	last := len(m.sections) - 1
	if m.sectionCursor > last {
		m.sectionCursor = last
	}
	if m.sectionCursor < 0 {
		m.sectionCursor = 0
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
		hints = StyleActive.Render("tab") + StyleSubtle.Render(" fold  ") +
			StyleActive.Render("[ ]") + StyleSubtle.Render(" sections  ") +
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
