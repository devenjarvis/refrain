package tui

import (
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/config"
)

// seedSessionListItems wires app.managers / app.cfg / app.activeRepo so that
// app.listItems() reproduces the hierarchical item list the caller passes.
//
// Tests used to inject the list straight onto a dashboard model mirror field;
// that mirror is gone, so the list is now derived live from the managers each
// frame. This helper keeps the call sites nearly identical — wrap the old
// literal in seedSessionListItems — while routing the data through the real
// derivation path.
//
// Agent rows in the input are ignored: each session already carries its agents
// (added via Session.AddTestAgent), and listItems() regenerates the agent rows
// from sess.Agents(). Repo headers define the cfg repo order and display name.
func seedSessionListItems(app *App, items []listItem) {
	var order []string
	seen := map[string]bool{}
	sessByRepo := map[string][]*agent.Session{}
	repoName := map[string]string{}
	for _, it := range items {
		rp := it.repoPath
		if !seen[rp] {
			seen[rp] = true
			order = append(order, rp)
		}
		switch it.kind {
		case listItemRepo:
			repoName[rp] = it.repoName
		case listItemSession:
			if it.session != nil {
				sessByRepo[rp] = append(sessByRepo[rp], it.session)
			}
		}
	}
	for _, rp := range order {
		app.managers[rp] = newFakeManager(rp, sessByRepo[rp]...)
	}
	// Build cfg so listItems() emits repo headers in the same order as the
	// injected list (matching the hierarchical layout the tests expect).
	app.cfg = &config.Config{}
	for _, rp := range order {
		app.cfg.Repos = append(app.cfg.Repos, config.Repo{Path: rp, Alias: repoName[rp]})
	}
	if len(order) > 0 && app.activeRepo == "" {
		app.activeRepo = order[0]
	}
}
