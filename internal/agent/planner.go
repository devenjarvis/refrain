package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/devenjarvis/refrain/internal/config"
)

// PlannerQuestionSocketEnv is the environment variable the planner Sonnet
// subprocess inherits to point its MCP bridge at the right unix socket.
// Exported so cmd/plannerquestion.go (and tests) can reference the same
// name without hardcoding the string in two places.
const PlannerQuestionSocketEnv = "REFRAIN_PLANNER_QUESTION_SOCKET"

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
// internal/planner.Server in the running refrain process. The drafter wires
// it into the Sonnet subprocess as an MCP server so the planner can pause
// and call ask_user; an empty value disables the feature for this draft.
//
// Cwd, when non-empty, sets the working directory for the Claude subprocess.
// Set to sess.Worktree.Path so the planner reads the right codebase when
// multiple repos are registered.
type DraftRequest struct {
	UserPrompt     string
	Model          string
	QuestionSocket string
	Cwd            string
}

// ReviseRequest is the input to PlanDrafter.Revise.
//
// Cwd, when non-empty, sets the working directory for the Claude subprocess.
// Set to sess.Worktree.Path so the reviser operates in the correct repo.
type ReviseRequest struct {
	CurrentPlan string
	Critique    string
	Cwd         string
	Model       string
}

// planDraftPrompt frames each Draft call. The eight fixed sections (Goal /
// Spec / Context / Reuse / Risks / Tasks / Verification / Not in scope) plus
// the per-task labeled sub-bullets are research-backed (Plan-and-Solve, TDAD,
// Superpowers, OpenSpec): plan completeness, not brevity, predicts build-agent
// success. Wall-of-text risk is bounded by writing-density rules baked into
// the prompt (Goal one sentence, Spec items one line, imperative task names,
// labeled one-line sub-bullets) so the human reviewer's scan cost stays low
// while the building agent gets executable detail. The checkbox-counting
// constraint is load-bearing: ParsePlanTasks (session.go) and planTaskCounts
// (tui/dashboard.go) count ALL "- [ ]" / "- [x]" lines top-to-bottom to derive
// [task N] commit prefixes, so the prompt forbids checkbox syntax outside the
// Tasks section.
const planDraftPrompt = `You are drafting a plan with two readers: a separate coding agent that will execute it end-to-end, and a human developer who will scan it for correctness before approving. Write for both — the human cares about Goal, Spec, and the task list; the agent cares about the per-task sub-bullets. Detail is what makes this work: a plan that names specific files, line ranges, type signatures, and test expectations turns into a deterministic agent run, while a plan that hand-waves turns into rework.

Your working directory is the developer's worktree root. You have a read-only toolset: Read, Grep, Glob, LS, LSP, WebFetch, WebSearch. USE THEM aggressively before drafting — locate every file the change touches, scan the surrounding code conventions, identify existing helpers you can reuse, and pull the relevant tests so you can name them by path. You cannot write, edit, or run shell commands; this is a research-then-draft pass.

If a load-bearing ambiguity would otherwise force you to fabricate an assumption, you may call ask_user once with a single focused clarifying question. Prefer reading the code; never ask for trivia you could grep for.

There is no word cap. Be as long as the task requires and no longer — a trivial one-file change might be 200 words; a cross-package refactor might be 2000. Length comes from completeness, not padding.

## Required section structure

Produce a markdown plan with exactly these sections, in this order. Section names are shown in backticks below; emit them as actual markdown headings in your output at the heading level shown.

` + "`# Goal`" + ` — exactly one sentence. What is the developer trying to accomplish?

` + "`## Spec`" + ` — numbered acceptance criteria, one line per item, at most ~12 items. Each item should be a sentence the developer could turn into an assertion. No vague verbs ("handles", "supports") without a measurable subject. If you need more than ~12, the change is too large for one plan — split it and say so in Not in scope.

` + "`## Context`" + ` — bullet list (one fact per bullet) of what part of the system this touches. Cite real file:line references from your research. Note architectural constraints and local conventions (e.g. "this package uses table-driven tests with testify/require").

` + "`## Reuse`" + ` — bullet list of existing helpers, utilities, types, or patterns the implementation should build on rather than recreate. Cite paths/symbols. If nothing suitable exists, say so explicitly — the absence is a finding.

` + "`## Risks`" + ` — bullet list of architectural unknowns, external API contracts, concurrency hazards, or tests that will need updating. State load-bearing assumptions so the building agent knows what to probe early.

` + "`## Tasks`" + ` — ordered checklist. Each ` + "`- [ ]`" + ` line is one task and maps to a [task N] commit prefix. Task names are imperative short phrases ("Add --json flag to doctor", not "Working on adding a JSON output mode to the doctor command"). Each task includes labeled sub-bullets, one line each:

  - Files: path/to/file.go:42, path/to/other.go
  - Signatures: func Foo(ctx context.Context, x Bar) (Baz, error)  (only if introducing or changing one; omit otherwise)
  - Test first: write failing test in path/to/file_test.go covering <case>; expect <specific failure message or assertion>
  - Implement: 1–3 sentences describing the production change
  - Verify: go test -race ./internal/foo passes; manually confirm <X>

Sub-bullets MUST use two-space-indent ` + "`  - `" + ` (a regular bullet, no brackets). NEVER use ` + "`- [ ]`" + ` or ` + "`- [x]`" + ` anywhere except as a task name in this section — the build agent counts ALL checkbox lines top-to-bottom across the entire document to derive [task N] commit prefixes, so a stray checkbox in Goal, Spec, Context, Reuse, Risks, Verification, or Not in scope shifts every commit's task index and breaks the review panel.

Every task follows test-first ordering. If a task has no meaningful test, say so explicitly in Verify ("manual: <specific check>") — never omit verification.

` + "`## Verification`" + ` — end-to-end checks once all tasks are done. Bullet list of concrete commands and expected outcomes, not prose:

  - go test -race ./...
  - go vet ./...
  - Manual: launch refrain, do <X>, observe <Y>

` + "`## Not in scope`" + ` — what this plan deliberately excludes. Use this to head off scope creep — if the developer's prompt implies a wider change, name the slice you're cutting and why.

## Forbidden language

Every word must be load-bearing. Reject these on sight:
- Placeholders: "TBD", "similar to task N", "appropriate error handling", "as needed", "etc.", "and so on"
- Filler: "this plan will...", "the goal is...", "to summarize", "in conclusion", "we will then..."

Every task must be self-contained and complete.

## Output discipline

Your response MUST begin with ` + "`# Goal`" + ` on the very first line — no preamble, no transitional sentence. Research with the tools as much as you need before you start writing; tool calls and what you read don't count toward output length.

The developer's task description follows.

`

// planRevisePrompt frames each Revise call. The current plan and critique
// are appended verbatim so the model sees the literal markdown it produced
// earlier alongside the change request.
const planRevisePrompt = `You are revising an existing plan for a coding task based on the developer's feedback. Your working directory is the developer's worktree root, and you have the same read-only toolset as the original drafter (Read, Grep, Glob, LS, LSP, WebFetch, WebSearch). If the critique points at code or files the current plan didn't already cover, re-read the relevant source before revising — don't invent paths or symbols.

Output the full revised plan with the same eight sections (Goal / Spec / Context / Reuse / Risks / Tasks / Verification / Not in scope). Preserve sections, wording, and tasks the feedback does not touch — make small, surgical changes.

The same length, density, structure, and forbidden-language rules from the original drafting prompt apply: Goal is one sentence; Spec items are one line each; Context, Reuse, Risks, and Verification are bullet lists with one fact per bullet; task names are imperative short phrases; per-task sub-bullets are labeled and indented with ` + "`  - `" + ` (NEVER ` + "`- [ ]`" + ` or ` + "`- [x]`" + ` outside task names — the build agent counts checkbox lines globally to derive [task N] commit prefixes, so a stray checkbox shifts every commit's task index); no filler phrases. There is no word cap.

Your response MUST begin with ` + "`# Goal`" + ` on the very first line — do not write any text, summary, or transitional sentence before it.

CURRENT PLAN:
`

// DefaultPlanDrafter returns a PlanDrafter that shells out to
// `claude -p --model <model>` with the planning instruction piped on stdin.
// An empty model falls back to config.DefaultPlanModel so callers that
// haven't migrated to the parameterized form keep their existing behavior.
// Env stripped of refrain hook wiring so the subprocess does not register
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
	model := req.Model
	if model == "" {
		model = d.model
	}
	return runClaudePlanner(ctx, claudePath, model, planDraftPrompt+prompt, req.QuestionSocket, req.Cwd)
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
	model := req.Model
	if model == "" {
		model = d.model
	}
	instruction := planRevisePrompt + current + "\n\nCRITIQUE:\n" + critique + "\n"
	// Revise does not surface ask_user — the user is already iterating on a
	// concrete plan with the editor's revise input, so an interactive prompt
	// would just compete for attention. Pass an empty socket path.
	return runClaudePlanner(ctx, claudePath, model, instruction, "", req.Cwd)
}

// plannerQuestionMCPName is the MCP-server key under which the planner
// `ask_user` bridge is registered. Claude prefixes tool names with the
// server key (`mcp__<name>__<tool>`), so this constant determines the
// concrete tool name that ends up on the --tools allowlist below.
const plannerQuestionMCPName = "refrain_planner_question"

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
// `refrain planner-question-server` MCP bridge so the planner can pause and
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

	tools := "Read,Grep,Glob,LS,LSP,WebFetch,WebSearch"
	if questionSocket != "" {
		tools += "," + plannerQuestionToolName
	}

	// --tools filters which tools are visible (keeps Bash/Edit/Write hidden even
	// if Claude's default set expands). --allowedTools auto-approves those same
	// tools so the non-interactive -p subprocess never hits a permission gate.
	// Both flags are required: --tools alone only controls availability, not approval.
	return buildClaudeArgs(claudeArgsOpts{
		model:          model,
		mcpConfig:      plannerMCPConfigJSON(questionSocket),
		tools:          tools,
		allowTools:     true,
		settingSources: "user,project",
	})
}

// plannerMCPConfigJSON renders the --mcp-config payload. With an empty
// socket path it returns the canonical `{"mcpServers":{}}` shape required
// by claude's strict validator. With a socket path it adds a single server
// entry that re-execs the running refrain binary as the question bridge.
//
// The socket path is also written into the server entry's env map so it
// reaches the MCP child reliably. Setting it on the parent claude process
// alone is not enough: Claude Code does not always forward its full env to
// MCP subprocess hosts, and a missing REFRAIN_PLANNER_QUESTION_SOCKET makes
// the bridge exit silently — the planner then sees ask_user as a tool error
// and moves past without surfacing the question to the editor.
func plannerMCPConfigJSON(questionSocket string) string {
	if questionSocket == "" {
		return `{"mcpServers":{}}`
	}
	bin, err := os.Executable()
	if err != nil || bin == "" {
		// Best effort: fall back to looking up `refrain` on PATH. If that also
		// fails, omit the server — better to plan without ask_user than to
		// hand claude a config it can't spawn.
		if p, lookErr := exec.LookPath("refrain"); lookErr == nil {
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
// stdin and returns the trimmed raw stdout (markdown). Strips refrain's hook
// env so the subprocess does not register against the running TUI's hook
// socket as the parent agent. When questionSocket is non-empty, the
// REFRAIN_PLANNER_QUESTION_SOCKET env is added so the spawned MCP bridge
// (registered via buildClaudePlannerArgs) can dial back into refrain.
// When cwd is non-empty, cmd.Dir is set so the subprocess reads the correct
// repo when multiple repos are registered in refrain.
func runClaudePlanner(ctx context.Context, claudePath, model, instruction, questionSocket, cwd string) (string, error) {
	var extraEnv []string
	if questionSocket != "" {
		extraEnv = append(extraEnv, PlannerQuestionSocketEnv+"="+questionSocket)
	}

	output, err := runClaudeSubprocess(ctx, claudePath, claudeRunOpts{
		args:             buildClaudePlannerArgs(model, questionSocket),
		stdin:            instruction,
		extraEnv:         extraEnv,
		dir:              cwd,
		errPrefix:        "claude planner",
		errIncludeStdout: true,
	})
	if err != nil {
		return "", err
	}

	if idx := strings.Index(output, "# Goal"); idx > 0 {
		output = output[idx:]
	}
	return output, nil
}

// runDraftWithRetry calls drafter.Draft up to planDraftAttempts times,
// retrying on transient failures (non-terminal errors or empty body).
// ErrEmptyPrompt and ErrClaudeNotFound short-circuit on the first attempt.
// onAttempt, if non-nil, is called before each subprocess invocation with
// (currentAttempt, maxAttempts) so callers can update display state.
func runDraftWithRetry(
	ctx context.Context,
	drafter PlanDrafter,
	req DraftRequest,
	done <-chan struct{},
	onAttempt func(attempt, max int),
) (string, error) {
	maxAttempts := planDraftAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Between attempts: check for cancellation before sleeping or retrying.
		// We do NOT check before the first attempt — the drafter itself receives
		// the (possibly already-cancelled) context and handles it gracefully,
		// which existing tests rely on.
		if attempt > 1 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			select {
			case <-done:
				return "", context.Canceled
			default:
			}
		}

		if onAttempt != nil {
			onAttempt(attempt, maxAttempts)
		}

		attemptCtx, cancel := context.WithTimeout(ctx, planDraftPerAttemptCap)
		body, err := drafter.Draft(attemptCtx, req)
		cancel()

		// Terminal errors: short-circuit immediately, no backoff.
		if errors.Is(err, ErrEmptyPrompt) || errors.Is(err, ErrClaudeNotFound) {
			return "", err
		}

		if err == nil && strings.TrimSpace(body) != "" {
			return body, nil
		}

		if err != nil {
			lastErr = err
		} else {
			lastErr = errors.New("planner returned empty plan")
		}

		if attempt == maxAttempts {
			break
		}

		// Sleep between attempts, aborting on context cancellation.
		var wait time.Duration
		if idx := attempt - 1; idx < len(planDraftBackoff) {
			wait = planDraftBackoff[idx]
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
