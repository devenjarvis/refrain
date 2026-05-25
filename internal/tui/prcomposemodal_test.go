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

// --- Task 2: scroll mode View and scroll keybindings ---

func TestPRCompose_ViewScrollMode_ContainsHeaderAndTitle(t *testing.T) {
	m := newPRComposeModal()
	m.SetSize(120, 40)
	m.Open("My PR Title", "Some body content", true, "feature-x")
	v := m.View()
	if !strings.Contains(v, "PR DRAFT") {
		t.Errorf("scroll view missing 'PR DRAFT', got: %q", v)
	}
	if !strings.Contains(v, "feature-x") {
		t.Errorf("scroll view missing session name 'feature-x', got: %q", v)
	}
	if !strings.Contains(v, "My PR Title") {
		t.Errorf("scroll view missing title text, got: %q", v)
	}
	// Verify body is rendered through mdrender (content must appear in output).
	if !strings.Contains(v, "Some body content") {
		t.Errorf("scroll view missing body content rendered through mdrender, got: %q", v)
	}
}

func TestPRCompose_ScrollKeyJ_IncrementsScrollOff(t *testing.T) {
	m := makePRComposeForTest(t)
	m.bodyArea.SetValue(strings.Repeat("line\n", 50))
	m.SetSize(120, 40)
	before := m.scrollOff
	m.Update(keyRune('j'))
	if m.scrollOff <= before {
		t.Errorf("j did not increment scrollOff: before=%d after=%d", before, m.scrollOff)
	}
}

func TestPRCompose_ScrollKeyK_DecrementsScrollOff(t *testing.T) {
	m := makePRComposeForTest(t)
	m.bodyArea.SetValue(strings.Repeat("line\n", 50))
	m.SetSize(120, 40)
	m.Update(keyRune('j'))
	m.Update(keyRune('j'))
	after := m.scrollOff
	m.Update(keyRune('k'))
	if m.scrollOff >= after {
		t.Errorf("k did not decrement scrollOff: before=%d after=%d", after, m.scrollOff)
	}
}

func TestPRCompose_ScrollKeyG_GoesToTop(t *testing.T) {
	m := makePRComposeForTest(t)
	m.bodyArea.SetValue(strings.Repeat("line\n", 50))
	m.SetSize(120, 40)
	m.Update(keyRune('j'))
	m.Update(keyRune('j'))
	m.Update(keyRune('g'))
	if m.scrollOff != 0 {
		t.Errorf("g did not reset scrollOff to 0, got %d", m.scrollOff)
	}
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

// --- Task 3: edit-mode transitions ---

func TestPRCompose_I_EntersEditModeAndFocusesTitle(t *testing.T) {
	m := makePRComposeForTest(t)
	if m.mode != prComposeModeScroll {
		t.Fatalf("prereq: mode=%v, want scroll", m.mode)
	}
	m.Update(keyRune('i'))
	if m.mode != prComposeModeEdit {
		t.Errorf("after i: mode=%v, want prComposeModeEdit", m.mode)
	}
	if m.focused != 0 {
		t.Errorf("after i: focused=%d, want 0 (title)", m.focused)
	}
}

func TestPRCompose_EscInEditMode_ReturnsToScroll(t *testing.T) {
	m := makePRComposeForTest(t)
	m.Update(keyRune('i'))
	if m.mode != prComposeModeEdit {
		t.Fatalf("prereq: expected edit mode")
	}
	m.Update(keyNamed(tea.KeyEscape))
	if m.mode != prComposeModeScroll {
		t.Errorf("esc in edit mode did not return to scroll: mode=%v", m.mode)
	}
	if !m.Active() {
		t.Error("esc in edit mode should not close the view")
	}
}

func TestPRCompose_Tab_SwitchesFocusToBodyInEditMode(t *testing.T) {
	m := makePRComposeForTest(t)
	m.Update(keyRune('i')) // enter edit mode; focused=0 (title)
	if m.focused != 0 {
		t.Fatalf("prereq: focused=%d, want 0 after i", m.focused)
	}
	m.Update(keyNamed(tea.KeyTab))
	if m.focused != 1 {
		t.Errorf("after tab: focused=%d, want 1 (body)", m.focused)
	}
	m.Update(keyNamed(tea.KeyTab))
	if m.focused != 0 {
		t.Errorf("after second tab: focused=%d, want 0 (title)", m.focused)
	}
}

func TestPRCompose_ShiftTab_SwitchesFocusInEditMode(t *testing.T) {
	m := makePRComposeForTest(t)
	m.Update(keyRune('i')) // enter edit mode
	m.Update(keyShiftNamed(tea.KeyTab))
	if m.focused != 1 {
		t.Errorf("after shift+tab: focused=%d, want 1", m.focused)
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

// --- Task 5: edit-mode View ---

func TestPRCompose_ViewEditMode_ContainsHeaderAndTitle(t *testing.T) {
	m := makePRComposeForTest(t)
	m.Update(keyRune('i')) // enter edit mode
	v := m.View()
	if !strings.Contains(v, "PR DRAFT") {
		t.Errorf("edit view missing 'PR DRAFT', got: %q", v)
	}
	if !strings.Contains(v, "Initial title") {
		t.Errorf("edit view missing title value, got: %q", v)
	}
}

// --- Task 4: submit, cancel, draft toggle across modes ---

func TestPRCompose_CtrlEnterInEditMode_Submits(t *testing.T) {
	m := makePRComposeForTest(t)
	m.Update(keyRune('i')) // enter edit mode
	cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("expected submit cmd from ctrl+enter in edit mode")
	}
	if _, ok := cmd().(prComposeSubmitMsg); !ok {
		t.Fatalf("got %T, want prComposeSubmitMsg", cmd())
	}
	if m.Active() {
		t.Error("modal should close on submit from edit mode")
	}
}

func TestPRCompose_CtrlD_TogglesDraftInEditMode(t *testing.T) {
	m := makePRComposeForTest(t)
	m.Update(keyRune('i')) // enter edit mode
	if !m.draft {
		t.Fatal("prereq: draft should be true")
	}
	m.Update(keyCtrlRune('d'))
	if m.draft {
		t.Error("ctrl+d in edit mode did not toggle draft off")
	}
}

// --- Task 6: PasteMsg routing ---

func TestPRCompose_PasteInEditMode_TitleFocused_UpdatesTitle(t *testing.T) {
	m := makePRComposeForTest(t)
	m.Update(keyRune('i')) // enter edit mode, title focused
	if m.focused != 0 {
		t.Fatalf("prereq: focused=%d, want 0 (title)", m.focused)
	}
	cmd := m.Update(tea.PasteMsg{Content: "pasted-title"})
	if cmd != nil {
		cmd()
	}
	if got := m.titleInput.Value(); !strings.Contains(got, "pasted-title") {
		t.Errorf("title = %q, want it to contain pasted content", got)
	}
}

func TestPRCompose_PasteInEditMode_BodyFocused_UpdatesBody(t *testing.T) {
	m := makePRComposeForTest(t)
	m.Update(keyRune('i'))         // enter edit mode
	m.Update(keyNamed(tea.KeyTab)) // switch to body
	if m.focused != 1 {
		t.Fatalf("prereq: focused=%d, want 1 (body)", m.focused)
	}
	bodyBefore := m.bodyArea.Value()
	m.Update(tea.PasteMsg{Content: "pasted-body"})
	if got := m.bodyArea.Value(); got == bodyBefore {
		t.Error("paste in body-focused edit mode did not update body")
	}
}

func TestPRCompose_PasteInScrollMode_IsNoOp(t *testing.T) {
	m := makePRComposeForTest(t)
	titleBefore := m.titleInput.Value()
	m.Update(tea.PasteMsg{Content: "should-be-ignored"})
	if m.titleInput.Value() != titleBefore {
		t.Error("paste in scroll mode should not modify title")
	}
}

func TestPRCompose_PrintableKey_AppendsToTitleInEditMode(t *testing.T) {
	m := makePRComposeForTest(t)
	m.Update(keyRune('i')) // enter edit mode (title focused)
	m.titleInput.SetValue("hi")
	m.Update(keyRune('!'))
	if got := m.titleInput.Value(); !strings.Contains(got, "!") {
		t.Errorf("title = %q, want it to include '!' after typing in edit mode", got)
	}
}

func TestPRCompose_PrintableKey_RoutedToBodyInEditModeWhenBodyFocused(t *testing.T) {
	m := makePRComposeForTest(t)
	m.Update(keyRune('i'))         // enter edit mode (title focused)
	m.Update(keyNamed(tea.KeyTab)) // switch to body
	if m.focused != 1 {
		t.Fatalf("focused = %d after tab, want 1", m.focused)
	}
	bodyBefore := m.bodyArea.Value()
	m.Update(keyRune('?'))
	if m.bodyArea.Value() == bodyBefore {
		t.Error("typing in body did not change body value")
	}
	if !strings.HasPrefix(m.titleInput.Value(), "Initial title") {
		t.Errorf("title leaked: %q", m.titleInput.Value())
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
