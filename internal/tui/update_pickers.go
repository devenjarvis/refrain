package tui

import (
	"fmt"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/config"
)

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
					counts[repo.Path] = mgr.ActiveSessionCount()
				}
			}
			a.repoPicker.width = a.width
			a.repoPicker.height = a.height - 1
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

func (a App) updateBranchPicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case branchPickerSelectMsg:
		a.view = ViewDashboard
		item := msg.item

		repoPath := a.activeRepo
		mgr := a.managers[repoPath]
		if mgr == nil {
			a.setError("No manager for repo")
			return a, nil
		}

		fixedW := a.dashboard.fixedTermWidth()
		fixedH := a.dashboard.fixedTermHeight()
		if fixedW <= 0 || fixedH <= 0 {
			a.setError("Terminal size not yet known; try again")
			return a, nil
		}

		resolved := a.resolvedCache[repoPath]
		cfg := agent.Config{
			Rows:              fixedH,
			Cols:              fixedW,
			BypassPermissions: resolved.BypassPermissions,
			AgentProgram:      resolved.AgentProgram,
			AgentModel:        resolved.AgentModel,
			BuildSystemPrompt: resolved.BuildSystemPrompt,
		}

		branch := item.branch
		baseBranch := item.baseBranch
		return a, func() tea.Msg {
			sess, ag, err := mgr.CreateSessionOnBranch(branch, baseBranch, cfg)
			if err != nil {
				return createResultMsg{err: err}
			}
			// Branch-picker sessions spawn an agent immediately on the
			// chosen branch; they belong in BUILDING. See the legacy n
			// path for the same rationale.
			sess.SetLifecyclePhase(agent.LifecycleInProgress)
			return createResultMsg{sessionID: sess.ID, agentID: ag.ID, isNewSession: true}
		}

	case branchPickerCancelMsg:
		a.view = ViewDashboard
		return a, nil
	}

	var cmd tea.Cmd
	a.branchPicker, cmd = a.branchPicker.Update(msg)
	return a, cmd
}

func (a App) updateRepoPicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case repoPickerSelectMsg:
		a.view = ViewDashboard
		repoPath := msg.path
		if repoPath == "" {
			return a, nil
		}
		a.activeRepo = repoPath
		a.refreshAgentList()
		fixedW := a.dashboard.fixedTermWidth()
		fixedH := a.dashboard.fixedTermHeight()
		if fixedW <= 0 || fixedH <= 0 {
			a.setError("Terminal size not yet known; try again")
			return a, nil
		}
		resolved := a.resolvedCache[repoPath]
		mgr := a.managers[repoPath]
		if mgr == nil {
			return a, nil
		}
		// Plan-first flow: same gate as the single-repo `n` keybind. Without
		// this branch, multi-repo users would silently bypass PlanFirstEnabled
		// and spawn the real agent immediately.
		if resolved.PlanFirstEnabled {
			return a, a.openNewSession(ViewRepoPicker)
		}
		pickerCfg := agent.Config{
			Rows:              fixedH,
			Cols:              fixedW,
			BypassPermissions: resolved.BypassPermissions,
			AgentProgram:      resolved.AgentProgram,
			AgentModel:        resolved.AgentModel,
			BuildSystemPrompt: resolved.BuildSystemPrompt,
		}
		return a, func() tea.Msg {
			sess, ag, err := mgr.CreateSession(pickerCfg)
			if err != nil {
				return createResultMsg{err: err}
			}
			// Repo-picker sessions spawn an agent immediately; they
			// belong in BUILDING. See the legacy n path for rationale.
			sess.SetLifecyclePhase(agent.LifecycleInProgress)
			return createResultMsg{sessionID: sess.ID, agentID: ag.ID, isNewSession: true}
		}

	case repoPickerSwitchActiveMsg:
		if msg.path == "" {
			return a, nil
		}
		a.activeRepo = msg.path
		a.refreshAgentList()
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
		a.refreshAgentList()
		counts := make(map[string]int, len(a.cfg.Repos))
		for _, repo := range a.cfg.Repos {
			if m := a.managers[repo.Path]; m != nil {
				counts[repo.Path] = m.ActiveSessionCount()
			}
		}
		a.repoPicker.setRepos(a.cfg.Repos, counts, a.activeRepo)
		return a, nil

	case repoPickerAddRepoMsg:
		a.repoPickerPending = true
		a.repoBrowser = newFileBrowserModel()
		a.repoBrowser.width = a.width
		a.repoBrowser.height = a.height - 1
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
