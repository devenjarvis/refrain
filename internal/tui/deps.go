package tui

import (
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/config"
	"github.com/devenjarvis/refrain/internal/github"
)

// ManagerFactory builds a SessionManager for the repo at path with resolved
// settings. cmd/ wires the production DefaultManagerFactory; tests inject a
// fake so paths like addRepo can run without spinning up PTYs, git worktrees,
// or a hook socket. Per CONVENTIONS.md §2 this factory is the single seam
// through which the presentation layer obtains a concrete manager.
type ManagerFactory func(path string, resolved config.ResolvedSettings) SessionManager

// DefaultManagerFactory is the production ManagerFactory. It is the one place
// agent.NewManager is constructed for the TUI, and it also ensures the repo's
// .gitignore excludes .refrain/ — bundling the two so every manager-creation
// path (startup wiring in cmd/, runtime addRepo) stays consistent.
func DefaultManagerFactory(path string, resolved config.ResolvedSettings) SessionManager {
	mgr := agent.NewManager(path, resolved)
	ensureGitignore(path)
	return mgr
}

// AppDeps carries the concrete dependencies wired by cmd/ into the App. Per
// CONVENTIONS.md §2, cmd/ is the only place these concretes are constructed;
// the TUI receives them through this struct rather than building them itself.
type AppDeps struct {
	Cfg            *config.Config
	GlobalSettings *config.GlobalSettings
	RepoSettings   map[string]*config.RepoSettings    // keyed by repo path
	ResolvedCache  map[string]config.ResolvedSettings // keyed by repo path
	Managers       map[string]SessionManager          // keyed by repo path
	GHClient       *github.Client
	Factory        ManagerFactory // used by the runtime addRepo path
}

// NewAppFromDeps builds an App with concretes injected by cmd/. It delegates
// base field initialization to NewApp() (keeping that constructor zero-arg so
// the existing tests are untouched) and then overlays the wired dependencies.
func NewAppFromDeps(deps AppDeps) App {
	a := NewApp()
	if deps.Factory != nil {
		a.managerFactory = deps.Factory
	}
	a.cfg = deps.Cfg
	a.globalSettings = deps.GlobalSettings
	if deps.RepoSettings != nil {
		a.repoSettings = deps.RepoSettings
	}
	if deps.ResolvedCache != nil {
		a.resolvedCache = deps.ResolvedCache
	}
	if deps.Managers != nil {
		a.managers = deps.Managers
	}
	a.ghClient = deps.GHClient
	return a
}
