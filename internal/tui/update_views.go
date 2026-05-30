package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/config"
)

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

func (a App) updateDiff(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case diffCloseMsg:
		a.view = ViewDashboard
		return a, nil
	}

	var cmd tea.Cmd
	a.diff, cmd = a.diff.Update(msg)
	return a, cmd
}

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

	form := newConfigForm(fields, a.dashboard.fixedTermWidth())
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

// filterValidationChecks drops rows whose Name or Command is empty after
// trimming. Empty rows arise when a user pressed `a` to add a check and then
// abandoned it; treating them as "never added" is friendlier than refusing to
// save the form.
func filterValidationChecks(in []config.ValidationCheck) []config.ValidationCheck {
	out := make([]config.ValidationCheck, 0, len(in))
	for _, c := range in {
		name := strings.TrimSpace(c.Name)
		cmd := strings.TrimSpace(c.Command)
		if name == "" || cmd == "" {
			continue
		}
		out = append(out, config.ValidationCheck{Name: name, Command: cmd})
	}
	return out
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
	a.openRepoChecksEditor(editor, repoPath)
}

// updateRepoChecks handles save and cancel messages from the checks
// sub-editor. On save the new list is copied back into pendingChecks and the
// repo config form's action-row hint is refreshed; on cancel the list is
// preserved as it was before the editor opened.
func (a App) updateRepoChecks(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case repoChecksSaveMsg:
		a.pendingChecks = append([]config.ValidationCheck(nil), m.Checks...)
		refreshChecksActionHint(a.modals.Config(), a.pendingChecks)
		a.closeRepoChecksEditor()
		return a, nil
	case repoChecksCancelMsg:
		a.closeRepoChecksEditor()
		return a, nil
	case tea.KeyPressMsg:
		a.wellness.RecordInput()
		var cmd tea.Cmd
		a.dashboard, cmd = a.dashboard.Update(msg, a.dashboardProps())
		return a, cmd
	}
	return a, nil
}

// refreshChecksActionHint walks the form's fields and updates the "Validation
// Checks" action row's right-aligned hint so it reflects the current count.
// A no-op when the form is nil or doesn't contain that row.
func refreshChecksActionHint(form *configForm, checks []config.ValidationCheck) {
	if form == nil {
		return
	}
	hint := repoChecksHint(filterValidationChecks(checks))
	for i := range form.fields {
		if form.fields[i].kind == fieldAction && form.fields[i].label == "Validation Checks" {
			form.fields[i].actionHint = hint
			return
		}
	}
}

func (a App) updateGlobalConfig(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case globalConfigSaveMsg:
		// Persist global settings.
		if err := config.SaveGlobalSettings(msg.settings); err != nil {
			a.setError(err.Error())
			a.view = ViewDashboard
			return a, nil
		}
		a.globalSettings = msg.settings
		// Rebuild resolved cache and push to all managers.
		for repoPath, rs := range a.repoSettings {
			a.resolvedCache[repoPath] = config.Resolve(a.globalSettings, rs)
			if mgr := a.managers[repoPath]; mgr != nil {
				mgr.UpdateSettings(a.resolvedCache[repoPath])
			}
		}
		newResolved := config.Resolve(a.globalSettings, nil)
		if newResolved.SidebarWidth != a.dashboard.sidebarWidth {
			a.dashboard.sidebarWidth = newResolved.SidebarWidth
			a.resizeAllForDashboard()
		}
		// Refresh wellness settings from updated global config.
		a.wellness.focusSessionMinutes = newResolved.FocusSessionMinutes
		a.wellness.focusBreakMinutes = newResolved.FocusBreakMinutes
		a.view = ViewDashboard
		return a, nil
	case globalConfigCancelMsg:
		a.view = ViewDashboard
		return a, nil
	}

	var cmd tea.Cmd
	a.globalConfig, cmd = a.globalConfig.Update(msg)
	return a, cmd
}
