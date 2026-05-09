package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultPlanDrafter_DraftSuccess(t *testing.T) {
	dir := t.TempDir()
	const planMD = "# Goal\nAdd dark mode\n\n## Tasks\n- [ ] thing"
	writeFakeClaude(t, dir, planMD, 0)
	withPATH(t, dir)

	d := DefaultPlanDrafter()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := d.Draft(ctx, DraftRequest{UserPrompt: "add dark mode"})
	if err != nil {
		t.Fatalf("Draft returned error: %v", err)
	}
	if got != planMD {
		t.Errorf("Draft = %q, want %q", got, planMD)
	}
}

func TestDefaultPlanDrafter_DraftEmptyPromptError(t *testing.T) {
	d := DefaultPlanDrafter()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	for _, prompt := range []string{"", "   ", "\t\n"} {
		_, err := d.Draft(ctx, DraftRequest{UserPrompt: prompt})
		if !errors.Is(err, ErrEmptyPrompt) {
			t.Errorf("prompt=%q: err = %v, want ErrEmptyPrompt", prompt, err)
		}
	}
}

func TestDefaultPlanDrafter_DraftPipesPromptVerbatim(t *testing.T) {
	dir := t.TempDir()
	stdinFile := filepath.Join(dir, "stdin.txt")
	writeStdinCapturingClaude(t, dir, stdinFile, "# Goal\nstub")
	withPATH(t, dir)

	d := DefaultPlanDrafter()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const prompt = "add dark mode toggle to settings"
	if _, err := d.Draft(ctx, DraftRequest{UserPrompt: prompt}); err != nil {
		t.Fatalf("Draft: %v", err)
	}

	got, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	body := string(got)
	if !strings.HasPrefix(body, planDraftPrompt) {
		t.Errorf("stdin missing planDraftPrompt prefix\nstdin=%q", body)
	}
	if !strings.Contains(body, prompt) {
		t.Errorf("stdin missing user prompt; got=%q", body)
	}
}

func TestDefaultPlanDrafter_DraftClaudeMissing(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("PATH", empty)

	d := DefaultPlanDrafter()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := d.Draft(ctx, DraftRequest{UserPrompt: "add dark mode"})
	if !errors.Is(err, ErrClaudeNotFound) {
		t.Errorf("err = %v, want errors.Is(err, ErrClaudeNotFound)", err)
	}
}

func TestDefaultPlanDrafter_DraftNonZeroExitSurfacesStderr(t *testing.T) {
	dir := t.TempDir()
	writeFakeClaude(t, dir, "noise", 1)
	withPATH(t, dir)

	d := DefaultPlanDrafter()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := d.Draft(ctx, DraftRequest{UserPrompt: "add dark mode"})
	if err == nil {
		t.Fatal("Draft should error on non-zero exit")
	}
	if !strings.Contains(err.Error(), "claude planner") {
		t.Errorf("err message should mention claude planner; got %q", err.Error())
	}
}

func TestDefaultPlanDrafter_DraftContextTimeout(t *testing.T) {
	dir := t.TempDir()
	writeSlowClaude(t, dir, 10)
	withPATH(t, dir)

	d := DefaultPlanDrafter()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := d.Draft(ctx, DraftRequest{UserPrompt: "add dark mode"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("Draft waited %v after 200ms timeout (expected to be killed promptly)", elapsed)
	}
}

func TestDefaultPlanDrafter_DraftStripsBatonHookEnv(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.txt")
	writeEnvDumpingClaude(t, dir, envFile, "# Goal\nstub")
	withPATH(t, dir)

	t.Setenv("BATON_HOOK_SOCKET", "/should/not/leak.sock")
	t.Setenv("BATON_AGENT_ID", "should-not-leak")

	d := DefaultPlanDrafter()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := d.Draft(ctx, DraftRequest{UserPrompt: "add dark mode"}); err != nil {
		t.Fatalf("Draft: %v", err)
	}

	envContents, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	body := string(envContents)
	for _, banned := range []string{"BATON_HOOK_SOCKET=", "BATON_AGENT_ID="} {
		if strings.Contains(body, banned) {
			t.Errorf("planner env contained %q; should have been stripped\n%s", banned, body)
		}
	}
}

func TestDefaultPlanDrafter_ReviseSuccess(t *testing.T) {
	dir := t.TempDir()
	const revised = "# Goal\nRevised\n\n## Tasks\n- [ ] one\n- [ ] two"
	writeFakeClaude(t, dir, revised, 0)
	withPATH(t, dir)

	d := DefaultPlanDrafter()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := d.Revise(ctx, ReviseRequest{
		CurrentPlan: "# Goal\nold\n",
		Critique:    "split task",
	})
	if err != nil {
		t.Fatalf("Revise: %v", err)
	}
	if got != revised {
		t.Errorf("Revise = %q, want %q", got, revised)
	}
}

func TestDefaultPlanDrafter_ReviseEmptyInputs(t *testing.T) {
	d := DefaultPlanDrafter()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := d.Revise(ctx, ReviseRequest{CurrentPlan: "", Critique: "x"})
	if !errors.Is(err, ErrEmptyPlan) {
		t.Errorf("empty plan err = %v, want ErrEmptyPlan", err)
	}
	_, err = d.Revise(ctx, ReviseRequest{CurrentPlan: "x", Critique: ""})
	if !errors.Is(err, ErrEmptyCritique) {
		t.Errorf("empty critique err = %v, want ErrEmptyCritique", err)
	}
}

func TestDefaultPlanDrafter_RevisePipesPlanAndCritique(t *testing.T) {
	dir := t.TempDir()
	stdinFile := filepath.Join(dir, "stdin.txt")
	writeStdinCapturingClaude(t, dir, stdinFile, "# Goal\nrevised")
	withPATH(t, dir)

	d := DefaultPlanDrafter()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const plan = "# Goal\nAdd thing\n## Tasks\n- [ ] do A"
	const critique = "split task A into A1 and A2"
	if _, err := d.Revise(ctx, ReviseRequest{CurrentPlan: plan, Critique: critique}); err != nil {
		t.Fatalf("Revise: %v", err)
	}

	got, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	body := string(got)
	if !strings.HasPrefix(body, planRevisePrompt) {
		t.Errorf("stdin missing planRevisePrompt prefix\nstdin=%q", body)
	}
	if !strings.Contains(body, plan) {
		t.Errorf("stdin missing current plan; got=%q", body)
	}
	if !strings.Contains(body, critique) {
		t.Errorf("stdin missing critique; got=%q", body)
	}
}

func TestBuildClaudePlannerArgs_UsesSonnet(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	args := buildClaudePlannerArgs()
	if args[0] != "-p" {
		t.Errorf("first arg = %q, want -p", args[0])
	}
	if !containsPair(args, "--model", claudeSonnetModel) {
		t.Errorf("missing --model %s; args=%v", claudeSonnetModel, args)
	}
	for _, banned := range []string{"claude-haiku-4-5", "claude-haiku-3-5"} {
		for _, a := range args {
			if a == banned {
				t.Errorf("planner args contain Haiku model %q; should be Sonnet", banned)
			}
		}
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--strict-mcp-config",
		"--disable-slash-commands",
		"--no-session-persistence",
		"--tools ",
		"--setting-sources user",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q; got %q", want, joined)
		}
	}

	// --mcp-config must be a JSON object with an "mcpServers" record. A bare
	// "{}" is rejected by claude's strict schema validator (regression guard
	// for the silent planner failure).
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

func TestBuildClaudePlannerArgs_BareGatedByAPIKey(t *testing.T) {
	t.Run("with API key", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "sk-test")
		args := buildClaudePlannerArgs()
		if !contains(args, "--bare") {
			t.Errorf("expected --bare with ANTHROPIC_API_KEY set; got %v", args)
		}
	})
	t.Run("without API key", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "")
		args := buildClaudePlannerArgs()
		if contains(args, "--bare") {
			t.Errorf("did not expect --bare without ANTHROPIC_API_KEY; got %v", args)
		}
	})
}
