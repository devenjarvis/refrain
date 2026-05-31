package tui

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/config"
	"github.com/devenjarvis/refrain/internal/tui/mdrender"
	"github.com/devenjarvis/refrain/internal/tui/mdrender/testutil"
	"github.com/muesli/termenv"
)

// newEditorTestSession creates a real session in a temp git repo so the
// editor's WritePlan/ReadPlan calls hit a real worktree without spawning a
// claude subprocess. The bash placeholder agent is killed after the test.
func newEditorTestSession(t *testing.T) (*agent.Session, *agent.Manager) {
	t.Helper()

	dir, err := os.MkdirTemp("", "refrain-planeditor-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	for _, args := range [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "t@t.com"},
		{"git", "config", "user.name", "T"},
		{"git", "config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git setup %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(dir+"/README.md", []byte("# t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "init"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("commit %v: %v\n%s", args, err, out)
		}
	}

	mgr := agent.NewManager(dir, config.Resolve(nil, nil))
	t.Cleanup(func() { mgr.Shutdown() })

	cfg := agent.Config{Rows: 24, Cols: 80, Task: "test"}
	sess, _, err := mgr.CreateSessionWithCommand(cfg, func(name string) *exec.Cmd {
		return exec.Command("bash", "-c", "sleep 30")
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return sess, mgr
}

func TestPlanEditor_RendersPlanFromDisk(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	const plan = "# Goal\nDo X\n\n## Tasks\n- [ ] step 1\n- [ ] step 2\n"
	if err := sess.WritePlan(plan); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}

	editor := newPlanEditor(sess, "", 80, 30)
	body := testutil.StripANSI(editor.View())
	if !strings.Contains(body, "Do X") || !strings.Contains(body, "step 1") {
		t.Errorf("editor view missing plan content:\n%s", body)
	}
}

func TestPlanEditor_EditModeSavesOnCtrlS(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	if err := sess.WritePlan("original\n"); err != nil {
		t.Fatal(err)
	}
	editor := newPlanEditor(sess, "", 80, 30)

	// `i` enters edit mode.
	editor, cmd := editor.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	_ = cmd
	if editor.mode != planEditorModeEdit {
		t.Fatalf("mode after i = %v, want edit", editor.mode)
	}

	// Replace textarea content directly (simulate user typing).
	editor.doc.textarea.SetValue("rewritten\n")
	editor.dirty = true

	editor, cmd = editor.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl, Text: "ctrl+s"})
	if cmd == nil {
		t.Fatal("expected planEditorSavedMsg cmd from ctrl+s")
	}
	got := cmd()
	saved, ok := got.(planEditorSavedMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want planEditorSavedMsg", got)
	}
	if saved.sessionID != sess.ID {
		t.Errorf("sessionID = %q, want %q", saved.sessionID, sess.ID)
	}
	on, err := sess.ReadPlan()
	if err != nil {
		t.Fatal(err)
	}
	if on != "rewritten\n" {
		t.Errorf("plan on disk = %q, want rewritten", on)
	}
	if editor.dirty {
		t.Error("dirty should be cleared after save")
	}
}

func TestPlanEditor_EscFromEditPreservesEdits(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	_ = sess.WritePlan("orig\n")
	editor := newPlanEditor(sess, "", 80, 30)
	editor, _ = editor.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	editor.doc.textarea.SetValue("typed but not saved\n")
	editor.dirty = true

	editor, _ = editor.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if editor.mode != planEditorModeScroll {
		t.Errorf("mode = %v, want scroll after esc", editor.mode)
	}
	if editor.doc.Value() != "typed but not saved\n" {
		t.Errorf("textarea cleared on esc; want preserved edits")
	}
	if !editor.dirty {
		t.Error("dirty should remain true after esc — edits unsaved")
	}
}

func TestPlanEditor_ApproveEmptyPlanShowsError(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	_ = sess.WritePlan("   \n\t\n")
	editor := newPlanEditor(sess, "", 80, 30)

	editor, cmd := editor.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if cmd != nil {
		t.Fatal("expected no cmd from approve on empty plan")
	}
	if !strings.Contains(editor.errMsg, "empty") {
		t.Errorf("errMsg = %q, want 'empty'", editor.errMsg)
	}
}

func TestPlanEditor_ApprovePersistsAndEmits(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	_ = sess.WritePlan("# initial\n")
	editor := newPlanEditor(sess, "", 80, 30)

	// User edits, doesn't ctrl+s, esc back, then approves.
	editor, _ = editor.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	editor.doc.textarea.SetValue("# revised\n- [ ] thing\n")
	editor.dirty = true
	editor, _ = editor.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	editor, cmd := editor.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if cmd == nil {
		t.Fatal("approve produced no cmd")
	}
	got := cmd()
	approve, ok := got.(planEditorApproveMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want planEditorApproveMsg", got)
	}
	if approve.sessionID != sess.ID {
		t.Errorf("sessionID mismatch")
	}
	on, _ := sess.ReadPlan()
	if !strings.Contains(on, "revised") {
		t.Errorf("approve did not flush textarea to disk: got %q", on)
	}
	if editor.dirty {
		t.Error("dirty should be cleared after approve auto-save")
	}
}

func TestPlanEditor_AbandonEmitsMessage(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	_ = sess.WritePlan("anything\n")
	editor := newPlanEditor(sess, "", 80, 30)

	_, cmd := editor.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd == nil {
		t.Fatal("abandon produced no cmd")
	}
	got := cmd()
	if _, ok := got.(planEditorAbandonMsg); !ok {
		t.Fatalf("cmd produced %T, want planEditorAbandonMsg", got)
	}
}

func TestPlanEditor_DraftingLocksEditAndApprove(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	editor := newPlanEditor(sess, "", 80, 30)
	editor.SetDrafting(true)

	editor, cmd := editor.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	if cmd != nil {
		t.Error("i should be a no-op while drafting")
	}
	editor, cmd = editor.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if cmd != nil {
		t.Error("a should be a no-op while drafting")
	}
	// esc still works to back out.
	editor, cmd = editor.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("esc should emit close even while drafting")
	}
	got := cmd()
	if _, ok := got.(planEditorCloseMsg); !ok {
		t.Errorf("got %T, want planEditorCloseMsg", got)
	}
}

func TestPlanEditor_ReviseInputEmitsCritique(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	_ = sess.WritePlan("# plan\n")
	editor := newPlanEditor(sess, "", 80, 30)

	editor, _ = editor.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if editor.mode != planEditorModeReviseInput {
		t.Fatalf("mode = %v, want reviseInput", editor.mode)
	}
	editor.reviseInput.SetValue("split task 2 into ui and persistence")

	editor, cmd := editor.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	if cmd == nil {
		t.Fatal("expected revise cmd")
	}
	got := cmd()
	revise, ok := got.(planEditorReviseMsg)
	if !ok {
		t.Fatalf("got %T, want planEditorReviseMsg", got)
	}
	if !strings.Contains(revise.critique, "split task 2") {
		t.Errorf("critique = %q", revise.critique)
	}
	if editor.mode != planEditorModeScroll {
		t.Errorf("mode after submit = %v, want scroll", editor.mode)
	}
}

// TestPlanEditor_ScrollAndEditModeUseSameWidth pins that contentWidth() ==
// textarea.Width() on all terminal widths — both narrow and wide terminals now
// produce matching widths so the visible wrap column is consistent across mode
// switches.
func TestPlanEditor_ScrollAndEditModeUseSameWidth(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	_ = sess.WritePlan("# H\nshort\n")

	t.Run("narrow", func(t *testing.T) {
		// textareaWidth(70) = 68, which is below docEditorMaxMeasure (72).
		// contentWidth() == 68 == textarea.Width().
		editor := newPlanEditor(sess, "", 70, 30)
		if got, want := editor.doc.ContentWidth(), editor.doc.textarea.Width(); got != want {
			t.Errorf("contentWidth=%d, textarea.Width()=%d; on narrow terminals both modes must wrap at the same column", got, want)
		}
	})

	t.Run("wide", func(t *testing.T) {
		// textareaWidth(120) = 118, but ContentWidth() is capped at docEditorMaxMeasure (72).
		// textarea.Width() must also be 72 so wrap columns match.
		editor := newPlanEditor(sess, "", 120, 30)
		if got, want := editor.doc.ContentWidth(), editor.doc.textarea.Width(); got != want {
			t.Errorf("contentWidth=%d, textarea.Width()=%d; on wide terminals both must equal docEditorMaxMeasure (%d)", got, want, docEditorMaxMeasure)
		}
		if editor.doc.ContentWidth() != docEditorMaxMeasure {
			t.Errorf("ContentWidth()=%d, want docEditorMaxMeasure=%d on wide terminal", editor.doc.ContentWidth(), docEditorMaxMeasure)
		}
	})
}

// TestPlanEditor_EditModeCenteredOnWideTerminal asserts that on wide terminals
// the textarea is capped at contentWidth (72) and the edit-mode View output
// prepends displayLeftPad() spaces, matching the scroll-mode centering.
func TestPlanEditor_EditModeCenteredOnWideTerminal(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	_ = sess.WritePlan("# Goal\nsome plan content here\n")

	editor := newPlanEditor(sess, "", 120, 30)

	// textarea.Width() must be capped at contentWidth() (72), not textareaWidth(120)=118.
	if got, want := editor.doc.textarea.Width(), editor.doc.ContentWidth(); got != want {
		t.Errorf("textarea.Width()=%d, want ContentWidth()=%d; edit mode must cap at docEditorMaxMeasure on wide terminals",
			got, want)
	}

	leftPad := editor.doc.DisplayLeftPad()
	if leftPad == 0 {
		t.Fatal("displayLeftPad() == 0 at width 120; test precondition failed")
	}

	editor, _ = editor.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	if editor.mode != planEditorModeEdit {
		t.Fatalf("mode = %v after 'i', want planEditorModeEdit", editor.mode)
	}

	view := testutil.StripANSI(editor.View())
	viewLines := strings.Split(view, "\n")
	// viewLines[0]=header, viewLines[1]=divider, viewLines[2]=first textarea line.
	if len(viewLines) < 3 {
		t.Fatalf("view has only %d lines, expected header+divider+content", len(viewLines))
	}
	prefix := strings.Repeat(" ", leftPad)
	if !strings.HasPrefix(viewLines[2], prefix) {
		t.Errorf("first edit-mode content line does not start with %d-space left pad:\n%q", leftPad, viewLines[2])
	}
}

// TestPlanEditor_DisplayLineCountAgreesWithRenderer asserts that the editor's
// scroll-mode display lines exactly match a direct mdrender call on the same
// content+width when no sections are folded. Folding intentionally elides
// content lines; this test pins the wrap-parity invariant for the fully-
// expanded case so the i/esc mode toggle guarantees "scroll position preserved".
func TestPlanEditor_DisplayLineCountAgreesWithRenderer(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	sess, _ := newEditorTestSession(t)
	const plan = "# Heading 1\n\n" +
		"A paragraph with **bold**, *italic*, and `code` that may need to wrap depending on the width.\n\n" +
		"## Heading 2\n\n" +
		"- bullet one\n- bullet two with a longer body to encourage wrap when the width is narrow\n- bullet three\n\n" +
		"```go\nfunc Example() error { return nil }\n```\n\n" +
		"> a final blockquote\n"
	if err := sess.WritePlan(plan); err != nil {
		t.Fatal(err)
	}
	editor := newPlanEditor(sess, "", 60, 30)

	// Expand all folds before comparing: folding intentionally elides content
	// lines, so the line-count invariant only holds when nothing is folded.
	for k := range editor.folds {
		editor.folds[k] = false
	}
	editor.invalidateDisplayCache()

	scrollLines := editor.displayLines()
	directLines := mdrender.New("monokai").RenderLines(editor.doc.Value(), editor.doc.ContentWidth())
	if len(scrollLines) != len(directLines) {
		t.Errorf("editor.displayLines()=%d vs direct mdrender.RenderLines=%d — scroll and edit modes will desync",
			len(scrollLines), len(directLines))
	}
}

// TestPlanEditor_ParsesCanonicalSectionsAndAppliesDefaults verifies that
// parsePlanSections finds all eight canonical H1/H2 headings and that
// defaultSectionFolded gives the right initial fold state: Goal (H1), Spec, and
// Tasks are expanded; every other H2 is collapsed.
func TestPlanEditor_ParsesCanonicalSectionsAndAppliesDefaults(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	const plan = "# Goal\nGoal body\n\n## Spec\nspec\n\n## Context\nctx\n\n## Reuse\nreuse\n\n## Risks\nrisks\n\n## Tasks\n- [ ] t1\n\n## Verification\nverify\n\n## Not in scope\nnot\n"
	if err := sess.WritePlan(plan); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	editor := newPlanEditor(sess, "", 80, 30)
	if len(editor.sections) != 8 {
		t.Fatalf("sections count = %d, want 8", len(editor.sections))
	}
	if editor.sections[0].heading != "Goal" || editor.sections[0].level != 1 {
		t.Errorf("sections[0] = {%q, level %d}, want Goal level 1", editor.sections[0].heading, editor.sections[0].level)
	}
	if editor.sections[1].heading != "Spec" {
		t.Errorf("sections[1].heading = %q, want Spec", editor.sections[1].heading)
	}
	if editor.sections[0].headingLine != 0 {
		t.Errorf("sections[0].headingLine = %d, want 0", editor.sections[0].headingLine)
	}
	expanded := []string{"Goal", "Spec", "Tasks"}
	for _, name := range expanded {
		if editor.folds[name] {
			t.Errorf("folds[%q] = true, want false (expanded by default)", name)
		}
	}
	collapsed := []string{"Context", "Reuse", "Risks", "Verification", "Not in scope"}
	for _, name := range collapsed {
		if !editor.folds[name] {
			t.Errorf("folds[%q] = false, want true (collapsed by default)", name)
		}
	}
}

// TestPlanEditor_FoldedSectionsCollapseToMarker verifies that a collapsed
// section renders as a single line containing the ▶ glyph, the heading, and a
// "N lines" suffix, while its content is hidden; expanded sections show their
// content and use the ▼ glyph.
func TestPlanEditor_FoldedSectionsCollapseToMarker(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	sess, _ := newEditorTestSession(t)
	const plan = "# Goal\nGoal body\n\n## Context\nctx line 1\nctx line 2\nctx line 3\n"
	if err := sess.WritePlan(plan); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	editor := newPlanEditor(sess, "", 80, 30)

	rendered := testutil.StripANSI(strings.Join(editor.displayLines(), "\n"))

	// Goal (H1) is expanded — body should be visible and ▼ present.
	if !strings.Contains(rendered, "Goal body") {
		t.Errorf("Goal body missing from rendered output:\n%s", rendered)
	}
	if !strings.Contains(rendered, "▼") {
		t.Errorf("expand glyph ▼ missing from rendered output:\n%s", rendered)
	}
	// Context (H2) is collapsed by default — heading + lines count visible, content hidden.
	if !strings.Contains(rendered, "▶") {
		t.Errorf("collapse glyph ▶ missing from rendered output:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Context") {
		t.Errorf("Context heading missing from rendered output:\n%s", rendered)
	}
	if !strings.Contains(rendered, "3 lines") {
		t.Errorf("line-count suffix '3 lines' missing:\n%s", rendered)
	}
	if strings.Contains(rendered, "ctx line 1") {
		t.Errorf("collapsed section content 'ctx line 1' should be hidden:\n%s", rendered)
	}
}

// TestPlanEditor_FoldedSectionMidPlanCountsBlankLine verifies that a blank line
// before the next heading is counted as a hidden line (it's real content, not a
// strings.Split artifact). The trailing-blank strip applies only to the last
// section whose nextLine == len(srcLines).
func TestPlanEditor_FoldedSectionMidPlanCountsBlankLine(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	sess, _ := newEditorTestSession(t)
	// Context has 2 content lines + 1 blank before Tasks — should show 3 lines.
	const plan = "# Goal\n\n## Context\nctx 1\nctx 2\n\n## Tasks\ntask\n"
	if err := sess.WritePlan(plan); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	editor := newPlanEditor(sess, "", 80, 30)
	rendered := testutil.StripANSI(strings.Join(editor.displayLines(), "\n"))
	if !strings.Contains(rendered, "3 lines") {
		t.Errorf("mid-plan collapsed section should show '3 lines' (including blank before next heading):\n%s", rendered)
	}
}

// TestPlanEditor_TabTogglesFoldAtViewportTop verifies that pressing tab in
// scroll mode toggles the fold of the section at sectionCursor.
func TestPlanEditor_TabTogglesFoldAtViewportTop(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	const plan = "# Goal\nGoal body\n\n## Spec\nspec body\n\n## Context\nctx body\n"
	if err := sess.WritePlan(plan); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	editor := newPlanEditor(sess, "", 80, 30)

	// Initially: Goal expanded, Spec expanded, Context collapsed.
	if editor.folds["Goal"] {
		t.Fatal("Goal should be expanded by default")
	}
	if editor.folds["Spec"] {
		t.Fatal("Spec should be expanded by default")
	}
	if !editor.folds["Context"] {
		t.Fatal("Context should be collapsed by default")
	}

	// Cursor at 0 (Goal); pressing tab collapses it.
	editor, _ = editor.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if !editor.folds["Goal"] {
		t.Errorf("after tab at Goal (cursor=0), folds[Goal] = false, want true (collapsed)")
	}

	// Move cursor to Spec (index 1), then tab toggles Spec.
	editor.sectionCursor = 1
	editor, _ = editor.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if !editor.folds["Spec"] {
		t.Errorf("after tab at Spec (cursor=1), folds[Spec] = false, want true (collapsed)")
	}
}

// TestPlanEditor_BracketsJumpBetweenSections verifies that ] and [ step
// sectionCursor to the next and previous sections respectively, clamping at
// the ends.
func TestPlanEditor_BracketsJumpBetweenSections(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	const plan = "# Goal\nGoal body\n\n## Spec\nspec body\n\n## Tasks\ntask body\n"
	if err := sess.WritePlan(plan); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	editor := newPlanEditor(sess, "", 80, 8)
	if len(editor.sections) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(editor.sections))
	}

	// ] from 0 → cursor 1 (Spec).
	editor, _ = editor.Update(tea.KeyPressMsg{Code: ']', Text: "]"})
	if editor.sectionCursor != 1 {
		t.Errorf("] from 0: sectionCursor = %d, want 1 (Spec)", editor.sectionCursor)
	}

	// ] again → cursor 2 (Tasks).
	editor, _ = editor.Update(tea.KeyPressMsg{Code: ']', Text: "]"})
	if editor.sectionCursor != 2 {
		t.Errorf("] from Spec: sectionCursor = %d, want 2 (Tasks)", editor.sectionCursor)
	}

	// ] at last section → still 2 (clamped).
	editor, _ = editor.Update(tea.KeyPressMsg{Code: ']', Text: "]"})
	if editor.sectionCursor != 2 {
		t.Errorf("] at last section: sectionCursor = %d, want 2 (clamp)", editor.sectionCursor)
	}

	// [ twice → back to 0 (Goal).
	editor, _ = editor.Update(tea.KeyPressMsg{Code: '[', Text: "["})
	if editor.sectionCursor != 1 {
		t.Errorf("[ from Tasks: sectionCursor = %d, want 1 (Spec)", editor.sectionCursor)
	}
	editor, _ = editor.Update(tea.KeyPressMsg{Code: '[', Text: "["})
	if editor.sectionCursor != 0 {
		t.Errorf("[ from Spec: sectionCursor = %d, want 0 (Goal)", editor.sectionCursor)
	}
}

// TestPlanEditor_ShiftZTogglesAllFolds verifies that pressing Z collapses every
// section when any is expanded, and expands every section when all are
// collapsed.
func TestPlanEditor_ShiftZTogglesAllFolds(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	const plan = "# Goal\nGoal body\n\n## Spec\nspec\n\n## Context\nctx\n\n## Reuse\nreuse\n\n## Risks\nrisks\n\n## Tasks\n- [ ] t1\n\n## Verification\nverify\n\n## Not in scope\nnot\n"
	if err := sess.WritePlan(plan); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	editor := newPlanEditor(sess, "", 80, 30)

	// Default: Goal/Spec/Tasks expanded, others collapsed. Press Z → all collapsed.
	editor, _ = editor.Update(tea.KeyPressMsg{Code: 'Z', Text: "Z"})
	for _, s := range editor.sections {
		if !editor.folds[s.heading] {
			t.Errorf("after first Z, folds[%q] = false, want true (all collapsed)", s.heading)
		}
	}

	// All collapsed. Press Z again → all expanded.
	editor, _ = editor.Update(tea.KeyPressMsg{Code: 'Z', Text: "Z"})
	for _, s := range editor.sections {
		if editor.folds[s.heading] {
			t.Errorf("after second Z, folds[%q] = true, want false (all expanded)", s.heading)
		}
	}
}

// TestPlanEditor_ReloadPreservesFoldsByHeading verifies that after Reload(),
// existing fold state is preserved for headings that survive the edit, and
// new headings get their default fold policy.
func TestPlanEditor_ReloadPreservesFoldsByHeading(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	const plan1 = "# Goal\n## Spec\n## Tasks\n"
	if err := sess.WritePlan(plan1); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	editor := newPlanEditor(sess, "", 80, 30)

	// Manually collapse Spec (user action).
	editor.folds["Spec"] = true

	// Write a new plan with an extra section.
	const plan2 = "# Goal\n## Spec\n## Tasks\n## Extra\n"
	if err := sess.WritePlan(plan2); err != nil {
		t.Fatalf("WritePlan plan2: %v", err)
	}
	editor.Reload()

	if !editor.folds["Spec"] {
		t.Error("folds[Spec] should remain true after Reload (user-collapsed)")
	}
	if editor.folds["Goal"] {
		t.Error("folds[Goal] should remain false after Reload (H1 default)")
	}
	if editor.folds["Tasks"] {
		t.Error("folds[Tasks] should remain false after Reload (default expanded)")
	}
	if !editor.folds["Extra"] {
		t.Error("folds[Extra] should be true after Reload (new H2 default-collapsed)")
	}
}

// TestPlanEditor_ScrollFooterIncludesFoldHints verifies that the footer row in
// scroll mode mentions the fold-navigation key bindings.
func TestPlanEditor_ScrollFooterIncludesFoldHints(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	if err := sess.WritePlan("# Goal\nsome content\n"); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	editor := newPlanEditor(sess, "", 80, 30)
	view := testutil.StripANSI(editor.View())
	for _, want := range []string{"j/k", "navigate", "tab", "fold", "Z", "toggle all"} {
		if !strings.Contains(view, want) {
			t.Errorf("footer hint %q missing from view:\n%s", want, view)
		}
	}
	// Brackets are now just aliases for j/k; they should not appear in the footer.
	for _, notWant := range []string{"[ ]", "sections"} {
		if strings.Contains(view, notWant) {
			t.Errorf("footer hint %q should no longer appear in view:\n%s", notWant, view)
		}
	}
}

// TestPlanEditor_ScrollModeStylesHeadings asserts that scroll-mode rendering
// actually emits ANSI styling for known markdown constructs. Without this,
// a regression that silently nil-checks the renderer or short-circuits
// before styling would compile and pass other tests but show plain output.
func TestPlanEditor_ScrollModeStylesHeadings(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	sess, _ := newEditorTestSession(t)
	_ = sess.WritePlan("# Hello world\n")
	editor := newPlanEditor(sess, "", 80, 30)

	view := editor.View()
	stripped := testutil.StripANSI(view)
	if !strings.Contains(stripped, "# Hello world") {
		t.Errorf("heading text missing from scroll view:\n%s", stripped)
	}
	if !strings.Contains(view, "\x1b[") {
		t.Errorf("expected ANSI styling in scroll view, got plain output:\n%s", view)
	}
}

// --- R-key retry tests ---

func TestPlanEditor_R_RetryEmitsMsgWhenDraftError(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	sess.SetDraftError(errors.New("boom"))
	sess.SetOriginalPrompt("do the thing")
	editor := newPlanEditor(sess, "testrepo", 80, 30)

	_, cmd := editor.Update(tea.KeyPressMsg{Code: 'R', Text: "R"})
	if cmd == nil {
		t.Fatal("R with draft error + original prompt should emit a cmd")
	}
	got := cmd()
	msg, ok := got.(planEditorRetryMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want planEditorRetryMsg", got)
	}
	if msg.sessionID != sess.ID {
		t.Errorf("sessionID = %q, want %q", msg.sessionID, sess.ID)
	}
	if msg.repoPath != "testrepo" {
		t.Errorf("repoPath = %q, want testrepo", msg.repoPath)
	}
}

func TestPlanEditor_R_NoopWhenNoDraftError(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	sess.SetOriginalPrompt("do the thing")
	// No draft error set.
	editor := newPlanEditor(sess, "testrepo", 80, 30)

	_, cmd := editor.Update(tea.KeyPressMsg{Code: 'R', Text: "R"})
	if cmd != nil {
		t.Errorf("R with no draft error should be a no-op, got cmd that returns %T", cmd())
	}
}

func TestPlanEditor_R_NoopWhenNoOriginalPrompt(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	sess.SetDraftError(errors.New("boom"))
	// No original prompt set.
	editor := newPlanEditor(sess, "testrepo", 80, 30)

	_, cmd := editor.Update(tea.KeyPressMsg{Code: 'R', Text: "R"})
	if cmd != nil {
		t.Errorf("R with no original prompt should be a no-op, got cmd that returns %T", cmd())
	}
}

func TestPlanEditor_JK_ScrollLines(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	const plan = "# Goal\nDo X\n\n## Spec\nspec body\n\n## Context\nctx\n"
	if err := sess.WritePlan(plan); err != nil {
		t.Fatal(err)
	}
	editor := newPlanEditor(sess, "", 80, 20)
	if editor.sectionCursor != 0 {
		t.Fatalf("sectionCursor = %d, want 0 initially", editor.sectionCursor)
	}
	editor, _ = editor.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if editor.sectionCursor != 1 {
		t.Errorf("after j, sectionCursor = %d, want 1", editor.sectionCursor)
	}
	editor, _ = editor.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if editor.sectionCursor != 0 {
		t.Errorf("after k, sectionCursor = %d, want 0", editor.sectionCursor)
	}
	// k at first section → stays 0 (clamped).
	editor, _ = editor.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if editor.sectionCursor != 0 {
		t.Errorf("k at first section: sectionCursor = %d, want 0", editor.sectionCursor)
	}
	// down/up are aliases for j/k.
	editor, _ = editor.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if editor.sectionCursor != 1 {
		t.Errorf("after down, sectionCursor = %d, want 1", editor.sectionCursor)
	}
	editor, _ = editor.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if editor.sectionCursor != 0 {
		t.Errorf("after up, sectionCursor = %d, want 0", editor.sectionCursor)
	}
}

func TestPlanEditor_CtrlD_CtrlU_HalfPage(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	var sb strings.Builder
	sb.WriteString("# Goal\n")
	for i := 0; i < 60; i++ {
		sb.WriteString("line\n")
	}
	if err := sess.WritePlan(sb.String()); err != nil {
		t.Fatal(err)
	}
	editor := newPlanEditor(sess, "", 80, 20)
	editor, _ = editor.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if editor.doc.scrollOff == 0 {
		t.Errorf("ctrl+d did not scroll")
	}
	pre := editor.doc.scrollOff
	editor, _ = editor.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if editor.doc.scrollOff >= pre {
		t.Errorf("ctrl+u did not reverse scroll: pre=%d post=%d", pre, editor.doc.scrollOff)
	}
}

func TestPlanEditor_GHomeAndShiftGEnd_Jump(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	var sb strings.Builder
	sb.WriteString("# Goal\n")
	for i := 0; i < 60; i++ {
		sb.WriteString("line\n")
	}
	if err := sess.WritePlan(sb.String()); err != nil {
		t.Fatal(err)
	}
	editor := newPlanEditor(sess, "", 80, 20)
	// G jumps to bottom.
	editor, _ = editor.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	if editor.doc.scrollOff == 0 {
		t.Errorf("G did not move scroll")
	}
	// g jumps back to top.
	editor, _ = editor.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	if editor.doc.scrollOff != 0 {
		t.Errorf("g did not jump to top, got %d", editor.doc.scrollOff)
	}
	// home/end aliases.
	editor, _ = editor.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if editor.doc.scrollOff == 0 {
		t.Errorf("end did not move scroll")
	}
	editor, _ = editor.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	if editor.doc.scrollOff != 0 {
		t.Errorf("home did not jump to top, got %d", editor.doc.scrollOff)
	}
}

func TestPlanEditor_U_NothingToUndo_ShowsInlineError(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	if err := sess.WritePlan("# Goal\n"); err != nil {
		t.Fatal(err)
	}
	editor := newPlanEditor(sess, "", 80, 20)
	editor, _ = editor.Update(tea.KeyPressMsg{Code: 'u', Text: "u"})
	if editor.errMsg == "" {
		t.Error("u with no prev plan should set an inline error message")
	}
	if !strings.Contains(editor.errMsg, "nothing to undo") {
		t.Errorf("errMsg = %q, want it to contain 'nothing to undo'", editor.errMsg)
	}
}

// TestPlanEditor_U_RestoresPrevPlanInline verifies the editor owns single-step
// undo: pressing `u` restores plan.prev.md → plan.md and reloads in-place,
// without routing a planEditorRestoreMsg through the App.
func TestPlanEditor_U_RestoresPrevPlanInline(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	if err := sess.WritePlan("# Goal\nNew plan\n"); err != nil {
		t.Fatal(err)
	}
	// Seed a previous-plan snapshot so `u` has something to restore.
	if err := os.WriteFile(sess.PrevPlanPath(), []byte("# Goal\nOld plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !sess.HasPrevPlan() {
		t.Fatal("test prereq: HasPrevPlan should be true after seeding snapshot")
	}
	editor := newPlanEditor(sess, "", 80, 20)
	editor, cmd := editor.Update(tea.KeyPressMsg{Code: 'u', Text: "u"})
	if cmd != nil {
		t.Error("restore should be handled inline (no App round-trip), got a cmd")
	}
	if editor.errMsg != "" {
		t.Errorf("unexpected errMsg after restore: %q", editor.errMsg)
	}
	if !strings.Contains(editor.plan, "Old plan") {
		t.Errorf("editor.plan after restore = %q, want it to contain 'Old plan'", editor.plan)
	}
	if sess.HasPrevPlan() {
		t.Error("snapshot should be consumed after a single-step undo")
	}
}

func TestPlanEditor_DraftingState_BlocksAllExceptEscAndQ(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	if err := sess.WritePlan("# Goal\nDo X\n"); err != nil {
		t.Fatal(err)
	}
	editor := newPlanEditor(sess, "", 80, 20)
	editor.drafting = true
	editor.doc.scrollOff = 0

	// Try a bunch of keys; they should all be no-ops.
	for _, k := range []tea.KeyPressMsg{
		{Code: 'j', Text: "j"},
		{Code: 'k', Text: "k"},
		{Code: 'G', Text: "G"},
		{Code: 'i', Text: "i"},
		{Code: 'a', Text: "a"},
		{Code: 'r', Text: "r"},
		{Code: 'R', Text: "R"},
		{Code: 'u', Text: "u"},
		{Code: tea.KeyTab},
	} {
		editor, _ = editor.Update(k)
	}
	if editor.doc.scrollOff != 0 {
		t.Errorf("scroll changed during drafting, got %d", editor.doc.scrollOff)
	}
	if editor.sectionCursor != 0 {
		t.Errorf("sectionCursor changed during drafting, got %d", editor.sectionCursor)
	}
	if editor.mode != planEditorModeScroll {
		t.Errorf("mode changed during drafting to %v", editor.mode)
	}
	if editor.errMsg != "" {
		t.Errorf("errMsg set during drafting: %q", editor.errMsg)
	}
}

// TestPlanEditor_Revise_PersistsDirtyEditsBeforeEmitting verifies the editor
// saves unsaved textarea edits to disk before emitting planEditorReviseMsg, so
// the drafter revises what the user sees. This save moved out of the App's
// revise handler (now pure manager-routing) and into the editor.
func TestPlanEditor_Revise_PersistsDirtyEditsBeforeEmitting(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	if err := sess.WritePlan("# Goal\nOriginal\n"); err != nil {
		t.Fatal(err)
	}
	editor := newPlanEditor(sess, "", 80, 30)
	// Simulate an unsaved edit left in the textarea before revising.
	editor.doc.SetValue("# Goal\nEdited but unsaved\n")
	editor.dirty = true

	editor, _ = editor.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	editor.reviseInput.SetValue("make it better")
	editor, cmd := editor.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	if cmd == nil {
		t.Fatal("expected revise cmd")
	}
	if _, ok := cmd().(planEditorReviseMsg); !ok {
		t.Fatal("expected planEditorReviseMsg from dirty revise submit")
	}
	if editor.dirty {
		t.Error("dirty should be cleared after save-before-revise")
	}
	onDisk, err := sess.ReadPlan()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(onDisk, "Edited but unsaved") {
		t.Errorf("plan on disk = %q, want the unsaved edit persisted before revise", onDisk)
	}
}

func TestPlanEditor_ReviseInput_EnterOnEmpty_ShowsError(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	if err := sess.WritePlan("# Goal\n"); err != nil {
		t.Fatal(err)
	}
	editor := newPlanEditor(sess, "", 80, 20)
	editor, _ = editor.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if editor.mode != planEditorModeReviseInput {
		t.Fatalf("test prereq: mode = %v, want reviseInput", editor.mode)
	}
	editor.reviseInput.SetValue("   ")
	editor, cmd := editor.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		if _, bad := cmd().(planEditorReviseMsg); bad {
			t.Error("empty critique should NOT emit reviseMsg")
		}
	}
	if editor.errMsg == "" {
		t.Error("empty critique should set an inline error")
	}
}

func TestPlanEditor_ReviseInput_EscCancels(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	if err := sess.WritePlan("# Goal\n"); err != nil {
		t.Fatal(err)
	}
	editor := newPlanEditor(sess, "", 80, 20)
	editor, _ = editor.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	editor.reviseInput.SetValue("partial input")
	editor, cmd := editor.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd != nil {
		if _, bad := cmd().(planEditorReviseMsg); bad {
			t.Error("esc in revise mode must not emit reviseMsg")
		}
	}
	if editor.mode != planEditorModeScroll {
		t.Errorf("esc should return to scroll mode, got %v", editor.mode)
	}
}

func TestPlanEditor_ScrollMode_UnknownKey_NoOp(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	if err := sess.WritePlan("# Goal\nDo X\n"); err != nil {
		t.Fatal(err)
	}
	editor := newPlanEditor(sess, "", 80, 20)
	before := struct {
		scroll int
		mode   planEditorMode
		err    string
		dirty  bool
	}{editor.doc.scrollOff, editor.mode, editor.errMsg, editor.dirty}
	editor, cmd := editor.Update(tea.KeyPressMsg{Code: 'z', Text: "z"})
	if cmd != nil {
		t.Errorf("unknown key produced cmd %T, want nil", cmd())
	}
	after := struct {
		scroll int
		mode   planEditorMode
		err    string
		dirty  bool
	}{editor.doc.scrollOff, editor.mode, editor.errMsg, editor.dirty}
	if before != after {
		t.Errorf("unknown key changed state: before=%+v after=%+v", before, after)
	}
}

func TestPlanEditor_R_NoopWhenDrafting(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	sess.SetDraftError(errors.New("boom"))
	sess.SetOriginalPrompt("do the thing")
	editor := newPlanEditor(sess, "testrepo", 80, 30)
	editor.SetDrafting(true)

	editor, cmd := editor.Update(tea.KeyPressMsg{Code: 'R', Text: "R"})
	if cmd != nil {
		t.Errorf("R while drafting should be a no-op, got cmd that returns %T", cmd())
	}
}

// TestPlanEditor_Brackets_MoveCursor verifies that ] and [ step sectionCursor
// forward and backward through sections respectively.
func TestPlanEditor_Brackets_MoveCursor(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	const plan = "# Goal\nbody\n\n## Spec\nspec\n\n## Context\nctx\n"
	if err := sess.WritePlan(plan); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	editor := newPlanEditor(sess, "", 80, 30)
	if len(editor.sections) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(editor.sections))
	}

	// ] twice: 0→1→2.
	editor, _ = editor.Update(tea.KeyPressMsg{Code: ']', Text: "]"})
	if editor.sectionCursor != 1 {
		t.Errorf("] once: sectionCursor=%d, want 1", editor.sectionCursor)
	}
	editor, _ = editor.Update(tea.KeyPressMsg{Code: ']', Text: "]"})
	if editor.sectionCursor != 2 {
		t.Errorf("] twice: sectionCursor=%d, want 2", editor.sectionCursor)
	}

	// ] at last section → still 2 (clamped).
	editor, _ = editor.Update(tea.KeyPressMsg{Code: ']', Text: "]"})
	if editor.sectionCursor != 2 {
		t.Errorf("] at last: sectionCursor=%d, want 2", editor.sectionCursor)
	}

	// [ once: 2→1.
	editor, _ = editor.Update(tea.KeyPressMsg{Code: '[', Text: "["})
	if editor.sectionCursor != 1 {
		t.Errorf("[ once: sectionCursor=%d, want 1", editor.sectionCursor)
	}
}

// TestPlanEditor_TabFoldsCursorSection verifies that tab toggles the fold of
// the section at sectionCursor, not the section at the viewport top.
func TestPlanEditor_TabFoldsCursorSection(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	const plan = "# Goal\nbody\n\n## Spec\nspec\n\n## Context\nctx\n"
	if err := sess.WritePlan(plan); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	editor := newPlanEditor(sess, "", 80, 30)
	// Defaults: Goal expanded, Spec expanded, Context collapsed (index 2).
	if editor.folds["Goal"] {
		t.Fatal("Goal should be expanded initially")
	}
	if editor.folds["Spec"] {
		t.Fatal("Spec should be expanded initially")
	}
	if !editor.folds["Context"] {
		t.Fatal("Context should be collapsed initially")
	}

	// Move cursor to Context (index 2), press tab → Context expands.
	editor.sectionCursor = 2
	editor, _ = editor.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if editor.folds["Context"] {
		t.Error("after tab at Context (cursor=2), Context should be expanded")
	}

	// Move cursor back to Goal (index 0), press tab → Goal collapses.
	editor.sectionCursor = 0
	editor, _ = editor.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if !editor.folds["Goal"] {
		t.Error("after tab at Goal (cursor=0), Goal should be collapsed")
	}
}

// TestPlanEditor_JK_MoveSectionCursor verifies j/k/down/up move sectionCursor
// through sections and auto-scroll so the selected heading is in the viewport.
func TestPlanEditor_JK_MoveSectionCursor(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	const plan = "# Goal\nbody\n\n## Spec\nspec\n\n## Context\nctx\n\n## Tasks\ntasks\n"
	if err := sess.WritePlan(plan); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	// height=8 → bodyHeight=3, small enough to force scrolling.
	editor := newPlanEditor(sess, "", 80, 8)
	// Expand all so sections have real display lines between them.
	for k := range editor.folds {
		editor.folds[k] = false
	}
	editor.invalidateDisplayCache()
	if len(editor.sections) != 4 {
		t.Fatalf("expected 4 sections, got %d", len(editor.sections))
	}

	// j three times: 0→1→2→3.
	for want := 1; want <= 3; want++ {
		editor, _ = editor.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		if editor.sectionCursor != want {
			t.Errorf("after j: sectionCursor=%d, want %d", editor.sectionCursor, want)
		}
	}

	// j at last section → still 3 (clamped).
	editor, _ = editor.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if editor.sectionCursor != 3 {
		t.Errorf("j at last section: sectionCursor=%d, want 3", editor.sectionCursor)
	}

	// k twice: 3→2→1.
	for want := 2; want >= 1; want-- {
		editor, _ = editor.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
		if editor.sectionCursor != want {
			t.Errorf("after k: sectionCursor=%d, want %d", editor.sectionCursor, want)
		}
	}

	// down/up are aliases.
	editor, _ = editor.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if editor.sectionCursor != 2 {
		t.Errorf("after down: sectionCursor=%d, want 2", editor.sectionCursor)
	}
	editor, _ = editor.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if editor.sectionCursor != 1 {
		t.Errorf("after up: sectionCursor=%d, want 1", editor.sectionCursor)
	}

	// Verify auto-scroll: sectionDisplayStart[sectionCursor] must be within
	// [scrollOff, scrollOff+bodyHeight).
	editor.displayLines()
	cursor := editor.sectionCursor
	headingLine := editor.sectionDisplayStart[cursor]
	body := editor.doc.BodyHeight(5)
	if headingLine < editor.doc.scrollOff || headingLine >= editor.doc.scrollOff+body {
		t.Errorf("heading line %d not in viewport [%d, %d)",
			headingLine, editor.doc.scrollOff, editor.doc.scrollOff+body)
	}
}

// TestPlanEditor_CursorHighlightsSelectedSection verifies that displayLines
// renders the cursor-selected section heading glyph in ColorSecondary (cyan)
// while the other headings use the muted gray of StyleSubtle.
func TestPlanEditor_CursorHighlightsSelectedSection(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	sess, _ := newEditorTestSession(t)
	const plan = "# Goal\nbody\n\n## Spec\nspec\n\n## Context\nctx\n"
	if err := sess.WritePlan(plan); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	editor := newPlanEditor(sess, "", 80, 30)
	if len(editor.sections) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(editor.sections))
	}
	editor.sectionCursor = 1
	editor.invalidateDisplayCache()

	lines := editor.displayLines()
	// sectionDisplayStart[i] is the display line index of section i's heading.
	if len(editor.sectionDisplayStart) < 3 {
		t.Fatalf("sectionDisplayStart has %d entries, want ≥3", len(editor.sectionDisplayStart))
	}

	// ColorSecondary #06B6D4 renders as ANSI decimal RGB 6;182;211.
	// We check that the glyph character (▼/▶) is directly preceded by the
	// cyan color escape on the cursor section and not on the others.
	// The gray StyleSubtle uses 107;113;128 in TrueColor mode.
	const cyanGlyph = "6;182;211m▼ "
	const grayGlyphExpanded = "107;113;128m▼ "
	const grayGlyphFolded = "107;113;128m▶ "

	for i := 0; i < 3; i++ {
		lineIdx := editor.sectionDisplayStart[i]
		if lineIdx >= len(lines) {
			t.Fatalf("sectionDisplayStart[%d]=%d out of range (len=%d)", i, lineIdx, len(lines))
		}
		raw := lines[lineIdx]
		hasCyanGlyph := strings.Contains(raw, cyanGlyph)
		hasGrayGlyph := strings.Contains(raw, grayGlyphExpanded) || strings.Contains(raw, grayGlyphFolded)
		if i == 1 {
			if !hasCyanGlyph {
				t.Errorf("cursor section (index 1) heading line missing cyan glyph:\n%s", raw)
			}
		} else {
			if !hasGrayGlyph {
				t.Errorf("non-cursor section (index %d) heading line missing gray glyph:\n%s", i, raw)
			}
			if hasCyanGlyph {
				t.Errorf("non-cursor section (index %d) heading line unexpectedly has cyan glyph:\n%s", i, raw)
			}
		}
	}
}

// TestPlanEditor_ClampCursor verifies that clampCursor keeps sectionCursor
// within [0, len(sections)-1], including when sections is empty.
func TestPlanEditor_ClampCursor(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	const plan = "# Goal\nbody\n\n## Spec\nspec\n\n## Context\nctx\n"
	if err := sess.WritePlan(plan); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	editor := newPlanEditor(sess, "", 80, 30)
	if len(editor.sections) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(editor.sections))
	}

	// Cursor above max → clamp to last index.
	editor.sectionCursor = 5
	editor.clampCursor()
	if editor.sectionCursor != 2 {
		t.Errorf("clampCursor with cursor=5/len=3: got %d, want 2", editor.sectionCursor)
	}

	// Cursor below 0 → clamp to 0.
	editor.sectionCursor = -3
	editor.clampCursor()
	if editor.sectionCursor != 0 {
		t.Errorf("clampCursor with cursor=-3: got %d, want 0", editor.sectionCursor)
	}

	// Empty sections → cursor stays 0 without panic.
	editor2 := newPlanEditor(sess, "", 80, 30)
	editor2.sections = nil
	editor2.sectionCursor = 3
	editor2.clampCursor()
	if editor2.sectionCursor != 0 {
		t.Errorf("clampCursor with empty sections: got %d, want 0", editor2.sectionCursor)
	}
}

func TestPlanEditor_View_FooterOnLastRow(t *testing.T) {
	const h = 30
	assertFooter := func(t *testing.T, editor planEditorModel) {
		t.Helper()
		view := editor.View()
		lines := strings.Split(view, "\n")
		if len(lines) != h {
			t.Errorf("View() returned %d lines, want %d", len(lines), h)
		}
		last := testutil.StripANSI(lines[len(lines)-1])
		if !strings.Contains(last, "esc") {
			t.Errorf("last line %q does not contain 'esc' hint", last)
		}
	}

	t.Run("empty plan", func(t *testing.T) {
		sess, _ := newEditorTestSession(t)
		// no plan written — renderBody returns the placeholder string
		editor := newPlanEditor(sess, "", 80, h)
		assertFooter(t, editor)
	})

	t.Run("short plan", func(t *testing.T) {
		sess, _ := newEditorTestSession(t)
		if err := sess.WritePlan("# Goal\nDo X\n"); err != nil {
			t.Fatal(err)
		}
		editor := newPlanEditor(sess, "", 80, h)
		assertFooter(t, editor)
	})

	t.Run("full plan fills bodyH", func(t *testing.T) {
		sess, _ := newEditorTestSession(t)
		// Build a plan long enough to fill all body rows.
		var sb strings.Builder
		sb.WriteString("# Goal\nDo X\n\n")
		for i := 0; i < 50; i++ {
			sb.WriteString("- item line\n")
		}
		if err := sess.WritePlan(sb.String()); err != nil {
			t.Fatal(err)
		}
		editor := newPlanEditor(sess, "", 80, h)
		assertFooter(t, editor)
	})

	t.Run("edit mode", func(t *testing.T) {
		sess, _ := newEditorTestSession(t)
		_ = sess.WritePlan("# Goal\nDo X\n")
		editor := newPlanEditor(sess, "", 80, h)
		editor, _ = editor.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
		if editor.mode != planEditorModeEdit {
			t.Fatalf("mode = %v, want edit", editor.mode)
		}
		assertFooter(t, editor)
	})

	t.Run("revise input mode", func(t *testing.T) {
		sess, _ := newEditorTestSession(t)
		_ = sess.WritePlan("# Goal\nDo X\n")
		editor := newPlanEditor(sess, "", 80, h)
		editor, _ = editor.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
		if editor.mode != planEditorModeReviseInput {
			t.Fatalf("mode = %v, want reviseInput", editor.mode)
		}
		assertFooter(t, editor)
	})
}
