package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// writeFakeClaude writes a shell script named "claude" into dir and marks it
// executable. It echoes the given stdout content and exits with exitCode.
// On non-unix platforms the test is skipped by the caller.
func writeFakeClaude(t *testing.T, dir, stdout string, exitCode int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake claude uses /bin/sh; skip on windows")
	}
	script := "#!/bin/sh\n" +
		"cat >/dev/null\n" + // drain stdin so the caller doesn't get SIGPIPE
		"printf %s " + shellSingleQuote(stdout) + "\n"
	if exitCode != 0 {
		script += "exit " + itoa(exitCode) + "\n"
	}
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
}

// writeSlowClaude writes a fake claude that sleeps for sleepSecs before
// responding — used to verify context cancellation.
func writeSlowClaude(t *testing.T, dir string, sleepSecs int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake claude uses /bin/sh; skip on windows")
	}
	script := "#!/bin/sh\ncat >/dev/null\nsleep " + itoa(sleepSecs) + "\necho done\n"
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write slow claude: %v", err)
	}
}

// writeStdinCapturingClaude writes a fake claude that copies stdin to
// stdinFile and then prints stdout. Used to assert the namer pipes the
// rendered instruction verbatim.
func writeStdinCapturingClaude(t *testing.T, dir, stdinFile, stdout string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake claude uses /bin/sh; skip on windows")
	}
	script := "#!/bin/sh\n" +
		"cat > " + shellSingleQuote(stdinFile) + "\n" +
		"printf %s " + shellSingleQuote(stdout) + "\n"
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write capturing claude: %v", err)
	}
}

// withPATH prepends dir to PATH for the duration of the test.
func withPATH(t *testing.T, dir string) {
	t.Helper()
	orig := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+orig)
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func TestDefaultBranchNamer_Success(t *testing.T) {
	dir := t.TempDir()
	writeFakeClaude(t, dir, "fix login flow", 0)
	withPATH(t, dir)

	namer := DefaultBranchNamer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug, err := namer(ctx, "Summarize this task: we need to fix the login flow")
	if err != nil {
		t.Fatalf("namer returned error: %v", err)
	}
	if slug != "fix-login-flow" {
		t.Errorf("slug = %q, want fix-login-flow", slug)
	}
}

func TestDefaultBranchNamer_PipesInstructionVerbatim(t *testing.T) {
	dir := t.TempDir()
	stdinFile := filepath.Join(dir, "stdin.txt")
	writeStdinCapturingClaude(t, dir, stdinFile, "the-result")
	withPATH(t, dir)

	namer := DefaultBranchNamer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const instruction = "Custom prompt header.\n\nuser-prompt-text"
	if _, err := namer(ctx, instruction); err != nil {
		t.Fatalf("namer returned error: %v", err)
	}

	got, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatalf("read captured stdin: %v", err)
	}
	if string(got) != instruction {
		t.Errorf("stdin = %q, want %q", string(got), instruction)
	}
}

func TestDefaultBranchNamer_NonZeroExit(t *testing.T) {
	dir := t.TempDir()
	writeFakeClaude(t, dir, "whatever", 1)
	withPATH(t, dir)

	namer := DefaultBranchNamer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := namer(ctx, "hello")
	if err == nil {
		t.Fatal("expected error from nonzero-exit claude")
	}
}

func TestDefaultBranchNamer_EmptyStdout(t *testing.T) {
	dir := t.TempDir()
	writeFakeClaude(t, dir, "", 0)
	withPATH(t, dir)

	namer := DefaultBranchNamer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := namer(ctx, "hello")
	if err == nil {
		t.Fatal("expected error when stdout slugs to empty")
	}
}

func TestDefaultBranchNamer_StdoutTooLongTruncates(t *testing.T) {
	// 200-char reply with spaces — slugify should truncate to 40 chars.
	long := strings.Repeat("word ", 40) // 200 chars
	dir := t.TempDir()
	writeFakeClaude(t, dir, long, 0)
	withPATH(t, dir)

	namer := DefaultBranchNamer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug, err := namer(ctx, "hello")
	if err != nil {
		t.Fatalf("namer returned error: %v", err)
	}
	if len(slug) > 40 {
		t.Errorf("slug length = %d, want <= 40 (slug=%q)", len(slug), slug)
	}
}

func TestDefaultBranchNamer_ContextTimeout(t *testing.T) {
	dir := t.TempDir()
	writeSlowClaude(t, dir, 10)
	withPATH(t, dir)

	namer := DefaultBranchNamer()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := namer(ctx, "hello")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("namer waited %v, expected to be killed near the 200ms timeout", elapsed)
	}
}

func TestDefaultBranchNamer_ClaudeMissing(t *testing.T) {
	// Point PATH at an empty directory so claude cannot be found.
	empty := t.TempDir()
	t.Setenv("PATH", empty)

	namer := DefaultBranchNamer()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := namer(ctx, "hello")
	if err == nil {
		t.Fatal("expected error when claude is absent from PATH")
	}
	if !errors.Is(err, ErrClaudeNotFound) {
		t.Errorf("err = %v, want errors.Is(err, ErrClaudeNotFound) == true", err)
	}
}

// writeEnvDumpingClaude writes a fake claude that dumps its env into envFile
// and prints stdout. Used to verify env-stripping in the Haiku subprocess.
func writeEnvDumpingClaude(t *testing.T, dir, envFile, stdout string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake claude uses /bin/sh; skip on windows")
	}
	script := "#!/bin/sh\n" +
		"cat >/dev/null\n" +
		"env > " + shellSingleQuote(envFile) + "\n" +
		"printf %s " + shellSingleQuote(stdout) + "\n"
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write env-dumping claude: %v", err)
	}
}

func TestDefaultBranchNamer_StripsBatonHookEnv(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.txt")
	writeEnvDumpingClaude(t, dir, envFile, "result-slug")
	withPATH(t, dir)

	t.Setenv("BATON_HOOK_SOCKET", "/should/not/leak.sock")
	t.Setenv("BATON_AGENT_ID", "should-not-leak")
	// Sentinel non-baton var should still be inherited.
	t.Setenv("BATON_KEEPME_TESTONLY", "yes")

	namer := DefaultBranchNamer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := namer(ctx, "hello"); err != nil {
		t.Fatalf("namer returned error: %v", err)
	}

	envContents, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	body := string(envContents)
	for _, banned := range []string{"BATON_HOOK_SOCKET=", "BATON_AGENT_ID="} {
		if strings.Contains(body, banned) {
			t.Errorf("subprocess env contained %q; should have been stripped\n%s", banned, body)
		}
	}
	if !strings.Contains(body, "BATON_KEEPME_TESTONLY=yes") {
		t.Errorf("subprocess env missing non-baton sentinel var; env stripping was too aggressive\n%s", body)
	}
}

func TestSanitizedHaikuEnv(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"BATON_HOOK_SOCKET=/sock",
		"HOME=/home/u",
		"BATON_AGENT_ID=abc",
		"BATON_FOO=keepme",     // not in strip list
		"BATON_HOOK_SOCKET_X=", // prefix-only similar key — must NOT be stripped
	}
	out := sanitizedHaikuEnv(in)
	got := strings.Join(out, "\n")
	for _, banned := range []string{"BATON_HOOK_SOCKET=/sock", "BATON_AGENT_ID=abc"} {
		if strings.Contains(got, banned) {
			t.Errorf("sanitized env contained %q\n%s", banned, got)
		}
	}
	for _, kept := range []string{"PATH=/usr/bin", "HOME=/home/u", "BATON_FOO=keepme", "BATON_HOOK_SOCKET_X="} {
		if !strings.Contains(got, kept) {
			t.Errorf("sanitized env missing %q\n%s", kept, got)
		}
	}
}

// TestCallNamerWithRetry_SucceedsAfterTransientErrors verifies that a transient
// error on attempts 1 and 2 is recovered by attempt 3.
func TestCallNamerWithRetry_SucceedsAfterTransientErrors(t *testing.T) {
	var calls atomic.Int32
	stub := func(ctx context.Context, instruction string) (string, error) {
		n := calls.Add(1)
		if n < 3 {
			return "", fmt.Errorf("transient failure %d", n)
		}
		return "good-slug", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})

	suffix, err := callNamerWithRetry(
		ctx, stub, "x", done,
		3, 1*time.Second, []time.Duration{10 * time.Millisecond, 10 * time.Millisecond},
		nil,
	)
	if err != nil {
		t.Fatalf("expected success after retry, got err=%v", err)
	}
	if suffix != "good-slug" {
		t.Errorf("suffix = %q, want good-slug", suffix)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("namer called %d times, want 3", got)
	}
}

// TestCallNamerWithRetry_StopsOnTerminalErrors verifies that terminal sentinel
// errors short-circuit the retry loop.
func TestCallNamerWithRetry_StopsOnTerminalErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"ClaudeNotFound", fmt.Errorf("wrap: %w", ErrClaudeNotFound)},
		{"EmptySlug", ErrEmptySlug},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			stub := func(ctx context.Context, instruction string) (string, error) {
				calls.Add(1)
				return "", tc.err
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan struct{})

			_, err := callNamerWithRetry(
				ctx, stub, "x", done,
				3, 100*time.Millisecond, []time.Duration{10 * time.Millisecond, 10 * time.Millisecond},
				nil,
			)
			if !errors.Is(err, tc.err) && !errors.Is(err, ErrClaudeNotFound) && !errors.Is(err, ErrEmptySlug) {
				t.Errorf("err = %v, want terminal", err)
			}
			if got := calls.Load(); got != 1 {
				t.Errorf("namer called %d times for terminal error, want 1", got)
			}
		})
	}
}

// TestCallNamerWithRetry_DoneAbortsBackoff verifies that closing the done
// channel during the inter-attempt sleep aborts retries promptly.
func TestCallNamerWithRetry_DoneAbortsBackoff(t *testing.T) {
	var calls atomic.Int32
	stub := func(ctx context.Context, instruction string) (string, error) {
		calls.Add(1)
		return "", fmt.Errorf("transient")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})

	go func() {
		time.Sleep(50 * time.Millisecond)
		close(done)
	}()

	start := time.Now()
	_, err := callNamerWithRetry(
		ctx, stub, "x", done,
		3, 1*time.Second, []time.Duration{2 * time.Second, 2 * time.Second},
		nil,
	)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if elapsed > 1*time.Second {
		t.Errorf("retry took %v after done close; expected ~50ms-ish", elapsed)
	}
	if got := calls.Load(); got > 2 {
		t.Errorf("namer called %d times after done close, want <= 2", got)
	}
}

// TestCallNamerWithRetry_LogsEachAttempt verifies the per-attempt logging
// callback fires exactly once per attempt with the right metadata.
func TestCallNamerWithRetry_LogsEachAttempt(t *testing.T) {
	var calls atomic.Int32
	stub := func(ctx context.Context, instruction string) (string, error) {
		n := calls.Add(1)
		if n < 2 {
			return "", fmt.Errorf("blip")
		}
		return "ok-slug", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})

	type entry struct {
		attempt int
		suffix  string
		err     error
	}
	var seen []entry
	logFn := func(attempt int, suffix string, err error, took time.Duration) {
		seen = append(seen, entry{attempt, suffix, err})
	}

	suffix, err := callNamerWithRetry(
		ctx, stub, "x", done,
		3, 100*time.Millisecond, []time.Duration{1 * time.Millisecond, 1 * time.Millisecond},
		logFn,
	)
	if err != nil || suffix != "ok-slug" {
		t.Fatalf("got suffix=%q err=%v, want ok-slug nil", suffix, err)
	}
	if len(seen) != 2 {
		t.Fatalf("len(seen) = %d, want 2", len(seen))
	}
	if seen[0].attempt != 1 || seen[0].err == nil {
		t.Errorf("first log entry: %+v, want attempt=1 with err", seen[0])
	}
	if seen[1].attempt != 2 || seen[1].suffix != "ok-slug" || seen[1].err != nil {
		t.Errorf("second log entry: %+v, want attempt=2 suffix=ok-slug err=nil", seen[1])
	}
}

// TestDefaultTaskSummarizer_NotNil verifies that DefaultTaskSummarizer returns
// a non-nil callable.
func TestDefaultTaskSummarizer_NotNil(t *testing.T) {
	s := DefaultTaskSummarizer()
	if s == nil {
		t.Fatal("DefaultTaskSummarizer() returned nil")
	}
}

// TestDefaultTaskSummarizer_EmptyPrompt verifies that an empty (or whitespace)
// prompt returns "" without panic and without error.
func TestDefaultTaskSummarizer_EmptyPrompt(t *testing.T) {
	s := DefaultTaskSummarizer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, prompt := range []string{"", "   ", "\t\n"} {
		got, err := s(ctx, prompt)
		if err != nil {
			t.Errorf("prompt=%q: unexpected error: %v", prompt, err)
		}
		if got != "" {
			t.Errorf("prompt=%q: got %q, want \"\"", prompt, got)
		}
	}
}

// TestDefaultTaskSummarizer_ClaudeMissing verifies that when claude is not on
// PATH the summarizer surfaces ErrClaudeNotFound so callHaikuWithRetry can
// short-circuit. The manager-side wrapper coerces the error back to "" before
// storing on the session, preserving the public-boundary "no summary" effect.
func TestDefaultTaskSummarizer_ClaudeMissing(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("PATH", empty)

	s := DefaultTaskSummarizer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := s(ctx, "implement dark mode for the settings panel")
	if !errors.Is(err, ErrClaudeNotFound) {
		t.Errorf("expected ErrClaudeNotFound when claude is absent, got: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string when claude is absent, got: %q", got)
	}
}

// TestDefaultTaskSummarizer_Success verifies that when a fake claude echoes a
// plain-English phrase the summarizer returns that phrase verbatim (no slugify).
func TestDefaultTaskSummarizer_Success(t *testing.T) {
	dir := t.TempDir()
	const response = "add dark mode to the settings panel"
	writeFakeClaude(t, dir, response, 0)
	withPATH(t, dir)

	s := DefaultTaskSummarizer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := s(ctx, "implement dark mode for the settings panel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != response {
		t.Errorf("got %q, want %q", got, response)
	}
}

// TestDefaultTaskSummarizer_NonZeroExitReturnsEmpty verifies that a non-zero
// claude exit results in ("", nil) — the summarizer swallows the error.
func TestDefaultTaskSummarizer_NonZeroExitReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	writeFakeClaude(t, dir, "some text", 1)
	withPATH(t, dir)

	s := DefaultTaskSummarizer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := s(ctx, "implement dark mode")
	if err != nil {
		t.Errorf("expected nil error on subprocess failure, got: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string on subprocess failure, got: %q", got)
	}
}

// TestBuildClaudeHaikuArgs_AlwaysOnFlags verifies the speedup flags that
// disable MCP discovery, slash commands, session persistence, built-in
// tools, and dynamic system prompt sections are present on every call,
// alongside the model + -p invariants.
func TestBuildClaudeHaikuArgs_AlwaysOnFlags(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	args := buildClaudeHaikuArgs()
	joined := strings.Join(args, " ")

	if args[0] != "-p" {
		t.Errorf("first arg = %q, want -p", args[0])
	}
	if !containsPair(args, "--model", claudeHaikuModel) {
		t.Errorf("missing --model %s; args=%v", claudeHaikuModel, args)
	}
	for _, want := range []string{
		"--strict-mcp-config",
		"--disable-slash-commands",
		"--no-session-persistence",
		"--tools ",
		"--setting-sources user",
		"--exclude-dynamic-system-prompt-sections",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q; got %q", want, joined)
		}
	}

	// --mcp-config must be a JSON object with an "mcpServers" record. A bare
	// "{}" is rejected by claude's strict schema validator (regression guard
	// for the silent branch-rename / task-summary failure).
	mcpIdx := -1
	for i, a := range args {
		if a == "--mcp-config" {
			mcpIdx = i
			break
		}
	}
	if mcpIdx < 0 {
		t.Fatalf("argv missing --mcp-config; got %v", args)
	}
	if mcpIdx+1 >= len(args) {
		t.Fatalf("--mcp-config has no value; got %v", args)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(args[mcpIdx+1]), &cfg); err != nil {
		t.Fatalf("--mcp-config value %q not valid JSON: %v", args[mcpIdx+1], err)
	}
	servers, ok := cfg["mcpServers"]
	if !ok {
		t.Errorf("--mcp-config value %q missing required \"mcpServers\" key", args[mcpIdx+1])
	}
	if _, ok := servers.(map[string]any); !ok {
		t.Errorf("--mcp-config \"mcpServers\" must be an object; got %T (%v)", servers, servers)
	}
}

// TestBuildClaudeHaikuArgs_BareGatedByAPIKey verifies --bare is added iff
// ANTHROPIC_API_KEY is set in the environment.
func TestBuildClaudeHaikuArgs_BareGatedByAPIKey(t *testing.T) {
	t.Run("with API key", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "sk-test")
		args := buildClaudeHaikuArgs()
		if !contains(args, "--bare") {
			t.Errorf("expected --bare with ANTHROPIC_API_KEY set; got %v", args)
		}
	})
	t.Run("without API key", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "")
		args := buildClaudeHaikuArgs()
		if contains(args, "--bare") {
			t.Errorf("did not expect --bare without ANTHROPIC_API_KEY; got %v", args)
		}
	})
}

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func containsPair(args []string, key, val string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == val {
			return true
		}
	}
	return false
}

// TestDefaultTaskSummarizer_OutputNotSlugified verifies that the summarizer
// returns plain text with spaces — NOT a hyphen-separated slug.
func TestDefaultTaskSummarizer_OutputNotSlugified(t *testing.T) {
	dir := t.TempDir()
	// Response with spaces — would become "implement dark mode" if not slugified,
	// or "implement-dark-mode" if slugified.
	const response = "implement dark mode for settings"
	writeFakeClaude(t, dir, response, 0)
	withPATH(t, dir)

	s := DefaultTaskSummarizer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := s(ctx, "add dark mode")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(got, "-") {
		t.Errorf("output appears slugified (contains hyphen): %q", got)
	}
	if got != response {
		t.Errorf("got %q, want %q", got, response)
	}
}
