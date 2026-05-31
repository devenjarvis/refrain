package tui

import "github.com/devenjarvis/refrain/internal/diffmodel"

// focusedDiffModel returns the parsed diff model for the cursor-selected task,
// a pointer to the corresponding taskReviewGroup, and ok=true. Returns ok=false
// when the focused group has no rawDiff (right pane renders an empty state).
// Results are cached in m.parsedDiffs keyed by taskIndex so re-visits are
// instant and re-parses don't hit the render hot path.
func (m *reviewPanelModel) focusedDiffModel(entry *reviewDiffEntry) (*diffmodel.Model, *taskReviewGroup, bool) {
	group := reviewTaskGroupAtCursor(entry, m.taskCursor)
	if group == nil || group.rawDiff == "" {
		return nil, group, false
	}

	if m.parsedDiffs == nil {
		m.parsedDiffs = make(map[int]*diffmodel.Model)
	}
	if cached, ok := m.parsedDiffs[group.taskIndex]; ok {
		return cached, group, true
	}

	parsed, err := diffmodel.Parse(group.rawDiff)
	if err != nil || parsed == nil {
		return nil, group, false
	}
	m.parsedDiffs[group.taskIndex] = parsed
	return parsed, group, true
}
