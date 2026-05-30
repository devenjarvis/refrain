package tui

import (
	tea "charm.land/bubbletea/v2"
)

func (d dashboardModel) Update(msg tea.Msg, props dashboardProps) (dashboardModel, tea.Cmd) {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		// Config overlay: delegate to the form. Pipeline navigation (j/k/enter)
		// is handled at the app level via moveFocusCursorUp/Down and
		// activateFocusCursor; nothing else needs to reach the dashboard here.
		if props.panelFocus == focusConfig && props.repoConfigForm != nil {
			var cmd tea.Cmd
			*props.repoConfigForm, cmd = props.repoConfigForm.Update(msg)
			return d, cmd
		}
		if props.panelFocus == focusRepoChecks && props.repoChecksEditor != nil {
			var cmd tea.Cmd
			*props.repoChecksEditor, cmd = props.repoChecksEditor.Update(msg)
			return d, cmd
		}
	}
	return d, nil
}
