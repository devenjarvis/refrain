package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/agent"
)

// updateBranchPicker routes messages to the branch picker while in
// ViewBranchPicker, spawning a session on the chosen existing branch.
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

		rows := a.agentTermRows()
		cols := a.agentTermCols()
		if rows <= 0 || cols <= 0 {
			a.setError("Terminal size not yet known; try again")
			return a, nil
		}

		resolved := a.resolvedCache[repoPath]
		cfg := agent.Config{
			Rows:              rows,
			Cols:              cols,
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
