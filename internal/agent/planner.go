package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/devenjarvis/baton/internal/config"
)

// PlannerQuestionSocketEnv is the environment variable the planner Sonnet
// subprocess inherits to point its MCP bridge at the right unix socket.
// Exported so cmd/plannerquestion.go (and tests) can reference the same
// name without hardcoding the string in two places.
const PlannerQuestionSocketEnv = "BATON_PLANNER_QUESTION_SOCKET"

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
//
// QuestionSocket, when non-empty, points at a unix socket served by an
// internal/planner.Server in the running baton process. The drafter wires
// it into the Sonnet subprocess as an MCP server so the planner can pause
// and call ask_user; an empty value disables the feature for this draft.
type DraftRequest struct {
	UserPrompt     string
	QuestionSocket string
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
const planDraftPrompt = `You are helping a developer plan a coding task before they hand it to an AI coding agent. Your working directory is the developer's worktree root.

You have a read-only toolset: Read, Grep, Glob, LS, LSP, WebFetch, WebSearch. Before writing the plan, USE THEM to ground your work in the real codebase — locate the files the task touches, scan the conventions in that area, and check related code or imports. A plan that names actual files, functions, and constraints is far more useful than one written from the prompt alone. You cannot write, edit, or run shell commands; this is a research-then-draft pass, not an implementation.

If the task description is genuinely ambiguous or missing information you can't infer from the codebase, you may call the ask_user tool with one focused clarifying question and wait for the developer's reply. Use it sparingly: prefer reading the code, and only ask when an unanswered ambiguity would force you to fabricate a load-bearing assumption. Never ask for trivia you could grep for.

Once you've researched enough, produce a concise markdown plan with these sections, in order:

# Goal
One sentence: what is the developer trying to accomplish?

## Context
2-3 sentences of background. What part of the system does this touch, and what constraints matter? Cite real file paths or symbols where relevant.

## Tasks
A short checklist of the steps to ship this. Each task should be small and independently verifiable. Use markdown task syntax: - [ ] description.

## Verification
How will the developer know the change works? Tests, manual checks, or both.

## Not in scope
What this plan deliberately excludes.

The 400-word cap applies to the PLAN OUTPUT only — research with the tools as much as you need; tool calls and what you read don't count toward the cap, so don't truncate research mid-flight to stay short. The developer will edit your output before approving — favor a short, clear, code-grounded plan they can refine over an exhaustive one. Your response MUST begin with ` + "`# Goal`" + ` on the very first line — do not write any text, summary, or transitional sentence before it.

The developer's task description follows.

`

// planRevisePrompt frames each Revise call. The current plan and critique
// are appended verbatim so the model sees the literal markdown it produced
// earlier alongside the change request.
const planRevisePrompt = `You are revising an existing plan for a coding task based on the developer's feedback. Your working directory is the developer's worktree root, and you have the same read-only toolset as the original drafter (Read, Grep, Glob, LS, LSP, WebFetch, WebSearch). If the critique points at code or files the current plan didn't already cover, re-read the relevant source before revising — don't invent paths or symbols.

Output the full revised plan with the same five sections (Goal / Context / Tasks / Verification / Not in scope). Preserve sections, wording, and tasks the feedback does not touch — make small, surgical changes. Keep the plan under 400 words; research and tool use don't count toward that cap. Your response MUST begin with ` + "`# Goal`" + ` on the very first line — do not write any text, summary, or transitional sentence before it.

CURRENT PLAN:
`

// DefaultPlanDrafter returns a PlanDrafter that shells out to
// `claude -p --model <model>` with the planning instruction piped on stdin.
// An empty model falls back to config.DefaultPlanModel so callers that
// haven't migrated to the parameterized form keep their existing behavior.
// Env stripped of baton hook wiring so the subprocess does not register
// against the running TUI's hook socket as the parent agent.
//
// Cancellation is caller-driven via ctx (no wall-clock timeout in the default
// path): Sonnet drafting can take a couple of minutes on complex prompts and
// the user is actively waiting for the editor — manager StartDraft / Revise
// pass a cancel-only context so the only kill paths are user-initiated
// (KillSession, manager shutdown, an explicit CancelDraft / CancelRevise).
//
// Sonnet (not Haiku) is the default for a reason: planning quality compounds
// downstream, since a fuzzier plan turns into a fuzzier agent run and more
// verification tax. The cost of one extra one-shot subprocess at planning
// time is low next to the human review time it saves later. Users who want
// to override this for cost or speed reasons can set plan_model in config.
func DefaultPlanDrafter(model string) PlanDrafter {
	if model == "" {
		model = config.DefaultPlanModel
	}
	return &defaultPlanDrafter{model: model}
}

type defaultPlanDrafter struct {
	model string
}

// Model returns the model string this drafter was constructed with.
// Used by the manager to detect whether a settings change requires
// swapping in a fresh drafter.
func (d *defaultPlanDrafter) Model() string {
	return d.model
}

func (d *defaultPlanDrafter) Draft(ctx context.Context, req DraftRequest) (string, error) {
	prompt := strings.TrimSpace(req.UserPrompt)
	if prompt == "" {
		return "", ErrEmptyPrompt
	}
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrClaudeNotFound, err)
	}
	return runClaudePlanner(ctx, claudePath, d.model, planDraftPrompt+prompt, req.QuestionSocket)
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
	// Revise does not surface ask_user — the user is already iterating on a
	// concrete plan with the editor's revise input, so an interactive prompt
	// would just compete for attention. Pass an empty socket path.
	return runClaudePlanner(ctx, claudePath, d.model, instruction, "")
}

// plannerQuestionMCPName is the MCP-server key under which the planner
// `ask_user` bridge is registered. Claude prefixes tool names with the
// server key (`mcp__<name>__<tool>`), so this constant determines the
// concrete tool name that ends up on the --tools allowlist below.
const plannerQuestionMCPName = "baton_planner_question"

// plannerQuestionToolName is the fully-qualified tool name Claude exposes
// for the ask_user bridge after MCP namespacing. Kept as a derived constant
// so the allowlist string and the server name can't drift.
const plannerQuestionToolName = "mcp__" + plannerQuestionMCPName + "__ask_user"

// buildClaudePlannerArgs returns the argv (excluding the binary path) for a
// one-shot planning subprocess. The model is parameterized so callers can
// override the default; an empty model falls back to config.DefaultPlanModel.
// The drafter is given a read-only tool allowlist so it can research the
// codebase (Read/Grep/Glob/LS/LSP) and pull external docs (WebFetch/WebSearch)
// before producing the plan markdown. Writes and Bash stay blocked — the
// planner is a thinker, not an editor. Setting sources include project so
// worktree-local CLAUDE.md guidance reaches the drafter.
//
// When questionSocket is non-empty, the argv is augmented to register the
// `baton planner-question-server` MCP bridge so the planner can pause and
// ask the user for clarification mid-draft. When empty (e.g. revise calls
// or tests that don't care), the planner runs with zero MCP servers.
//
// Under --bare (API-key path) the harness skips LSP and CLAUDE.md
// auto-discovery regardless of these flags; that's accepted as a known
// limitation. Read/Grep/Glob/LS still function in bare mode.
//
// The --mcp-config payload MUST be a JSON object containing an "mcpServers"
// key (with an empty record as the value to declare zero servers). Claude's
// strict schema validator rejects a bare "{}" with
// `mcpServers: Invalid input: expected record, received undefined`, which
// makes every planner subprocess exit 1 and surfaces as
// `claude planner: exit status 1`. Do not simplify this back to "{}".
func buildClaudePlannerArgs(model, questionSocket string) []string {
	if model == "" {
		model = config.DefaultPlanModel
	}
	args := []string{"-p", "--model", model}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		args = append(args, "--bare")
	}

	mcpConfig := plannerMCPConfigJSON(questionSocket)
	tools := "Read,Grep,Glob,LS,LSP,WebFetch,WebSearch"
	if questionSocket != "" {
		tools += "," + plannerQuestionToolName
	}

	args = append(
		args,
		"--strict-mcp-config", "--mcp-config", mcpConfig,
		"--disable-slash-commands",
		"--no-session-persistence",
		"--tools", tools,
		"--setting-sources", "user,project",
		"--exclude-dynamic-system-prompt-sections",
	)
	return args
}

// plannerMCPConfigJSON renders the --mcp-config payload. With an empty
// socket path it returns the canonical `{"mcpServers":{}}` shape required
// by claude's strict validator. With a socket path it adds a single server
// entry that re-execs the running baton binary as the question bridge.
//
// The socket path is also written into the server entry's env map so it
// reaches the MCP child reliably. Setting it on the parent claude process
// alone is not enough: Claude Code does not always forward its full env to
// MCP subprocess hosts, and a missing BATON_PLANNER_QUESTION_SOCKET makes
// the bridge exit silently — the planner then sees ask_user as a tool error
// and moves past without surfacing the question to the editor.
func plannerMCPConfigJSON(questionSocket string) string {
	if questionSocket == "" {
		return `{"mcpServers":{}}`
	}
	bin, err := os.Executable()
	if err != nil || bin == "" {
		// Best effort: fall back to looking up `baton` on PATH. If that also
		// fails, omit the server — better to plan without ask_user than to
		// hand claude a config it can't spawn.
		if p, lookErr := exec.LookPath("baton"); lookErr == nil {
			bin = p
		} else {
			return `{"mcpServers":{}}`
		}
	}
	payload := map[string]any{
		"mcpServers": map[string]any{
			plannerQuestionMCPName: map[string]any{
				"command": bin,
				"args":    []string{"planner-question-server"},
				"env": map[string]any{
					PlannerQuestionSocketEnv: questionSocket,
				},
			},
		},
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return `{"mcpServers":{}}`
	}
	return string(out)
}

// runClaudePlanner runs `claude -p --model <model>` with instruction on
// stdin and returns the trimmed raw stdout (markdown). Strips baton's hook
// env so the subprocess does not register against the running TUI's hook
// socket as the parent agent. When questionSocket is non-empty, the
// BATON_PLANNER_QUESTION_SOCKET env is added so the spawned MCP bridge
// (registered via buildClaudePlannerArgs) can dial back into baton.
func runClaudePlanner(ctx context.Context, claudePath, model, instruction, questionSocket string) (string, error) {
	cmd := exec.CommandContext(ctx, claudePath, buildClaudePlannerArgs(model, questionSocket)...)
	cmd.Stdin = strings.NewReader(instruction)
	env := sanitizedHaikuEnv(os.Environ())
	if questionSocket != "" {
		env = append(env, PlannerQuestionSocketEnv+"="+questionSocket)
	}
	cmd.Env = env
	cmd.WaitDelay = 500 * time.Millisecond

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude planner: %w (stderr=%q)", err, strings.TrimSpace(stderr.String()))
	}

	output := strings.TrimSpace(stdout.String())
	if idx := strings.Index(output, "# Goal"); idx > 0 {
		output = output[idx:]
	}
	return output, nil
}
