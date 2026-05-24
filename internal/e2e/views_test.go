//go:build e2e

package e2e

import "testing"

// Anchor strings used to wait for specific TUI views to render.
//
//   - dashboardAnchor: only appears in the dashboard status bar (not in any
//     overlay's hint set, all of which use "navigate" too).
//   - listFocusAnchor: dashboard text shown only when focus is on the list.
const (
	dashboardAnchor = "n session"
	listFocusAnchor = "n session"
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

var diffScenario = scenarioFile{
	name: "diff.yaml",
	content: `name: diff
match:
  prompt: "go"
session:
  id: "e2e-diff"
  model: "claude-sonnet-4-6"
turns:
  - exec: "echo test > file.txt && git add file.txt && git commit -m 'add file'"
    assistant:
      - type: text
        text: "COMMIT_DONE"
`,
}

func TestDiffView(t *testing.T) {
	s := newScrimSession(t, diffScenario)
	s.Start()

	s.WaitForText("FOCUS", 10000)

	s.Press("n")
	s.WaitForText("back", 10000)

	s.Type("go\n")
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
