package config_test

import (
	"testing"

	"github.com/devenjarvis/refrain/internal/config"
	"pgregory.net/rapid"
)

// Resolve(nil, nil) produces the documented defaults for every field.
func TestResolve_DefaultsProperty(t *testing.T) {
	r := config.Resolve(nil, nil)

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"AudioEnabled", r.AudioEnabled, config.DefaultAudioEnabled},
		{"BypassPermissions", r.BypassPermissions, config.DefaultBypassPermissions},
		{"BranchPrefix", r.BranchPrefix, config.DefaultBranchPrefix},
		{"AgentProgram", r.AgentProgram, config.DefaultAgentProgram},
		{"WorktreeDir", r.WorktreeDir, config.DefaultWorktreeDir},
		{"SidebarWidth", r.SidebarWidth, config.DefaultSidebarWidth},
		{"MergeMethod", r.MergeMethod, config.DefaultMergeMethod},
		{"FocusSessionMinutes", r.FocusSessionMinutes, config.DefaultFocusSessionMinutes},
		{"FocusBreakMinutes", r.FocusBreakMinutes, config.DefaultFocusBreakMinutes},
		{"MaxConcurrentSessions", r.MaxConcurrentSessions, config.DefaultMaxConcurrentSessions},
		{"MaxReviewBacklog", r.MaxReviewBacklog, config.DefaultMaxReviewBacklog},
		{"PlanFirstEnabled", r.PlanFirstEnabled, config.DefaultPlanFirstEnabled},
		{"PRDraftByDefault", r.PRDraftByDefault, config.DefaultPRDraftByDefault},
		{"AutoOpenPRInBrowser", r.AutoOpenPRInBrowser, config.DefaultAutoOpenPRInBrowser},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// For any non-nil RepoSettings field, that field's value appears in the resolved output.
func TestResolve_RepoOverridesGlobal_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		global := genGlobalSettings(t)
		repo := genRepoSettings(t)
		r := config.Resolve(global, repo)

		if repo.BypassPermissions != nil && r.BypassPermissions != *repo.BypassPermissions {
			t.Fatalf("BypassPermissions: repo=%v resolved=%v", *repo.BypassPermissions, r.BypassPermissions)
		}
		if repo.DefaultBranch != nil && r.DefaultBranch != *repo.DefaultBranch {
			t.Fatalf("DefaultBranch: repo=%q resolved=%q", *repo.DefaultBranch, r.DefaultBranch)
		}
		if repo.BranchPrefix != nil && r.BranchPrefix != *repo.BranchPrefix {
			t.Fatalf("BranchPrefix: repo=%q resolved=%q", *repo.BranchPrefix, r.BranchPrefix)
		}
		if repo.BranchNamePrompt != nil && r.BranchNamePrompt != *repo.BranchNamePrompt {
			t.Fatalf("BranchNamePrompt: repo=%q resolved=%q", *repo.BranchNamePrompt, r.BranchNamePrompt)
		}
		if repo.AgentProgram != nil && r.AgentProgram != *repo.AgentProgram {
			t.Fatalf("AgentProgram: repo=%q resolved=%q", *repo.AgentProgram, r.AgentProgram)
		}
		if repo.AgentModel != nil && r.AgentModel != *repo.AgentModel {
			t.Fatalf("AgentModel: repo=%q resolved=%q", *repo.AgentModel, r.AgentModel)
		}
		if repo.PlanModel != nil && r.PlanModel != *repo.PlanModel {
			t.Fatalf("PlanModel: repo=%q resolved=%q", *repo.PlanModel, r.PlanModel)
		}
		if repo.ReviewerModel != nil && r.ReviewerModel != *repo.ReviewerModel {
			t.Fatalf("ReviewerModel: repo=%q resolved=%q", *repo.ReviewerModel, r.ReviewerModel)
		}
		if repo.IDECommand != nil && r.IDECommand != *repo.IDECommand {
			t.Fatalf("IDECommand: repo=%q resolved=%q", *repo.IDECommand, r.IDECommand)
		}
		if repo.WorktreeDir != nil && r.WorktreeDir != *repo.WorktreeDir {
			t.Fatalf("WorktreeDir: repo=%q resolved=%q", *repo.WorktreeDir, r.WorktreeDir)
		}
		if repo.PlanFirstEnabled != nil && r.PlanFirstEnabled != *repo.PlanFirstEnabled {
			t.Fatalf("PlanFirstEnabled: repo=%v resolved=%v", *repo.PlanFirstEnabled, r.PlanFirstEnabled)
		}
		if repo.BuildFromPlanPrompt != nil && r.BuildFromPlanPrompt != *repo.BuildFromPlanPrompt {
			t.Fatalf("BuildFromPlanPrompt: repo=%q resolved=%q", *repo.BuildFromPlanPrompt, r.BuildFromPlanPrompt)
		}
		if repo.BuildSystemPrompt != nil && r.BuildSystemPrompt != *repo.BuildSystemPrompt {
			t.Fatalf("BuildSystemPrompt: repo=%q resolved=%q", *repo.BuildSystemPrompt, r.BuildSystemPrompt)
		}
		if repo.PRDraftByDefault != nil && r.PRDraftByDefault != *repo.PRDraftByDefault {
			t.Fatalf("PRDraftByDefault: repo=%v resolved=%v", *repo.PRDraftByDefault, r.PRDraftByDefault)
		}
		if repo.AutoOpenPRInBrowser != nil && r.AutoOpenPRInBrowser != *repo.AutoOpenPRInBrowser {
			t.Fatalf("AutoOpenPRInBrowser: repo=%v resolved=%v", *repo.AutoOpenPRInBrowser, r.AutoOpenPRInBrowser)
		}
		// MergeMethod is normalized; verify through Resolve itself rather than raw string comparison.
		if repo.MergeMethod != nil {
			want := config.Resolve(nil, &config.RepoSettings{MergeMethod: repo.MergeMethod}).MergeMethod
			if r.MergeMethod != want {
				t.Fatalf("MergeMethod: repo=%q resolved=%q want=%q", *repo.MergeMethod, r.MergeMethod, want)
			}
		}
	})
}

// MergeMethod is always one of {"merge", "squash", "rebase"} regardless of input.
func TestResolve_MergeMethodAlwaysValid(t *testing.T) {
	valid := map[string]bool{"merge": true, "squash": true, "rebase": true}

	rapid.Check(t, func(t *rapid.T) {
		g := &config.GlobalSettings{}
		if rapid.Bool().Draw(t, "set_global") {
			v := genMergeMethod(t)
			g.MergeMethod = &v
		}
		r := &config.RepoSettings{}
		if rapid.Bool().Draw(t, "set_repo") {
			v := genMergeMethod(t)
			r.MergeMethod = &v
		}
		resolved := config.Resolve(g, r)
		if !valid[resolved.MergeMethod] {
			t.Fatalf("MergeMethod = %q, want one of {merge, squash, rebase}", resolved.MergeMethod)
		}
	})
}

// SidebarWidth is always clamped to [MinSidebarWidth, MaxSidebarWidth] regardless of input.
func TestResolve_SidebarWidthClamped(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		v := rapid.IntRange(-1000, 1000).Draw(t, "sidebar_width")
		g := &config.GlobalSettings{SidebarWidth: &v}
		resolved := config.Resolve(g, nil)
		if resolved.SidebarWidth < config.MinSidebarWidth || resolved.SidebarWidth > config.MaxSidebarWidth {
			t.Fatalf("SidebarWidth = %d (input %d), want [%d, %d]",
				resolved.SidebarWidth, v, config.MinSidebarWidth, config.MaxSidebarWidth)
		}
	})
}
