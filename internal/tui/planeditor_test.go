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

	editor := newPlanEditor(sess, 80, 30)
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
	editor := newPlanEditor(sess, 80, 30)

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
	editor := newPlanEditor(sess, 80, 30)
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
	editor := newPlanEditor(sess, 80, 30)

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
	editor := newPlanEditor(sess, 80, 30)

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
	editor := newPlanEditor(sess, 80, 30)

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
	editor := newPlanEditor(sess, 80, 30)
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
	editor := newPlanEditor(sess, 80, 30)

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
	editor := newPlanEditor(sess, 80, 30)
	if got, want := editor.contentWidth(), editor.textarea.Width(); got != want {
		t.Errorf("contentWidth=%d, textarea.Width()=%d; both modes must wrap at the same column", got, want)
	}
}

// TestPlanEditor_DisplayLineCountAgreesWithRenderer asserts that the editor's
// scroll-mode display lines exactly match a direct mdrender call on the same
// content+width. If this drifts apart the i/esc toggle no longer guarantees
// "scroll position preserved" — the post-wrap row count would change between
// modes, scrolling the user past content unexpectedly.
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
	editor := newPlanEditor(sess, 60, 30)

	scrollLines := editor.displayLines()
	directLines := mdrender.New("monokai").RenderLines(editor.textarea.Value(), editor.contentWidth())
	if len(scrollLines) != len(directLines) {
		t.Errorf("editor.displayLines()=%d vs direct mdrender.RenderLines=%d — scroll and edit modes will desync",
			len(scrollLines), len(directLines))
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
	editor := newPlanEditor(sess, 80, 30)

	view := editor.View()
	stripped := testutil.StripANSI(view)
	if !strings.Contains(stripped, "# Hello world") {
		t.Errorf("heading text missing from scroll view:\n%s", stripped)
	}
	if !strings.Contains(view, "\x1b[") {
		t.Errorf("expected ANSI styling in scroll view, got plain output:\n%s", view)
	}
}
