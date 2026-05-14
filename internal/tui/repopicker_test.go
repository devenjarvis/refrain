package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/config"
)

// fixtureRepoPicker builds a populated repoPickerModel without touching disk.
func fixtureRepoPicker() repoPickerModel {
	repos := []config.Repo{
		{Path: "/Code/alpha", Name: "alpha"},
		{Path: "/Code/beta", Name: "beta"},
		{Path: "/Code/gamma", Name: "gamma"},
	}
	counts := map[string]int{
		"/Code/alpha": 2,
		"/Code/gamma": 1,
	}
	m := newRepoPickerModel()
	m.width = 100
	m.height = 24
	m.setRepos(repos, counts, "/Code/beta")
	return m
}

func TestRepoPicker_SetReposSelectsInitialPath(t *testing.T) {
	m := fixtureRepoPicker()
	// initialPath was "/Code/beta", which is index 1 in the repos slice and
	// also at index 1 of filtered (no filter applied).
	if m.selected != 1 {
		t.Errorf("selected = %d, want 1 (the entry for /Code/beta)", m.selected)
	}
	// filtered should hold all 3 repos plus the add-repo sentinel.
	if len(m.filtered) != 4 {
		t.Fatalf("filtered length = %d, want 4 (3 repos + add-repo)", len(m.filtered))
	}
	if m.filtered[3] != repoPickerAddRepoIdx {
		t.Errorf("last filtered entry = %d, want repoPickerAddRepoIdx", m.filtered[3])
	}
}

func TestRepoPicker_FilterNarrowsAndClampsCursor(t *testing.T) {
	m := fixtureRepoPicker()
	// Move cursor to position 2 (gamma).
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.selected != 2 {
		t.Fatalf("expected selected=2 after one down, got %d", m.selected)
	}
	// Filter "be" matches only beta. The filtered slice should hold beta + add-repo entry.
	m, _ = m.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if len(m.filtered) != 2 {
		t.Errorf("after filter 'be', expected 2 entries (beta + add-repo), got %d", len(m.filtered))
	}
	if m.selected >= len(m.filtered) {
		t.Errorf("cursor=%d not clamped within filtered=%d", m.selected, len(m.filtered))
	}
}

func TestRepoPicker_FilterAlsoMatchesPath(t *testing.T) {
	m := fixtureRepoPicker()
	// Path-only match: "Code" appears in every repo path. After filter "Code"
	// every real repo + add-repo entry should be retained.
	m, _ = m.Update(tea.KeyPressMsg{Code: 'C', Text: "C"})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if len(m.filtered) != 4 {
		t.Errorf("after filter 'Code' (path match), expected 4 entries, got %d", len(m.filtered))
	}
}

func TestRepoPicker_EnterOnRealRepoEmitsSelectMsg(t *testing.T) {
	m := fixtureRepoPicker()
	// Cursor starts at /Code/beta (initialPath).
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd from enter on real repo row")
	}
	out := cmd()
	sel, ok := out.(repoPickerSelectMsg)
	if !ok {
		t.Fatalf("expected repoPickerSelectMsg, got %T", out)
	}
	if sel.path != "/Code/beta" {
		t.Errorf("select path = %q, want %q", sel.path, "/Code/beta")
	}
}

func TestRepoPicker_EnterOnAddRepoEntryEmitsAddMsg(t *testing.T) {
	m := fixtureRepoPicker()
	// Move cursor to the add-repo sentinel (last filtered entry, index 3).
	for range 3 {
		m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if m.selected != len(m.filtered)-1 {
		t.Fatalf("expected cursor on last entry, got selected=%d / filtered=%d", m.selected, len(m.filtered))
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd from enter on add-repo entry")
	}
	if _, ok := cmd().(repoPickerAddRepoMsg); !ok {
		t.Errorf("expected repoPickerAddRepoMsg, got %T", cmd())
	}
}

func TestRepoPicker_AHotkeyEmitsAddMsgWhenFilterEmpty(t *testing.T) {
	m := fixtureRepoPicker()
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if cmd == nil {
		t.Fatal("expected non-nil cmd from 'a' hotkey with empty filter")
	}
	if _, ok := cmd().(repoPickerAddRepoMsg); !ok {
		t.Errorf("expected repoPickerAddRepoMsg, got %T", cmd())
	}
}

func TestRepoPicker_AKeyAppendsToFilterWhenFilterActive(t *testing.T) {
	m := fixtureRepoPicker()
	// Start a filter with a non-'a' character.
	m, _ = m.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	if m.filter != "l" {
		t.Fatalf("filter = %q, want %q", m.filter, "l")
	}
	// Now typing 'a' should append to filter, not emit add-repo.
	m, cmd := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if cmd != nil {
		if _, ok := cmd().(repoPickerAddRepoMsg); ok {
			t.Error("'a' with active filter should NOT emit repoPickerAddRepoMsg")
		}
	}
	if m.filter != "la" {
		t.Errorf("filter = %q, want %q", m.filter, "la")
	}
}

func TestRepoPicker_EscEmitsCancelMsg(t *testing.T) {
	m := fixtureRepoPicker()
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("expected non-nil cmd from esc")
	}
	if _, ok := cmd().(repoPickerCancelMsg); !ok {
		t.Errorf("expected repoPickerCancelMsg, got %T", cmd())
	}
}

func TestRepoPicker_BackspaceDeletesFilterChar(t *testing.T) {
	m := fixtureRepoPicker()
	m, _ = m.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if m.filter != "be" {
		t.Fatalf("filter = %q, want %q", m.filter, "be")
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.filter != "b" {
		t.Errorf("after backspace, filter = %q, want %q", m.filter, "b")
	}
}

func TestRepoPicker_AgentCountsRenderInRows(t *testing.T) {
	m := fixtureRepoPicker()
	out := m.View()
	// alpha has 2 active agents; gamma has 1; beta has 0 (em dash).
	if !strings.Contains(out, "2 active") {
		t.Errorf("expected '2 active' in view output, got:\n%s", out)
	}
	if !strings.Contains(out, "1 active") {
		t.Errorf("expected '1 active' in view output, got:\n%s", out)
	}
	if !strings.Contains(out, "—") {
		t.Errorf("expected em-dash for zero-count repo, got:\n%s", out)
	}
}

func TestRepoPicker_AddRepoRowAlwaysPresent(t *testing.T) {
	m := fixtureRepoPicker()
	// Filter to a string that matches no repo.
	for _, ch := range "zzznomatch" {
		m, _ = m.Update(tea.KeyPressMsg{Code: rune(ch), Text: string(ch)})
	}
	if len(m.filtered) != 1 {
		t.Errorf("expected only add-repo entry, got %d filtered entries", len(m.filtered))
	}
	if m.filtered[0] != repoPickerAddRepoIdx {
		t.Errorf("expected sole entry to be add-repo sentinel, got %d", m.filtered[0])
	}
}

// fixtureRepoPickerManage returns a manage-mode picker seeded with the same
// three repos as fixtureRepoPicker, with the cursor on /Code/beta (index 1).
func fixtureRepoPickerManage() repoPickerModel {
	m := fixtureRepoPicker()
	m.SetMode(repoPickerModeManage)
	return m
}

func TestRepoPicker_ManageMode_EnterEmitsSwitchActive(t *testing.T) {
	m := fixtureRepoPickerManage()
	// cursor is on /Code/beta (selected=1 from initialPath)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd from enter in manage mode")
	}
	msg, ok := cmd().(repoPickerSwitchActiveMsg)
	if !ok {
		t.Fatalf("expected repoPickerSwitchActiveMsg, got %T", cmd())
	}
	if msg.path != "/Code/beta" {
		t.Errorf("path = %q, want /Code/beta", msg.path)
	}
}

func TestRepoPicker_ManageMode_SOpensEditSettings(t *testing.T) {
	m := fixtureRepoPickerManage()
	_, cmd := m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	if cmd == nil {
		t.Fatal("expected non-nil cmd from s in manage mode")
	}
	msg, ok := cmd().(repoPickerEditSettingsMsg)
	if !ok {
		t.Fatalf("expected repoPickerEditSettingsMsg, got %T", cmd())
	}
	if msg.path != "/Code/beta" {
		t.Errorf("path = %q, want /Code/beta", msg.path)
	}
}

func TestRepoPicker_ManageMode_DTwiceEmitsRemove(t *testing.T) {
	m := fixtureRepoPickerManage()
	// first d: sets pendingRemoveIdx
	m, cmd := m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if cmd != nil {
		t.Errorf("first d should not emit a cmd, got %T", cmd())
	}
	if m.pendingRemoveIdx != m.selected {
		t.Errorf("after first d, pendingRemoveIdx=%d, want selected=%d", m.pendingRemoveIdx, m.selected)
	}
	// second d on same row: emits remove
	_, cmd = m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if cmd == nil {
		t.Fatal("second d should emit a cmd")
	}
	msg, ok := cmd().(repoPickerRemoveMsg)
	if !ok {
		t.Fatalf("expected repoPickerRemoveMsg, got %T", cmd())
	}
	if msg.path != "/Code/beta" {
		t.Errorf("path = %q, want /Code/beta", msg.path)
	}
}

func TestRepoPicker_ManageMode_DOnDifferentRowResetsConfirm(t *testing.T) {
	m := fixtureRepoPickerManage()
	// First d on /Code/beta (selected=1)
	m, _ = m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if m.pendingRemoveIdx != 1 {
		t.Fatalf("pendingRemoveIdx = %d, want 1", m.pendingRemoveIdx)
	}
	// Move down to /Code/gamma (selected=2): should clear pendingRemoveIdx
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.pendingRemoveIdx != -1 {
		t.Errorf("navigation should reset pendingRemoveIdx to -1, got %d", m.pendingRemoveIdx)
	}
	// d again on new row: sets a new pending (not emit)
	m, cmd := m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if cmd != nil {
		t.Errorf("first d on new row should not emit, got cmd")
	}
	if m.pendingRemoveIdx != 2 {
		t.Errorf("pendingRemoveIdx = %d, want 2", m.pendingRemoveIdx)
	}
}

func TestRepoPicker_ManageMode_FilterEmptyGatesSAndD(t *testing.T) {
	m := fixtureRepoPickerManage()
	// Start typing a filter char so filter is non-empty.
	m, _ = m.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	if m.filter == "" {
		t.Fatal("filter should be non-empty after typing b")
	}
	// s should append to filter, not emit edit-settings
	m, cmd := m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	if cmd != nil {
		if _, ok := cmd().(repoPickerEditSettingsMsg); ok {
			t.Error("s with active filter should NOT emit repoPickerEditSettingsMsg")
		}
	}
	if m.filter != "bs" {
		t.Errorf("filter = %q, want \"bs\"", m.filter)
	}
	// d should also append to filter, not set pendingRemoveIdx
	m, _ = m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if m.pendingRemoveIdx != -1 {
		t.Errorf("d with active filter should not set pendingRemoveIdx, got %d", m.pendingRemoveIdx)
	}
	if m.filter != "bsd" {
		t.Errorf("filter = %q, want \"bsd\"", m.filter)
	}
}

func TestRepoPicker_NavigationClampsAtBounds(t *testing.T) {
	m := fixtureRepoPicker()
	// Push past the top.
	m.selected = 0
	m, _ = m.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if m.selected != 0 {
		t.Errorf("up at top should clamp at 0, got %d", m.selected)
	}
	// Push past the bottom.
	for range 100 {
		m, _ = m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	last := len(m.filtered) - 1
	if m.selected != last {
		t.Errorf("down past bottom should clamp at %d, got %d", last, m.selected)
	}
}
