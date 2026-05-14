package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/devenjarvis/refrain/internal/hook"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check environment for required tools",
	RunE:  runDoctor,
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}

func runDoctor(cmd *cobra.Command, args []string) error {
	allOk := true

	// Check git
	if gitVersion, err := getGitVersion(); err != nil {
		fmt.Println("  [FAIL] git: not found")
		allOk = false
	} else {
		major, minor := parseGitVersion(gitVersion)
		if major > 2 || (major == 2 && minor >= 20) {
			fmt.Printf("  [OK]   git: %s\n", gitVersion)
		} else {
			fmt.Printf("  [FAIL] git: %s (need >= 2.20)\n", gitVersion)
			allOk = false
		}
	}

	// Check claude
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		fmt.Println("  [FAIL] claude: not found")
		allOk = false
	} else {
		fmt.Printf("  [OK]   claude: %s\n", claudePath)

		// Verify claude supports --settings, which refrain uses to inject the
		// hooks that drive status detection and chimes.
		if supportsSettingsFlag(claudePath) {
			fmt.Println("  [OK]   claude --settings: supported")
		} else {
			fmt.Println("  [FAIL] claude --settings: not supported (required for hook integration)")
			allOk = false
		}
	}

	// Refrain's own binary path — hooks commands reference it.
	refrainExe, err := resolveRefrainBinary()
	if err != nil {
		fmt.Printf("  [FAIL] refrain binary: unresolved (%v)\n", err)
		allOk = false
	} else {
		fmt.Printf("  [OK]   refrain binary: %s\n", refrainExe)
	}

	// Hook pipeline round-trip: spin up a temporary socket, invoke
	// `refrain hook session-start` against it, and confirm the event arrives.
	if refrainExe != "" {
		if err := checkHookPipeline(refrainExe); err != nil {
			fmt.Printf("  [FAIL] hook pipeline: %v\n", err)
			allOk = false
		} else {
			fmt.Println("  [OK]   hook pipeline: socket round-trip ok")
		}
	}

	// Check git repo
	if isGitRepo() {
		fmt.Println("  [OK]   git repo: yes")
	} else {
		fmt.Println("  [FAIL] git repo: not a git repository")
		allOk = false
	}

	// Check github auth (advisory only)
	if err := exec.Command("gh", "auth", "status").Run(); err == nil {
		fmt.Println("  [OK]   github: gh CLI authenticated")
	} else if os.Getenv("GITHUB_TOKEN") != "" {
		fmt.Println("  [OK]   github: GITHUB_TOKEN set")
	} else {
		fmt.Println("  [WARN] github: not configured (install gh CLI or set GITHUB_TOKEN)")
	}

	if !allOk {
		fmt.Println("\nSome checks failed.")
		os.Exit(1)
	}

	fmt.Println("\nAll checks passed!")
	return nil
}

func getGitVersion() (string, error) {
	out, err := exec.Command("git", "--version").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func parseGitVersion(version string) (int, int) {
	// "git version 2.49.0" -> major=2, minor=49
	parts := strings.Fields(version)
	if len(parts) < 3 {
		return 0, 0
	}
	nums := strings.Split(parts[2], ".")
	if len(nums) < 2 {
		return 0, 0
	}
	major, _ := strconv.Atoi(nums[0])
	minor, _ := strconv.Atoi(nums[1])
	return major, minor
}

func isGitRepo() bool {
	err := exec.Command("git", "rev-parse", "--is-inside-work-tree").Run()
	return err == nil
}

// supportsSettingsFlag returns true if `claude --help` advertises the
// --settings flag. We only spawn the real binary with --help so this is safe
// to run from doctor in any environment.
func supportsSettingsFlag(claudePath string) bool {
	out, err := exec.Command(claudePath, "--help").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "--settings")
}

// resolveRefrainBinary returns the refrain binary path, preferring the resolved
// symlink target. Mirrors the logic in internal/agent so doctor invokes the
// same binary the hooks file would invoke.
func resolveRefrainBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// checkHookPipeline exercises the socket round-trip: it spins up a hook
// server on a short temp path, runs `refrain hook session-start` with the
// socket env wired up, and confirms the event arrives within 2 seconds.
// Returns a descriptive error on failure so users can act on it.
func checkHookPipeline(refrainExe string) error {
	// macOS caps unix socket paths at 104 bytes. Use os.TempDir with a short
	// name and surface a friendly error if it still won't fit.
	sockDir, err := os.MkdirTemp("", "bd")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(sockDir) }()

	socket := filepath.Join(sockDir, "h.sock")
	if len(socket) > 103 {
		return fmt.Errorf("socket path too long for this platform (%d bytes)", len(socket))
	}

	srv, err := hook.NewServer(socket)
	if err != nil {
		return fmt.Errorf("socket bind: %w", err)
	}
	defer func() { _ = srv.Close() }()

	cmd := exec.Command(refrainExe, "hook", "session-start")
	// Scrub any REFRAIN_* env inherited from a parent refrain session — the
	// check must exercise *this* temp socket, not a lingering one. Filter
	// by the full `REFRAIN_` prefix so future vars (not just the two used
	// today) stay isolated from the doctor subprocess.
	baseEnv := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "REFRAIN_") {
			continue
		}
		baseEnv = append(baseEnv, kv)
	}
	cmd.Env = append(
		baseEnv,
		"REFRAIN_HOOK_SOCKET="+socket,
		"REFRAIN_AGENT_ID=doctor",
	)
	cmd.Stdin = strings.NewReader(`{"session_id":"doctor","cwd":"/"}`)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("hook cli: %w: %s", err, strings.TrimSpace(string(out)))
	}

	select {
	case e := <-srv.Events():
		if e.Kind != hook.KindSessionStart || e.AgentID != "doctor" {
			return fmt.Errorf("unexpected event: kind=%q agent=%q", e.Kind, e.AgentID)
		}
		return nil
	case <-time.After(2 * time.Second):
		return fmt.Errorf("event not received within 2s")
	}
}
