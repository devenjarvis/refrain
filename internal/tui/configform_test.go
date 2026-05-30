package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func sampleForm() configForm {
	fields := []formField{}
	fields = addToggle(fields, "Enable focus mode", true)
	fields = addTextInput(fields, "Branch prefix", "refrain/", "refrain/", 30)
	fields = addSelect(fields, "Theme", []string{"light", "dark", "auto"}, 1)
	return newConfigForm(fields, 60)
}

func TestConfigForm_InitialFocusIsFirstField(t *testing.T) {
	f := sampleForm()
	if f.focused != 0 {
		t.Errorf("focused = %d, want 0", f.focused)
	}
}

func TestConfigForm_DownArrowAdvancesFocus(t *testing.T) {
	f := sampleForm()
	f, _ = f.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if f.focused != 1 {
		t.Errorf("after 'j', focused = %d, want 1", f.focused)
	}
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if f.focused != 2 {
		t.Errorf("after 'down', focused = %d, want 2", f.focused)
	}
}

func TestConfigForm_DownClampsAtLastField(t *testing.T) {
	f := sampleForm()
	for range 10 {
		f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if f.focused != len(f.fields)-1 {
		t.Errorf("expected focused = %d (last), got %d", len(f.fields)-1, f.focused)
	}
}

func TestConfigForm_UpClampsAtFirstField(t *testing.T) {
	f := sampleForm()
	f, _ = f.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if f.focused != 0 {
		t.Errorf("up at top should clamp at 0, got %d", f.focused)
	}
}

func TestConfigForm_SpaceTogglesBoolean(t *testing.T) {
	f := sampleForm()
	if !f.toggleValue("Enable focus mode") {
		t.Fatal("fixture should start with toggle=true")
	}
	f, _ = f.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	if f.toggleValue("Enable focus mode") {
		t.Error("space should toggle boolean to false")
	}
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !f.toggleValue("Enable focus mode") {
		t.Error("enter on toggle should also toggle, back to true")
	}
}

func TestConfigForm_RightCyclesSelect(t *testing.T) {
	f := sampleForm()
	// Move to select field (index 2).
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	if got := f.selectValue("Theme"); got != "dark" {
		t.Fatalf("initial Theme = %q, want dark", got)
	}
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if got := f.selectValue("Theme"); got != "auto" {
		t.Errorf("after right, Theme = %q, want auto", got)
	}
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if got := f.selectValue("Theme"); got != "light" {
		t.Errorf("after second right, Theme = %q (expected wraparound to light)", got)
	}
}

func TestConfigForm_LeftCyclesSelectBackwards(t *testing.T) {
	f := sampleForm()
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if got := f.selectValue("Theme"); got != "light" {
		t.Errorf("after left from dark, Theme = %q, want light", got)
	}
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if got := f.selectValue("Theme"); got != "auto" {
		t.Errorf("after wraparound, Theme = %q, want auto", got)
	}
}

func TestConfigForm_EnterOnTextEntersEditMode(t *testing.T) {
	f := sampleForm()
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if f.fields[1].editing {
		t.Fatal("text field should not be editing initially")
	}
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !f.fields[1].editing {
		t.Error("enter on text field should enter edit mode")
	}
	// Esc exits edit mode without submitting form.
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if f.fields[1].editing {
		t.Error("esc in edit mode should exit edit mode")
	}
}

func TestConfigForm_EnterExitsEditModeOnText(t *testing.T) {
	f := sampleForm()
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // enter edit
	if !f.fields[1].editing {
		t.Fatal("expected editing=true")
	}
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // exit edit
	if f.fields[1].editing {
		t.Error("second enter should exit edit mode")
	}
}

func TestConfigForm_CtrlSEmitsSaveMsg(t *testing.T) {
	f := sampleForm()
	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("expected non-nil cmd for ctrl+s")
	}
	if _, ok := cmd().(configFormSaveMsg); !ok {
		t.Errorf("expected configFormSaveMsg, got %T", cmd())
	}
}

func TestConfigForm_EscEmitsCancelMsg(t *testing.T) {
	f := sampleForm()
	_, cmd := f.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("expected non-nil cmd for esc")
	}
	if _, ok := cmd().(configFormCancelMsg); !ok {
		t.Errorf("expected configFormCancelMsg, got %T", cmd())
	}
}

func TestConfigForm_AccessorsByLabel(t *testing.T) {
	f := sampleForm()
	if !f.toggleValue("Enable focus mode") {
		t.Error("toggleValue should read fixture value")
	}
	if got := f.textValue("Branch prefix"); got != "refrain/" {
		t.Errorf("textValue = %q, want %q", got, "refrain/")
	}
	if got := f.selectValue("Theme"); got != "dark" {
		t.Errorf("selectValue = %q, want %q", got, "dark")
	}
	// Missing label: zero values.
	if f.toggleValue("missing") || f.textValue("missing") != "" || f.selectValue("missing") != "" {
		t.Error("accessors on missing label should return zero values")
	}
}

func TestConfigForm_AddSelectClampsInvalidIndex(t *testing.T) {
	fields := addSelect(nil, "T", []string{"a", "b"}, 99)
	if fields[0].selected != 0 {
		t.Errorf("invalid index should clamp to 0, got %d", fields[0].selected)
	}
}

func TestConfigForm_AddSelectEmptyOptionsGetsPlaceholder(t *testing.T) {
	fields := addSelect(nil, "T", nil, 0)
	if len(fields[0].options) == 0 {
		t.Error("addSelect with nil options should provide a placeholder")
	}
}

func TestConfigForm_EmptyFormUpdateNoCrash(t *testing.T) {
	f := newConfigForm(nil, 60)
	if _, cmd := f.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Errorf("empty form should return nil cmd, got %v", cmd)
	}
}

func TestConfigForm_UnknownKey_NavMode_NoOp(t *testing.T) {
	f := sampleForm()
	before := struct {
		focused int
		toggle  bool
		text    string
		sel     string
	}{f.focused, f.toggleValue("Enable focus mode"), f.textValue("Branch prefix"), f.selectValue("Theme")}
	f, cmd := f.Update(tea.KeyPressMsg{Code: 'z', Text: "z"})
	if cmd != nil {
		t.Errorf("unknown key in nav mode produced cmd %T, want nil", cmd())
	}
	after := struct {
		focused int
		toggle  bool
		text    string
		sel     string
	}{f.focused, f.toggleValue("Enable focus mode"), f.textValue("Branch prefix"), f.selectValue("Theme")}
	if before != after {
		t.Errorf("unknown key changed state: before=%+v after=%+v", before, after)
	}
}

func TestConfigForm_PrintableKey_EditMode_AppendsToTextInput(t *testing.T) {
	f := sampleForm()
	// Focus the text field and enter edit mode.
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !f.fields[1].editing {
		t.Fatal("test prereq: text field should be in edit mode")
	}
	before := f.textValue("Branch prefix")
	f, _ = f.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if got := f.textValue("Branch prefix"); got == before {
		t.Errorf("text value unchanged after typing 'x': still %q", got)
	}
}

func TestConfigForm_LeftRightOnNonSelect_NoOp(t *testing.T) {
	// Focused on the toggle field (index 0). Left/right should not toggle it
	// — that's the job of space/enter.
	f := sampleForm()
	if !f.toggleValue("Enable focus mode") {
		t.Fatal("test prereq: toggle should start true")
	}
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if !f.toggleValue("Enable focus mode") {
		t.Error("right arrow on toggle field should not change its value")
	}
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if !f.toggleValue("Enable focus mode") {
		t.Error("left arrow on toggle field should not change its value")
	}
	// And on a text field (index 1): also a no-op.
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	before := f.textValue("Branch prefix")
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if got := f.textValue("Branch prefix"); got != before {
		t.Errorf("right arrow on text field changed value from %q to %q", before, got)
	}
}

func TestConfigForm_CtrlS_InEditMode_NotSubmitted(t *testing.T) {
	// In edit mode the textinput swallows everything including ctrl+s — the
	// outer save shortcut is intentionally only active in nav mode.
	f := sampleForm()
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !f.fields[1].editing {
		t.Fatal("test prereq: edit mode")
	}
	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd != nil {
		if _, bad := cmd().(configFormSaveMsg); bad {
			t.Error("ctrl+s should not save while in edit mode")
		}
	}
}

func TestConfigForm_EscInEditMode_DoesNotEmitCancel(t *testing.T) {
	// Esc in edit mode only blurs the textinput; it must NOT emit
	// configFormCancelMsg or the user would unintentionally drop their edits.
	f := sampleForm()
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	f, cmd := f.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd != nil {
		if _, bad := cmd().(configFormCancelMsg); bad {
			t.Error("esc in edit mode should NOT emit configFormCancelMsg")
		}
	}
	if f.fields[1].editing {
		t.Error("esc should exit edit mode")
	}
}

func TestConfigForm_KeysDeferToTextInputInEditMode(t *testing.T) {
	// In edit mode, j/k/h/l are literal characters in the textinput, not
	// navigation. Pin so they don't silently move focus.
	f := sampleForm()
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	f, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	before := f.focused
	f, _ = f.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if f.focused != before {
		t.Errorf("'j' in edit mode moved focus: before=%d after=%d", before, f.focused)
	}
	if got := f.textValue("Branch prefix"); got == "refrain/" {
		// Text should have changed by 'j' insertion.
		t.Errorf("'j' in edit mode did not modify text input: %q", got)
	}
}
