//go:build e2e

package e2e

import "testing"

// Anchor strings used to wait for specific TUI views to render.
//
//   - dashboardAnchor: "n session" only appears in the dashboard status bar
//     (no overlay hint set contains "session").
//   - listFocusAnchor: dashboard text shown only when focus is on the list.
const (
	dashboardAnchor = "n session"
	listFocusAnchor = "n session"
)

func TestSettingsOverlay(t *testing.T) {
	s := newSession(t)
	s.Start()

	s.WaitForText("FOCUS", 10000)

	// Press "s" to open global settings overlay; wait for a known field label.
	s.Press("s")
	s.WaitForText("Audio Enabled", 5000)
	s.AssertScreenContains("Bypass Permissions")

	// Press Escape to return to dashboard.
	s.Press("Escape")
	s.WaitForText(dashboardAnchor, 5000)
	s.AssertScreenContains("FOCUS")
}

func TestDiffView(t *testing.T) {
	s := newSession(t)
	s.Start()

	s.WaitForText("FOCUS", 10000)

	// Create a new session — "n" key auto-focuses the terminal.
	s.Press("n")
	s.WaitForText(`\$`, 10000)

	// Create + commit a file in the worktree, then a sentinel echo we can
	// wait for so we know the commit completed before moving on.
	s.Type("echo test > file.txt && git add file.txt && git commit -m 'add file' && echo COMMIT_DONE\n")
	s.WaitForText("COMMIT_DONE", 10000)

	// Return to list focus.
	s.Press("Escape")
	s.WaitForText(listFocusAnchor, 5000)

	// Press "d" to open diff view; wait for a status bar hint unique to it.
	s.Press("d")
	s.WaitForText("side-by-side", 5000)
	s.AssertScreenContains("file.txt")

	// Exit diff view.
	s.Press("Escape")
	s.WaitForText(dashboardAnchor, 5000)
	s.AssertScreenContains("FOCUS")
}

func TestFileBrowser(t *testing.T) {
	s := newSession(t)
	s.Start()

	s.WaitForText("FOCUS", 10000)

	// Press "a" to open file browser overlay; wait for a header that's unique
	// to the browser.
	s.Press("a")
	s.WaitForText("DIRECTORIES", 5000)
	s.AssertScreenContains("DETAILS")

	// Press Escape to close.
	s.Press("Escape")
	s.WaitForText(dashboardAnchor, 5000)
	s.AssertScreenContains("FOCUS")
}
