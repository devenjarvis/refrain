package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
