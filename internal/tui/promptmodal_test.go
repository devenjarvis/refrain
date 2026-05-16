package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	xlipgloss "charm.land/lipgloss/v2"
)

func TestPromptModal_OpenFocusesAndClearsValue(t *testing.T) {
	m := newPromptModal()
	m.SetSize(120, 40)
	m.textarea.SetValue("stale")
	cmd := m.Open()
	if !m.Active() {
		t.Fatal("modal should be active after Open")
	}
	if m.textarea.Value() != "" {
		t.Errorf("textarea not cleared on Open, got %q", m.textarea.Value())
	}
	_ = cmd // Focus may return a Cmd; we just verify it doesn't panic.
}

func TestPromptModal_EnterSubmitsViaPlanningPath(t *testing.T) {
	m := newPromptModal()
	m.SetSize(120, 40)
	m.Open()
	m.textarea.SetValue("add dark mode toggle")

	cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	if cmd == nil {
		t.Fatal("expected cmd from enter")
	}
	got := cmd()
	submit, ok := got.(promptModalSubmitMsg)
	if !ok {
		t.Fatalf("got %T, want promptModalSubmitMsg", got)
	}
	if submit.skipPlanning {
		t.Error("plain enter should NOT set skipPlanning")
	}
	if !strings.Contains(submit.prompt, "dark mode") {
		t.Errorf("prompt = %q", submit.prompt)
	}
	if m.Active() {
		t.Error("modal should close on submit")
	}
}

func TestPromptModal_CtrlEnterSubmitsSkipPath(t *testing.T) {
	m := newPromptModal()
	m.SetSize(120, 40)
	m.Open()
	m.textarea.SetValue("trivial fix")

	cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl, Text: "ctrl+enter"})
	if cmd == nil {
		t.Fatal("expected cmd from ctrl+enter")
	}
	got := cmd()
	submit, ok := got.(promptModalSubmitMsg)
	if !ok {
		t.Fatalf("got %T, want promptModalSubmitMsg", got)
	}
	if !submit.skipPlanning {
		t.Error("ctrl+enter MUST set skipPlanning=true")
	}
	if submit.prompt != "trivial fix" {
		t.Errorf("prompt = %q", submit.prompt)
	}
}

func TestPromptModal_EmptyEnterIsNoop(t *testing.T) {
	m := newPromptModal()
	m.SetSize(120, 40)
	m.Open()
	m.textarea.SetValue("   \n\t")

	cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Text: "enter"})
	if cmd != nil {
		t.Fatal("empty/whitespace enter should be a no-op (no cmd)")
	}
	if !m.Active() {
		t.Error("modal should stay open on empty submit")
	}
}

func TestPromptModal_EscCancels(t *testing.T) {
	m := newPromptModal()
	m.SetSize(120, 40)
	m.Open()
	m.textarea.SetValue("never mind")

	cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("expected cancel cmd")
	}
	got := cmd()
	if _, ok := got.(promptModalCancelMsg); !ok {
		t.Fatalf("got %T, want promptModalCancelMsg", got)
	}
	if m.Active() {
		t.Error("modal should close on esc")
	}
}

func TestPromptModal_ShiftEnterInsertsNewline(t *testing.T) {
	m := newPromptModal()
	m.SetSize(120, 40)
	m.Open()
	m.textarea.SetValue("first line")

	cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift, Text: "shift+enter"})
	if cmd != nil {
		// shift+enter must not produce a submit/cancel cmd; running it
		// would surface that mistake.
		if msg := cmd(); msg != nil {
			if _, ok := msg.(promptModalSubmitMsg); ok {
				t.Fatalf("shift+enter triggered submit; want newline insertion")
			}
		}
	}
	if !m.Active() {
		t.Fatal("shift+enter must not close the modal")
	}
	m.textarea.InsertString("second line")
	if got := m.textarea.Value(); !strings.Contains(got, "\n") {
		t.Errorf("textarea value = %q, want a newline between the two lines", got)
	}
}

func TestPromptModal_KeysDeferToTextareaWhenNotControl(t *testing.T) {
	m := newPromptModal()
	m.SetSize(120, 40)
	m.Open()

	// A printable character should reach the textarea, not produce a cmd
	// that closes the modal.
	cmd := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	_ = cmd
	if !m.Active() {
		t.Error("typing should not close the modal")
	}
}

func TestModalWidth_Clamps(t *testing.T) {
	// Wide viewport: clamped to max.
	if w := modalWidth(200); w > promptModalMaxWidth {
		t.Errorf("modalWidth(200) = %d, want <= %d", w, promptModalMaxWidth)
	}
	// Mid viewport: 60 * 2/3 = 40, fits in min.
	if w := modalWidth(60); w != 40 {
		t.Errorf("modalWidth(60) = %d, want 40 (60*2/3)", w)
	}
	// Narrow viewport: must leave 2 cols of margin so border fits.
	if w := modalWidth(40); w > 38 {
		t.Errorf("modalWidth(40) = %d, want <= 38 (viewport-2 clamp)", w)
	}
}

func TestPromptModal_PasteForwardsToTextarea(t *testing.T) {
	m := newPromptModal()
	m.SetSize(120, 40)
	m.Open()

	cmd := m.Update(tea.PasteMsg{Content: "pasted clipboard text"})
	// Bubbles v2 commits paste synchronously today, but run any returned
	// cmd anyway so this test doesn't silently false-positive if a future
	// bubbles release flips paste to async.
	if cmd != nil {
		cmd()
	}
	if got := m.textarea.Value(); !strings.Contains(got, "pasted clipboard text") {
		t.Errorf("textarea value = %q, want it to contain pasted content", got)
	}
}

func TestPromptModal_OpenPicksStablePairAndRotates(t *testing.T) {
	prev := pickPrompt
	defer func() { pickPrompt = prev }()

	picks := []int{2, 5, 0, 3}
	calls := 0
	pickPrompt = func(n int) int {
		i := picks[calls] % n
		calls++
		return i
	}

	m := newPromptModal()
	m.SetSize(120, 40)
	m.Open()
	if m.titleIdx != 2 {
		t.Errorf("first open titleIdx = %d, want 2", m.titleIdx)
	}
	if m.placeholderIdx != 5 {
		t.Errorf("first open placeholderIdx = %d, want 5", m.placeholderIdx)
	}
	wantTitle := promptModalTitles[2]
	wantPH := promptModalPlaceholders[5]

	view1 := m.View()
	view2 := m.View()
	if view1 != view2 {
		t.Error("View() must be stable across renders within one open session")
	}
	if !strings.Contains(view1, wantTitle) {
		t.Errorf("view does not contain selected title %q", wantTitle)
	}
	if m.textarea.Placeholder != wantPH {
		t.Errorf("textarea.Placeholder = %q, want %q", m.textarea.Placeholder, wantPH)
	}

	m.Close()
	m.Open()
	if m.titleIdx != 0 {
		t.Errorf("second open titleIdx = %d, want 0", m.titleIdx)
	}
	if m.placeholderIdx != 3 {
		t.Errorf("second open placeholderIdx = %d, want 3", m.placeholderIdx)
	}
}

func TestPromptModal_FocusedCursorLineHasNoBackground(t *testing.T) {
	m := newPromptModal()
	bg := m.textarea.Styles().Focused.CursorLine.GetBackground()
	// An untouched style returns NoColor{} from GetBackground; the bubbles
	// default would return a real Color. Compare to a fresh empty style's
	// background to avoid coupling the test to NoColor's exact type.
	want := xlipgloss.NewStyle().GetBackground()
	if bg != want {
		t.Errorf("CursorLine background = %v, want %v (no background)", bg, want)
	}
}

func TestPromptModal_EmptyCtrlEnterIsNoop(t *testing.T) {
	// Mirrors the empty-enter case: ctrl+enter on whitespace-only input must
	// not submit. Pin so future refactors keep the symmetric behaviour.
	m := newPromptModal()
	m.SetSize(120, 40)
	m.Open()
	m.textarea.SetValue("\n   \t")

	cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl, Text: "ctrl+enter"})
	if cmd != nil {
		t.Errorf("empty ctrl+enter should be a no-op; got cmd %T", cmd())
	}
	if !m.Active() {
		t.Error("modal should stay open on empty ctrl+enter")
	}
}

func TestPromptModal_InactiveSwallowsAllInput(t *testing.T) {
	m := newPromptModal()
	m.SetSize(120, 40)
	// Modal NOT opened.
	for _, k := range []tea.KeyPressMsg{
		{Code: tea.KeyEscape},
		{Code: tea.KeyEnter},
		{Code: tea.KeyEnter, Mod: tea.ModCtrl},
		{Code: 'x', Text: "x"},
	} {
		if cmd := m.Update(k); cmd != nil {
			t.Errorf("inactive modal returned cmd for %v: %T", k, cmd())
		}
	}
	if m.Active() {
		t.Error("modal should still be inactive")
	}
}

func TestPromptModal_UnknownControlKey_DoesNotSubmit(t *testing.T) {
	m := newPromptModal()
	m.SetSize(120, 40)
	m.Open()
	m.textarea.SetValue("some text")
	cmd := m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if cmd != nil {
		msg := cmd()
		if _, bad := msg.(promptModalSubmitMsg); bad {
			t.Error("ctrl+x submitted a modal — should be forwarded silently")
		}
		if _, bad := msg.(promptModalCancelMsg); bad {
			t.Error("ctrl+x cancelled a modal")
		}
	}
	if !m.Active() {
		t.Error("modal should still be open after unknown control key")
	}
}
