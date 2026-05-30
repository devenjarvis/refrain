package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/config"
)

func sampleChecks() []config.ValidationCheck {
	return []config.ValidationCheck{
		{Name: "Tests", Command: "go test ./..."},
		{Name: "Lint", Command: "golangci-lint run"},
	}
}

func TestRepoChecks_NewSeedsCopy(t *testing.T) {
	seed := sampleChecks()
	m := newRepoChecksModel("myrepo", seed)
	if len(m.checks) != 2 {
		t.Fatalf("checks len = %d, want 2", len(m.checks))
	}
	seed[0].Name = "MUTATED"
	if m.checks[0].Name != "Tests" {
		t.Error("model checks should be independent of caller's slice")
	}
}

func TestRepoChecks_DownClampsAtLast(t *testing.T) {
	m := newRepoChecksModel("r", sampleChecks())
	for range 5 {
		m, _ = m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1", m.cursor)
	}
}

func TestRepoChecks_UpClampsAtZero(t *testing.T) {
	m := newRepoChecksModel("r", sampleChecks())
	m, _ = m.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0", m.cursor)
	}
}

func TestRepoChecks_AddBeginsEditOnNewRow(t *testing.T) {
	m := newRepoChecksModel("r", nil)
	m, _ = m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if !m.editing {
		t.Fatal("expected editing=true after 'a'")
	}
	if len(m.checks) != 1 {
		t.Fatalf("len(checks) = %d, want 1 after 'a'", len(m.checks))
	}
	if m.editIdx != 0 || m.cursor != 0 {
		t.Errorf("editIdx=%d cursor=%d, want both 0", m.editIdx, m.cursor)
	}
}

func TestRepoChecks_EditCommitsValuesAndExitsEditMode(t *testing.T) {
	m := newRepoChecksModel("r", nil)
	m, _ = m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m.nameInput.SetValue("Smoke")
	m.cmdInput.SetValue("./smoke.sh")
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.editing {
		t.Error("editing should be false after committing")
	}
	if m.checks[0].Name != "Smoke" || m.checks[0].Command != "./smoke.sh" {
		t.Errorf("checks[0] = %+v, want {Smoke ./smoke.sh}", m.checks[0])
	}
}

func TestRepoChecks_EditEscDiscardsBlankRow(t *testing.T) {
	m := newRepoChecksModel("r", sampleChecks())
	m, _ = m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if len(m.checks) != 3 {
		t.Fatalf("setup: len = %d, want 3", len(m.checks))
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.editing {
		t.Error("editing should be false after esc")
	}
	if len(m.checks) != 2 {
		t.Errorf("blank row should be dropped, got len = %d", len(m.checks))
	}
}

func TestRepoChecks_EditEscPreservesNonBlankRow(t *testing.T) {
	m := newRepoChecksModel("r", sampleChecks())
	m.beginEdit(0)
	m.nameInput.SetValue("Edited but cancelled")
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.checks[0].Name != "Tests" {
		t.Errorf("esc should not commit, got %q", m.checks[0].Name)
	}
	if len(m.checks) != 2 {
		t.Errorf("non-blank row should be preserved, len = %d", len(m.checks))
	}
}

func TestRepoChecks_DeleteRemovesAndClampsCursor(t *testing.T) {
	m := newRepoChecksModel("r", sampleChecks())
	m.cursor = 1
	m, _ = m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if len(m.checks) != 1 {
		t.Errorf("after delete, len = %d, want 1", len(m.checks))
	}
	if m.cursor != 0 {
		t.Errorf("cursor should clamp to last row, got %d", m.cursor)
	}
	// Delete the last remaining row -> cursor clamps to 0 with empty slice.
	m, _ = m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if len(m.checks) != 0 {
		t.Errorf("after second delete, len = %d, want 0", len(m.checks))
	}
	if m.cursor != 0 {
		t.Errorf("cursor on empty list should be 0, got %d", m.cursor)
	}
}

func TestRepoChecks_DeleteOnEmptyIsNoOp(t *testing.T) {
	m := newRepoChecksModel("r", nil)
	m, _ = m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if len(m.checks) != 0 {
		t.Errorf("delete on empty should keep len 0, got %d", len(m.checks))
	}
}

func TestRepoChecks_CtrlSEmitsSaveMsgWithCurrentList(t *testing.T) {
	m := newRepoChecksModel("r", sampleChecks())
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+s should return a non-nil cmd")
	}
	msg := cmd()
	save, ok := msg.(repoChecksSaveMsg)
	if !ok {
		t.Fatalf("expected repoChecksSaveMsg, got %T", msg)
	}
	if len(save.Checks) != 2 || save.Checks[0].Name != "Tests" {
		t.Errorf("save payload = %+v, want sample list", save.Checks)
	}
}

func TestRepoChecks_EscEmitsCancelMsg(t *testing.T) {
	m := newRepoChecksModel("r", sampleChecks())
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("esc should return a non-nil cmd")
	}
	if _, ok := cmd().(repoChecksCancelMsg); !ok {
		t.Errorf("expected repoChecksCancelMsg, got %T", cmd())
	}
}

func TestRepoChecks_TabSwitchesInputFocus(t *testing.T) {
	m := newRepoChecksModel("r", sampleChecks())
	m.beginEdit(0)
	if m.activeInput != repoChecksInputName {
		t.Fatalf("initial activeInput = %v, want name", m.activeInput)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.activeInput != repoChecksInputCommand {
		t.Errorf("after tab, activeInput = %v, want command", m.activeInput)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if m.activeInput != repoChecksInputName {
		t.Errorf("after shift+tab, activeInput = %v, want name", m.activeInput)
	}
}

func TestRepoChecks_ViewEmptyStateMentionsAdd(t *testing.T) {
	m := newRepoChecksModel("r", nil)
	view := m.View()
	if !strings.Contains(view, "No checks yet") {
		t.Errorf("empty view should hint that none are configured, got:\n%s", view)
	}
}

func TestRepoChecks_HintFormatsCount(t *testing.T) {
	if got := repoChecksHint(nil); !strings.Contains(got, "none configured") {
		t.Errorf("nil list hint = %q, want to contain 'none configured'", got)
	}
	if got := repoChecksHint([]config.ValidationCheck{{Name: "x", Command: "y"}}); !strings.Contains(got, "1 configured") {
		t.Errorf("single hint = %q, want to contain '1 configured'", got)
	}
	if got := repoChecksHint(sampleChecks()); !strings.Contains(got, "2 configured") {
		t.Errorf("two hint = %q, want to contain '2 configured'", got)
	}
}

func TestFilterValidationChecks_DropsBlankRows(t *testing.T) {
	in := []config.ValidationCheck{
		{Name: "  Tests  ", Command: "go test ./..."},
		{Name: "", Command: "ignored"},
		{Name: "ignored", Command: ""},
		{Name: "   ", Command: "   "},
		{Name: "Lint", Command: "  golangci-lint run  "},
	}
	out := filterValidationChecks(in)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2 (blanks dropped)", len(out))
	}
	if out[0].Name != "Tests" || out[0].Command != "go test ./..." {
		t.Errorf("out[0] = %+v, want trimmed Tests row", out[0])
	}
	if out[1].Name != "Lint" || out[1].Command != "golangci-lint run" {
		t.Errorf("out[1] = %+v, want trimmed Lint row", out[1])
	}
}
