package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/devenjarvis/refrain/internal/config"
)

// All key/navigation behaviour for the underlying configForm is exercised by
// configform_test.go; these tests cover the globalConfigModel wrapper —
// settings extraction, save/cancel message wiring, and view rendering.

func newGlobalConfigForTest() globalConfigModel {
	gs := &config.GlobalSettings{}
	// Seed a couple of values so extractSettings has something to round-trip.
	audio := false
	bypass := true
	branch := "main"
	width := 42
	gs.AudioEnabled = &audio
	gs.BypassPermissions = &bypass
	gs.DefaultBranch = &branch
	gs.SidebarWidth = &width
	return newGlobalConfigModel(gs, 120, 40)
}

func TestGlobalConfig_NewSeedsFromSettings(t *testing.T) {
	m := newGlobalConfigForTest()
	if got := m.form.toggleValue("Audio Enabled"); got {
		t.Errorf("Audio Enabled = true; want false (seeded)")
	}
	if got := m.form.toggleValue("Bypass Permissions"); !got {
		t.Errorf("Bypass Permissions = false; want true (seeded)")
	}
	if got := m.form.textValue("Default Branch"); got != "main" {
		t.Errorf("Default Branch = %q, want %q", got, "main")
	}
	if got := m.form.textValue("Sidebar Width"); got != "42" {
		t.Errorf("Sidebar Width = %q, want %q", got, "42")
	}
}

func TestGlobalConfig_NewWithNilUsesDefaults(t *testing.T) {
	m := newGlobalConfigModel(nil, 120, 40)
	// Nothing seeded — toggles fall back to the package defaults, text fields
	// to empty strings. We mostly care that this does not panic and gives us
	// a navigable form.
	if len(m.form.fields) == 0 {
		t.Fatal("nil settings should still produce a populated form")
	}
}

// pressGlobalConfigKey simulates the tea Update→cmd→Update round-trip that
// translates a configFormSaveMsg / configFormCancelMsg from the underlying
// form into the wrapper's globalConfig{Save,Cancel}Msg.
func pressGlobalConfigKey(t *testing.T, m globalConfigModel, key tea.KeyPressMsg) (globalConfigModel, tea.Msg) {
	t.Helper()
	m2, cmd := m.Update(key)
	if cmd == nil {
		return m2, nil
	}
	inner := cmd()
	m3, cmd2 := m2.Update(inner)
	if cmd2 == nil {
		return m3, inner
	}
	return m3, cmd2()
}

func TestGlobalConfig_CtrlS_EmitsSaveMsgWithExtractedSettings(t *testing.T) {
	m := newGlobalConfigForTest()
	_, msg := pressGlobalConfigKey(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	got, ok := msg.(globalConfigSaveMsg)
	if !ok {
		t.Fatalf("got %T, want globalConfigSaveMsg (form should translate via wrapper)", msg)
	}
	if got.settings == nil {
		t.Fatal("save msg should carry non-nil settings")
	}
	if got.settings.AudioEnabled == nil || *got.settings.AudioEnabled {
		t.Errorf("AudioEnabled = %v, want pointer to false", got.settings.AudioEnabled)
	}
	if got.settings.BypassPermissions == nil || !*got.settings.BypassPermissions {
		t.Errorf("BypassPermissions = %v, want pointer to true", got.settings.BypassPermissions)
	}
	if got.settings.DefaultBranch == nil || *got.settings.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %v, want pointer to %q", got.settings.DefaultBranch, "main")
	}
}

func TestGlobalConfig_Esc_EmitsCancelMsg(t *testing.T) {
	m := newGlobalConfigForTest()
	_, msg := pressGlobalConfigKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if _, ok := msg.(globalConfigCancelMsg); !ok {
		t.Fatalf("got %T, want globalConfigCancelMsg", msg)
	}
}

func TestGlobalConfig_NavigationKeyForwardedToForm(t *testing.T) {
	m := newGlobalConfigForTest()
	before := m.form.focused
	m2, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if cmd != nil {
		t.Errorf("navigation should not emit a cmd, got %T", cmd())
	}
	if m2.form.focused <= before {
		t.Errorf("focused did not advance: before=%d after=%d", before, m2.form.focused)
	}
}

func TestGlobalConfig_ToggleFieldRoundTripsThroughSave(t *testing.T) {
	m := newGlobalConfigForTest()
	// Audio Enabled is field index 0 and was seeded to false. Toggle it.
	if m.form.toggleValue("Audio Enabled") {
		t.Fatal("test prereq: Audio Enabled should start false")
	}
	m2, _ := m.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	if !m2.form.toggleValue("Audio Enabled") {
		t.Error("space should have toggled Audio Enabled to true")
	}
	_, msg := pressGlobalConfigKey(t, m2, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	got, ok := msg.(globalConfigSaveMsg)
	if !ok {
		t.Fatalf("got %T, want globalConfigSaveMsg", msg)
	}
	if got.settings.AudioEnabled == nil || !*got.settings.AudioEnabled {
		t.Errorf("AudioEnabled after toggle+save = %v, want true", got.settings.AudioEnabled)
	}
}

func TestGlobalConfig_UnknownKey_NoOp(t *testing.T) {
	m := newGlobalConfigForTest()
	before := m.form.focused
	m2, cmd := m.Update(tea.KeyPressMsg{Code: 'z', Text: "z"})
	if cmd != nil {
		t.Errorf("unknown key produced cmd %T, want nil", cmd())
	}
	if m2.form.focused != before {
		t.Errorf("unknown key changed focus: before=%d after=%d", before, m2.form.focused)
	}
}

func TestGlobalConfig_View_ContainsTitleAndHint(t *testing.T) {
	m := newGlobalConfigForTest()
	v := m.View()
	if !strings.Contains(v, "Global Settings") {
		t.Error("view missing 'Global Settings' title")
	}
	if !strings.Contains(v, "ctrl+s") || !strings.Contains(v, "esc") {
		t.Error("view missing keybinding hints")
	}
}

func TestGlobalConfig_View_RendersOnAnyValidSize(t *testing.T) {
	// Ensure View doesn't panic for narrow/tall terminals.
	for _, sz := range []struct{ w, h int }{
		{60, 20}, {80, 24}, {120, 40}, {200, 60},
	} {
		m := newGlobalConfigModel(nil, sz.w, sz.h)
		_ = m.View()
	}
}
