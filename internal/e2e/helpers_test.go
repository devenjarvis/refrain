//go:build e2e

package e2e

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var (
	refrainBin string
	scrimBin   string
)

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "refrain-e2e-bin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	bin := filepath.Join(tmp, "refrain")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	// Build from the repo root (two levels up from internal/e2e).
	cmd.Dir = filepath.Join(repoRoot())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: failed to build refrain: %v\n", err)
		os.Exit(1)
	}
	refrainBin = bin

	// Build scrim and name it "claude" so supportsHooks() recognises it.
	scrimCmd := exec.Command("go", "install", "github.com/devenjarvis/scrim/cmd/scrim@latest")
	scrimCmd.Dir = repoRoot()
	scrimCmd.Env = append(os.Environ(), "GOBIN="+tmp)
	scrimCmd.Stdout = os.Stdout
	scrimCmd.Stderr = os.Stderr
	if err := scrimCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: failed to install scrim: %v\n", err)
		os.Exit(1)
	}
	claudePath := filepath.Join(tmp, "claude")
	if err := os.Rename(filepath.Join(tmp, "scrim"), claudePath); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: failed to rename scrim to claude: %v\n", err)
		os.Exit(1)
	}
	scrimBin = claudePath

	os.Exit(m.Run())
}

// repoRoot returns the project root by walking up from this file's directory.
func repoRoot() string {
	// This file is at internal/e2e/helpers_test.go, so root is ../..
	// Use runtime-independent approach: find go.mod.
	dir, err := os.Getwd()
	if err != nil {
		panic("e2e: cannot get working directory: " + err.Error())
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("e2e: cannot find repo root (no go.mod found)")
		}
		dir = parent
	}
}

// Session wraps a tu CLI session for e2e testing.
type Session struct {
	t        *testing.T
	name     string
	tempDir  string   // parent temp dir
	home     string   // fake HOME
	repoDir  string   // temp git repo (also CWD for refrain)
	extraEnv []string // additional "K=V" env entries to pass through tu to refrain
}

// newSession creates an isolated test environment and launches refrain via tu.
// It sets up a fake HOME with refrain config, a temp git repo, and starts refrain.
// Cleanup is automatic via t.Cleanup.
func newSession(t *testing.T) *Session {
	t.Helper()

	suffix := randomSuffix()
	name := sanitizeName(t.Name()) + "-" + suffix

	tempDir := t.TempDir()
	home := filepath.Join(tempDir, "home")
	// Use a distinctive directory name so screen assertions can match it
	// without colliding with common UI strings like "repository".
	repoDir := filepath.Join(tempDir, "e2erepo-"+suffix)

	// Create directory structure.
	for _, d := range []string{
		home,
		filepath.Join(home, ".refrain"),
		repoDir,
		filepath.Join(repoDir, ".refrain"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("e2e: mkdir %s: %v", d, err)
		}
	}

	// Write a minimal gitconfig in fake HOME (we redirect GIT_CONFIG_GLOBAL
	// here in Start() to avoid picking up the developer's real ~/.gitconfig).
	writeFile(
		t, filepath.Join(home, ".gitconfig"),
		"[user]\n\tname = e2e-test\n\temail = e2e@test.local\n[init]\n\tdefaultBranch = main\n",
	)

	// Write global config: set agent_program to bash. plan_first_enabled is
	// pinned to false here so the existing e2e tests (which press `n` and
	// expect an immediate bash prompt in focusLaunch) keep working under
	// the new default. The plan-first flow itself is exercised by unit
	// tests in internal/agent and internal/tui; e2e tests target the
	// session-creation path, not plan generation.
	globalCfg := map[string]any{
		"agent_program":      "bash",
		"bypass_permissions": false,
		"plan_first_enabled": false,
	}
	writeJSON(t, filepath.Join(home, ".refrain", "config.json"), globalCfg)

	// Write repo config: override agent_program and bypass_permissions.
	repoCfg := map[string]any{
		"agent_program":      "bash",
		"bypass_permissions": false,
		"plan_first_enabled": false,
	}
	writeJSON(t, filepath.Join(repoDir, ".refrain", "config.json"), repoCfg)

	// Initialize a git repo so refrain can auto-register it. Use the same
	// sandboxed git env as Start() so the developer's real ~/.gitconfig
	// (signing keys, hooks, templates) doesn't break test setup.
	runGit(t, repoDir, home, "init")
	runGit(t, repoDir, home, "config", "user.name", "Test")
	runGit(t, repoDir, home, "config", "user.email", "test@test.com")
	// Create an initial commit so the repo has a branch.
	writeFile(t, filepath.Join(repoDir, "README.md"), "# test repo\n")
	runGit(t, repoDir, home, "add", ".")
	runGit(t, repoDir, home, "commit", "-m", "initial commit")

	s := &Session{
		t:       t,
		name:    name,
		tempDir: tempDir,
		home:    home,
		repoDir: repoDir,
	}

	t.Cleanup(func() { s.Kill() })
	return s
}

// scenarioFile is a YAML scenario file written to the scrim scenarios directory.
type scenarioFile struct {
	name    string // filename, e.g. "default.yaml"
	content string // YAML content
}

// defaultScenario is a catch-all scenario that matches any typed input.
// Scrim stays alive in interactive mode; this fires SessionStart immediately
// and responds with "Ready." when the user types anything.
var defaultScenario = scenarioFile{
	name: "default.yaml",
	content: `name: default
match:
  prompt: ""
session:
  id: "e2e-default"
  model: "claude-sonnet-4-6"
turns:
  - assistant:
      - type: text
        text: "Ready."
`,
}

// newScrimSession creates an isolated test environment configured to use scrim
// (named "claude") as the agent program instead of bash. Scrim fires hook events
// through the same settings-file mechanism as real Claude Code, giving tests
// deterministic lifecycle events without a bespoke bash stub.
//
// scenarios are written to a temp directory and SCRIM_SCENARIOS_DIR is passed
// through the tu → refrain → scrim env chain. If no scenarios are provided,
// defaultScenario is used.
func newScrimSession(t *testing.T, scenarios ...scenarioFile) *Session {
	t.Helper()

	suffix := randomSuffix()
	name := sanitizeName(t.Name()) + "-" + suffix

	tempDir := t.TempDir()
	home := filepath.Join(tempDir, "home")
	repoDir := filepath.Join(tempDir, "e2erepo-"+suffix)

	for _, d := range []string{
		home,
		filepath.Join(home, ".refrain"),
		repoDir,
		filepath.Join(repoDir, ".refrain"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("e2e: mkdir %s: %v", d, err)
		}
	}

	writeFile(
		t, filepath.Join(home, ".gitconfig"),
		"[user]\n\tname = e2e-test\n\temail = e2e@test.local\n[init]\n\tdefaultBranch = main\n",
	)

	cfg := map[string]any{
		"agent_program":      scrimBin,
		"bypass_permissions": false,
		"plan_first_enabled": false,
	}
	writeJSON(t, filepath.Join(home, ".refrain", "config.json"), cfg)
	writeJSON(t, filepath.Join(repoDir, ".refrain", "config.json"), cfg)

	runGit(t, repoDir, home, "init")
	runGit(t, repoDir, home, "config", "user.name", "Test")
	runGit(t, repoDir, home, "config", "user.email", "test@test.com")
	writeFile(t, filepath.Join(repoDir, "README.md"), "# test repo\n")
	runGit(t, repoDir, home, "add", ".")
	runGit(t, repoDir, home, "commit", "-m", "initial commit")

	// Write scrim scenario files.
	scenariosDir := filepath.Join(tempDir, "scenarios")
	if err := os.MkdirAll(scenariosDir, 0o755); err != nil {
		t.Fatalf("e2e: mkdir scenarios: %v", err)
	}
	if len(scenarios) == 0 {
		scenarios = []scenarioFile{defaultScenario}
	}
	for _, sc := range scenarios {
		writeFile(t, filepath.Join(scenariosDir, sc.name), sc.content)
	}

	s := &Session{
		t:       t,
		name:    name,
		tempDir: tempDir,
		home:    home,
		repoDir: repoDir,
		extraEnv: []string{
			"SCRIM_SCENARIOS_DIR=" + scenariosDir,
		},
	}

	t.Cleanup(func() { s.Kill() })
	return s
}

// Start launches refrain inside a tu virtual terminal session.
func (s *Session) Start() {
	s.t.Helper()
	// Hardening against env leakage from the host: pin XDG paths inside the
	// fake HOME, and redirect git's global config to the fake HOME so the
	// developer's real ~/.gitconfig (signing keys, hooks, templates) doesn't
	// leak in. We deliberately do NOT set GIT_DIR/GIT_WORK_TREE — those would
	// override git's repo discovery and break it.
	args := []string{
		"run",
		"--name", s.name,
		"--size", "120x40",
		"--env", "HOME=" + s.home,
		"--env", "XDG_CONFIG_HOME=" + filepath.Join(s.home, ".config"),
		"--env", "XDG_DATA_HOME=" + filepath.Join(s.home, ".local", "share"),
		"--env", "XDG_CACHE_HOME=" + filepath.Join(s.home, ".cache"),
		"--env", "GIT_CONFIG_GLOBAL=" + filepath.Join(s.home, ".gitconfig"),
		"--env", "GIT_CONFIG_SYSTEM=/dev/null",
		// Scrub parent refrain hook wiring so running these tests from inside
		// another refrain session doesn't leak a live socket into the child's
		// env (the stub would forward hook events to the wrong socket).
		"--env", "REFRAIN_HOOK_SOCKET=",
		"--env", "REFRAIN_AGENT_ID=",
	}
	for _, kv := range s.extraEnv {
		args = append(args, "--env", kv)
	}
	args = append(args, "--cwd", s.repoDir, "--", refrainBin)
	cmd := exec.Command("tu", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.t.Fatalf("e2e: tu run failed: %v\n%s", err, out)
	}
}

// WaitForText waits until the screen content matches the given regex.
func (s *Session) WaitForText(pattern string, timeoutMs int) {
	s.t.Helper()
	cmd := exec.Command(
		"tu", "wait",
		"--name", s.name,
		"--text", pattern,
		"--timeout", fmt.Sprintf("%d", timeoutMs),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		screen := s.Screenshot()
		s.t.Fatalf("e2e: WaitForText(%q) timed out after %dms: %v\n%s\nScreen:\n%s",
			pattern, timeoutMs, err, out, screen)
	}
}

// WaitStable waits until the screen is unchanged for the given duration.
// Best-effort: a timeout is silent, since with a bash agent emitting cursor
// activity the screen rarely fully stabilizes — but the wait still serves as
// a "give the next render a chance" pause. Tests should rely on WaitForText
// or content assertions for actual synchronization.
func (s *Session) WaitStable(ms int) {
	s.t.Helper()
	cmd := exec.Command(
		"tu", "wait",
		"--name", s.name,
		"--stable", fmt.Sprintf("%d", ms),
		"--timeout", fmt.Sprintf("%d", ms+5000),
	)
	_, _ = cmd.CombinedOutput()
}

// WaitForExit polls Status until the process is no longer alive or timeoutMs
// elapses. Returns the final (alive, exitCode). Use this instead of
// WaitStable+Status when confirming that refrain has exited.
func (s *Session) WaitForExit(timeoutMs int) (alive bool, exitCode int) {
	s.t.Helper()
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for time.Now().Before(deadline) {
		alive, exitCode = s.Status()
		if !alive {
			return alive, exitCode
		}
		time.Sleep(100 * time.Millisecond)
	}
	return s.Status()
}

// Press sends one or more keystrokes to the terminal.
func (s *Session) Press(keys ...string) {
	s.t.Helper()
	args := append([]string{"press", "--name", s.name}, keys...)
	cmd := exec.Command("tu", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.t.Fatalf("e2e: Press(%v) failed: %v\n%s", keys, err, out)
	}
}

// Type sends literal text to the terminal.
func (s *Session) Type(text string) {
	s.t.Helper()
	cmd := exec.Command("tu", "type", "--name", s.name, text)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.t.Fatalf("e2e: Type(%q) failed: %v\n%s", text, err, out)
	}
}

// Screenshot captures the current terminal screen as plain text.
// When stdout is piped (as it is here), tu auto-detects non-TTY and emits
// JSON regardless of the --json flag, so we always parse the JSON envelope
// and return just the `content` field.
func (s *Session) Screenshot() string {
	s.t.Helper()
	cmd := exec.Command("tu", "screenshot", "--json", "--name", s.name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.t.Fatalf("e2e: Screenshot failed: %v\n%s", err, out)
	}
	var env struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		s.t.Fatalf("e2e: parsing screenshot JSON: %v\n%s", err, out)
	}
	return env.Content
}

// Kill terminates the tu session. Safe to call multiple times. Logs (but does
// not fail the test on) errors that aren't "session already gone" — silent
// failures here would leak orphan tu sessions and only surface via `tu list`
// on a future run.
func (s *Session) Kill() {
	cmd := exec.Command("tu", "kill", "--name", s.name)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	// Treat any "not found"/"no such session" output as expected.
	lower := strings.ToLower(string(out))
	if strings.Contains(lower, "not found") || strings.Contains(lower, "no such") {
		return
	}
	s.t.Logf("e2e: tu kill --name %s returned %v: %s", s.name, err, out)
}

// tuStatus holds parsed output from tu status --json.
type tuStatus struct {
	Alive    bool `json:"alive"`
	ExitCode *int `json:"exit_code"`
}

// Status returns the session status (alive, exit code). A "session not found"
// error from tu is treated as "dead" (alive=false, exitCode=-1); other errors
// fail the test so debugging isn't hidden behind a generic dead-session result.
func (s *Session) Status() (alive bool, exitCode int) {
	s.t.Helper()
	cmd := exec.Command("tu", "status", "--json", "--name", s.name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		lower := strings.ToLower(string(out))
		if strings.Contains(lower, "not found") || strings.Contains(lower, "no such") {
			return false, -1
		}
		s.t.Fatalf("e2e: tu status failed: %v\n%s", err, out)
	}
	var st tuStatus
	if err := json.Unmarshal(out, &st); err != nil {
		s.t.Fatalf("e2e: parsing status JSON: %v\n%s", err, out)
	}
	code := -1
	if st.ExitCode != nil {
		code = *st.ExitCode
	}
	return st.Alive, code
}

// AssertScreenContains asserts that the current screen contains the given substring.
func (s *Session) AssertScreenContains(substr string) {
	s.t.Helper()
	screen := s.Screenshot()
	if !strings.Contains(screen, substr) {
		s.t.Errorf("e2e: screen does not contain %q\nScreen:\n%s", substr, screen)
	}
}

// AssertScreenNotContains asserts that the current screen does NOT contain the given substring.
func (s *Session) AssertScreenNotContains(substr string) {
	s.t.Helper()
	screen := s.Screenshot()
	if strings.Contains(screen, substr) {
		s.t.Errorf("e2e: screen unexpectedly contains %q\nScreen:\n%s", substr, screen)
	}
}

// AssertScreenMatches asserts that the current screen matches the given regex.
func (s *Session) AssertScreenMatches(pattern string) {
	s.t.Helper()
	screen := s.Screenshot()
	re, err := regexp.Compile(pattern)
	if err != nil {
		s.t.Fatalf("e2e: invalid regex %q: %v", pattern, err)
	}
	if !re.MatchString(screen) {
		s.t.Errorf("e2e: screen does not match /%s/\nScreen:\n%s", pattern, screen)
	}
}

// --- helpers ---

func randomSuffix() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic("e2e: rand.Read failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func sanitizeName(name string) string {
	// Replace any non-alphanumeric character with a dash, collapse runs.
	var b strings.Builder
	prev := byte('-')
	for i := range len(name) {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteByte(c)
			prev = c
		} else if prev != '-' {
			b.WriteByte('-')
			prev = '-'
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("e2e: marshalling JSON: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("e2e: writing %s: %v", path, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("e2e: writing %s: %v", path, err)
	}
}

// runGit runs `git <args>` in dir with a sandboxed global config (pointed at
// home/.gitconfig) and a /dev/null system config. This isolates the test from
// the developer's real ~/.gitconfig (gpg signing, hooks, templates, etc.).
func runGit(t *testing.T, dir, home string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(
		os.Environ(),
		"HOME="+home,
		"GIT_CONFIG_GLOBAL="+filepath.Join(home, ".gitconfig"),
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("e2e: git %v failed: %v\n%s", args, err, out)
	}
}
