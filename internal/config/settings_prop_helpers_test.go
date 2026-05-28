package config_test

import (
	"fmt"

	"github.com/devenjarvis/refrain/internal/config"
	"pgregory.net/rapid"
)

// genGlobalSettings generates a GlobalSettings with a random subset of pointer
// fields set (the rest remain nil, meaning "use default").
func genGlobalSettings(t *rapid.T) *config.GlobalSettings {
	g := &config.GlobalSettings{}
	if rapid.Bool().Draw(t, "g_audio") {
		v := rapid.Bool().Draw(t, "g_audio_val")
		g.AudioEnabled = &v
	}
	if rapid.Bool().Draw(t, "g_bypass") {
		v := rapid.Bool().Draw(t, "g_bypass_val")
		g.BypassPermissions = &v
	}
	if rapid.Bool().Draw(t, "g_default_branch") {
		v := rapid.String().Draw(t, "g_default_branch_val")
		g.DefaultBranch = &v
	}
	if rapid.Bool().Draw(t, "g_branch_prefix") {
		v := rapid.String().Draw(t, "g_branch_prefix_val")
		g.BranchPrefix = &v
	}
	if rapid.Bool().Draw(t, "g_branch_name_prompt") {
		v := rapid.String().Draw(t, "g_branch_name_prompt_val")
		g.BranchNamePrompt = &v
	}
	if rapid.Bool().Draw(t, "g_agent_program") {
		v := rapid.String().Draw(t, "g_agent_program_val")
		g.AgentProgram = &v
	}
	if rapid.Bool().Draw(t, "g_agent_model") {
		v := rapid.String().Draw(t, "g_agent_model_val")
		g.AgentModel = &v
	}
	if rapid.Bool().Draw(t, "g_plan_model") {
		v := rapid.String().Draw(t, "g_plan_model_val")
		g.PlanModel = &v
	}
	if rapid.Bool().Draw(t, "g_reviewer_model") {
		v := rapid.String().Draw(t, "g_reviewer_model_val")
		g.ReviewerModel = &v
	}
	if rapid.Bool().Draw(t, "g_ide_command") {
		v := rapid.String().Draw(t, "g_ide_command_val")
		g.IDECommand = &v
	}
	if rapid.Bool().Draw(t, "g_sidebar_width") {
		v := rapid.IntRange(-100, 200).Draw(t, "g_sidebar_width_val")
		g.SidebarWidth = &v
	}
	if rapid.Bool().Draw(t, "g_focus_session") {
		v := rapid.IntRange(-5, 200).Draw(t, "g_focus_session_val")
		g.FocusSessionMinutes = &v
	}
	if rapid.Bool().Draw(t, "g_focus_break") {
		v := rapid.IntRange(-5, 60).Draw(t, "g_focus_break_val")
		g.FocusBreakMinutes = &v
	}
	if rapid.Bool().Draw(t, "g_max_sessions") {
		v := rapid.IntRange(-5, 20).Draw(t, "g_max_sessions_val")
		g.MaxConcurrentSessions = &v
	}
	if rapid.Bool().Draw(t, "g_max_review") {
		v := rapid.IntRange(-5, 20).Draw(t, "g_max_review_val")
		g.MaxReviewBacklog = &v
	}
	if rapid.Bool().Draw(t, "g_plan_first") {
		v := rapid.Bool().Draw(t, "g_plan_first_val")
		g.PlanFirstEnabled = &v
	}
	if rapid.Bool().Draw(t, "g_build_prompt") {
		v := rapid.String().Draw(t, "g_build_prompt_val")
		g.BuildFromPlanPrompt = &v
	}
	if rapid.Bool().Draw(t, "g_build_sys_prompt") {
		v := rapid.String().Draw(t, "g_build_sys_prompt_val")
		g.BuildSystemPrompt = &v
	}
	if rapid.Bool().Draw(t, "g_pr_draft") {
		v := rapid.Bool().Draw(t, "g_pr_draft_val")
		g.PRDraftByDefault = &v
	}
	if rapid.Bool().Draw(t, "g_auto_open") {
		v := rapid.Bool().Draw(t, "g_auto_open_val")
		g.AutoOpenPRInBrowser = &v
	}
	if rapid.Bool().Draw(t, "g_merge_method") {
		v := genMergeMethod(t)
		g.MergeMethod = &v
	}
	return g
}

// genRepoSettings generates a RepoSettings with a random subset of all pointer
// fields set, covering every field that Resolve handles for repo overrides.
func genRepoSettings(t *rapid.T) *config.RepoSettings {
	r := &config.RepoSettings{}
	if rapid.Bool().Draw(t, "r_bypass") {
		v := rapid.Bool().Draw(t, "r_bypass_val")
		r.BypassPermissions = &v
	}
	if rapid.Bool().Draw(t, "r_default_branch") {
		v := rapid.String().Draw(t, "r_default_branch_val")
		r.DefaultBranch = &v
	}
	if rapid.Bool().Draw(t, "r_branch_prefix") {
		v := rapid.String().Draw(t, "r_branch_prefix_val")
		r.BranchPrefix = &v
	}
	if rapid.Bool().Draw(t, "r_branch_name_prompt") {
		v := rapid.String().Draw(t, "r_branch_name_prompt_val")
		r.BranchNamePrompt = &v
	}
	if rapid.Bool().Draw(t, "r_agent_program") {
		v := rapid.String().Draw(t, "r_agent_program_val")
		r.AgentProgram = &v
	}
	if rapid.Bool().Draw(t, "r_agent_model") {
		v := rapid.String().Draw(t, "r_agent_model_val")
		r.AgentModel = &v
	}
	if rapid.Bool().Draw(t, "r_plan_model") {
		v := rapid.String().Draw(t, "r_plan_model_val")
		r.PlanModel = &v
	}
	if rapid.Bool().Draw(t, "r_reviewer_model") {
		v := rapid.String().Draw(t, "r_reviewer_model_val")
		r.ReviewerModel = &v
	}
	if rapid.Bool().Draw(t, "r_ide_command") {
		v := rapid.String().Draw(t, "r_ide_command_val")
		r.IDECommand = &v
	}
	if rapid.Bool().Draw(t, "r_worktree_dir") {
		v := rapid.String().Draw(t, "r_worktree_dir_val")
		r.WorktreeDir = &v
	}
	if rapid.Bool().Draw(t, "r_plan_first") {
		v := rapid.Bool().Draw(t, "r_plan_first_val")
		r.PlanFirstEnabled = &v
	}
	if rapid.Bool().Draw(t, "r_build_prompt") {
		v := rapid.String().Draw(t, "r_build_prompt_val")
		r.BuildFromPlanPrompt = &v
	}
	if rapid.Bool().Draw(t, "r_build_sys_prompt") {
		v := rapid.String().Draw(t, "r_build_sys_prompt_val")
		r.BuildSystemPrompt = &v
	}
	if rapid.Bool().Draw(t, "r_pr_draft") {
		v := rapid.Bool().Draw(t, "r_pr_draft_val")
		r.PRDraftByDefault = &v
	}
	if rapid.Bool().Draw(t, "r_auto_open") {
		v := rapid.Bool().Draw(t, "r_auto_open_val")
		r.AutoOpenPRInBrowser = &v
	}
	if rapid.Bool().Draw(t, "r_merge_method") {
		v := genMergeMethod(t)
		r.MergeMethod = &v
	}
	if rapid.Bool().Draw(t, "r_validation_checks") {
		n := rapid.IntRange(0, 4).Draw(t, "r_validation_checks_n")
		checks := make([]config.ValidationCheck, n)
		for i := range checks {
			checks[i] = config.ValidationCheck{
				Name:    rapid.String().Draw(t, fmt.Sprintf("r_check_name_%d", i)),
				Command: rapid.String().Draw(t, fmt.Sprintf("r_check_cmd_%d", i)),
			}
		}
		r.ValidationChecks = checks
	}
	return r
}

// genMergeMethod produces strings exercising both valid merge methods and
// invalid/garbage inputs: valid lowercase, valid mixed-case, whitespace-padded,
// and completely unrelated strings.
func genMergeMethod(t *rapid.T) string {
	kind := rapid.IntRange(0, 4).Draw(t, "merge_method_kind")
	switch kind {
	case 0:
		return rapid.SampledFrom([]string{"merge", "squash", "rebase"}).Draw(t, "valid_method")
	case 1:
		return rapid.SampledFrom([]string{"Merge", "SQUASH", "Rebase", "MERGE"}).Draw(t, "mixed_case")
	case 2:
		return rapid.SampledFrom([]string{" merge", "squash ", " rebase "}).Draw(t, "padded")
	case 3:
		return rapid.String().Draw(t, "garbage")
	default:
		return fmt.Sprintf("invalid-%d", rapid.IntRange(100, 999).Draw(t, "invalid_num"))
	}
}
