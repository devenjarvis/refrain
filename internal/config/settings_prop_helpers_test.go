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
	if rapid.Bool().Draw(t, "g_branch_prefix") {
		v := rapid.String().Draw(t, "g_branch_prefix_val")
		g.BranchPrefix = &v
	}
	if rapid.Bool().Draw(t, "g_agent_program") {
		v := rapid.String().Draw(t, "g_agent_program_val")
		g.AgentProgram = &v
	}
	if rapid.Bool().Draw(t, "g_sidebar_width") {
		v := rapid.IntRange(-100, 200).Draw(t, "g_sidebar_width_val")
		g.SidebarWidth = &v
	}
	if rapid.Bool().Draw(t, "g_merge_method") {
		v := genMergeMethod(t)
		g.MergeMethod = &v
	}
	if rapid.Bool().Draw(t, "g_plan_first") {
		v := rapid.Bool().Draw(t, "g_plan_first_val")
		g.PlanFirstEnabled = &v
	}
	if rapid.Bool().Draw(t, "g_max_agents") {
		v := rapid.IntRange(-5, 20).Draw(t, "g_max_agents_val")
		g.MaxConcurrentAgents = &v
	}
	return g
}

// genRepoSettings generates a RepoSettings with a random subset of pointer
// fields set.
func genRepoSettings(t *rapid.T) *config.RepoSettings {
	r := &config.RepoSettings{}
	if rapid.Bool().Draw(t, "r_bypass") {
		v := rapid.Bool().Draw(t, "r_bypass_val")
		r.BypassPermissions = &v
	}
	if rapid.Bool().Draw(t, "r_branch_prefix") {
		v := rapid.String().Draw(t, "r_branch_prefix_val")
		r.BranchPrefix = &v
	}
	if rapid.Bool().Draw(t, "r_agent_program") {
		v := rapid.String().Draw(t, "r_agent_program_val")
		r.AgentProgram = &v
	}
	if rapid.Bool().Draw(t, "r_worktree_dir") {
		v := rapid.String().Draw(t, "r_worktree_dir_val")
		r.WorktreeDir = &v
	}
	if rapid.Bool().Draw(t, "r_merge_method") {
		v := genMergeMethod(t)
		r.MergeMethod = &v
	}
	if rapid.Bool().Draw(t, "r_plan_first") {
		v := rapid.Bool().Draw(t, "r_plan_first_val")
		r.PlanFirstEnabled = &v
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
		// Valid method with mixed case.
		m := rapid.SampledFrom([]string{"Merge", "SQUASH", "Rebase", "MERGE", "squash"}).Draw(t, "mixed_case")
		return m
	case 2:
		// Whitespace-padded valid method.
		m := rapid.SampledFrom([]string{" merge", "squash ", " rebase "}).Draw(t, "padded")
		return m
	case 3:
		// Completely garbage string.
		return rapid.String().Draw(t, "garbage")
	default:
		return fmt.Sprintf("invalid-%d", rapid.IntRange(100, 999).Draw(t, "invalid_num"))
	}
}
