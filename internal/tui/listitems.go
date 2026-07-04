package tui

import (
	"github.com/devenjarvis/refrain/internal/agent"
)

// clampedMove shifts a list cursor at cur by delta and clamps the result to
// [0, n-1], where n is the item count. A non-positive n yields 0. Shared by
// the list pickers for up/down (k/j) navigation.
func clampedMove(cur, delta, n int) int {
	cur += delta
	if cur < 0 {
		return 0
	}
	if n <= 0 {
		return 0
	}
	if cur > n-1 {
		return n - 1
	}
	return cur
}

// listItemKind distinguishes repo headers, session rows, and agent rows in the
// hierarchical row list App.listItems() builds each frame.
type listItemKind int

const (
	listItemRepo listItemKind = iota
	listItemSession
	listItemAgent
)

// listItem represents one row in the hierarchical repo/session/agent list.
type listItem struct {
	kind     listItemKind
	repoPath string
	repoName string         // set for repo header items
	session  *agent.Session // set for session and agent items
	agent    *agent.Agent   // set for agent items
}

// listItems is the hierarchical repo/session/agent row list. App.listItems()
// rebuilds it from the managers each frame; the session list derives everything
// it renders from this without owning a mirrored copy (CONVENTIONS.md §5/§6).
type listItems []listItem

// agents returns every agent row in the list (for resize operations).
func (items listItems) agents() []*agent.Agent {
	var result []*agent.Agent
	for _, item := range items {
		if item.kind == listItemAgent {
			result = append(result, item.agent)
		}
	}
	return result
}
