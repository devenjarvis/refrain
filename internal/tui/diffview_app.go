package tui

import (
	tea "charm.land/bubbletea/v2"
)

// updateDiff routes messages to the diff component while in ViewDiff and
// returns to the dashboard on diffCloseMsg.
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
