package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// makePRComposeForTest returns an opened modal with sane defaults so each
// test can focus on a single key.
func makePRComposeForTest(t *testing.T) *prComposeModal {
	t.Helper()
	m := newPRComposeModal()
	m.SetSize(120, 40)
	m.Open("Initial title", "Initial body line 1\nbody line 2", true, "")
	return &m
}

func TestPRCompose_OpenSetsScrollModeAndHasRenderer(t *testing.T) {
	m := newPRComposeModal()
	m.SetSize(120, 40)
	m.Open("Title", "Body", true, "my-session")
	if m.mode != prComposeModeScroll {
		t.Errorf("mode = %v, want prComposeModeScroll", m.mode)
	}
	if m.bodyArea.MarkdownRenderer() == nil {
		t.Error("bodyArea should have a MarkdownRenderer set")
	}
}

func TestPRCompose_OpenSeedsFieldsAndFocusesTitle(t *testing.T) {
	m := newPRComposeModal()
	m.SetSize(120, 40)
	cmd := m.Open("My title", "Body text", false, "")
	if !m.Active() {
		t.Fatal("modal should be Active after Open")
	}
	if m.titleInput.Value() != "My title" {
		t.Errorf("title = %q, want %q", m.titleInput.Value(), "My title")
	}
	if m.bodyArea.Value() != "Body text" {
		t.Errorf("body = %q, want %q", m.bodyArea.Value(), "Body text")
	}
	if m.draft {
		t.Error("draft should be false when Open passes draft=false")
	}
	if m.focused != 0 {
		t.Errorf("focused = %d, want 0 (title)", m.focused)
	}
	_ = cmd // Open returns nil in scroll mode; we only verify state here.
}

func TestPRCompose_EscCancels(t *testing.T) {
	m := makePRComposeForTest(t)
	cmd := m.Update(keyNamed(tea.KeyEscape))
	if cmd == nil {
		t.Fatal("expected cancel cmd")
	}
	if _, ok := cmd().(prComposeCancelMsg); !ok {
		t.Fatalf("got %T, want prComposeCancelMsg", cmd())
	}
	if m.Active() {
		t.Error("modal should close on esc")
	}
}

func TestPRCompose_CtrlEnterSubmitsTrimmedValues(t *testing.T) {
	m := makePRComposeForTest(t)
	m.titleInput.SetValue("  trimmed title  ")
	m.bodyArea.SetValue("\nbody with leading newline\n")

	cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("expected submit cmd from ctrl+enter")
	}
	got, ok := cmd().(prComposeSubmitMsg)
	if !ok {
		t.Fatalf("got %T, want prComposeSubmitMsg", cmd())
	}
	if got.title != "trimmed title" {
		t.Errorf("title = %q, want %q", got.title, "trimmed title")
	}
	if got.body != "body with leading newline" {
		t.Errorf("body = %q, want trimmed body", got.body)
	}
	if !got.draft {
		t.Error("draft snapshot should reflect m.draft at submit time")
	}
	if m.Active() {
		t.Error("modal should close on submit")
	}
}

func TestPRCompose_CtrlEnter_EmptyTitle_NoOp(t *testing.T) {
	m := makePRComposeForTest(t)
	m.titleInput.SetValue("   ")
	cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatalf("empty title submit should be a no-op (no cmd); got %T", cmd())
	}
	if !m.Active() {
		t.Error("modal should stay open when title is empty")
	}
}

func TestPRCompose_Tab_SwitchesFocusToBody(t *testing.T) {
	m := makePRComposeForTest(t)
	if m.focused != 0 {
		t.Fatalf("test prereq: focused=%d, want 0", m.focused)
	}
	cmd := m.Update(keyNamed(tea.KeyTab))
	// Update returns a focus cmd; we only verify state here.
	_ = cmd
	if m.focused != 1 {
		t.Errorf("after tab focused = %d, want 1 (body)", m.focused)
	}
	// Another tab should swing back to title (it cycles 1→0).
	m.Update(keyNamed(tea.KeyTab))
	if m.focused != 0 {
		t.Errorf("after second tab focused = %d, want 0 (title)", m.focused)
	}
}

func TestPRCompose_ShiftTab_SwitchesFocus(t *testing.T) {
	m := makePRComposeForTest(t)
	m.Update(keyShiftNamed(tea.KeyTab))
	if m.focused != 1 {
		t.Errorf("after shift+tab focused = %d, want 1", m.focused)
	}
}

func TestPRCompose_CtrlD_TogglesDraft(t *testing.T) {
	m := makePRComposeForTest(t)
	if !m.draft {
		t.Fatal("test prereq: draft should start true")
	}
	cmd := m.Update(keyCtrlRune('d'))
	if cmd != nil {
		t.Errorf("ctrl+d should not emit a cmd, got %T", cmd())
	}
	if m.draft {
		t.Error("ctrl+d did not toggle draft off")
	}
	m.Update(keyCtrlRune('d'))
	if !m.draft {
		t.Error("second ctrl+d did not toggle draft back on")
	}
}

func TestPRCompose_PrintableKey_AppendsToFocusedField(t *testing.T) {
	m := makePRComposeForTest(t)
	m.titleInput.SetValue("hi")
	// Move cursor to end. bubbles textarea handles this on Focus, but be safe.
	m.titleInput.SetValue("hi")
	m.Update(keyRune('!'))
	if got := m.titleInput.Value(); !strings.Contains(got, "!") {
		t.Errorf("title = %q, want it to include '!' after typing", got)
	}
}

func TestPRCompose_PrintableKey_RoutedToBodyWhenFocused(t *testing.T) {
	m := makePRComposeForTest(t)
	// Switch focus to body field, then type.
	m.Update(keyNamed(tea.KeyTab))
	if m.focused != 1 {
		t.Fatalf("focused = %d after tab, want 1", m.focused)
	}
	bodyBefore := m.bodyArea.Value()
	m.Update(keyRune('?'))
	bodyAfter := m.bodyArea.Value()
	if bodyAfter == bodyBefore {
		t.Error("typing in body did not change body value")
	}
	// Title should be unchanged when body has focus.
	if !strings.HasPrefix(m.titleInput.Value(), "Initial title") {
		t.Errorf("title leaked: %q", m.titleInput.Value())
	}
}

func TestPRCompose_PasteForwardedToFocusedField(t *testing.T) {
	m := makePRComposeForTest(t)
	cmd := m.Update(tea.PasteMsg{Content: "pasted-title"})
	if cmd != nil {
		cmd()
	}
	if got := m.titleInput.Value(); !strings.Contains(got, "pasted-title") {
		t.Errorf("title = %q, want it to contain pasted content", got)
	}
}

func TestPRCompose_NotActive_AllInputsNoOp(t *testing.T) {
	m := newPRComposeModal()
	m.SetSize(120, 40)
	// Modal not opened.
	for _, k := range []tea.KeyPressMsg{
		keyNamed(tea.KeyEscape),
		{Code: tea.KeyEnter, Mod: tea.ModCtrl},
		keyCtrlRune('d'),
		keyNamed(tea.KeyTab),
		keyRune('x'),
	} {
		cmd := m.Update(k)
		if cmd != nil {
			t.Errorf("inactive modal returned cmd for %v: %T", k, cmd())
		}
	}
	if m.Active() {
		t.Error("modal should still be inactive")
	}
}

func TestPRCompose_UnknownKey_DoesNotCloseOrSubmit(t *testing.T) {
	m := makePRComposeForTest(t)
	cmd := m.Update(keyRune('z'))
	// 'z' is forwarded to the textarea; it may produce a cmd (cursor blink
	// etc.) but it must NOT produce submit/cancel.
	if cmd != nil {
		msg := cmd()
		if _, bad := msg.(prComposeSubmitMsg); bad {
			t.Error("'z' produced a submit msg")
		}
		if _, bad := msg.(prComposeCancelMsg); bad {
			t.Error("'z' produced a cancel msg")
		}
	}
	if !m.Active() {
		t.Error("modal should still be open after unknown key")
	}
}
