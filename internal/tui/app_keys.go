package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/config"
	"github.com/devenjarvis/refrain/internal/state"
)

// App-level key plumbing shared by the root session list: overlay-panel
// forwarding, config-form persistence, paste routing, and the quit gesture.
// Lifted out of the retired dashboard dispatch (rollback design Phase 2).

// handleKeysPlanEditor forwards a keypress to the open plan editor. handled is
// true exactly when the editor owns focus, signalling to the caller that no
// further dispatch is needed.
func (a App) handleKeysPlanEditor(msg tea.KeyPressMsg) (App, tea.Cmd, bool) {
	pe := a.modals.PlanEditor()
	if pe == nil {
		return a, nil, false
	}
	updated, cmd := pe.Update(msg)
	*pe = updated
	return a, cmd, true
}

// handleKeysReviewPanel forwards a keypress to the review panel. handled is true
// exactly when the review panel owns focus. The snapshot dance preserves the
// invariant that svc.ClosePanel wins over a stale restore — if the panel was
// closed mid-Update, CompareAndSetReview returns false and the modals state
// stays nil.
func (a App) handleKeysReviewPanel(msg tea.KeyPressMsg) (App, tea.Cmd, bool) {
	snapshot := a.modals.Review()
	if snapshot == nil {
		return a, nil, false
	}
	updated, cmd := snapshot.Update(msg)
	if rp, ok := updated.(*reviewPanelModel); ok {
		a.modals.CompareAndSetReview(snapshot, rp)
	}
	return a, cmd, true
}

// handleKeysPRPanel forwards a keypress to the PR panel. Mirrors the
// review-panel snapshot-and-restore pattern.
func (a App) handleKeysPRPanel(msg tea.KeyPressMsg) (App, tea.Cmd, bool) {
	snapshot := a.modals.PRPanel()
	if snapshot == nil {
		return a, nil, false
	}
	updated, cmd := snapshot.Update(msg)
	if sp, ok := updated.(*prPanelModel); ok {
		a.modals.CompareAndSetPRPanel(snapshot, sp)
	}
	return a, cmd, true
}

// handleConfigFormAction dispatches a configFormActionMsg emitted from a
// fieldAction row to the appropriate sub-editor. Today the only action row
// is "Validation Checks"; other labels are ignored (forward-compatible).
func (a App) handleConfigFormAction(msg configFormActionMsg) (tea.Model, tea.Cmd) {
	if msg.Label == "Validation Checks" && a.modals.Is(focusConfig) {
		repoPath := a.modals.ConfigRepoPath()
		if repoPath != "" {
			a.initRepoChecksEditor(repoPath)
		}
	}
	return a, nil
}

// handleConfigFormSave persists the in-flight repo config form. On success
// the repo's alias may have changed, in which case the config file is also
// saved so the list header reflects the new label. Either way the form is
// closed and focus returns to the session list (or the repo picker when the
// form was opened from there).
func (a App) handleConfigFormSave() (tea.Model, tea.Cmd) {
	if form := a.modals.Config(); form != nil && a.modals.ConfigRepoPath() != "" {
		repoPath := a.modals.ConfigRepoPath()
		alias := strings.TrimSpace(form.textValue("Alias"))
		settings := a.extractRepoSettings()
		if err := config.SaveRepoSettings(repoPath, settings); err != nil {
			a.setError(err.Error())
		} else {
			a.repoSettings[repoPath] = settings
			a.resolvedCache[repoPath] = config.Resolve(a.globalSettings, settings)
			if mgr := a.managers[repoPath]; mgr != nil {
				mgr.UpdateSettings(a.resolvedCache[repoPath])
			}
		}
		for i, r := range a.cfg.Repos {
			if r.Path == repoPath && r.Alias != alias {
				a.cfg.Repos[i].Alias = alias
				if err := config.Save(a.cfg); err != nil {
					a.setError(err.Error())
				}
				break
			}
		}
	}
	return a.returnFromConfigForm()
}

// handleDashboardPaste routes a paste event to whichever modal owns focus:
// the PR compose modal, the plan-goal modal, or the fullscreen agent
// terminal. When no consumer is active the paste is dropped (the session
// list consumes no arbitrary text).
func (a App) handleDashboardPaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if a.prComposeModal.Active() {
		var cmd tea.Cmd
		a.prComposeModal, cmd = a.prComposeModal.Update(msg)
		return a, cmd
	}
	if a.planGoal.Active() {
		var cmd tea.Cmd
		a.planGoal, cmd = a.planGoal.Update(msg)
		return a, cmd
	}
	if ag := a.modals.LaunchAgent(); ag != nil {
		ag.Paste(msg.Content)
		return a, nil
	}
	return a, nil
}

// handleQuitKey implements the q / ctrl+c detach-and-exit gesture. With
// running agents the first press arms a confirm prompt; the second press
// (or first press when no agents are running) detaches all managers,
// persists per-repo state, and quits.
//
// Returns handled=true when q/ctrl+c was pressed. On any other key the
// confirm flag is cleared and handled=false is returned so the dispatch
// continues to the workflow handler.
func (a App) handleQuitKey(msg tea.KeyPressMsg) (App, tea.Cmd, bool) {
	switch msg.String() {
	case "q", "ctrl+c":
	default:
		a.confirmQuit = false
		return a, nil, false
	}
	// Detach path: save state and exit, preserving worktrees.
	hasRunning := false
	for _, mgr := range a.managers {
		if mgr.AgentCount() > 0 {
			hasRunning = true
			break
		}
	}
	if hasRunning && !a.confirmQuit {
		a.confirmQuit = true
		return a, nil, true
	}
	// Detach all managers and save state.
	for repoPath, mgr := range a.managers {
		bs := mgr.Detach()
		if bs != nil {
			_ = state.Save(repoPath, bs)
		} else {
			_ = state.Remove(repoPath)
		}
	}
	if a.audioPlayer != nil {
		a.audioPlayer.Close()
	}
	a.writeWellnessLog()
	return a, tea.Quit, true
}
