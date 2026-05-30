package tui

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/devenjarvis/refrain/internal/config"
)

// TestAddRepo_UsesInjectedFactory verifies that addRepo obtains its manager
// through the injectable managerFactory rather than constructing a real
// *agent.Manager. A fake factory lets the test assert the wiring without
// spinning up PTYs, worktrees, or a hook socket.
func TestAddRepo_UsesInjectedFactory(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate config.Save from the real config.
	repo := t.TempDir()
	makeGitRepo(t, repo)

	app := NewApp()
	app.cfg = &config.Config{}

	var calls int
	var gotPath string
	var gotResolved config.ResolvedSettings
	fake := newFakeManager(repo)
	app.managerFactory = func(path string, resolved config.ResolvedSettings) SessionManager {
		calls++
		gotPath = path
		gotResolved = resolved
		return fake
	}

	cmd := app.addRepo(repo)

	if calls != 1 {
		t.Fatalf("managerFactory called %d times, want 1", calls)
	}
	absRepo, _ := filepath.Abs(repo)
	if gotPath != absRepo {
		t.Errorf("factory got path %q, want %q", gotPath, absRepo)
	}
	if app.managers[absRepo] != SessionManager(fake) {
		t.Error("addRepo did not store the manager returned by the factory")
	}
	// The factory must receive the resolved settings addRepo cached for the repo.
	if got, want := gotResolved, app.resolvedCache[absRepo]; !reflect.DeepEqual(got, want) {
		t.Errorf("factory got resolved %+v, want cached %+v", got, want)
	}
	if cmd == nil {
		t.Error("addRepo should return a listen cmd for the newly created manager")
	}
}

// TestNewAppFromDeps_OverlaysInjectedConcretes verifies the dependency-injecting
// constructor keeps NewApp's zero-arg defaults while overlaying the wired
// concretes, and leaves the default factory in place when none is injected.
func TestNewAppFromDeps_OverlaysInjectedConcretes(t *testing.T) {
	cfg := &config.Config{Repos: []config.Repo{{Path: "/repo"}}}
	gs := &config.GlobalSettings{}
	mgr := newFakeManager("/repo")
	deps := AppDeps{
		Cfg:            cfg,
		GlobalSettings: gs,
		Managers:       map[string]SessionManager{"/repo": mgr},
		ResolvedCache:  map[string]config.ResolvedSettings{"/repo": {AgentProgram: "bash"}},
		RepoSettings:   map[string]*config.RepoSettings{"/repo": {}},
	}

	app := NewAppFromDeps(deps)

	if app.cfg != cfg {
		t.Error("cfg not injected")
	}
	if app.globalSettings != gs {
		t.Error("globalSettings not injected")
	}
	if app.managers["/repo"] != SessionManager(mgr) {
		t.Error("managers not injected")
	}
	if app.resolvedCache["/repo"].AgentProgram != "bash" {
		t.Error("resolvedCache not injected")
	}
	// No factory in deps → the NewApp default must remain non-nil so the
	// runtime addRepo path still works.
	if app.managerFactory == nil {
		t.Error("managerFactory must default to DefaultManagerFactory when none injected")
	}
	// Base defaults from NewApp() must survive.
	if app.view != ViewDashboard {
		t.Error("NewAppFromDeps lost NewApp's base initialization")
	}
}

// TestNewAppFromDeps_SurfacesInitWarning verifies a non-fatal wiring warning
// from cmd/ (e.g. unreadable global settings) is surfaced transiently on init,
// preserving the pre-injection behavior.
func TestNewAppFromDeps_SurfacesInitWarning(t *testing.T) {
	app := NewAppFromDeps(AppDeps{
		Cfg:         &config.Config{},
		InitWarning: "settings: bad json",
	})
	if app.initWarning != "settings: bad json" {
		t.Fatalf("initWarning = %q, want %q", app.initWarning, "settings: bad json")
	}

	model, _ := app.Update(initAppMsg{})
	got := model.(App)
	if got.err != "settings: bad json" {
		t.Errorf("handleInit did not surface the warning: err = %q", got.err)
	}
}
