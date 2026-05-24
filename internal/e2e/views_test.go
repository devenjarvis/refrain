//go:build e2e

package e2e

import "testing"

// Anchor strings used to wait for specific TUI views to render.
//
//   - dashboardAnchor: only appears in the dashboard status bar (not in any
//     overlay's hint set, all of which use "navigate" too).
//   - listFocusAnchor: dashboard text shown only when focus is on the list.
const (
	dashboardAnchor = "new session"
	listFocusAnchor = "new session"
)

func TestSettingsOverlay(t *testing.T) {
	s := newScrimSession(t)
	s.Start()

	s.WaitForText("FOCUS", 10000)

	s.Press("s")
	s.WaitForText("Audio Enabled", 5000)
	s.AssertScreenContains("Bypass Permissions")

	s.Press("Escape")
	s.WaitForText(dashboardAnchor, 5000)
	s.AssertScreenContains("FOCUS")
}

func TestDiffView(t *testing.T) {
	// This test requires real bash commands to create files and git commits
	// in the worktree. Scrim can't execute real commands, so bash stays.
	s := newSession(t)
	s.Start()

	s.WaitForText("FOCUS", 10000)

	s.Press("n")
	s.WaitForText(`\$`, 10000)

	s.Type("echo test > file.txt && git add file.txt && git commit -m 'add file' && echo COMMIT_DONE\n")
	s.WaitForText("COMMIT_DONE", 10000)

	s.Press("Escape")
	s.WaitForText(listFocusAnchor, 5000)

	s.Press("d")
	s.WaitForText("side-by-side", 5000)
	s.AssertScreenContains("file.txt")

	s.Press("Escape")
	s.WaitForText(dashboardAnchor, 5000)
	s.AssertScreenContains("FOCUS")
}

func TestFileBrowser(t *testing.T) {
	s := newScrimSession(t)
	s.Start()

	s.WaitForText("FOCUS", 10000)

	s.Press("a")
	s.WaitForText("DIRECTORIES", 5000)
	s.AssertScreenContains("DETAILS")

	s.Press("Escape")
	s.WaitForText(dashboardAnchor, 5000)
	s.AssertScreenContains("FOCUS")
}
