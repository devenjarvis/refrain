package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// fixtureFileBrowserAt builds a fileBrowserModel rooted at dir without
// touching $HOME, so tests don't depend on the developer's filesystem.
func fixtureFileBrowserAt(dir string) fileBrowserModel {
	m := fileBrowserModel{
		currentDir: dir,
		width:      80,
		height:     24,
	}
	m.entries = loadEntries(dir, false)
	m.applyFilter()
	m.refreshGitStatus()
	return m
}

func makeGitRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = path
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "init"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = path
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup %v: %v\n%s", args, err, out)
		}
	}
}

func TestFileBrowser_LoadEntriesSkipsFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "beta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "regular.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := fixtureFileBrowserAt(root)
	if len(m.entries) != 2 {
		t.Errorf("expected 2 dir entries (regular file skipped), got %d", len(m.entries))
	}
}

func TestFileBrowser_HiddenDirsHiddenByDefault(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".hidden", "visible"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m := fixtureFileBrowserAt(root)
	if len(m.entries) != 1 || m.entries[0].Name() != "visible" {
		names := make([]string, 0, len(m.entries))
		for _, e := range m.entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected only [visible], got %v", names)
	}
}

func TestFileBrowser_DotTogglesHiddenWhenFilterEmpty(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".hidden", "visible"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m := fixtureFileBrowserAt(root)
	m, _ = m.Update(tea.KeyPressMsg{Code: '.', Text: "."})
	if !m.showHidden {
		t.Error("expected showHidden=true after '.' with empty filter")
	}
	if len(m.entries) != 2 {
		t.Errorf("expected 2 entries when hidden shown, got %d", len(m.entries))
	}
}

func TestFileBrowser_DotIsFilterCharWhenFilterActive(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"v2.0", "v3"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m := fixtureFileBrowserAt(root)
	m, _ = m.Update(tea.KeyPressMsg{Code: 'v', Text: "v"})
	m, _ = m.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	// Now filter is "v2"; '.' should append, not toggle.
	prevHidden := m.showHidden
	m, _ = m.Update(tea.KeyPressMsg{Code: '.', Text: "."})
	if m.showHidden != prevHidden {
		t.Error("'.' with filter active should not toggle showHidden")
	}
	if m.filter != "v2." {
		t.Errorf("filter = %q, want %q", m.filter, "v2.")
	}
}

func TestFileBrowser_BackspaceAscendsWhenFilterEmpty(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "subdir")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	m := fixtureFileBrowserAt(child)
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.currentDir != root {
		t.Errorf("expected ascend to %s, got %s", root, m.currentDir)
	}
}

func TestFileBrowser_BackspaceDeletesFilterChar(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := fixtureFileBrowserAt(root)
	m, _ = m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.filter != "" {
		t.Errorf("filter should be cleared, got %q", m.filter)
	}
	if m.currentDir != root {
		t.Error("backspace with non-empty filter should not ascend")
	}
}

func TestFileBrowser_EnterOnNonRepoDescends(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	m := fixtureFileBrowserAt(root)
	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Errorf("descending into non-repo should return nil cmd, got %v", cmd)
	}
	if m.currentDir != child {
		t.Errorf("expected currentDir = %s, got %s", child, m.currentDir)
	}
}

func TestFileBrowser_EnterOnGitRepoEmitsSelectMsg(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "myrepo")
	makeGitRepo(t, repo)

	m := fixtureFileBrowserAt(root)
	if !m.isGitRepo {
		t.Fatal("expected refreshGitStatus to detect git repo on selected entry")
	}

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd when entering on a git repo")
	}
	out := cmd()
	sel, ok := out.(fileBrowserSelectMsg)
	if !ok {
		t.Fatalf("expected fileBrowserSelectMsg, got %T", out)
	}
	if sel.path != repo {
		t.Errorf("select path = %q, want %q", sel.path, repo)
	}
}

func TestFileBrowser_EscEmitsCancel(t *testing.T) {
	root := t.TempDir()
	m := fixtureFileBrowserAt(root)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("expected non-nil cmd for esc")
	}
	if _, ok := cmd().(fileBrowserCancelMsg); !ok {
		t.Errorf("expected fileBrowserCancelMsg, got %T", cmd())
	}
}

func TestFileBrowser_EnterOnEmptyDirIsNoop(t *testing.T) {
	root := t.TempDir()
	m := fixtureFileBrowserAt(root) // no children
	prev := m.currentDir
	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Errorf("expected nil cmd when filtered list is empty, got %v", cmd)
	}
	if m.currentDir != prev {
		t.Errorf("currentDir changed unexpectedly: %s -> %s", prev, m.currentDir)
	}
}

func TestFileBrowser_JK_NavigateSelection(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m := fixtureFileBrowserAt(root)
	if m.selected != 0 {
		t.Fatalf("test prereq: selected=%d, want 0", m.selected)
	}
	// 'j' should advance the cursor.
	m, _ = m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if m.selected != 1 {
		t.Errorf("after 'j' selected = %d, want 1", m.selected)
	}
	// 'k' should retreat.
	m, _ = m.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if m.selected != 0 {
		t.Errorf("after 'k' selected = %d, want 0", m.selected)
	}
}

func TestFileBrowser_J_ClampsAtLastEntry(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m := fixtureFileBrowserAt(root)
	for range 5 {
		m, _ = m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	if m.selected != len(m.filtered)-1 {
		t.Errorf("selected=%d, want clamp at %d", m.selected, len(m.filtered)-1)
	}
}

func TestFileBrowser_K_ClampsAtZero(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := fixtureFileBrowserAt(root)
	for range 5 {
		m, _ = m.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	}
	if m.selected != 0 {
		t.Errorf("selected=%d, want 0 (clamped)", m.selected)
	}
}

func TestFileBrowser_DotRoundTripsShowHidden(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".hidden", "visible"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m := fixtureFileBrowserAt(root)
	m, _ = m.Update(tea.KeyPressMsg{Code: '.', Text: "."}) // toggle ON
	m, _ = m.Update(tea.KeyPressMsg{Code: '.', Text: "."}) // toggle OFF
	if m.showHidden {
		t.Error("two '.' presses should round-trip showHidden to false")
	}
	if len(m.entries) != 1 {
		t.Errorf("after toggle off, want 1 entry, got %d", len(m.entries))
	}
}

func TestFileBrowser_NonPrintableKey_NoOp(t *testing.T) {
	// ctrl+w is neither in the switch nor a printable filter char. The
	// fileBrowser falls through to default and key.String() returns "ctrl+w"
	// (len > 1) so the printable-filter branch is skipped. State must be
	// unchanged and cmd must be nil.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := fixtureFileBrowserAt(root)
	before := struct {
		dir, filter string
		sel         int
		hidden      bool
	}{m.currentDir, m.filter, m.selected, m.showHidden}
	m2, cmd := m.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Errorf("ctrl+w produced cmd %T, want nil", cmd())
	}
	after := struct {
		dir, filter string
		sel         int
		hidden      bool
	}{m2.currentDir, m2.filter, m2.selected, m2.showHidden}
	if before != after {
		t.Errorf("ctrl+w changed state: before=%+v after=%+v", before, after)
	}
}

func TestFileBrowser_BackspaceAtRoot_NoOp(t *testing.T) {
	// At the filesystem root, ascending is impossible. Pinned so that
	// backspace doesn't corrupt currentDir.
	root := string(filepath.Separator) // platform root
	m := fixtureFileBrowserAt(root)
	m2, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if cmd != nil {
		t.Errorf("backspace at root produced cmd %T, want nil", cmd())
	}
	if m2.currentDir != root {
		t.Errorf("currentDir changed from root: %q", m2.currentDir)
	}
}

func TestFileBrowser_FilterNarrowsAndClampsCursor(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m := fixtureFileBrowserAt(root)
	if len(m.filtered) != 3 {
		t.Fatalf("expected 3 entries pre-filter, got %d", len(m.filtered))
	}
	// Move cursor down to position 2.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.selected != 2 {
		t.Fatalf("expected selected=2, got %d", m.selected)
	}
	// Filter "a" matches alpha and gamma — cursor should clamp.
	m, _ = m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if m.selected >= len(m.filtered) {
		t.Errorf("cursor=%d not clamped within filtered=%d", m.selected, len(m.filtered))
	}
}
