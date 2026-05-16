package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// fixtureBranchPickerLoaded returns a model populated as if loadBranchPickerData
// had completed, so tests can drive Update without hitting git or GitHub.
func fixtureBranchPickerLoaded() branchPickerModel {
	m := newBranchPickerModel()
	m.width = 80
	m.height = 24
	msg := branchPickerDataMsg{
		prs: []branchPickerItem{
			{kind: "pr", branch: "feature-a", baseBranch: "main", prNumber: 42, prTitle: "Add feature A"},
		},
		local: []branchPickerItem{
			{kind: "local", branch: "bugfix-123"},
			{kind: "local", branch: "experiment"},
		},
		remote: []branchPickerItem{
			{kind: "remote", branch: "origin/foo"},
		},
	}
	m, _ = m.Update(msg)
	return m
}

func TestBranchPicker_LoadingThenLoaded(t *testing.T) {
	m := newBranchPickerModel()
	if !m.loading {
		t.Error("expected initial state to be loading=true")
	}
	m, _ = m.Update(branchPickerDataMsg{
		local: []branchPickerItem{{kind: "local", branch: "main"}},
	})
	if m.loading {
		t.Error("expected loading=false after data msg")
	}
	if len(m.items) != 1 {
		t.Errorf("expected 1 item after load, got %d", len(m.items))
	}
}

func TestBranchPicker_LoadError(t *testing.T) {
	m := newBranchPickerModel()
	m, _ = m.Update(branchPickerDataMsg{err: errStub("network down")})
	if m.loadErr != "network down" {
		t.Errorf("loadErr = %q, want %q", m.loadErr, "network down")
	}
	if m.loading {
		t.Error("loading should clear after error")
	}
}

func TestBranchPicker_DownKeyMovesCursor(t *testing.T) {
	m := fixtureBranchPickerLoaded()
	if m.selected != 0 {
		t.Fatalf("initial selected = %d, want 0", m.selected)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if m.selected != 1 {
		t.Errorf("after 'j', selected = %d, want 1", m.selected)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.selected != 2 {
		t.Errorf("after 'down', selected = %d, want 2", m.selected)
	}
}

func TestBranchPicker_UpKeyClampsAtZero(t *testing.T) {
	m := fixtureBranchPickerLoaded()
	m, _ = m.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if m.selected != 0 {
		t.Errorf("up at top should clamp at 0, got %d", m.selected)
	}
}

func TestBranchPicker_DownKeyClampsAtEnd(t *testing.T) {
	m := fixtureBranchPickerLoaded()
	for range 10 {
		m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	last := len(m.filtered) - 1
	if m.selected != last {
		t.Errorf("over-scrolled selected = %d, want %d", m.selected, last)
	}
}

func TestBranchPicker_FilterNarrowsList(t *testing.T) {
	m := fixtureBranchPickerLoaded()
	original := len(m.filtered)
	if original < 4 {
		t.Fatalf("expected ≥4 items in fixture, got %d", original)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	if len(m.filtered) != 1 {
		t.Errorf("after filter 'b', expected 1 match (bugfix-123), got %d", len(m.filtered))
	}
	if m.filter != "b" {
		t.Errorf("filter = %q, want %q", m.filter, "b")
	}
}

func TestBranchPicker_BackspaceClearsFilter(t *testing.T) {
	m := fixtureBranchPickerLoaded()
	m, _ = m.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.filter != "" {
		t.Errorf("filter after backspace = %q, want empty", m.filter)
	}
}

func TestBranchPicker_FilterMatchesPRTitle(t *testing.T) {
	m := fixtureBranchPickerLoaded()
	// Type "feature" — matches PR title and the 'feature-a' branch.
	for _, c := range "feature" {
		m, _ = m.Update(tea.KeyPressMsg{Code: rune(c), Text: string(c)})
	}
	if len(m.filtered) == 0 {
		t.Fatal("expected at least one match for 'feature'")
	}
	// PR with title "Add feature A" must be present.
	found := false
	for _, idx := range m.filtered {
		if m.items[idx].prTitle == "Add feature A" {
			found = true
			break
		}
	}
	if !found {
		t.Error("PR title match not in filtered set")
	}
}

func TestBranchPicker_EnterEmitsSelectMsg(t *testing.T) {
	m := fixtureBranchPickerLoaded()
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd for enter")
	}
	out := cmd()
	sel, ok := out.(branchPickerSelectMsg)
	if !ok {
		t.Fatalf("expected branchPickerSelectMsg, got %T", out)
	}
	if sel.item.branch != "feature-a" {
		t.Errorf("selected item branch = %q, want %q", sel.item.branch, "feature-a")
	}
}

func TestBranchPicker_EscEmitsCancelMsg(t *testing.T) {
	m := fixtureBranchPickerLoaded()
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("expected non-nil cmd for esc")
	}
	if _, ok := cmd().(branchPickerCancelMsg); !ok {
		t.Errorf("expected branchPickerCancelMsg, got %T", cmd())
	}
}

func TestBranchPicker_EnterWithEmptyListIsNoop(t *testing.T) {
	m := newBranchPickerModel()
	m, _ = m.Update(branchPickerDataMsg{}) // no items
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Errorf("expected nil cmd when list is empty, got %v", cmd)
	}
}

func TestBranchPicker_UpArrow_ClampsAtZero(t *testing.T) {
	// The "k" mapping is tested; verify the named-key alias "up" too so a
	// future refactor that splits the cases doesn't lose one.
	m := fixtureBranchPickerLoaded()
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.selected != 0 {
		t.Errorf("up at top should clamp at 0, got %d", m.selected)
	}
}

func TestBranchPicker_UpArrow_MovesCursor(t *testing.T) {
	m := fixtureBranchPickerLoaded()
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.selected != 2 {
		t.Fatalf("test prereq: selected=%d, want 2", m.selected)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.selected != 1 {
		t.Errorf("after up, selected = %d, want 1", m.selected)
	}
}

func TestBranchPicker_BackspaceWithEmptyFilter_NoOp(t *testing.T) {
	m := fixtureBranchPickerLoaded()
	before := struct {
		filter string
		sel    int
		count  int
	}{m.filter, m.selected, len(m.filtered)}
	m2, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if cmd != nil {
		t.Errorf("backspace with empty filter produced cmd %T, want nil", cmd())
	}
	after := struct {
		filter string
		sel    int
		count  int
	}{m2.filter, m2.selected, len(m2.filtered)}
	if before != after {
		t.Errorf("backspace with empty filter changed state: before=%+v after=%+v", before, after)
	}
}

func TestBranchPicker_UnknownControlKey_NoOp(t *testing.T) {
	m := fixtureBranchPickerLoaded()
	before := struct {
		filter string
		sel    int
	}{m.filter, m.selected}
	m2, cmd := m.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Errorf("ctrl+w produced cmd %T, want nil", cmd())
	}
	after := struct {
		filter string
		sel    int
	}{m2.filter, m2.selected}
	if before != after {
		t.Errorf("ctrl+w changed state: before=%+v after=%+v", before, after)
	}
}

func TestBranchPicker_LoadingSwallowsKeys(t *testing.T) {
	// While loading, the picker has no items. Confirm key presses other than
	// esc don't crash and don't pre-populate state.
	m := newBranchPickerModel()
	m.width = 80
	m.height = 24
	if !m.loading {
		t.Fatal("test prereq: should start loading")
	}
	m, cmd := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if cmd != nil {
		t.Errorf("j during loading produced cmd %T, want nil", cmd())
	}
	if m.selected != 0 {
		t.Errorf("selected = %d during loading, want 0", m.selected)
	}
}

// errStub is a tiny helper so we don't pull in errors.New everywhere.
type errStub string

func (e errStub) Error() string { return string(e) }
