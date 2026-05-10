package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Default values for all settings.
const (
	DefaultAudioEnabled      = true
	DefaultBypassPermissions = true
	DefaultBranchPrefix      = "baton/"
	DefaultAgentProgram      = "claude"
	DefaultWorktreeDir       = ".baton/worktrees"
	DefaultSidebarWidth      = 30

	// DefaultPlanModel is the model used by the plan drafter subprocess.
	// Sonnet (not Haiku) is intentional: planning quality compounds
	// downstream — see the comment on agent.DefaultPlanDrafter for details.
	// The agent package references this constant rather than declaring its
	// own so the default stays a single source of truth.
	DefaultPlanModel = "claude-sonnet-4-6"
	MinSidebarWidth  = 20
	MaxSidebarWidth  = 60

	// Wellness defaults.
	DefaultFocusSessionMinutes = 90
	DefaultFocusBreakMinutes   = 15
	DefaultMaxConcurrentAgents = 3
	DefaultMaxReviewBacklog    = 5

	// DefaultBranchNamePrompt is the instruction sent to Haiku to summarize
	// the user's first prompt into a branch slug. Users can override via the
	// branch_name_prompt key in global or per-repo config; the {prompt} token
	// is substituted with the user's prompt before the call.
	DefaultBranchNamePrompt = "Summarize this task into a 3-5 word git branch slug (lowercase, kebab-case, no prefix). Respond with ONLY the slug.\n\n{prompt}"

	// DefaultPlanFirstEnabled gates the plan-first planning flow. When true
	// (the default) the n keybind opens a prompt modal, drafts a plan via
	// claude -p, and only spawns the agent after the user approves the plan.
	// Users who prefer today's behaviour (immediate spawn) can set
	// "plan_first_enabled": false in global or per-repo config, or use
	// ctrl+enter on the modal to skip the planning step on a per-session basis.
	DefaultPlanFirstEnabled = true

	// DefaultBuildFromPlanPrompt is the initial prompt baton sends to the
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

If a file at .claude/plan.md exists in the worktree, the commit subject MUST start with "[task N] " where N is the 1-based index of the corresponding "- [ ]" line in that file (counting only task list lines, top-to-bottom, ignoring section headers). If no .claude/plan.md exists, prefix the subject with "task: " instead. The rest of the subject is a normal short description. Example: "[task 3] add --append-system-prompt flag plumbing".

Do not squash unrelated work into a single commit, and do not amend a previous commit to add new task work — each task is its own commit so the work can be reviewed task-by-task.`
)

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

// GlobalSettings holds user-wide settings stored at ~/.baton/config.json.
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
	FocusSessionMinutes *int `json:"focus_session_minutes,omitempty"`
	FocusBreakMinutes   *int `json:"focus_break_minutes,omitempty"`
	MaxConcurrentAgents *int `json:"max_concurrent_agents,omitempty"`
	MaxReviewBacklog    *int `json:"max_review_backlog,omitempty"`

	// Plan-first planning. Both fields are also overridable per-repo.
	PlanFirstEnabled    *bool   `json:"plan_first_enabled,omitempty"`
	BuildFromPlanPrompt *string `json:"build_from_plan_prompt,omitempty"`
	BuildSystemPrompt   *string `json:"build_system_prompt,omitempty"`
}

// RepoSettings holds per-repo overrides stored at <repo>/.baton/config.json.
// Fields here override the corresponding GlobalSettings value.
type RepoSettings struct {
	BypassPermissions   *bool   `json:"bypass_permissions,omitempty"`
	DefaultBranch       *string `json:"default_branch,omitempty"`
	BranchPrefix        *string `json:"branch_prefix,omitempty"`
	BranchNamePrompt    *string `json:"branch_name_prompt,omitempty"`
	AgentProgram        *string `json:"agent_program,omitempty"`
	AgentModel          *string `json:"agent_model,omitempty"`
	PlanModel           *string `json:"plan_model,omitempty"`
	IDECommand          *string `json:"ide_command,omitempty"`
	WorktreeDir         *string `json:"worktree_dir,omitempty"`
	PlanFirstEnabled    *bool   `json:"plan_first_enabled,omitempty"`
	BuildFromPlanPrompt *string `json:"build_from_plan_prompt,omitempty"`
	BuildSystemPrompt   *string `json:"build_system_prompt,omitempty"`
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
	PlanModel    string
	IDECommand   string
	WorktreeDir  string
	SidebarWidth int

	// Wellness settings.
	FocusSessionMinutes int
	FocusBreakMinutes   int
	MaxConcurrentAgents int
	MaxReviewBacklog    int

	// Plan-first planning.
	PlanFirstEnabled    bool
	BuildFromPlanPrompt string
	// BuildSystemPrompt is forwarded as `--append-system-prompt <text>` to
	// the spawned `claude` agent at every Building-phase spawn site (and
	// resume). Empty means no flag.
	BuildSystemPrompt string
}

// Resolve merges global and repo settings over built-in defaults.
// Global overrides defaults; repo overrides global.
func Resolve(global *GlobalSettings, repo *RepoSettings) ResolvedSettings {
	r := ResolvedSettings{
		AudioEnabled:        DefaultAudioEnabled,
		BypassPermissions:   DefaultBypassPermissions,
		BranchPrefix:        DefaultBranchPrefix,
		BranchNamePrompt:    DefaultBranchNamePrompt,
		AgentProgram:        DefaultAgentProgram,
		PlanModel:           DefaultPlanModel,
		WorktreeDir:         DefaultWorktreeDir,
		SidebarWidth:        DefaultSidebarWidth,
		FocusSessionMinutes: DefaultFocusSessionMinutes,
		FocusBreakMinutes:   DefaultFocusBreakMinutes,
		MaxConcurrentAgents: DefaultMaxConcurrentAgents,
		MaxReviewBacklog:    DefaultMaxReviewBacklog,
		PlanFirstEnabled:    DefaultPlanFirstEnabled,
		BuildFromPlanPrompt: DefaultBuildFromPlanPrompt,
		BuildSystemPrompt:   DefaultBuildSystemPrompt,
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
		if global.MaxConcurrentAgents != nil {
			r.MaxConcurrentAgents = *global.MaxConcurrentAgents
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
	}

	return r
}

// globalSettingsFile returns the path to ~/.baton/config.json.
func globalSettingsFile() (string, error) {
	dir, err := BatonDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// repoSettingsFile returns the path to <repoPath>/.baton/config.json.
func repoSettingsFile(repoPath string) string {
	return filepath.Join(repoPath, ".baton", "config.json")
}

// LoadGlobalSettings reads ~/.baton/config.json.
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

// SaveGlobalSettings writes settings atomically to ~/.baton/config.json.
func SaveGlobalSettings(s *GlobalSettings) error {
	path, err := globalSettingsFile()
	if err != nil {
		return err
	}
	return atomicWriteJSON(path, s)
}

// LoadRepoSettings reads <repoPath>/.baton/config.json.
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

// SaveRepoSettings writes settings atomically to <repoPath>/.baton/config.json.
func SaveRepoSettings(repoPath string, s *RepoSettings) error {
	return atomicWriteJSON(repoSettingsFile(repoPath), s)
}

// LoadResolved is a convenience that loads both global and repo settings
// and returns the merged result.
func LoadResolved(repoPath string) (ResolvedSettings, error) {
	global, err := LoadGlobalSettings()
	if err != nil {
		return ResolvedSettings{}, fmt.Errorf("loading global settings: %w", err)
	}
	repo, err := LoadRepoSettings(repoPath)
	if err != nil {
		return ResolvedSettings{}, fmt.Errorf("loading repo settings for %s: %w", repoPath, err)
	}
	return Resolve(global, repo), nil
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
