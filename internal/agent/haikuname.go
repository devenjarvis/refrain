package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// BranchNamer asynchronously summarizes a user prompt into a branch-slug
// suitable for concatenation with the configured branch prefix. The instruction
// passed in is the fully rendered template (the user's BranchNamePrompt with
// the {prompt} token already substituted) so the namer can pipe it verbatim to
// the underlying model. The returned slug has already been normalized through
// slugify() so callers can use it without additional sanitization.
type BranchNamer func(ctx context.Context, instruction string) (string, error)

// TaskSummarizer asynchronously summarizes a user prompt into a short
// plain-English description (10-15 words) suitable for display in the TUI.
// Unlike BranchNamer the result is NOT slugified — it is display text.
// Returns ("", nil) on empty prompt and on any subprocess failure — callers
// should treat "" as "no summary available" and fall back gracefully.
type TaskSummarizer func(ctx context.Context, prompt string) (string, error)

// taskSummaryPrompt is prepended to the user prompt when asking Haiku for a
// short plain-English task description.
const taskSummaryPrompt = "Describe this task in 10-15 words of plain English, no punctuation at the end:\n\n"

const claudeHaikuModel = "claude-haiku-4-5"

// ErrClaudeNotFound is returned when the `claude` binary is not on PATH.
// The retry wrapper treats this as terminal — no amount of retrying will
// produce the binary, so we fail fast and let the next user prompt re-attempt.
var ErrClaudeNotFound = errors.New("claude not found on PATH")

// ErrEmptySlug is returned when claude's response slugifies to the empty
// string (e.g. punctuation-only output, or output that doesn't begin with
// an alphanumeric character). Treated as terminal — retrying the same
// instruction is unlikely to produce a different result.
var ErrEmptySlug = errors.New("empty slug after slugify")

// batonHookEnvVars lists env vars that wire a claude subprocess into baton's
// running hook server. These must be stripped from the Haiku subprocess so
// it doesn't fire phantom hook events back into the TUI as the parent agent.
var batonHookEnvVars = []string{
	"BATON_HOOK_SOCKET",
	"BATON_AGENT_ID",
}

// DefaultBranchNamer returns a BranchNamer that shells out to
// `claude -p --model claude-haiku-4-5` to summarize the user's first prompt.
// The instruction is piped in on stdin so the argv stays bounded regardless
// of prompt or template length.
//
// This namer always uses "claude" on PATH, independent of cfg.AgentProgram —
// users who configure a non-claude agent will simply get no rename when
// claude is absent (the random branch persists, retried on next prompt).
func DefaultBranchNamer() BranchNamer {
	return func(ctx context.Context, instruction string) (string, error) {
		claudePath, err := exec.LookPath("claude")
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrClaudeNotFound, err)
		}
		return runClaudeHaiku(ctx, claudePath, instruction)
	}
}

// DefaultTaskSummarizer returns a TaskSummarizer that shells out to
// `claude -p --model claude-haiku-4-5` to produce a short plain-English
// description of the user's task. The result is NOT slugified — it is display
// text for the TUI. Returns ("", nil) on empty prompt and ("", nil) on a
// subprocess runtime failure (callers treat "" as "no summary available").
// Surfacing ErrClaudeNotFound when `claude` is missing from PATH is the one
// exception: that's a terminal condition the retry helper should short-circuit
// on, mirroring DefaultBranchNamer; the manager-side wrapper coerces it back
// to "" before storing on the session.
func DefaultTaskSummarizer() TaskSummarizer {
	return func(ctx context.Context, prompt string) (string, error) {
		if strings.TrimSpace(prompt) == "" {
			return "", nil
		}
		claudePath, err := exec.LookPath("claude")
		if err != nil {
			// Mirror DefaultBranchNamer: surface the sentinel so the retry
			// helper short-circuits on attempt 1 instead of burning the
			// remaining budget on a lookup that will fail identically every
			// time. The manager-level wrapper coerces this back to "" before
			// storing on the session, so the public-boundary "no summary"
			// behavior is preserved.
			return "", fmt.Errorf("%w: %v", ErrClaudeNotFound, err)
		}
		text, err := runClaudeHaikuText(ctx, claudePath, taskSummaryPrompt+prompt)
		if err != nil {
			return "", nil //nolint:nilerr // swallow subprocess errors; callers treat "" as "no summary available"
		}
		return text, nil
	}
}

// buildClaudeHaikuArgs returns the argv (excluding the binary path) used to
// invoke `claude` for one-shot Haiku summarization. Always-on flags disable
// the parts of `claude -p`'s cold start path that aren't needed for a
// non-interactive text summarization (MCP server discovery, slash command
// resolution, session persistence, built-in tools, dynamic system prompt
// sections). When ANTHROPIC_API_KEY is present we additionally pass --bare,
// which disables hooks, LSP, plugin sync, attribution, auto-memory,
// background prefetches, keychain reads, and CLAUDE.md auto-discovery.
// --bare requires API-key auth; OAuth-only users silently miss the win.
//
// The --mcp-config payload MUST be a JSON object containing an "mcpServers"
// key (with an empty record as the value to declare zero servers). Claude's
// strict schema validator rejects a bare "{}" with
// `mcpServers: Invalid input: expected record, received undefined`, which
// makes every Haiku subprocess exit 1 in ~225ms and silently breaks the
// branch rename and task summary flows. Do not simplify this back to "{}".
func buildClaudeHaikuArgs() []string {
	args := []string{"-p", "--model", claudeHaikuModel}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		args = append(args, "--bare")
	}
	args = append(
		args,
		"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--disable-slash-commands",
		"--no-session-persistence",
		"--tools", "",
		"--setting-sources", "user",
		"--exclude-dynamic-system-prompt-sections",
	)
	return args
}

// runClaudeHaikuText runs `claude -p --model claude-haiku-4-5` with instruction
// on stdin and returns the trimmed raw stdout. No slugification is applied.
// This is the shared subprocess path; runClaudeHaiku adds slugification on top.
func runClaudeHaikuText(ctx context.Context, claudePath, instruction string) (string, error) {
	cmd := exec.CommandContext(ctx, claudePath, buildClaudeHaikuArgs()...)
	cmd.Stdin = strings.NewReader(instruction)
	// Strip baton's hook-wiring env vars so the Haiku subprocess does not
	// register hook callbacks against the running TUI's socket as the parent
	// agent. Inheriting these caused phantom SessionStart/SessionEnd events
	// to land on the parent agent and added per-call socket roundtrips that
	// inflated cold-start latency past the rename timeout.
	cmd.Env = sanitizedHaikuEnv(os.Environ())
	// Bound how long Wait() blocks on pipe drain after the context kills the
	// process — otherwise a descendant sleep can hold the stdout pipe open
	// and keep Wait blocked long past cancellation.
	cmd.WaitDelay = 500 * time.Millisecond

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude haiku: %w (stderr=%q)", err, strings.TrimSpace(stderr.String()))
	}

	return strings.TrimSpace(stdout.String()), nil
}

func runClaudeHaiku(ctx context.Context, claudePath, instruction string) (string, error) {
	raw, err := runClaudeHaikuText(ctx, claudePath, instruction)
	if err != nil {
		return "", err
	}

	slug := slugify(raw)
	if slug == "" {
		return "", ErrEmptySlug
	}
	return slug, nil
}

// sanitizedHaikuEnv returns env minus baton's hook-wiring vars. Everything
// else (PATH, HOME, XDG_*, ANTHROPIC_*, etc.) is preserved — claude needs
// auth and config to run.
func sanitizedHaikuEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		if shouldStripHaikuEnv(e) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func shouldStripHaikuEnv(entry string) bool {
	for _, name := range batonHookEnvVars {
		if strings.HasPrefix(entry, name+"=") {
			return true
		}
	}
	return false
}

// haikuCallable is the per-attempt subprocess closure invoked by
// callHaikuWithRetry. Returning a non-nil error or an empty string triggers
// retry (subject to terminalEmpty); ErrClaudeNotFound and ErrEmptySlug are
// always terminal regardless.
type haikuCallable func(ctx context.Context) (string, error)

// callHaikuWithRetry invokes call up to maxAttempts times, each bounded by
// perAttempt, returning early on success. Treats ErrClaudeNotFound and
// ErrEmptySlug as terminal — retrying won't fix a missing binary or a model
// that returned junk. When terminalEmpty is true, an ("", nil) return is
// also treated as terminal (the branch namer wants this); when false, the
// helper retries on empty results too. Aborts immediately when done is
// closed (manager shutdown) or when the outer ctx expires.
//
// perAttemptLog, if non-nil, is invoked with the attempt number (1-based),
// the result/error, and the wall-clock duration. Used to write per-attempt
// lines to the diagnostic log without coupling this wrapper to a file path.
func callHaikuWithRetry(
	ctx context.Context,
	call haikuCallable,
	done <-chan struct{},
	maxAttempts int,
	perAttempt time.Duration,
	backoff []time.Duration,
	terminalEmpty bool,
	perAttemptLog func(attempt int, result string, err error, took time.Duration),
) (string, error) {
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		select {
		case <-done:
			return "", context.Canceled
		default:
		}

		attemptCtx, cancel := context.WithTimeout(ctx, perAttempt)
		start := time.Now()
		result, err := call(attemptCtx)
		cancel()
		took := time.Since(start)

		if perAttemptLog != nil {
			perAttemptLog(attempt, result, err, took)
		}

		if err == nil && result != "" {
			return result, nil
		}

		// Terminal errors: don't retry.
		if errors.Is(err, ErrClaudeNotFound) || errors.Is(err, ErrEmptySlug) {
			return "", err
		}
		if err == nil && result == "" && terminalEmpty {
			// Defensive: callable returned ("", nil). Treat as terminal for
			// callers (e.g. the branch namer) that opt in.
			return "", ErrEmptySlug
		}

		if err != nil {
			lastErr = err
		} else if result == "" {
			lastErr = errors.New("empty result")
		}
		if attempt == maxAttempts {
			break
		}

		var wait time.Duration
		if idx := attempt - 1; idx < len(backoff) {
			wait = backoff[idx]
		}
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-done:
				timer.Stop()
				return "", context.Canceled
			case <-ctx.Done():
				timer.Stop()
				return "", ctx.Err()
			}
		}
	}
	return "", lastErr
}

// callNamerWithRetry is a thin compatibility wrapper around callHaikuWithRetry
// that binds a BranchNamer + instruction into the generalized callable.
// Preserved so existing tests of the retry mechanics keep working unchanged.
func callNamerWithRetry(
	ctx context.Context,
	namer BranchNamer,
	instruction string,
	done <-chan struct{},
	maxAttempts int,
	perAttempt time.Duration,
	backoff []time.Duration,
	perAttemptLog func(attempt int, suffix string, err error, took time.Duration),
) (string, error) {
	call := func(attemptCtx context.Context) (string, error) {
		return namer(attemptCtx, instruction)
	}
	return callHaikuWithRetry(ctx, call, done, maxAttempts, perAttempt, backoff, true, perAttemptLog)
}
