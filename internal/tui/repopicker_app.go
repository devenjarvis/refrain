package tui

import (
	"fmt"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/config"
)

// updateRepoPicker routes messages to the repo picker while in ViewRepoPicker,
// handling repo selection (spawn / plan-first), active-repo switching, settings
// edits, removal, and add-repo.
func (a App) updateRepoPicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case repoPickerSelectMsg:
		a.view = ViewDashboard
		repoPath := msg.path
		if repoPath == "" {
			return a, nil
		}
		a.activeRepo = repoPath
		a.clampCursor()
		if a.managers[repoPath] == nil {
			return a, nil
		}
		// Every new session composes through the full-viewport screen: it
		// carries the raw/plan-first submit pair and the context toggle.
		return a, a.openNewSession(ViewRepoPicker)

	case repoPickerSwitchActiveMsg:
		if msg.path == "" {
			return a, nil
		}
		a.activeRepo = msg.path
		a.clampCursor()
		a.view = ViewDashboard
		return a, nil

	case repoPickerEditSettingsMsg:
		a.initRepoConfigForm(msg.path)
		a.repoPickerPendingFromConfig = true
		a.view = ViewDashboard
		return a, nil

	case repoPickerRemoveMsg:
		mgr := a.managers[msg.path]
		if mgr != nil && mgr.AgentCount() > 0 {
			a.setError(fmt.Sprintf("cannot remove %q while %d session(s) are running",
				filepath.Base(msg.path), mgr.AgentCount()))
			return a, nil
		}
		origRepos := make([]config.Repo, len(a.cfg.Repos))
		copy(origRepos, a.cfg.Repos)
		if err := config.RemoveRepo(a.cfg, msg.path); err != nil {
			a.setError(err.Error())
			return a, nil
		}
		if err := config.Save(a.cfg); err != nil {
			a.cfg.Repos = origRepos
			a.setError(err.Error())
			return a, nil
		}
		if mgr != nil {
			mgr.Shutdown()
		}
		delete(a.managers, msg.path)
		delete(a.repoSettings, msg.path)
		delete(a.resolvedCache, msg.path)
		if a.activeRepo == msg.path {
			a.activeRepo = ""
			if len(a.cfg.Repos) > 0 {
				a.activeRepo = a.cfg.Repos[0].Path
			}
		}
		a.clampCursor()
		counts := make(map[string]int, len(a.cfg.Repos))
		for _, repo := range a.cfg.Repos {
			if m := a.managers[repo.Path]; m != nil {
				counts[repo.Path] = m.SessionCount()
			}
		}
		a.repoPicker.setRepos(a.cfg.Repos, counts, a.activeRepo)
		return a, nil

	case repoPickerAddRepoMsg:
		a.repoPickerPending = true
		a.repoBrowser = newFileBrowserModel()
		a.repoBrowser.width = a.width
		a.repoBrowser.height = a.height - statusBarHeight
		a.view = ViewFileBrowser
		return a, nil

	case repoPickerCancelMsg:
		a.view = ViewDashboard
		return a, nil
	}

	var cmd tea.Cmd
	a.repoPicker, cmd = a.repoPicker.Update(msg)
	return a, cmd
}
