//go:build e2e

package e2e

import "testing"

func TestSettingsOverlay(t *testing.T) {
	s := newScrimSession(t)
	s.Start()

	s.WaitForText(listAnchor, 10000)

	s.Press("s")
	s.WaitForText("Audio Enabled", 5000)
	s.AssertScreenContains("Bypass Permissions")

	s.Press("Escape")
	s.WaitForText(listAnchor, 5000)
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

// TestDiffView drives a raw session end-to-end: blank REPL, a typed prompt in
// the passthrough terminal, a commit made by the agent, and the diff viewer
// over the result.
func TestDiffView(t *testing.T) {
	s := newScrimSession(t, diffScenario)
	s.Start()

	s.WaitForText(listAnchor, 10000)

	createBlankSession(t, s)

	s.Type("go\n")
	s.WaitForText("COMMIT_DONE", 10000)

	s.Press("Escape")
	s.WaitForText(listAnchor, 5000)

	s.Press("d")
	s.WaitForText("side-by-side", 5000)
	s.AssertScreenContains("file.txt")

	s.Press("Escape")
	s.WaitForText(listAnchor, 5000)
}

func TestFileBrowser(t *testing.T) {
	s := newScrimSession(t)
	s.Start()

	s.WaitForText(listAnchor, 10000)

	s.Press("a")
	s.WaitForText("DIRECTORIES", 5000)
	s.AssertScreenContains("DETAILS")

	s.Press("Escape")
	s.WaitForText(listAnchor, 5000)
}
