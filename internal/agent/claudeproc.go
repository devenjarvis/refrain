package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// claudeArgsOpts configures the shared `claude -p` flag set used by the
// planner, reviewer, and Haiku subprocesses. Each caller differs only in
// model, tool list, MCP config, and setting sources; the rest of the flags
// (strict MCP, no slash commands, no session persistence, trimmed system
// prompt) are identical and live in buildClaudeArgs.
type claudeArgsOpts struct {
	// model is the --model value. Required.
	model string
	// mcpConfig is the --mcp-config payload. Empty defaults to the canonical
	// `{"mcpServers":{}}` shape required by claude's strict validator — a bare
	// "{}" is rejected and makes every subprocess exit 1.
	mcpConfig string
	// tools is the --tools value (visibility filter). May be empty to expose
	// no tools.
	tools string
	// allowTools, when true, additionally passes --allowedTools <tools> so the
	// non-interactive -p subprocess auto-approves those same tools instead of
	// hitting a permission gate. --tools alone only controls availability.
	// The camelCase spelling is used (claude also accepts --allowed-tools) so
	// the scrim test double, which only registers --allowedTools, parses it.
	allowTools bool
	// settingSources is the --setting-sources value, e.g. "user" or
	// "user,project".
	settingSources string
}

// buildClaudeArgs assembles the shared `claude -p` argument list. When
// ANTHROPIC_API_KEY is present it adds --bare, which disables hooks, LSP,
// plugin sync, attribution, auto-memory, background prefetches, keychain
// reads, and CLAUDE.md auto-discovery. --bare requires API-key auth;
// OAuth-only users silently miss the win.
func buildClaudeArgs(o claudeArgsOpts) []string {
	args := []string{"-p", "--model", o.model}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		args = append(args, "--bare")
	}

	mcp := o.mcpConfig
	if mcp == "" {
		mcp = `{"mcpServers":{}}`
	}

	args = append(
		args,
		"--strict-mcp-config", "--mcp-config", mcp,
		"--disable-slash-commands",
		"--no-session-persistence",
		"--tools", o.tools,
	)
	if o.allowTools {
		args = append(args, "--allowedTools", o.tools)
	}
	args = append(
		args,
		"--setting-sources", o.settingSources,
		"--exclude-dynamic-system-prompt-sections",
	)
	return args
}

// claudeRunOpts configures a one-shot `claude -p` subprocess invocation.
type claudeRunOpts struct {
	// args is the CLI argument list, typically from buildClaudeArgs.
	args []string
	// stdin is piped to the subprocess.
	stdin string
	// extraEnv is appended after the sanitized base environment, e.g. the
	// planner's REFRAIN_PLANNER_QUESTION_SOCKET entry.
	extraEnv []string
	// dir sets cmd.Dir when non-empty so the subprocess reads the correct repo.
	dir string
	// errPrefix prefixes the wrapped error, e.g. "claude planner".
	errPrefix string
	// errIncludeStdout includes trimmed stdout in the error message (useful for
	// the planner, where partial markdown aids debugging).
	errIncludeStdout bool
}

// runClaudeSubprocess runs claude with refrain's hook-wiring env stripped and
// returns trimmed stdout. It centralizes the env sanitization, WaitDelay drain
// bound, and stdout/stderr capture shared by the planner, reviewer, and Haiku
// callers.
//
// The env is sanitized (via sanitizedHaikuEnv) so the subprocess does not
// register hook callbacks against the running TUI's socket as the parent
// agent — inheriting those vars caused phantom SessionStart/SessionEnd events
// and per-call socket roundtrips that inflated cold-start latency.
//
// WaitDelay bounds how long Wait() blocks on pipe drain after the context
// kills the process; otherwise a descendant sleep can hold the stdout pipe
// open and keep Wait blocked long past cancellation.
func runClaudeSubprocess(ctx context.Context, claudePath string, opts claudeRunOpts) (string, error) {
	cmd := exec.CommandContext(ctx, claudePath, opts.args...)
	cmd.Stdin = strings.NewReader(opts.stdin)

	env := sanitizedHaikuEnv(os.Environ())
	if len(opts.extraEnv) > 0 {
		env = append(env, opts.extraEnv...)
	}
	cmd.Env = env
	if opts.dir != "" {
		cmd.Dir = opts.dir
	}
	cmd.WaitDelay = 500 * time.Millisecond

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if opts.errIncludeStdout {
			return "", fmt.Errorf("%s: %w (stdout=%q stderr=%q)", opts.errPrefix, err,
				strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()))
		}
		return "", fmt.Errorf("%s: %w (stderr=%q)", opts.errPrefix, err, strings.TrimSpace(stderr.String()))
	}

	return strings.TrimSpace(stdout.String()), nil
}
