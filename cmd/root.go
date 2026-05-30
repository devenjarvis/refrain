package cmd

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/config"
	"github.com/devenjarvis/refrain/internal/github"
	"github.com/devenjarvis/refrain/internal/migrate"
	"github.com/devenjarvis/refrain/internal/tui"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.Version = resolvedVersion()
}

var rootCmd = &cobra.Command{
	Use:   "refrain",
	Short: "A terminal-native tool for orchestrating multiple Claude Code agents",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Skip migration for the hook subprocess: it runs once per Claude hook
		// event and must stay silent on stderr so it doesn't confuse Claude.
		if cmd.Name() == "hook" {
			return
		}
		if err := migrate.GlobalHome(); err != nil {
			fmt.Fprintf(os.Stderr, "refrain: %v\n", err)
		}
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		deps, err := buildAppDeps()
		if err != nil {
			return err
		}
		p := tea.NewProgram(tui.NewAppFromDeps(deps))
		if _, err := p.Run(); err != nil {
			return fmt.Errorf("error running TUI: %w", err)
		}
		return nil
	},
}

// buildAppDeps wires the concrete dependencies the TUI needs. Per
// CONVENTIONS.md §2, cmd/ is the only place these concretes are constructed:
// config, per-repo settings, a manager per repo, and the GitHub client are all
// built here and injected via tui.AppDeps.
func buildAppDeps() (tui.AppDeps, error) {
	cfg, err := config.Load()
	if err != nil {
		return tui.AppDeps{}, fmt.Errorf("cmd: load config: %w", err)
	}
	if len(cfg.Repos) == 0 {
		// Auto-register the current working directory on first run.
		if err := config.AddRepo(cfg, "."); err != nil {
			return tui.AppDeps{}, fmt.Errorf("cmd: bootstrap repo: %w", err)
		}
		if err := config.Save(cfg); err != nil {
			return tui.AppDeps{}, fmt.Errorf("cmd: save config: %w", err)
		}
	}

	// Global settings are best-effort: a malformed file should not block
	// launch. On failure we proceed with nil (config.Resolve tolerates it),
	// matching the prior in-TUI behavior. The one-time bypass migration runs
	// only when settings loaded cleanly.
	var initWarning string
	globalSettings, gerr := config.LoadGlobalSettings()
	if gerr != nil {
		globalSettings = nil
		initWarning = gerr.Error()
	} else {
		_ = config.MigrateBypassPermissions(cfg)
	}

	factory := tui.ManagerFactory(tui.DefaultManagerFactory)
	repoSettings := make(map[string]*config.RepoSettings, len(cfg.Repos))
	resolvedCache := make(map[string]config.ResolvedSettings, len(cfg.Repos))
	managers := make(map[string]tui.SessionManager, len(cfg.Repos))
	for _, repo := range cfg.Repos {
		rs, _ := config.LoadRepoSettings(repo.Path)
		repoSettings[repo.Path] = rs
		resolved := config.Resolve(globalSettings, rs)
		resolvedCache[repo.Path] = resolved
		managers[repo.Path] = factory(repo.Path, resolved)
	}

	// GitHub client is best-effort (nil when gh auth is absent).
	ghClient, _ := github.NewClient()

	return tui.AppDeps{
		Cfg:            cfg,
		GlobalSettings: globalSettings,
		RepoSettings:   repoSettings,
		ResolvedCache:  resolvedCache,
		Managers:       managers,
		GHClient:       ghClient,
		Factory:        factory,
		InitWarning:    initWarning,
	}, nil
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
