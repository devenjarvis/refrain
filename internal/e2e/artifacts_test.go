//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestArtifactsOnPlanReview drives a bash-scripted agent through a
// plan-approval-shaped interaction: enter alt-screen, draw a frame ending in a
// distinctive marker, sleep long enough for baton to render it to the outer
// terminal, then redraw a shorter frame. The test asserts on baton's emitted
// View (written to BATON_E2E_DEBUG_DUMP by dashboard.View), not on the
// downstream terminal emulator — because lipgloss already width-pads content
// inside the preview box, the Render()→RenderPadded() distinction shows up
// deterministically in baton's output but can be masked by tu/Bubble Tea diff
// rendering at the terminal layer. Regression target: after alt-screen entry
// and a clean redraw, baton's preview View must not contain GHOST_ARTIFACT_FOO.
func TestArtifactsOnPlanReview(t *testing.T) {
	s := newSession(t)
	dumpPath := t.TempDir() + "/baton_view_dump.txt"
	s.extraEnv = append(s.extraEnv, "BATON_E2E_DEBUG_DUMP="+dumpPath)
	s.Start()
	s.WaitForText("FOCUS", 10000)

	// Create a session; "n" auto-focuses the terminal and launches bash.
	s.Press("n")
	s.WaitForText(`\$`, 10000)

	// Frame 1: alt-screen enter + clear + home, 4 rows, then a long
	// GHOST_ARTIFACT marker on row 5. The marker is longer than frame 2's
	// prompt so trailing cells must be cleared for the artifact to vanish.
	// The first sleep is tuned above the 100ms baton tick so baton definitely
	// ticks frame 1 into the outer terminal before frame 2 overwrites the VT
	// cells. Frame 2 uses \e[2J\e[H (clear + home) so every VT cell beyond
	// '> ' is explicitly EmptyCell — this is exactly the condition that
	// exposes renderLine's trailing-whitespace trim.
	script := `printf '\033[?1049h\033[2J\033[H' && ` +
		`for i in 1 2 3 4; do echo "LINE $i"; done && ` +
		`printf 'GHOST_ARTIFACT_FOO'; ` +
		`sleep 0.6; ` +
		`printf '\033[2J\033[H' && ` +
		`for i in 1 2 3 4; do echo "LINE $i"; done && ` +
		`printf '> '; sleep 30`
	s.Type(script + "\n")

	// Wait past the in-script sleep plus a few baton ticks so the latest
	// dump reflects frame 2.
	time.Sleep(2 * time.Second)
	s.WaitStable(500)

	view, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("reading view dump: %v", err)
	}
	viewStr := string(view)

	if !strings.Contains(viewStr, "LINE 4") {
		t.Fatalf("expected frame 2 content (LINE 4) in baton view\nView:\n%s", viewStr)
	}
	if strings.Contains(viewStr, "GHOST_ARTIFACT_FOO") {
		t.Errorf("frame 1 ghost marker still in baton's view after frame 2 redraw\nView:\n%s", viewStr)
	}
}
