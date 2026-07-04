package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestSessionListHints_FitIn120Cols verifies that the session-list status bar
// fits within 120 columns so that "q quit" is never truncated to a second line.
func TestSessionListHints_FitIn120Cols(t *testing.T) {
	out := renderStatusBar(sessionListHints, 120)
	lines := strings.Split(out, "\n")
	if len(lines) > 1 {
		t.Errorf("sessionListHints wrapped to %d lines at width=120; q quit may be truncated", len(lines))
	}
	w := lipgloss.Width(out)
	if w > 120 {
		t.Errorf("renderStatusBar width = %d, want ≤ 120", w)
	}
	if !strings.Contains(out, "quit") {
		t.Errorf("expected 'quit' to be visible in status bar output")
	}
}
