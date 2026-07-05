package tui

import (
	tea "charm.land/bubbletea/v2"
)

// updateFileBrowser routes messages to the add-repo file browser while in
// ViewFileBrowser, registering the chosen path and returning to the repo
// picker (when reached from there) or the dashboard.
func (a App) updateFileBrowser(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case fileBrowserSelectMsg:
		// Snapshot count before addRepo so we can tell whether registration
		// actually appended a new entry (vs. failing or dedup'ing). Without this
		// the picker would highlight whatever was already last on failure.
		priorRepoCount := 0
		if a.cfg != nil {
			priorRepoCount = len(a.cfg.Repos)
		}
		cmd := a.addRepo(msg.path)
		if a.repoPickerPending {
			a.repoPickerPending = false
			newPath := a.activeRepo
			if a.cfg != nil && len(a.cfg.Repos) > priorRepoCount {
				newPath = a.cfg.Repos[len(a.cfg.Repos)-1].Path
			}
			counts := make(map[string]int, len(a.cfg.Repos))
			for _, repo := range a.cfg.Repos {
				if mgr := a.managers[repo.Path]; mgr != nil {
					counts[repo.Path] = mgr.SessionCount()
				}
			}
			a.repoPicker.width = a.width
			a.repoPicker.height = a.height - statusBarHeight
			a.repoPicker.setRepos(a.cfg.Repos, counts, newPath)
			a.view = ViewRepoPicker
			return a, cmd
		}
		a.view = ViewDashboard
		return a, cmd
	case fileBrowserCancelMsg:
		if a.repoPickerPending {
			a.repoPickerPending = false
			a.view = ViewRepoPicker
			return a, nil
		}
		a.view = ViewDashboard
		return a, nil
	}
	var cmd tea.Cmd
	a.repoBrowser, cmd = a.repoBrowser.Update(msg)
	return a, cmd
}
