// Package hook carries Claude Code hook events between a spawned `refrain hook`
// CLI invocation and the long-running refrain TUI process over a unix socket.
//
// The protocol is newline-delimited JSON: one event per line. See event.go for
// the struct, server.go for the listener, and client.go for the one-shot sender.
package hook

import "encoding/json"

// Kind identifies which Claude Code lifecycle event fired.
type Kind string

const (
	KindSessionStart     Kind = "session-start"
	KindStop             Kind = "stop"
	KindSessionEnd       Kind = "session-end"
	KindNotification     Kind = "notification"
	KindUserPromptSubmit Kind = "user-prompt-submit"
	KindPreToolUse       Kind = "pre-tool-use"
)

// Event is the wire-format payload sent from the refrain hook CLI to the
// refrain TUI process.
//
// SessionID and CWD come straight from the Claude JSON payload; AgentID is
// supplied by the CLI wrapper from the REFRAIN_AGENT_ID environment variable
// so the server can dispatch to the right agent. Message is populated from
// Notification payloads (empty for other kinds). Prompt is populated from
// UserPromptSubmit payloads (empty for other kinds). ToolName and ToolInput
// are populated from PreToolUse payloads (empty for other kinds). Raw preserves
// the original Claude JSON for forward-compatibility with fields the server
// doesn't currently consume.
type Event struct {
	Kind      Kind            `json:"kind"`
	AgentID   string          `json:"agent_id"`
	SessionID string          `json:"session_id,omitempty"`
	CWD       string          `json:"cwd,omitempty"`
	Message   string          `json:"message,omitempty"`
	Prompt    string          `json:"prompt,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
	Raw       json.RawMessage `json:"raw,omitempty"`
}
