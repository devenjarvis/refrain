package tui

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/devenjarvis/baton/internal/agent"
	"github.com/devenjarvis/baton/internal/config"
	"github.com/devenjarvis/baton/internal/tui/mdrender"
	"github.com/devenjarvis/baton/internal/tui/mdrender/testutil"
	"github.com/muesli/termenv"
)

// newEditorTestSession creates a real session in a temp git repo so the
// editor's WritePlan/ReadPlan calls hit a real worktree without spawning a
// claude subprocess. The bash placeholder agent is killed after the test.
func newEditorTestSession(t *testing.T) (*agent.Session, *agent.Manager) {
	t.Helper()

	dir, err := os.MkdirTemp("", "baton-planeditor-*")
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
	cmd := editor.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	_ = cmd
	if editor.mode != planEditorModeEdit {
		t.Fatalf("mode after i = %v, want edit", editor.mode)
	}

	// Replace textarea content directly (simulate user typing).
	editor.textarea.SetValue("rewritten\n")
	editor.dirty = true

	cmd = editor.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl, Text: "ctrl+s"})
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
	editor.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	editor.textarea.SetValue("typed but not saved\n")
	editor.dirty = true

	editor.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if editor.mode != planEditorModeScroll {
		t.Errorf("mode = %v, want scroll after esc", editor.mode)
	}
	if editor.textarea.Value() != "typed but not saved\n" {
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

	cmd := editor.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
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
	editor.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	editor.textarea.SetValue("# revised\n- [ ] thing\n")
	editor.dirty = true
	editor.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	cmd := editor.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
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

	cmd := editor.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
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

	if cmd := editor.Update(tea.KeyPressMsg{Code: 'i', Text: "i"}); cmd != nil {
		t.Error("i should be a no-op while drafting")
	}
	if cmd := editor.Update(tea.KeyPressMsg{Code: 'a', Text: "a"}); cmd != nil {
		t.Error("a should be a no-op while drafting")
	}
	// esc still works to back out.
	cmd := editor.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
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

	editor.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if editor.mode != planEditorModeReviseInput {
		t.Fatalf("mode = %v, want reviseInput", editor.mode)
	}
	editor.reviseInput.SetValue("split task 2 into ui and persistence")

	cmd := editor.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
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

// TestPlanEditor_ScrollAndEditModeUseSameWidth pins the wiring contract that
// scroll-mode wrap (via mdrender.RenderLines) and edit-mode wrap (via the
// embedded mdtextarea) operate at the same column width. Without this, the
// `i`/`esc` mode toggle would visually reflow content — exactly the
// regression Task 5's display-line agreement test is meant to catch.
func TestPlanEditor_ScrollAndEditModeUseSameWidth(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	_ = sess.WritePlan("# H\nshort\n")
	editor := newPlanEditor(sess, "", 80, 30)
	if got, want := editor.contentWidth(), editor.textarea.Width(); got != want {
		t.Errorf("contentWidth=%d, textarea.Width()=%d; both modes must wrap at the same column", got, want)
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
	directLines := mdrender.New("monokai").RenderLines(editor.textarea.Value(), editor.contentWidth())
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
// scroll mode toggles the fold of whichever section contains the viewport top.
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

	// Viewport top is at scrollOff=0 → Goal section; pressing tab collapses it.
	editor.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if !editor.folds["Goal"] {
		t.Errorf("after tab at Goal, folds[Goal] = false, want true (collapsed)")
	}

	// Move viewport to Spec's heading display line, then tab toggles Spec.
	lines := editor.displayLines()
	specDisplayLine := -1
	for i, l := range lines {
		stripped := testutil.StripANSI(l)
		if strings.Contains(stripped, "Spec") && !strings.Contains(stripped, "Goal") {
			specDisplayLine = i
			break
		}
	}
	if specDisplayLine < 0 {
		t.Fatal("could not find Spec heading in displayLines")
	}
	editor.scrollOff = specDisplayLine
	editor.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if !editor.folds["Spec"] {
		t.Errorf("after tab at Spec, folds[Spec] = false, want true (collapsed)")
	}
}

// TestPlanEditor_BracketsJumpBetweenSections verifies that ] and [ step the
// viewport to the next and previous section headings respectively, clamping at
// the ends.
func TestPlanEditor_BracketsJumpBetweenSections(t *testing.T) {
	sess, _ := newEditorTestSession(t)
	// Use non-default folds: all expanded so we get display lines between sections.
	const plan = "# Goal\nGoal body\n\n## Spec\nspec body\n\n## Tasks\ntask body\n"
	if err := sess.WritePlan(plan); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	// height=8 gives bodyHeight=3, so clampScroll allows scrolling (content > viewport).
	editor := newPlanEditor(sess, "", 80, 8)
	// Expand all sections so navigation has multiple display lines to cross.
	for k := range editor.folds {
		editor.folds[k] = false
	}
	editor.invalidateDisplayCache()
	editor.scrollOff = 0

	lines := editor.displayLines()
	if len(lines) == 0 {
		t.Fatal("no display lines")
	}

	// Find display-line indices for ## Spec and ## Tasks headings.
	specLine, tasksLine := -1, -1
	for i, l := range lines {
		stripped := testutil.StripANSI(l)
		if strings.Contains(stripped, "## Spec") && specLine < 0 {
			specLine = i
		}
		if strings.Contains(stripped, "## Tasks") && tasksLine < 0 {
			tasksLine = i
		}
	}
	if specLine < 0 || tasksLine < 0 {
		t.Fatalf("couldn't find section headings; specLine=%d tasksLine=%d\nlines:\n%s",
			specLine, tasksLine, strings.Join(lines, "\n"))
	}

	// ] from top → Spec heading.
	editor.Update(tea.KeyPressMsg{Code: ']', Text: "]"})
	if editor.scrollOff != specLine {
		t.Errorf("] from 0: scrollOff = %d, want %d (Spec)", editor.scrollOff, specLine)
	}

	// ] again → Tasks heading.
	editor.Update(tea.KeyPressMsg{Code: ']', Text: "]"})
	if editor.scrollOff != tasksLine {
		t.Errorf("] from Spec: scrollOff = %d, want %d (Tasks)", editor.scrollOff, tasksLine)
	}

	// ] at last section → no change.
	editor.Update(tea.KeyPressMsg{Code: ']', Text: "]"})
	if editor.scrollOff != tasksLine {
		t.Errorf("] at last section: scrollOff = %d, want %d (clamp)", editor.scrollOff, tasksLine)
	}

	// [ twice → back to top (Goal heading).
	editor.Update(tea.KeyPressMsg{Code: '[', Text: "["})
	if editor.scrollOff != specLine {
		t.Errorf("[ from Tasks: scrollOff = %d, want %d (Spec)", editor.scrollOff, specLine)
	}
	editor.Update(tea.KeyPressMsg{Code: '[', Text: "["})
	if editor.scrollOff != 0 {
		t.Errorf("[ from Spec: scrollOff = %d, want 0 (Goal)", editor.scrollOff)
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
	editor.Update(tea.KeyPressMsg{Code: 'Z', Text: "Z"})
	for _, s := range editor.sections {
		if !editor.folds[s.heading] {
			t.Errorf("after first Z, folds[%q] = false, want true (all collapsed)", s.heading)
		}
	}

	// All collapsed. Press Z again → all expanded.
	editor.Update(tea.KeyPressMsg{Code: 'Z', Text: "Z"})
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
	for _, want := range []string{"tab", "fold", "[", "]", "Z", "toggle all"} {
		if !strings.Contains(view, want) {
			t.Errorf("footer hint %q missing from view:\n%s", want, view)
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
