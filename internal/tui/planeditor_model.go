package tui

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/tui/mdrender"
)

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

	doc docEditor // shared textarea, renderer, dimensions, scrollOff

	mode            planEditorMode
	plan            string // last-loaded plan content; used in scroll mode
	dirty           bool   // textarea has unsaved edits relative to file
	saveNote        string // transient confirmation ("saved") or error
	saveAt          time.Time
	saveNoteVisible bool // saveNote still within its 3s display window; refreshed on tick

	reviseInput     textinput.Model
	drafting        bool // session is currently in LifecycleDrafting; show placeholder
	revising        bool // a revise call is in flight; lock i/a/ctrl+s
	revisingFor     time.Time
	revisingElapsed int    // whole seconds since revisingFor; refreshed on tick
	statusMsg       string // generic status line under the header (e.g. "Drafting…")
	errMsg          string // inline error message; cleared on next interaction

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

// newPlanEditor constructs a fresh editor model bound to sess. Plan content
// is loaded from disk on construction; if the session is currently drafting
// the model renders a "Drafting…" placeholder and locks input.
func newPlanEditor(sess *agent.Session, repoPath string, width, height int) planEditorModel {
	m := planEditorModel{
		sess:     sess,
		repoPath: repoPath,
		mode:     planEditorModeScroll,
	}
	m.doc = newDocEditor(width, height)

	ti := textinput.New()
	ti.Placeholder = "What should change?"
	ti.CharLimit = 512
	ti.SetWidth(modalContentWidth(width))
	m.reviseInput = ti

	qi := textinput.New()
	qi.Placeholder = "Type an answer (enter to submit, esc to skip)"
	qi.CharLimit = 1024
	qi.SetWidth(modalContentWidth(width))
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
	m.doc.SetSize(w, h)
	m.reviseInput.SetWidth(modalContentWidth(w))
	m.questionInput.SetWidth(modalContentWidth(w))
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
		m.revisingElapsed = 0
		m.statusMsg = "Revising…"
	} else if m.statusMsg == "Revising…" {
		m.statusMsg = ""
	}
}

// refreshDerived recomputes time-based display values (the revising-elapsed
// counter and the save-note visibility window) from the editor's timestamps.
// Driven by the app tick so View()/renderHeader stay pure (§5): they read the
// stored fields instead of calling time.Since at render time.
func (m *planEditorModel) refreshDerived(now time.Time) {
	if m.revising {
		m.revisingElapsed = int(now.Sub(m.revisingFor).Seconds())
	}
	m.saveNoteVisible = m.saveNote != "" && now.Sub(m.saveAt) < 3*time.Second
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
	m.doc.Blur()
	m.reviseInput.Blur()
	return m.questionInput.Focus()
}

// HasPendingQuestion reports whether the editor is currently waiting on the
// user to answer (or skip) a planner question. The App uses this to suppress
// duplicate dispatches and to decide whether esc should close the editor or
// resolve the question.
func (m *planEditorModel) HasPendingQuestion() bool { return m.questionAnswerCh != nil }

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
	m.doc.SetValue(plan)
	m.dirty = false
	m.doc.scrollOff = 0
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
	v := m.doc.Value()
	srcLines := splitPlanLines(v)
	ctxs := m.doc.renderer.LineContexts(v)
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

func (m *planEditorModel) clampScroll() {
	m.doc.ClampScroll(len(m.displayLines()))
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
	body := m.doc.BodyHeight(5)
	if headingLine < m.doc.scrollOff {
		m.doc.scrollOff = headingLine
	} else if headingLine >= m.doc.scrollOff+body {
		m.doc.scrollOff = headingLine - body + 1
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
