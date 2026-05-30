package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/config"
)

// TestRepoChecks_RoundTrip_FormToEditorToDisk exercises the full path from
// the repo settings form's action row through the checks sub-editor and back
// to a configFormSaveMsg, then verifies the new check is persisted to
// .refrain/config.json.
func TestRepoChecks_RoundTrip_FormToEditorToDisk(t *testing.T) {
	dir := t.TempDir()
	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.cfg = &config.Config{Repos: []config.Repo{{Path: dir, Name: "myrepo"}}}
	app.activeRepo = dir
	app.repoSettings[dir] = &config.RepoSettings{}
	app.globalSettings = &config.GlobalSettings{}
	app.resolvedCache[dir] = config.Resolve(app.globalSettings, app.repoSettings[dir])

	// Open the repo settings form.
	app.initRepoConfigForm(dir)
	if !app.modals.Is(focusConfig) {
		t.Fatalf("expected focusConfig after initRepoConfigForm, got %v", app.modals.Current())
	}

	// Emit the action message that the form would emit on enter on the
	// "Validation Checks" row.
	model, _ := app.Update(configFormActionMsg{Label: "Validation Checks"})
	app = model.(App)
	if !app.modals.Is(focusRepoChecks) {
		t.Fatalf("expected focusRepoChecks after action, got %v", app.modals.Current())
	}
	editor := app.modals.RepoChecks()
	if editor == nil {
		t.Fatal("expected non-nil RepoChecks editor after open")
	}

	// Add a check via the editor and commit it.
	*editor, _ = editor.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	editor.nameInput.SetValue("Smoke")
	editor.cmdInput.SetValue("true")
	*editor, _ = editor.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	var saveCmd tea.Cmd
	*editor, saveCmd = editor.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if saveCmd == nil {
		t.Fatal("expected non-nil cmd from ctrl+s in editor")
	}

	// Dispatch the save msg through the app — should return focus to the
	// config form and copy the editor's list into pendingChecks.
	model, _ = app.Update(saveCmd())
	app = model.(App)
	if !app.modals.Is(focusConfig) {
		t.Fatalf("expected focusConfig after editor save, got %v", app.modals.Current())
	}
	if len(app.pendingChecks) != 1 || app.pendingChecks[0].Name != "Smoke" {
		t.Fatalf("pendingChecks = %+v, want one Smoke entry", app.pendingChecks)
	}

	// Now save the repo config form. This should write .refrain/config.json
	// with the new check included.
	model, _ = app.Update(configFormSaveMsg{})
	app = model.(App)
	if app.modals.Current() != focusList {
		t.Errorf("expected focusList after form save, got %v", app.modals.Current())
	}
	if app.pendingChecks != nil {
		t.Errorf("expected pendingChecks cleared after form close, got %+v", app.pendingChecks)
	}

	// Re-read from disk and assert the persisted shape.
	settingsPath := filepath.Join(dir, ".refrain", "config.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read %s: %v", settingsPath, err)
	}
	body := string(data)
	if !strings.Contains(body, `"validation_checks"`) {
		t.Errorf("expected validation_checks key in saved config, got:\n%s", body)
	}
	if !strings.Contains(body, `"Smoke"`) || !strings.Contains(body, `"true"`) {
		t.Errorf("expected Smoke/true entry in saved config, got:\n%s", body)
	}

	// And via the typed loader.
	loaded, err := config.LoadRepoSettings(dir)
	if err != nil {
		t.Fatalf("LoadRepoSettings: %v", err)
	}
	if len(loaded.ValidationChecks) != 1 {
		t.Fatalf("loaded len = %d, want 1", len(loaded.ValidationChecks))
	}
	if loaded.ValidationChecks[0].Name != "Smoke" || loaded.ValidationChecks[0].Command != "true" {
		t.Errorf("loaded[0] = %+v, want {Smoke true}", loaded.ValidationChecks[0])
	}
}

// TestRepoChecks_CancelDiscardsPending verifies that pressing esc in the
// checks editor leaves the buffer unchanged from the value seeded by the
// parent form.
func TestRepoChecks_CancelDiscardsPending(t *testing.T) {
	dir := t.TempDir()
	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.cfg = &config.Config{Repos: []config.Repo{{Path: dir, Name: "r"}}}
	app.activeRepo = dir
	app.repoSettings[dir] = &config.RepoSettings{
		ValidationChecks: []config.ValidationCheck{{Name: "Original", Command: "echo hi"}},
	}
	app.globalSettings = &config.GlobalSettings{}
	app.resolvedCache[dir] = config.Resolve(app.globalSettings, app.repoSettings[dir])

	app.initRepoConfigForm(dir)
	if len(app.pendingChecks) != 1 || app.pendingChecks[0].Name != "Original" {
		t.Fatalf("pendingChecks seed = %+v, want one Original entry", app.pendingChecks)
	}

	// Open the editor, replace the list entirely, then cancel.
	model, _ := app.Update(configFormActionMsg{Label: "Validation Checks"})
	app = model.(App)
	editor := app.modals.RepoChecks()
	editor.checks = []config.ValidationCheck{{Name: "Replaced", Command: "false"}}
	var cancelCmd tea.Cmd
	*editor, cancelCmd = editor.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cancelCmd == nil {
		t.Fatal("esc should produce a cancel cmd")
	}

	model, _ = app.Update(cancelCmd())
	app = model.(App)
	if !app.modals.Is(focusConfig) {
		t.Errorf("expected focusConfig after editor cancel, got %v", app.modals.Current())
	}
	if len(app.pendingChecks) != 1 || app.pendingChecks[0].Name != "Original" {
		t.Errorf("cancel should not mutate pendingChecks, got %+v", app.pendingChecks)
	}
}

// TestRepoChecks_BlankEntryFilteredOnFormSave verifies that a row left blank
// in the editor never reaches disk.
func TestRepoChecks_BlankEntryFilteredOnFormSave(t *testing.T) {
	dir := t.TempDir()
	app := NewApp()
	app.width = 120
	app.height = 40
	app.dashboard.width = 120
	app.dashboard.height = 39
	app.cfg = &config.Config{Repos: []config.Repo{{Path: dir, Name: "r"}}}
	app.activeRepo = dir
	app.repoSettings[dir] = &config.RepoSettings{}
	app.globalSettings = &config.GlobalSettings{}
	app.resolvedCache[dir] = config.Resolve(app.globalSettings, app.repoSettings[dir])

	app.initRepoConfigForm(dir)
	// Inject directly into pendingChecks; this mimics what would be on the
	// wire if save somehow leaked an incomplete row through the editor's
	// own filter.
	app.pendingChecks = []config.ValidationCheck{
		{Name: "OK", Command: "true"},
		{Name: "", Command: "false"},
	}
	model, _ := app.Update(configFormSaveMsg{})
	app = model.(App)

	loaded, err := config.LoadRepoSettings(dir)
	if err != nil {
		t.Fatalf("LoadRepoSettings: %v", err)
	}
	if len(loaded.ValidationChecks) != 1 || loaded.ValidationChecks[0].Name != "OK" {
		t.Errorf("expected only OK entry persisted, got %+v", loaded.ValidationChecks)
	}
}
