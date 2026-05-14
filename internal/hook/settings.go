package hook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// settingsSchema mirrors the shape Claude's settings.json expects for hooks.
//
// Each event name (SessionStart, Stop, SessionEnd) maps to a list of matcher
// objects, each carrying a list of hook commands. We don't use matchers for
// lifecycle hooks, so the matcher list has a single empty entry.
type settingsSchema struct {
	Hooks map[string][]hookMatcher `json:"hooks"`
}

type hookMatcher struct {
	// Matcher filters which tool calls trigger this hook entry. Required for
	// PreToolUse — without it Claude Code does not fire the hook. Use "*" to
	// match all tool calls. Omitted (empty) for lifecycle hooks (SessionStart,
	// Stop, etc.) where the event type alone is the filter.
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []hookCommand `json:"hooks"`
}

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// WriteHooksFile writes a minimal Claude settings file to path that wires up
// SessionStart, Stop, and SessionEnd to `refrain hook <event>`.
//
// The refrain binary is resolved via os.Executable so the generated file works
// regardless of where Claude is invoked from. Socket path and agent ID are
// not baked into the command — they travel through REFRAIN_HOOK_SOCKET and
// REFRAIN_AGENT_ID, which the refrain hook CLI reads at runtime.
func WriteHooksFile(path string) error {
	batonPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving refrain binary: %w", err)
	}
	// Symlink resolution gives us a stable path; fall back to the raw result if
	// EvalSymlinks fails for any reason.
	if resolved, err := filepath.EvalSymlinks(batonPath); err == nil {
		batonPath = resolved
	}

	schema := settingsSchema{
		Hooks: map[string][]hookMatcher{
			"SessionStart":     {{Hooks: []hookCommand{{Type: "command", Command: batonPath + " hook session-start"}}}},
			"Stop":             {{Hooks: []hookCommand{{Type: "command", Command: batonPath + " hook stop"}}}},
			"SessionEnd":       {{Hooks: []hookCommand{{Type: "command", Command: batonPath + " hook session-end"}}}},
			"Notification":     {{Hooks: []hookCommand{{Type: "command", Command: batonPath + " hook notification"}}}},
			"UserPromptSubmit": {{Hooks: []hookCommand{{Type: "command", Command: batonPath + " hook user-prompt-submit"}}}},
			"PreToolUse":       {{Matcher: "*", Hooks: []hookCommand{{Type: "command", Command: batonPath + " hook pre-tool-use"}}}},
		},
	}

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling hooks settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating settings dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing hooks settings: %w", err)
	}
	return nil
}
