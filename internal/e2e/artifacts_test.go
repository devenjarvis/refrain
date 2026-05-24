//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"
	"time"
)

// artifactScenario replays a two-frame alt-screen sequence via raw content
// blocks. Turn 1 enters alt-screen, draws 4 lines plus a GHOST_ARTIFACT_FOO
// marker. Turn 2 (after 600ms) clears and redraws without the marker. The
// delay ensures refrain ticks frame 1 before frame 2 overwrites the VT cells.
var artifactScenario = scenarioFile{
	name: "artifact.yaml",
	content: `name: artifact
match:
  prompt: "go"
session:
  id: "e2e-artifact"
  model: "claude-sonnet-4-6"
turns:
  - assistant:
      - type: raw
        text: "\x1b[?1049h\x1b[2J\x1b[H"
      - type: raw
        text: "LINE 1\nLINE 2\nLINE 3\nLINE 4\n"
      - type: raw
        text: "GHOST_ARTIFACT_FOO"
  - delay: "600ms"
    assistant:
      - type: raw
        text: "\x1b[2J\x1b[H"
      - type: raw
        text: "LINE 1\nLINE 2\nLINE 3\nLINE 4\n"
      - type: raw
        text: "> "
`,
}

// TestArtifactsOnPlanReview drives a scrim agent through a plan-approval-shaped
// interaction: enter alt-screen, draw a frame ending in a distinctive marker,
// wait long enough for refrain to render it to the outer terminal, then redraw
// a shorter frame. The test asserts on refrain's emitted View (written to
// REFRAIN_E2E_DEBUG_DUMP by dashboard.View). Regression target: after
// alt-screen entry and a clean redraw, refrain's preview View must not contain
// GHOST_ARTIFACT_FOO.
func TestArtifactsOnPlanReview(t *testing.T) {
	s := newScrimSession(t, artifactScenario)
	dumpPath := t.TempDir() + "/baton_view_dump.txt"
	s.extraEnv = append(s.extraEnv, "REFRAIN_E2E_DEBUG_DUMP="+dumpPath)
	s.Start()
	s.WaitForText("FOCUS", 10000)

	s.Press("n")
	s.WaitForText("back", 10000)

	s.Type("go\n")

	// Wait past the in-scenario delay (600ms) plus a few refrain ticks so
	// the latest dump reflects frame 2.
	time.Sleep(2 * time.Second)
	s.WaitStable(500)

	view, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("reading view dump: %v", err)
	}
	viewStr := string(view)

	if !strings.Contains(viewStr, "LINE 4") {
		t.Fatalf("expected frame 2 content (LINE 4) in refrain view\nView:\n%s", viewStr)
	}
	if strings.Contains(viewStr, "GHOST_ARTIFACT_FOO") {
		t.Errorf("frame 1 ghost marker still in refrain's view after frame 2 redraw\nView:\n%s", viewStr)
	}
}
