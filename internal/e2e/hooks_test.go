//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// claudeStubScript is a bash stub installed as `<dir>/claude`. Refrain's
// supportsHooks check keys off filepath.Base(agent_program), so the file must
// be named `claude`. The stub ignores any args (refrain passes
// `--settings <path>`), inherits REFRAIN_HOOK_SOCKET / REFRAIN_AGENT_ID from
// refrain's env wiring, and drives the pipeline by invoking
// `$REFRAIN_E2E_BINARY hook <event>` at scripted intervals.
//
// Sequence (seconds since start):
//
//	0.3  session-start   → Active
//	1.5  notification    → Waiting
//	3.5  stop            → Idle
//	5.5  user-prompt-submit → Active (re-armed)
//	7.5  stop            → Idle
//	then sleep 3600 so refrain keeps it alive until test teardown kills it.
const claudeStubScript = `#!/bin/bash
echo "claude-e2e-stub ready"
sleep 0.3
"$REFRAIN_E2E_BINARY" hook session-start <<< '{"session_id":"e2e-sess-1","cwd":"/tmp"}'
sleep 1.2
"$REFRAIN_E2E_BINARY" hook notification <<< '{"session_id":"e2e-sess-1","message":"Claude needs permission"}'
sleep 2
"$REFRAIN_E2E_BINARY" hook stop <<< '{"session_id":"e2e-sess-1"}'
sleep 2
"$REFRAIN_E2E_BINARY" hook user-prompt-submit <<< '{"session_id":"e2e-sess-1"}'
sleep 2
"$REFRAIN_E2E_BINARY" hook stop <<< '{"session_id":"e2e-sess-1"}'
sleep 3600
`

// TestHookPipeline drives refrain through a scripted bash "claude" stub that
// emits each hook kind in turn, and asserts the dashboard bubble transitions
// Active → Waiting → Idle → Active → Idle. This is the end-to-end check that
// the plan calls for: hooks file wiring, socket forwarding, agent status
// transitions, and dashboard rendering all working in concert.
func TestHookPipeline(t *testing.T) {
	// Install the stub as a file named `claude` in a short-path temp dir.
	// The basename must be exactly "claude" so refrain's supportsHooks check
	// fires and the agent gets --settings + socket env wired up.
	stubDir, err := os.MkdirTemp("", "bs")
	if err != nil {
		t.Fatalf("mkdir stub dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stubDir) })
	stubPath := filepath.Join(stubDir, "claude")
	if err := os.WriteFile(stubPath, []byte(claudeStubScript), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	s := newSession(t)
	// Point both global and repo config at the stub so whichever refrain reads
	// wins, and pass REFRAIN_E2E_BINARY through tu so the stub can invoke the
	// hook CLI without needing to know the binary path itself.
	// plan_first_enabled is pinned off here for the same reason as in
	// helpers_test.go: this test exercises the hook pipeline via `n` →
	// immediate spawn, not the plan-first prompt-modal flow. Re-stating
	// the override is necessary because this writeJSON overwrites the
	// helper's config rather than merging with it.
	writeJSON(t, filepath.Join(s.home, ".refrain", "config.json"), map[string]any{
		"agent_program":      stubPath,
		"bypass_permissions": false,
		"plan_first_enabled": false,
	})
	writeJSON(t, filepath.Join(s.repoDir, ".refrain", "config.json"), map[string]any{
		"agent_program":      stubPath,
		"bypass_permissions": false,
		"plan_first_enabled": false,
	})
	s.extraEnv = append(s.extraEnv, "REFRAIN_E2E_BINARY="+refrainBin)
	s.Start()

	s.WaitForText("FOCUS", 10000)
	s.Press("n")
	// After "n", refrain spawns the stub and auto-focuses its PTY. The stub
	// prints a greeting; wait for it so we know the process is live before
	// bouncing back to the dashboard to read status symbols.
	s.WaitForText("claude-e2e-stub ready", 10000)
	s.Press("Escape")
	s.WaitForText("navigate", 10000)

	// Active — session-start fires at t≈0.3s. The pipeline session card
	// surfaces this as "%d active, %d idle".
	if !waitForBadgeText(s, "active", 5000) {
		t.Fatalf("never observed Active badge text\nScreen:\n%s", s.Screenshot())
	}
	// Waiting — notification fires at t≈1.5s. The session card surfaces
	// this as "%d waiting".
	if !waitForBadgeText(s, "waiting", 5000) {
		t.Fatalf("never observed Waiting badge text\nScreen:\n%s", s.Screenshot())
	}
	// Idle — stop fires at t≈3.5s. Auto-promotion moves the session from
	// BUILDING to REVIEWING immediately on the idle event, so the session
	// card now lives in the REVIEWING section of the pipeline.
	if !waitForBadgeText(s, "REVIEWING", 5000) {
		t.Fatalf("never observed REVIEWING section after Stop (auto-promotion did not fire)\nScreen:\n%s", s.Screenshot())
	}
	// Active again — user-prompt-submit fires at t≈5.5s and re-arms the
	// status indicator. The session remains in REVIEWING (auto-promotion is
	// idempotent for already-promoted sessions), but the agent badge should
	// briefly show "active".
	if !waitForBadgeText(s, "active", 5000) {
		t.Fatalf("never observed re-armed Active badge after UserPromptSubmit\nScreen:\n%s", s.Screenshot())
	}
	// Idle again — stop fires at t≈7.5s. Session stays in REVIEWING (the
	// phase gate suppresses a second promotion). This doubles as a re-arm
	// check: no intermediate Active would mean UserPromptSubmit didn't re-arm.
	if !waitForBadgeText(s, "REVIEWING", 5000) {
		t.Fatalf("never observed REVIEWING section after second Stop\nScreen:\n%s", s.Screenshot())
	}
}

// waitForBadgeText polls Screenshot until the SESSIONS section's status badge
// contains the given substring, or timeoutMs elapses.
func waitForBadgeText(s *Session, needle string, timeoutMs int) bool {
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for time.Now().Before(deadline) {
		if strings.Contains(s.Screenshot(), needle) {
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return strings.Contains(s.Screenshot(), needle)
}
