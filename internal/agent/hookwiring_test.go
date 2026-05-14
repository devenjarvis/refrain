package agent

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestAgentProgramDefault(t *testing.T) {
	if got := agentProgram(Config{}); got != "claude" {
		t.Errorf("agentProgram(empty) = %q, want %q", got, "claude")
	}
}

func TestAgentProgramOverride(t *testing.T) {
	if got := agentProgram(Config{AgentProgram: "/usr/local/bin/claude-shim"}); got != "/usr/local/bin/claude-shim" {
		t.Errorf("agentProgram override = %q", got)
	}
}

func TestSupportsHooks(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"default claude", Config{}, true},
		{"explicit claude", Config{AgentProgram: "claude"}, true},
		{"claude with absolute path", Config{AgentProgram: "/usr/local/bin/claude"}, true},
		{"bash shim", Config{AgentProgram: "bash"}, false},
		{"custom tool", Config{AgentProgram: "my-agent-tool"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := supportsHooks(tt.cfg); got != tt.want {
				t.Errorf("supportsHooks(%+v) = %v, want %v", tt.cfg, got, tt.want)
			}
		})
	}
}

func TestApplyHookEnv_NoOpWhenSocketEmpty(t *testing.T) {
	cmd := exec.Command("echo")
	if err := applyHookEnv(cmd, Config{}, "agent-1", ""); err != nil {
		t.Fatalf("expected no error with empty socket, got: %v", err)
	}
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "REFRAIN_HOOK_SOCKET=") || strings.HasPrefix(e, "REFRAIN_AGENT_ID=") {
			t.Errorf("expected no REFRAIN_* env when socket empty, got %q", e)
		}
	}
}

func TestApplyHookEnv_NoOpWhenProgramNotClaude(t *testing.T) {
	cmd := exec.Command("echo")
	cfg := Config{AgentProgram: "bash"}
	if err := applyHookEnv(cmd, cfg, "agent-1", "/tmp/hook.sock"); err != nil {
		t.Fatalf("expected no error for non-claude program, got: %v", err)
	}
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "REFRAIN_HOOK_SOCKET=") {
			t.Errorf("expected no REFRAIN_HOOK_SOCKET for non-claude program, got %q", e)
		}
	}
}

func TestApplyHookEnv_SetsEnvWhenWired(t *testing.T) {
	cmd := exec.Command("echo")
	cfg := Config{AgentProgram: "claude"}
	if err := applyHookEnv(cmd, cfg, "agent-42", "/tmp/refrain-hook.sock"); err != nil {
		t.Fatalf("applyHookEnv: %v", err)
	}

	if !slices.Contains(cmd.Env, "REFRAIN_HOOK_SOCKET=/tmp/refrain-hook.sock") {
		t.Errorf("expected REFRAIN_HOOK_SOCKET in env, got %v", cmd.Env)
	}
	if !slices.Contains(cmd.Env, "REFRAIN_AGENT_ID=agent-42") {
		t.Errorf("expected REFRAIN_AGENT_ID=agent-42 in env, got %v", cmd.Env)
	}
}

func TestApplyHookEnv_ErrorsWhenAgentIDMissing(t *testing.T) {
	cmd := exec.Command("echo")
	cfg := Config{AgentProgram: "claude"}
	err := applyHookEnv(cmd, cfg, "", "/tmp/hook.sock")
	if err == nil {
		t.Fatal("expected error when agentID empty for claude agent")
	}
	if !strings.Contains(err.Error(), "agentID") {
		t.Errorf("expected error to mention agentID, got: %v", err)
	}
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "REFRAIN_HOOK_SOCKET=") {
			t.Errorf("env should not contain REFRAIN_HOOK_SOCKET on error, got %q", e)
		}
	}
}

func TestBuildHookArgs_DisabledForBashShim(t *testing.T) {
	dir := t.TempDir()
	args, err := buildHookArgs(Config{AgentProgram: "bash"}, dir, "/tmp/socket")
	if err != nil {
		t.Fatalf("buildHookArgs: %v", err)
	}
	if len(args) != 0 {
		t.Errorf("expected no args for non-claude, got %v", args)
	}
}

func TestBuildHookArgs_DisabledWhenSocketEmpty(t *testing.T) {
	dir := t.TempDir()
	args, err := buildHookArgs(Config{AgentProgram: "claude"}, dir, "")
	if err != nil {
		t.Fatalf("buildHookArgs: %v", err)
	}
	if len(args) != 0 {
		t.Errorf("expected no args when socket empty, got %v", args)
	}
}

func TestBuildHookArgs_WritesSettingsAndReturnsFlags(t *testing.T) {
	dir := t.TempDir()
	args, err := buildHookArgs(Config{AgentProgram: "claude"}, dir, "/tmp/socket")
	if err != nil {
		t.Fatalf("buildHookArgs: %v", err)
	}
	if len(args) != 2 || args[0] != "--settings" {
		t.Fatalf("expected [--settings <path>], got %v", args)
	}
	wantPath := filepath.Join(dir, ".refrain", "hooks.json")
	if args[1] != wantPath {
		t.Errorf("settings path = %q, want %q", args[1], wantPath)
	}
}
