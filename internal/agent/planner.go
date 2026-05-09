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

const claudeSonnetModel = "claude-sonnet-4-6"

// PlanDraftTimeout bounds a single Draft or Revise subprocess. Plan generation
// is one-shot (no retry), so this is also the wall-clock budget the user
// waits before the editor opens. Declared as var so tests can shrink it.
var PlanDraftTimeout = 60 * time.Second

// ErrEmptyPrompt is returned when Draft is called with an empty user prompt.
var ErrEmptyPrompt = errors.New("planner: empty user prompt")

// ErrEmptyPlan is returned when Revise is called with an empty current plan.
var ErrEmptyPlan = errors.New("planner: empty current plan")

// ErrEmptyCritique is returned when Revise is called with an empty critique.
var ErrEmptyCritique = errors.New("planner: empty critique")

// PlanDrafter generates and revises plan markdown via a backing model.
// Implementations may shell out to a binary, mock the call for tests, or
// call an API directly. Both methods accept a context for cancellation.
type PlanDrafter interface {
	Draft(ctx context.Context, req DraftRequest) (string, error)
	Revise(ctx context.Context, req ReviseRequest) (string, error)
}

// DraftRequest is the input to PlanDrafter.Draft.
type DraftRequest struct {
	UserPrompt string
}

// ReviseRequest is the input to PlanDrafter.Revise.
type ReviseRequest struct {
	CurrentPlan string
	Critique    string
}

// planDraftPrompt frames each Draft call. The five fixed sections match what
// the plan editor expects (Goal / Context / Tasks / Verification / Not in
// scope). Kept in lockstep with the editor's render so a renamed section
// would be a code-coupled change, not a silent drift.
const planDraftPrompt = `You are helping a developer plan a coding task before they hand it to an AI coding agent. Produce a concise markdown plan with these sections, in order:

# Goal
One sentence: what is the developer trying to accomplish?

## Context
2-3 sentences of background. What part of the system does this touch, and what constraints matter?

## Tasks
A short checklist of the steps to ship this. Each task should be small and independently verifiable. Use markdown task syntax: - [ ] description.

## Verification
How will the developer know the change works? Tests, manual checks, or both.

## Not in scope
What this plan deliberately excludes.

Keep the whole plan under 400 words. The developer will edit your output before approving — favor a short, clear plan they can refine over an exhaustive one. Output only the markdown plan; no preamble, no surrounding explanation.

The developer's task description follows.

`

// planRevisePrompt frames each Revise call. The current plan and critique
// are appended verbatim so the model sees the literal markdown it produced
// earlier alongside the change request.
const planRevisePrompt = `You are revising an existing plan for a coding task based on the developer's feedback. Output the full revised plan with the same five sections (Goal / Context / Tasks / Verification / Not in scope). Preserve sections, wording, and tasks the feedback does not touch — make small, surgical changes. Output only the markdown plan; no preamble.

CURRENT PLAN:
`

// DefaultPlanDrafter returns a PlanDrafter that shells out to
// `claude -p --model claude-sonnet-4-6` with the planning instruction piped
// on stdin. Mirrors DefaultBranchNamer's approach: bounded per-call context,
// env stripped of baton hook wiring so the subprocess does not register
// against the running TUI's hook socket as the parent agent.
//
// Sonnet (not Haiku) is intentional: planning quality compounds downstream,
// since a fuzzier plan turns into a fuzzier agent run and more verification
// tax. The cost of one extra one-shot subprocess at planning time is low
// next to the human review time it saves later.
func DefaultPlanDrafter() PlanDrafter {
	return &defaultPlanDrafter{}
}

type defaultPlanDrafter struct{}

func (d *defaultPlanDrafter) Draft(ctx context.Context, req DraftRequest) (string, error) {
	prompt := strings.TrimSpace(req.UserPrompt)
	if prompt == "" {
		return "", ErrEmptyPrompt
	}
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrClaudeNotFound, err)
	}
	return runClaudePlanner(ctx, claudePath, planDraftPrompt+prompt)
}

func (d *defaultPlanDrafter) Revise(ctx context.Context, req ReviseRequest) (string, error) {
	current := strings.TrimSpace(req.CurrentPlan)
	critique := strings.TrimSpace(req.Critique)
	if current == "" {
		return "", ErrEmptyPlan
	}
	if critique == "" {
		return "", ErrEmptyCritique
	}
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrClaudeNotFound, err)
	}
	instruction := planRevisePrompt + current + "\n\nCRITIQUE:\n" + critique + "\n"
	return runClaudePlanner(ctx, claudePath, instruction)
}

// buildClaudePlannerArgs returns the argv (excluding the binary path) for a
// one-shot planning subprocess. Mirrors buildClaudeHaikuArgs but uses Sonnet.
// Tools stay disabled — the planner generates a markdown document, it does
// not need to touch the filesystem or call MCP servers.
//
// The --mcp-config payload MUST be a JSON object containing an "mcpServers"
// key (with an empty record as the value to declare zero servers). Claude's
// strict schema validator rejects a bare "{}" with
// `mcpServers: Invalid input: expected record, received undefined`, which
// makes every planner subprocess exit 1 and surfaces as
// `claude planner: exit status 1`. Do not simplify this back to "{}".
func buildClaudePlannerArgs() []string {
	args := []string{"-p", "--model", claudeSonnetModel}
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

// runClaudePlanner runs `claude -p --model claude-sonnet-4-6` with instruction
// on stdin and returns the trimmed raw stdout (markdown). Strips baton's hook
// env so the subprocess does not register against the running TUI's hook
// socket as the parent agent.
func runClaudePlanner(ctx context.Context, claudePath, instruction string) (string, error) {
	cmd := exec.CommandContext(ctx, claudePath, buildClaudePlannerArgs()...)
	cmd.Stdin = strings.NewReader(instruction)
	cmd.Env = sanitizedHaikuEnv(os.Environ())
	cmd.WaitDelay = 500 * time.Millisecond

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude planner: %w (stderr=%q)", err, strings.TrimSpace(stderr.String()))
	}

	return strings.TrimSpace(stdout.String()), nil
}
