package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// feedbackSubmitFromCmd runs cmd (if any) and reports the feedbackNoteSubmitMsg
// it yields. ok is false when cmd is nil or yields a different message — i.e.
// the modal did not submit.
func feedbackSubmitFromCmd(cmd tea.Cmd) (feedbackNoteSubmitMsg, bool) {
	if cmd == nil {
		return feedbackNoteSubmitMsg{}, false
	}
	sm, ok := cmd().(feedbackNoteSubmitMsg)
	return sm, ok
}

func TestFeedbackNoteModal_SubmitEmitsSubmitMsg(t *testing.T) {
	m := newFeedbackNoteModal()
	m.SetSize(100, 40)
	_ = m.Open("comment:42", "old note")

	if !m.Active() {
		t.Fatal("expected modal to be active after Open")
	}
	if m.ta.Value() != "old note" {
		t.Errorf("textarea value = %q, want %q", m.ta.Value(), "old note")
	}

	// Pressing enter should close the modal and emit a submit msg.
	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	sm, ok := feedbackSubmitFromCmd(cmd)
	if !ok {
		t.Fatal("expected feedbackNoteSubmitMsg on enter")
	}
	if sm.note != "old note" {
		t.Errorf("note = %q, want %q", sm.note, "old note")
	}
	if sm.itemKey != "comment:42" {
		t.Errorf("itemKey = %q, want %q", sm.itemKey, "comment:42")
	}
	if m.Active() {
		t.Error("expected modal inactive after submit")
	}
}

func TestFeedbackNoteModal_EscCancels(t *testing.T) {
	m := newFeedbackNoteModal()
	m.SetSize(100, 40)
	_ = m.Open("comment:42", "existing")

	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if _, ok := feedbackSubmitFromCmd(cmd); ok {
		t.Error("esc should not emit a submit msg")
	}
	if m.Active() {
		t.Error("expected modal inactive after esc")
	}
}

func TestFeedbackNoteModal_InactiveNoop(t *testing.T) {
	m := newFeedbackNoteModal()
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if cmd != nil {
		t.Errorf("inactive modal should be a noop; got cmd=%v", cmd)
	}
}

func TestFeedbackNoteModal_SubmitTrimsWhitespace(t *testing.T) {
	m := newFeedbackNoteModal()
	m.SetSize(100, 40)
	_ = m.Open("c", "  padded note  \n")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	sm, ok := feedbackSubmitFromCmd(cmd)
	if !ok {
		t.Fatal("expected feedbackNoteSubmitMsg")
	}
	if sm.note != "padded note" {
		t.Errorf("note = %q, want trimmed value %q", sm.note, "padded note")
	}
}

func TestFeedbackNoteModal_SubmitEmptyEmitsBlankNote(t *testing.T) {
	// Whitespace-only contents trim to "", but the user explicitly hit enter
	// — a submit msg should still be emitted and the owner decides what to do
	// with an empty note. Pinned so the trim/submit semantics don't drift.
	m := newFeedbackNoteModal()
	m.SetSize(100, 40)
	_ = m.Open("c", "   \t")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	sm, ok := feedbackSubmitFromCmd(cmd)
	if !ok {
		t.Error("expected a submit msg when enter is pressed, regardless of content")
	}
	if sm.note != "" {
		t.Errorf("note = %q, want empty (trim of whitespace-only)", sm.note)
	}
}

func TestFeedbackNoteModal_ShiftEnterInsertsNewline_DoesNotSubmit(t *testing.T) {
	m := newFeedbackNoteModal()
	m.SetSize(100, 40)
	_ = m.Open("c", "line 1")
	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift, Text: "shift+enter"})
	if _, ok := feedbackSubmitFromCmd(cmd); ok {
		t.Error("shift+enter must NOT submit")
	}
	if !m.Active() {
		t.Error("modal must stay open on shift+enter")
	}
	// Type something to confirm there's a newline between "line 1" and the new text.
	m.ta.InsertString("line 2")
	if got := m.ta.Value(); got == "line 1line 2" || got == "line 1 line 2" {
		t.Errorf("textarea value = %q, want a newline between segments", got)
	}
}

func TestFeedbackNoteModal_PrintableKeyForwardedToTextarea(t *testing.T) {
	m := newFeedbackNoteModal()
	m.SetSize(100, 40)
	_ = m.Open("c", "")
	m, cmd := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if _, ok := feedbackSubmitFromCmd(cmd); ok {
		t.Error("printable key must not submit")
	}
	if !m.Active() {
		t.Error("modal closed on printable key")
	}
}

func TestFeedbackNoteModal_PasteForwardedToTextarea(t *testing.T) {
	m := newFeedbackNoteModal()
	m.SetSize(100, 40)
	_ = m.Open("c", "")
	m, cmd := m.Update(tea.PasteMsg{Content: "pasted-feedback"})
	if cmd != nil {
		cmd()
	}
	// Paste should populate the textarea.
	if got := m.ta.Value(); got == "" {
		t.Errorf("textarea empty after paste; expected non-empty content")
	}
}

func TestFeedbackNoteModal_UnknownControlKey_NoOp(t *testing.T) {
	// ctrl+x is not handled; modal forwards to textarea which should ignore
	// it (or treat as a no-op for cursor). Either way: still active, no submit.
	m := newFeedbackNoteModal()
	m.SetSize(100, 40)
	_ = m.Open("c", "hi")
	m, cmd := m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if _, ok := feedbackSubmitFromCmd(cmd); ok {
		t.Error("ctrl+x should not submit")
	}
	if !m.Active() {
		t.Error("ctrl+x closed the modal")
	}
}
