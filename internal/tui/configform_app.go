package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/config"
)

// returnFromConfigForm closes the repo config form and returns to the repo
// picker if one was pending, or back to the dashboard list otherwise.
func (a App) returnFromConfigForm() (tea.Model, tea.Cmd) {
	if a.repoPickerPendingFromConfig {
		a.repoPickerPendingFromConfig = false
		counts := make(map[string]int, len(a.cfg.Repos))
		for _, repo := range a.cfg.Repos {
			if mgr := a.managers[repo.Path]; mgr != nil {
				counts[repo.Path] = mgr.ActiveSessionCount()
			}
		}
		a.repoPicker.setRepos(a.cfg.Repos, counts, a.modals.ConfigRepoPath())
		a.closeModal()
		a.pendingChecks = nil
		a.view = ViewRepoPicker
		return a, nil
	}
	a.closeModal()
	a.pendingChecks = nil
	return a, nil
}

// initRepoConfigForm creates a config form for the given repo and enters config focus.
func (a *App) initRepoConfigForm(repoPath string) {
	rs := a.repoSettings[repoPath]
	if rs == nil {
		rs = &config.RepoSettings{}
	}

	bypassPerms := config.DefaultBypassPermissions
	if rs.BypassPermissions != nil {
		bypassPerms = *rs.BypassPermissions
	}
	defaultBranch := ""
	if rs.DefaultBranch != nil {
		defaultBranch = *rs.DefaultBranch
	}
	branchPrefix := ""
	if rs.BranchPrefix != nil {
		branchPrefix = *rs.BranchPrefix
	}
	agentProgram := ""
	if rs.AgentProgram != nil {
		agentProgram = *rs.AgentProgram
	}
	planModel := ""
	if rs.PlanModel != nil {
		planModel = *rs.PlanModel
	}
	agentModel := ""
	if rs.AgentModel != nil {
		agentModel = *rs.AgentModel
	}
	ideCommand := ""
	if rs.IDECommand != nil {
		ideCommand = *rs.IDECommand
	}
	worktreeDir := ""
	if rs.WorktreeDir != nil {
		worktreeDir = *rs.WorktreeDir
	}
	alias := ""
	for _, r := range a.cfg.Repos {
		if r.Path == repoPath {
			alias = r.Alias
			break
		}
	}

	inputWidth := 30
	var fields []formField
	fields = addTextInput(fields, "Alias", alias, "short nickname", inputWidth)
	fields = addToggle(fields, "Bypass Permissions", bypassPerms)
	fields = addTextInput(fields, "Default Branch", defaultBranch, "auto-detect", inputWidth)
	fields = addTextInput(fields, "Branch Prefix", branchPrefix, config.DefaultBranchPrefix, inputWidth)
	fields = addTextInput(fields, "Agent Program", agentProgram, config.DefaultAgentProgram, inputWidth)
	fields = addSelect(fields, "Plan Model", config.KnownModels, optionIndex(config.KnownModels, planModel))
	fields = addSelect(fields, "Agent Model", config.KnownAgentModels, optionIndex(config.KnownAgentModels, agentModel))
	fields = addEditorFields(fields, ideCommand)
	fields = addTextInput(fields, "Worktree Directory", worktreeDir, config.DefaultWorktreeDir, inputWidth)

	// Seed the validation-checks buffer from the loaded settings and append
	// the action row that opens the sub-editor. The buffer is the source of
	// truth while the form is open; extractRepoSettings reads from it on save.
	a.pendingChecks = append([]config.ValidationCheck(nil), rs.ValidationChecks...)
	fields = addAction(fields, "Validation Checks", repoChecksHint(a.pendingChecks))

	form := newConfigForm(fields, a.width)
	a.openConfigForm(&form, repoPath)
}

// extractRepoSettings reads form values and creates a RepoSettings struct.
func (a App) extractRepoSettings() *config.RepoSettings {
	form := a.modals.Config()
	if form == nil {
		return &config.RepoSettings{}
	}
	s := &config.RepoSettings{}

	bypassPerms := form.toggleValue("Bypass Permissions")
	s.BypassPermissions = &bypassPerms

	if v := form.textValue("Default Branch"); v != "" {
		s.DefaultBranch = &v
	}
	if v := form.textValue("Branch Prefix"); v != "" {
		s.BranchPrefix = &v
	}
	if v := form.textValue("Agent Program"); v != "" {
		s.AgentProgram = &v
	}
	if v := form.selectValue("Plan Model"); v != "" {
		s.PlanModel = &v
	}
	if v := form.selectValue("Agent Model"); v != "" {
		s.AgentModel = &v
	}
	if v := extractIDECommand(*form); v != "" {
		s.IDECommand = &v
	}
	if v := form.textValue("Worktree Directory"); v != "" {
		s.WorktreeDir = &v
	}
	if checks := filterValidationChecks(a.pendingChecks); len(checks) > 0 {
		s.ValidationChecks = checks
	}
	return s
}

// initRepoChecksEditor switches the panel from the repo config form to the
// validation-checks sub-editor. The current pendingChecks list is handed to
// the editor as its working buffer; on save it's copied back, on cancel it's
// discarded.
func (a *App) initRepoChecksEditor(repoPath string) {
	repoName := repoPath
	for _, r := range a.cfg.Repos {
		if r.Path == repoPath {
			if r.Alias != "" {
				repoName = r.Alias
			}
			break
		}
	}
	editor := newRepoChecksModel(repoName, a.pendingChecks)
	a.openRepoChecksEditor(&editor, repoPath)
}

// updateRepoChecks handles save and cancel messages from the checks
// sub-editor. On save the new list is copied back into pendingChecks and the
// repo config form's action-row hint is refreshed; on cancel the list is
// preserved as it was before the editor opened.
func (a App) updateRepoChecks(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case repoChecksSaveMsg:
		a.pendingChecks = append([]config.ValidationCheck(nil), m.Checks...)
		a.modals.Config().refreshChecksActionHint(a.pendingChecks)
		a.closeRepoChecksEditor()
		return a, nil
	case repoChecksCancelMsg:
		a.closeRepoChecksEditor()
		return a, nil
	case tea.KeyPressMsg:
		if editor := a.modals.RepoChecks(); editor != nil {
			var cmd tea.Cmd
			*editor, cmd = editor.Update(msg)
			return a, cmd
		}
	}
	return a, nil
}
