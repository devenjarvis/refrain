package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Default values for all settings.
const (
	DefaultAudioEnabled      = true
	DefaultBypassPermissions = true
	DefaultBranchPrefix      = "refrain/"
	DefaultAgentProgram      = "claude"
	DefaultWorktreeDir       = ".refrain/worktrees"
	DefaultSidebarWidth      = 30
	DefaultMergeMethod       = "squash"

	// DefaultPlanModel is the model used by the plan drafter subprocess.
	// Sonnet (not Haiku) is intentional: planning quality compounds
	// downstream — see the comment on agent.DefaultPlanDrafter for details.
	// The agent package references this constant rather than declaring its
	// own so the default stays a single source of truth.
	DefaultPlanModel = "claude-sonnet-4-6"

	// DefaultReviewerModel is the model used by the per-task reviewer subprocess.
	// Sonnet matches the planner default — reviewer output quality directly affects
	// the developer's confidence in the review verdict.
	DefaultReviewerModel = "claude-sonnet-4-6"
	MinSidebarWidth      = 20
	MaxSidebarWidth      = 60

	// Wellness defaults.
	DefaultFocusSessionMinutes   = 90
	DefaultFocusBreakMinutes     = 15
	DefaultMaxConcurrentSessions = 3
	DefaultMaxReviewBacklog      = 5

	// DefaultBranchNamePrompt is the instruction sent to Haiku to summarize
	// the user's first prompt into a branch slug. Users can override via the
	// branch_name_prompt key in global or per-repo config; the {prompt} token
	// is substituted with the user's prompt before the call.
	DefaultBranchNamePrompt = "Summarize this task into a 3-5 word git branch slug (lowercase, kebab-case, no prefix). Respond with ONLY the slug.\n\n{prompt}"

	// DefaultPRDraftByDefault controls whether new PRs are created as drafts.
	// Defaults to true — drafts require an explicit "ready for review" action
	// on GitHub, which prompts a deliberate review decision.
	DefaultPRDraftByDefault = true

	// DefaultAutoOpenPRInBrowser controls whether the browser opens
	// automatically after a PR is created. Defaults to true.
	DefaultAutoOpenPRInBrowser = true

	// DefaultPlanFirstEnabled gates the plan-first planning flow. When true
	// (the default) the n keybind opens a prompt modal, drafts a plan via
	// claude -p, and only spawns the agent after the user approves the plan.
	// Users who prefer today's behaviour (immediate spawn) can set
	// "plan_first_enabled": false in global or per-repo config, or use
	// ctrl+enter on the modal to skip the planning step on a per-session basis.
	DefaultPlanFirstEnabled = true

	// DefaultBuildFromPlanPrompt is the initial prompt refrain sends to the
	// real agent when an approved plan is handed off. The agent is expected
	// to read .claude/plan.md and execute it.
	DefaultBuildFromPlanPrompt = "Read .claude/plan.md and execute the plan. Update task checkboxes as you complete them. Stop when all tasks are complete or when you need a decision."

	// DefaultBuildSystemPrompt is appended (via Claude's
	// --append-system-prompt flag) to every Building-phase agent. It tells
	// Claude to (a) plan via TodoWrite so progress can be scraped through
	// PostToolUse hooks, and (b) commit per task with a parseable subject
	// prefix so commits can be mapped back to plan tasks during review. The
	// "[task N]" indexing matches the "- [ ]" / "- [x]" counting in
	// internal/tui/dashboard.go's planTaskCounts so the prompt and the
	// future parser agree on what counts as a task line.
	DefaultBuildSystemPrompt = `When you start a non-trivial task in this session, use the TodoWrite tool first to break the work into ordered, atomic steps and update item status as you progress.

Make exactly one git commit per completed step.

If a file at .claude/plan.md exists in the worktree, re-read .claude/plan.md immediately before each commit. The commit subject MUST start with "[task N] " where N is the 1-based index of the task line (counting ALL task list lines — both "- [ ]" and "- [x]" — top-to-bottom, ignoring non-task lines such as section headers). N is counted across the ENTIRE file, including any task lines that appear outside the ## Tasks section. If no .claude/plan.md exists, prefix the subject with "task: " instead. The rest of the subject is a normal short description.

Worked example — given a plan whose full task list (all sections) is:
  line 1: - [ ] write tests        → [task 1]
  line 2: - [ ] implement feature  → [task 2]
  line 3: - [x] update docs        → [task 3]
the third commit subject is "[task 3] update documentation".

NEVER squash commits, NEVER amend a previous commit to add new task work, and NEVER use git commit --amend. Each task MUST be its own commit so the review panel can map commits back to plan tasks one-to-one. Violating this breaks the per-task diff view.`
)

// KnownModels is the list of Claude model IDs offered in the config form
// dropdowns for fields that pass `--model` to a real subprocess (e.g. the
// plan drafter). The first entry is the form's default selection.
var KnownModels = []string{
	"claude-opus-4-7",
	"claude-sonnet-4-6",
	"claude-haiku-4-5-20251001",
}

// KnownAgentModels is the option list for the Agent Model dropdown. The
// leading empty string represents "claude default" — when selected, no
// `--model` flag is forwarded to the spawned agent and Claude picks its own
// default.
var KnownAgentModels = append([]string{""}, KnownModels...)

// ClampSidebarWidth returns w constrained to [MinSidebarWidth, MaxSidebarWidth].
func ClampSidebarWidth(w int) int {
	if w < MinSidebarWidth {
		return MinSidebarWidth
	}
	if w > MaxSidebarWidth {
		return MaxSidebarWidth
	}
	return w
}

// GlobalSettings holds user-wide settings stored at ~/.refrain/config.json.
// All fields are pointers so nil means "not set, use default."
type GlobalSettings struct {
	AudioEnabled      *bool   `json:"audio_enabled,omitempty"`
	BypassPermissions *bool   `json:"bypass_permissions,omitempty"`
	DefaultBranch     *string `json:"default_branch,omitempty"`
	BranchPrefix      *string `json:"branch_prefix,omitempty"`
	BranchNamePrompt  *string `json:"branch_name_prompt,omitempty"`
	AgentProgram      *string `json:"agent_program,omitempty"`
	AgentModel        *string `json:"agent_model,omitempty"`
	PlanModel         *string `json:"plan_model,omitempty"`
	IDECommand        *string `json:"ide_command,omitempty"`
	SidebarWidth      *int    `json:"sidebar_width,omitempty"`

	// Wellness settings — global preferences, not per-repo.
	FocusSessionMinutes   *int `json:"focus_session_minutes,omitempty"`
	FocusBreakMinutes     *int `json:"focus_break_minutes,omitempty"`
	MaxConcurrentSessions *int `json:"max_concurrent_sessions,omitempty"`
	MaxReviewBacklog      *int `json:"max_review_backlog,omitempty"`

	// ReviewerModel overrides the model used for per-task review subprocesses.
	ReviewerModel *string `json:"reviewer_model,omitempty"`

	// Plan-first planning. Both fields are also overridable per-repo.
	PlanFirstEnabled    *bool   `json:"plan_first_enabled,omitempty"`
	BuildFromPlanPrompt *string `json:"build_from_plan_prompt,omitempty"`
	BuildSystemPrompt   *string `json:"build_system_prompt,omitempty"`

	// PR creation settings.
	PRDraftByDefault    *bool `json:"pr_draft_by_default,omitempty"`
	AutoOpenPRInBrowser *bool `json:"auto_open_pr_in_browser,omitempty"`

	// MergeMethod controls how PRs are merged from the shipping panel.
	// Valid values: "squash" (default), "merge", "rebase".
	MergeMethod *string `json:"merge_method,omitempty"`
}

// ValidationCheck defines a named shell command to run against the session's
// worktree during the Checks tab of the review panel.
type ValidationCheck struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

// RepoSettings holds per-repo overrides stored at <repo>/.refrain/config.json.
// Fields here override the corresponding GlobalSettings value.
type RepoSettings struct {
	BypassPermissions   *bool   `json:"bypass_permissions,omitempty"`
	DefaultBranch       *string `json:"default_branch,omitempty"`
	BranchPrefix        *string `json:"branch_prefix,omitempty"`
	BranchNamePrompt    *string `json:"branch_name_prompt,omitempty"`
	AgentProgram        *string `json:"agent_program,omitempty"`
	AgentModel          *string `json:"agent_model,omitempty"`
	PlanModel           *string `json:"plan_model,omitempty"`
	ReviewerModel       *string `json:"reviewer_model,omitempty"`
	IDECommand          *string `json:"ide_command,omitempty"`
	WorktreeDir         *string `json:"worktree_dir,omitempty"`
	PlanFirstEnabled    *bool   `json:"plan_first_enabled,omitempty"`
	BuildFromPlanPrompt *string `json:"build_from_plan_prompt,omitempty"`
	BuildSystemPrompt   *string `json:"build_system_prompt,omitempty"`

	// PR creation settings.
	PRDraftByDefault    *bool   `json:"pr_draft_by_default,omitempty"`
	AutoOpenPRInBrowser *bool   `json:"auto_open_pr_in_browser,omitempty"`
	MergeMethod         *string `json:"merge_method,omitempty"`

	// ValidationChecks are shell commands run against the worktree during review.
	ValidationChecks []ValidationCheck `json:"validation_checks,omitempty"`
}

// ResolvedSettings is the fully merged configuration with no nil pointers.
// Consumers should use this rather than the raw Global/RepoSettings.
type ResolvedSettings struct {
	AudioEnabled      bool
	BypassPermissions bool
	DefaultBranch     string // "" means auto-detect
	BranchPrefix      string
	BranchNamePrompt  string
	AgentProgram      string
	// AgentModel is the --model flag value passed to spawned `claude`
	// agents. Empty means "no --model flag" — the Claude CLI picks its
	// own default.
	AgentModel string
	// PlanModel is the --model flag value passed to the plan drafter
	// subprocess. Defaults to DefaultPlanModel.
	PlanModel string
	// ReviewerModel is the --model flag value passed to per-task reviewer
	// subprocesses. Defaults to DefaultReviewerModel.
	ReviewerModel string
	IDECommand    string
	WorktreeDir   string
	SidebarWidth  int

	// Wellness settings.
	FocusSessionMinutes   int
	FocusBreakMinutes     int
	MaxConcurrentSessions int
	MaxReviewBacklog      int

	// Plan-first planning.
	PlanFirstEnabled    bool
	BuildFromPlanPrompt string
	// BuildSystemPrompt is forwarded as `--append-system-prompt <text>` to
	// the spawned `claude` agent at every Building-phase spawn site (and
	// resume). Empty means no flag.
	BuildSystemPrompt string

	// PR creation settings.
	PRDraftByDefault    bool
	AutoOpenPRInBrowser bool

	// MergeMethod is how PRs are merged from the shipping panel.
	// "squash" | "merge" | "rebase" — defaults to "squash".
	MergeMethod string

	// ValidationChecks are the shell commands to run during the Checks tab.
	// Sourced from per-repo settings only; empty slice means no checks configured.
	ValidationChecks []ValidationCheck
}

// Resolve merges global and repo settings over built-in defaults.
// Global overrides defaults; repo overrides global.
func Resolve(global *GlobalSettings, repo *RepoSettings) ResolvedSettings {
	r := ResolvedSettings{
		AudioEnabled:          DefaultAudioEnabled,
		BypassPermissions:     DefaultBypassPermissions,
		BranchPrefix:          DefaultBranchPrefix,
		BranchNamePrompt:      DefaultBranchNamePrompt,
		AgentProgram:          DefaultAgentProgram,
		PlanModel:             DefaultPlanModel,
		ReviewerModel:         DefaultReviewerModel,
		WorktreeDir:           DefaultWorktreeDir,
		SidebarWidth:          DefaultSidebarWidth,
		FocusSessionMinutes:   DefaultFocusSessionMinutes,
		FocusBreakMinutes:     DefaultFocusBreakMinutes,
		MaxConcurrentSessions: DefaultMaxConcurrentSessions,
		MaxReviewBacklog:      DefaultMaxReviewBacklog,
		PlanFirstEnabled:      DefaultPlanFirstEnabled,
		BuildFromPlanPrompt:   DefaultBuildFromPlanPrompt,
		BuildSystemPrompt:     DefaultBuildSystemPrompt,
		PRDraftByDefault:      DefaultPRDraftByDefault,
		AutoOpenPRInBrowser:   DefaultAutoOpenPRInBrowser,
		MergeMethod:           DefaultMergeMethod,
	}

	if global != nil {
		if global.AudioEnabled != nil {
			r.AudioEnabled = *global.AudioEnabled
		}
		if global.BypassPermissions != nil {
			r.BypassPermissions = *global.BypassPermissions
		}
		if global.DefaultBranch != nil {
			r.DefaultBranch = *global.DefaultBranch
		}
		if global.BranchPrefix != nil {
			r.BranchPrefix = *global.BranchPrefix
		}
		if global.BranchNamePrompt != nil {
			r.BranchNamePrompt = *global.BranchNamePrompt
		}
		if global.AgentProgram != nil {
			r.AgentProgram = *global.AgentProgram
		}
		if global.AgentModel != nil {
			r.AgentModel = *global.AgentModel
		}
		if global.PlanModel != nil {
			r.PlanModel = *global.PlanModel
		}
		if global.ReviewerModel != nil {
			r.ReviewerModel = *global.ReviewerModel
		}
		if global.IDECommand != nil {
			r.IDECommand = *global.IDECommand
		}
		if global.SidebarWidth != nil {
			r.SidebarWidth = ClampSidebarWidth(*global.SidebarWidth)
		}
		if global.FocusSessionMinutes != nil {
			r.FocusSessionMinutes = *global.FocusSessionMinutes
		}
		if global.FocusBreakMinutes != nil {
			r.FocusBreakMinutes = *global.FocusBreakMinutes
		}
		if global.MaxConcurrentSessions != nil {
			r.MaxConcurrentSessions = *global.MaxConcurrentSessions
		}
		if global.MaxReviewBacklog != nil {
			r.MaxReviewBacklog = *global.MaxReviewBacklog
		}
		if global.PlanFirstEnabled != nil {
			r.PlanFirstEnabled = *global.PlanFirstEnabled
		}
		if global.BuildFromPlanPrompt != nil {
			r.BuildFromPlanPrompt = *global.BuildFromPlanPrompt
		}
		if global.BuildSystemPrompt != nil {
			r.BuildSystemPrompt = *global.BuildSystemPrompt
		}
		if global.PRDraftByDefault != nil {
			r.PRDraftByDefault = *global.PRDraftByDefault
		}
		if global.AutoOpenPRInBrowser != nil {
			r.AutoOpenPRInBrowser = *global.AutoOpenPRInBrowser
		}
		if global.MergeMethod != nil {
			r.MergeMethod = *global.MergeMethod
		}
	}

	if repo != nil {
		if repo.BypassPermissions != nil {
			r.BypassPermissions = *repo.BypassPermissions
		}
		if repo.DefaultBranch != nil {
			r.DefaultBranch = *repo.DefaultBranch
		}
		if repo.BranchPrefix != nil {
			r.BranchPrefix = *repo.BranchPrefix
		}
		if repo.BranchNamePrompt != nil {
			r.BranchNamePrompt = *repo.BranchNamePrompt
		}
		if repo.AgentProgram != nil {
			r.AgentProgram = *repo.AgentProgram
		}
		if repo.AgentModel != nil {
			r.AgentModel = *repo.AgentModel
		}
		if repo.PlanModel != nil {
			r.PlanModel = *repo.PlanModel
		}
		if repo.ReviewerModel != nil {
			r.ReviewerModel = *repo.ReviewerModel
		}
		if repo.IDECommand != nil {
			r.IDECommand = *repo.IDECommand
		}
		if repo.WorktreeDir != nil {
			r.WorktreeDir = *repo.WorktreeDir
		}
		if repo.PlanFirstEnabled != nil {
			r.PlanFirstEnabled = *repo.PlanFirstEnabled
		}
		if repo.BuildFromPlanPrompt != nil {
			r.BuildFromPlanPrompt = *repo.BuildFromPlanPrompt
		}
		if repo.BuildSystemPrompt != nil {
			r.BuildSystemPrompt = *repo.BuildSystemPrompt
		}
		if repo.PRDraftByDefault != nil {
			r.PRDraftByDefault = *repo.PRDraftByDefault
		}
		if repo.AutoOpenPRInBrowser != nil {
			r.AutoOpenPRInBrowser = *repo.AutoOpenPRInBrowser
		}
		if repo.MergeMethod != nil {
			r.MergeMethod = *repo.MergeMethod
		}
		if repo.ValidationChecks != nil {
			r.ValidationChecks = repo.ValidationChecks
		}
	}

	// Normalize MergeMethod after both layers have written. GitHub accepts
	// only {"merge","squash","rebase"} and silently coercing in the API client
	// hides typos from the user — capital "Squash" would be turned into
	// "squash" without explanation. Lowercase + whitelist with fallback to
	// default keeps the resolved value safe and surfaces invalid input
	// later (doctor / merge command can compare against the default).
	r.MergeMethod = strings.ToLower(strings.TrimSpace(r.MergeMethod))
	switch r.MergeMethod {
	case "merge", "squash", "rebase":
	default:
		r.MergeMethod = DefaultMergeMethod
	}

	return r
}

// globalSettingsFile returns the path to ~/.refrain/config.json.
func globalSettingsFile() (string, error) {
	dir, err := RefrainDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// repoSettingsFile returns the path to <repoPath>/.refrain/config.json.
func repoSettingsFile(repoPath string) string {
	return filepath.Join(repoPath, ".refrain", "config.json")
}

// LoadGlobalSettings reads ~/.refrain/config.json.
// Returns an empty GlobalSettings (no error) if the file does not exist.
func LoadGlobalSettings() (*GlobalSettings, error) {
	path, err := globalSettingsFile()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &GlobalSettings{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: reading %s: %w", path, err)
	}

	var s GlobalSettings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("config: parsing %s: %w", path, err)
	}
	return &s, nil
}

// SaveGlobalSettings writes settings atomically to ~/.refrain/config.json.
func SaveGlobalSettings(s *GlobalSettings) error {
	path, err := globalSettingsFile()
	if err != nil {
		return err
	}
	return atomicWriteJSON(path, s)
}

// LoadRepoSettings reads <repoPath>/.refrain/config.json.
// Returns an empty RepoSettings (no error) if the file does not exist.
func LoadRepoSettings(repoPath string) (*RepoSettings, error) {
	path := repoSettingsFile(repoPath)

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &RepoSettings{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: reading %s: %w", path, err)
	}

	var s RepoSettings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("config: parsing %s: %w", path, err)
	}
	return &s, nil
}

// SaveRepoSettings writes settings atomically to <repoPath>/.refrain/config.json.
func SaveRepoSettings(repoPath string, s *RepoSettings) error {
	return atomicWriteJSON(repoSettingsFile(repoPath), s)
}

// MigrateBypassPermissions checks if repos.json still has the legacy
// BypassPermissions field and migrates it to GlobalSettings.
// This is a one-time migration; after it runs, BypassPermissions is cleared
// from repos.json.
func MigrateBypassPermissions(cfg *Config) error {
	if cfg.BypassPermissions == nil {
		return nil
	}

	global, err := LoadGlobalSettings()
	if err != nil {
		return err
	}

	// Only migrate if global settings don't already have it set.
	if global.BypassPermissions == nil {
		val := *cfg.BypassPermissions
		global.BypassPermissions = &val
		if err := SaveGlobalSettings(global); err != nil {
			return err
		}
	}

	cfg.BypassPermissions = nil
	return Save(cfg)
}
