package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/devenjarvis/baton/internal/hook"
	"github.com/spf13/cobra"
)

var hookCmd = &cobra.Command{
	Use:   "hook <event>",
	Short: "Forward a Claude Code hook event to the running baton process",
	Long: `hook is invoked by Claude Code via the settings file baton writes before
spawning a session. It reads the Claude hook JSON payload on stdin and
forwards it to the baton TUI over the unix socket named by BATON_HOOK_SOCKET.

This command is not intended to be run by humans. Output is kept silent —
Claude interprets stdout from hooks as feedback. Errors go to stderr. Exit
code is always 0 so hook failures never block Claude.`,
	Args: cobra.ExactArgs(1),
	RunE: runHook,
	// Silence Cobra's usage printing on error — a stray usage dump would be
	// interpreted by Claude as hook feedback.
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.AddCommand(hookCmd)
}

func runHook(cmd *cobra.Command, args []string) error {
	// Resolve the event kind. Unknown events exit 0 silently so we don't break
	// Claude if a future hook name is wired in by accident.
	var kind hook.Kind
	switch args[0] {
	case "session-start":
		kind = hook.KindSessionStart
	case "stop":
		kind = hook.KindStop
	case "session-end":
		kind = hook.KindSessionEnd
	case "notification":
		kind = hook.KindNotification
	case "user-prompt-submit":
		kind = hook.KindUserPromptSubmit
	case "pre-tool-use":
		kind = hook.KindPreToolUse
	default:
		return nil
	}

	socketPath := os.Getenv("BATON_HOOK_SOCKET")
	agentID := os.Getenv("BATON_AGENT_ID")
	// Without the env vars there's no route to any running baton — exit
	// silently so running `claude` outside of baton doesn't spew errors.
	if socketPath == "" || agentID == "" {
		return nil
	}

	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		fmt.Fprintln(os.Stderr, "baton hook: reading stdin:", err)
		return nil
	}

	// Parse just the fields we route on; keep the rest in Raw so the server
	// can inspect extras if it cares later. `message` is only populated by
	// Notification payloads; `prompt` only by UserPromptSubmit; `tool_name`
	// and `tool_input` only by PreToolUse — other kinds leave them empty.
	var payload struct {
		SessionID string          `json:"session_id"`
		CWD       string          `json:"cwd"`
		Message   string          `json:"message"`
		Prompt    string          `json:"prompt"`
		ToolName  string          `json:"tool_name"`
		ToolInput json.RawMessage `json:"tool_input"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			// Claude may send a non-JSON payload or nothing at all; that's fine.
			payload.SessionID = ""
		}
	}

	e := hook.Event{
		Kind:      kind,
		AgentID:   agentID,
		SessionID: payload.SessionID,
		CWD:       payload.CWD,
		Message:   payload.Message,
		Raw:       json.RawMessage(raw),
	}
	if kind == hook.KindUserPromptSubmit {
		e.Prompt = payload.Prompt
	}
	if kind == hook.KindPreToolUse {
		e.ToolName = payload.ToolName
		e.ToolInput = payload.ToolInput
	}

	if os.Getenv("BATON_HOOK_DEBUG") != "" {
		hookDebugLog(socketPath, kind, payload.ToolName, e.ToolInput, raw)
	}

	if err := hook.SendEvent(socketPath, e); err != nil {
		fmt.Fprintln(os.Stderr, "baton hook: forwarding event:", err)
	}
	return nil
}

// hookDebugLog appends a debug line to .baton/logs/hooks.log when BATON_HOOK_DEBUG is set.
// socketPath is used to derive the .baton directory — it lives at <repo>/.baton/hook.sock.
func hookDebugLog(socketPath string, kind hook.Kind, toolName string, toolInput json.RawMessage, raw []byte) {
	dir := filepath.Join(filepath.Dir(socketPath), "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "hooks.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	ts := time.Now().Format(time.RFC3339)
	_, _ = fmt.Fprintf(f, "%s kind=%s tool_name=%q tool_input_len=%d raw_len=%d\n",
		ts, kind, toolName, len(toolInput), len(raw))
}
