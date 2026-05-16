package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestFeedbackNoteModal_SubmitReturnsNote(t *testing.T) {
	m := newFeedbackNoteModal()
	m.SetSize(100, 40)
	_ = m.Open("comment:42", "old note")

	if !m.Active() {
		t.Fatal("expected modal to be active after Open")
	}
	if m.ta.Value() != "old note" {
		t.Errorf("textarea value = %q, want %q", m.ta.Value(), "old note")
	}

	// Pressing enter should submit.
	cmd, submitted, note := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !submitted {
		t.Error("expected submitted=true on enter")
	}
	if note != "old note" {
		t.Errorf("note = %q, want %q", note, "old note")
	}
	if m.Active() {
		t.Error("expected modal inactive after submit")
	}
	_ = cmd
}

func TestFeedbackNoteModal_EscCancels(t *testing.T) {
	m := newFeedbackNoteModal()
	m.SetSize(100, 40)
	_ = m.Open("comment:42", "existing")

	cmd, submitted, note := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if submitted {
		t.Error("expected submitted=false on esc")
	}
	if note != "" {
		t.Errorf("note = %q, want empty on cancel", note)
	}
	if m.Active() {
		t.Error("expected modal inactive after esc")
	}
	_ = cmd
}

func TestFeedbackNoteModal_InactiveNoop(t *testing.T) {
	m := newFeedbackNoteModal()
	cmd, submitted, note := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if submitted || note != "" || cmd != nil {
		t.Errorf("inactive modal should be a noop; got submitted=%v note=%q cmd=%v", submitted, note, cmd)
	}
}

func TestFeedbackNoteModal_SubmitTrimsWhitespace(t *testing.T) {
	m := newFeedbackNoteModal()
	m.SetSize(100, 40)
	_ = m.Open("c", "  padded note  \n")
	_, submitted, note := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !submitted {
		t.Fatal("expected submitted=true")
	}
	if note != "padded note" {
		t.Errorf("note = %q, want trimmed value %q", note, "padded note")
	}
}

func TestFeedbackNoteModal_SubmitEmptyReturnsBlankNote(t *testing.T) {
	// Whitespace-only contents trim to "", but the user explicitly hit enter
	// — submitted should be true and the caller decides what to do with an
	// empty note. Pinned so the trim/submit semantics don't drift.
	m := newFeedbackNoteModal()
	m.SetSize(100, 40)
	_ = m.Open("c", "   \t")
	_, submitted, note := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !submitted {
		t.Error("submitted should be true when enter is pressed, regardless of content")
	}
	if note != "" {
		t.Errorf("note = %q, want empty (trim of whitespace-only)", note)
	}
}

func TestFeedbackNoteModal_ShiftEnterInsertsNewline_DoesNotSubmit(t *testing.T) {
	m := newFeedbackNoteModal()
	m.SetSize(100, 40)
	_ = m.Open("c", "line 1")
	cmd, submitted, note := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift, Text: "shift+enter"})
	if submitted {
		t.Error("shift+enter must NOT submit")
	}
	if note != "" {
		t.Errorf("note = %q on shift+enter, want empty", note)
	}
	if !m.Active() {
		t.Error("modal must stay open on shift+enter")
	}
	// Type something to confirm there's a newline between "line 1" and the new text.
	m.ta.InsertString("line 2")
	if got := m.ta.Value(); got == "line 1line 2" || got == "line 1 line 2" {
		t.Errorf("textarea value = %q, want a newline between segments", got)
	}
	_ = cmd
}

func TestFeedbackNoteModal_PrintableKeyForwardedToTextarea(t *testing.T) {
	m := newFeedbackNoteModal()
	m.SetSize(100, 40)
	_ = m.Open("c", "")
	cmd, submitted, _ := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if submitted {
		t.Error("printable key must not submit")
	}
	if !m.Active() {
		t.Error("modal closed on printable key")
	}
	// bubbles' textarea cmd is for cursor blink; we just verify it doesn't
	// trigger our submit/cancel control flow.
	_ = cmd
}

func TestFeedbackNoteModal_PasteForwardedToTextarea(t *testing.T) {
	m := newFeedbackNoteModal()
	m.SetSize(100, 40)
	_ = m.Open("c", "")
	cmd, _, _ := m.Update(tea.PasteMsg{Content: "pasted-feedback"})
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
	// it (or treat as a no-op for cursor). Either way: still active, no
	// submit, no cancel.
	m := newFeedbackNoteModal()
	m.SetSize(100, 40)
	_ = m.Open("c", "hi")
	_, submitted, note := m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if submitted {
		t.Error("ctrl+x should not submit")
	}
	if note != "" {
		t.Error("ctrl+x produced a non-empty note")
	}
	if !m.Active() {
		t.Error("ctrl+x closed the modal")
	}
}
