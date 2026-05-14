package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteHooksFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".refrain-hooks.json")

	if err := WriteHooksFile(path); err != nil {
		t.Fatalf("WriteHooksFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading hooks file: %v", err)
	}

	var parsed settingsSchema
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshalling hooks file: %v", err)
	}

	wantEvents := []string{"SessionStart", "Stop", "SessionEnd", "Notification", "UserPromptSubmit", "PreToolUse"}
	wantCLIEvents := map[string]string{
		"SessionStart":     "session-start",
		"Stop":             "stop",
		"SessionEnd":       "session-end",
		"Notification":     "notification",
		"UserPromptSubmit": "user-prompt-submit",
		"PreToolUse":       "pre-tool-use",
	}

	batonPath, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(batonPath); err == nil {
		batonPath = resolved
	}

	// PreToolUse must have matcher="*" so Claude fires it for all tool calls;
	// lifecycle hooks (SessionStart, Stop, etc.) need no matcher.
	wantMatcher := map[string]string{
		"PreToolUse": "*",
	}

	for _, event := range wantEvents {
		matchers, ok := parsed.Hooks[event]
		if !ok {
			t.Errorf("missing hook for event %q", event)
			continue
		}
		if len(matchers) != 1 {
			t.Errorf("event %q: expected 1 matcher, got %d", event, len(matchers))
			continue
		}
		if m := wantMatcher[event]; m != "" && matchers[0].Matcher != m {
			t.Errorf("event %q: expected matcher %q, got %q", event, m, matchers[0].Matcher)
		}
		if len(matchers[0].Hooks) != 1 {
			t.Errorf("event %q: expected 1 hook, got %d", event, len(matchers[0].Hooks))
			continue
		}
		hook := matchers[0].Hooks[0]
		if hook.Type != "command" {
			t.Errorf("event %q: expected type 'command', got %q", event, hook.Type)
		}
		if !strings.HasPrefix(hook.Command, batonPath) {
			t.Errorf("event %q: command %q does not start with refrain path %q", event, hook.Command, batonPath)
		}
		if !strings.Contains(hook.Command, "hook "+wantCLIEvents[event]) {
			t.Errorf("event %q: command %q missing CLI event %q", event, hook.Command, wantCLIEvents[event])
		}
	}
}

func TestWriteHooksFileCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "subdir", "hooks.json")

	if err := WriteHooksFile(path); err != nil {
		t.Fatalf("WriteHooksFile: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("hooks file not created: %v", err)
	}
}
