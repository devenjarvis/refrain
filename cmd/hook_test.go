package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/refrain/internal/hook"
)

// envWithout returns env with the named vars filtered out.
func envWithout(env []string, names ...string) []string {
	filtered := make([]string, 0, len(env))
outer:
	for _, kv := range env {
		for _, n := range names {
			if strings.HasPrefix(kv, n+"=") {
				continue outer
			}
		}
		filtered = append(filtered, kv)
	}
	return filtered
}

// buildRefrain builds the refrain binary into a temp dir and returns the path.
func buildRefrain(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Dir(filepath.Dir(thisFile))
	bin := filepath.Join(t.TempDir(), "refrain")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building refrain: %v\n%s", err, out)
	}
	return bin
}

// TestHookSubcommandForwards runs the built refrain binary with each supported
// subcommand and asserts the server receives the event with the right kind,
// AgentID, and parsed payload fields.
func TestHookSubcommandForwards(t *testing.T) {
	bin := buildRefrain(t)

	cases := []struct {
		name        string
		subcmd      string
		wantKind    hook.Kind
		stdin       string
		wantSession string
		wantCWD     string
		wantMessage string
		wantPrompt  string
	}{
		{
			name:        "session-start",
			subcmd:      "session-start",
			wantKind:    hook.KindSessionStart,
			stdin:       `{"session_id":"uuid-xyz","cwd":"/tmp/wt"}`,
			wantSession: "uuid-xyz",
			wantCWD:     "/tmp/wt",
		},
		{
			name:        "stop",
			subcmd:      "stop",
			wantKind:    hook.KindStop,
			stdin:       `{"session_id":"uuid-xyz"}`,
			wantSession: "uuid-xyz",
		},
		{
			name:     "session-end",
			subcmd:   "session-end",
			wantKind: hook.KindSessionEnd,
			stdin:    `{}`,
		},
		{
			name:        "notification",
			subcmd:      "notification",
			wantKind:    hook.KindNotification,
			stdin:       `{"session_id":"uuid-xyz","message":"Claude needs your permission to use Bash"}`,
			wantSession: "uuid-xyz",
			wantMessage: "Claude needs your permission to use Bash",
		},
		{
			name:        "user-prompt-submit",
			subcmd:      "user-prompt-submit",
			wantKind:    hook.KindUserPromptSubmit,
			stdin:       `{"session_id":"uuid-xyz","prompt":"add dark mode"}`,
			wantSession: "uuid-xyz",
			wantPrompt:  "add dark mode",
		},
		{
			// Prompt on a non-UserPromptSubmit kind must be ignored.
			subcmd:      "notification",
			wantKind:    hook.KindNotification,
			stdin:       `{"session_id":"uuid-xyz","message":"perm","prompt":"shouldn't leak"}`,
			wantSession: "uuid-xyz",
			wantMessage: "perm",
		},
		{
			name:        "user-prompt-submit-with-prompt",
			subcmd:      "user-prompt-submit",
			wantKind:    hook.KindUserPromptSubmit,
			stdin:       `{"session_id":"uuid-xyz","prompt":"investigate flaky checkout test"}`,
			wantSession: "uuid-xyz",
			wantPrompt:  "investigate flaky checkout test",
		},
		{
			name:        "notification-ignores-prompt",
			subcmd:      "notification",
			wantKind:    hook.KindNotification,
			stdin:       `{"session_id":"uuid-xyz","prompt":"should be dropped","message":"perm"}`,
			wantSession: "uuid-xyz",
			wantMessage: "perm",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// macOS caps unix socket paths at 104 bytes — t.TempDir() under the
			// test name is too long, so use a short dir under os.TempDir().
			sockDir, err := os.MkdirTemp("", "bh")
			if err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
			socket := filepath.Join(sockDir, "h.sock")
			srv, err := hook.NewServer(socket)
			if err != nil {
				t.Fatalf("NewServer: %v", err)
			}
			defer func() { _ = srv.Close() }()

			cmd := exec.Command(bin, "hook", tc.subcmd)
			// Strip parent REFRAIN_* env to avoid leaking outer refrain session
			// state into the test subprocess.
			cleanEnv := envWithout(os.Environ(), "REFRAIN_HOOK_SOCKET", "REFRAIN_AGENT_ID")
			cmd.Env = append(
				cleanEnv,
				"REFRAIN_HOOK_SOCKET="+socket,
				"REFRAIN_AGENT_ID=test-agent-42",
			)
			cmd.Stdin = strings.NewReader(tc.stdin)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("running hook: %v\n%s", err, out)
			}

			select {
			case e := <-srv.Events():
				if e.Kind != tc.wantKind {
					t.Errorf("kind: got %q, want %q", e.Kind, tc.wantKind)
				}
				if e.AgentID != "test-agent-42" {
					t.Errorf("agent id: got %q, want %q", e.AgentID, "test-agent-42")
				}
				if e.SessionID != tc.wantSession {
					t.Errorf("session id: got %q, want %q", e.SessionID, tc.wantSession)
				}
				if e.CWD != tc.wantCWD {
					t.Errorf("cwd: got %q, want %q", e.CWD, tc.wantCWD)
				}
				if e.Message != tc.wantMessage {
					t.Errorf("message: got %q, want %q", e.Message, tc.wantMessage)
				}
				if e.Prompt != tc.wantPrompt {
					t.Errorf("prompt: got %q, want %q", e.Prompt, tc.wantPrompt)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("timed out waiting for hook event")
			}
		})
	}
}

// TestHookSubcommandNoEnv ensures the hook subcommand silently no-ops when
// REFRAIN_HOOK_SOCKET and REFRAIN_AGENT_ID aren't set — this is the case for a
// user running `claude` outside of refrain.
func TestHookSubcommandNoEnv(t *testing.T) {
	bin := buildRefrain(t)

	cmd := exec.Command(bin, "hook", "stop")
	// Deliberately no REFRAIN_* env vars — if refrain itself was launched inside
	// another refrain session the parent env may have leaked them in, so filter.
	cmd.Env = envWithout(os.Environ(), "REFRAIN_HOOK_SOCKET", "REFRAIN_AGENT_ID")
	cmd.Stdin = strings.NewReader(`{}`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0, got err: %v\n%s", err, out)
	}
	if len(out) != 0 {
		t.Errorf("expected no output, got: %q", out)
	}
}

// TestHookSubcommandUnknownEvent ensures unknown event names exit 0 silently.
func TestHookSubcommandUnknownEvent(t *testing.T) {
	bin := buildRefrain(t)

	cmd := exec.Command(bin, "hook", "made-up-event")
	cmd.Stdin = strings.NewReader(`{}`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("expected exit 0 for unknown event, got err: %v\n%s", err, out)
	}
}
