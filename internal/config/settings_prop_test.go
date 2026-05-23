package config_test

import (
	"testing"

	"github.com/devenjarvis/refrain/internal/config"
	"pgregory.net/rapid"
)

// Resolve(nil, nil) always produces the documented defaults for every field.
func TestResolve_DefaultsProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		r := config.Resolve(nil, nil)

		if r.AudioEnabled != config.DefaultAudioEnabled {
			t.Fatalf("AudioEnabled = %v, want %v", r.AudioEnabled, config.DefaultAudioEnabled)
		}
		if r.BypassPermissions != config.DefaultBypassPermissions {
			t.Fatalf("BypassPermissions = %v, want %v", r.BypassPermissions, config.DefaultBypassPermissions)
		}
		if r.BranchPrefix != config.DefaultBranchPrefix {
			t.Fatalf("BranchPrefix = %q, want %q", r.BranchPrefix, config.DefaultBranchPrefix)
		}
		if r.AgentProgram != config.DefaultAgentProgram {
			t.Fatalf("AgentProgram = %q, want %q", r.AgentProgram, config.DefaultAgentProgram)
		}
		if r.WorktreeDir != config.DefaultWorktreeDir {
			t.Fatalf("WorktreeDir = %q, want %q", r.WorktreeDir, config.DefaultWorktreeDir)
		}
		if r.SidebarWidth != config.DefaultSidebarWidth {
			t.Fatalf("SidebarWidth = %d, want %d", r.SidebarWidth, config.DefaultSidebarWidth)
		}
		if r.MergeMethod != config.DefaultMergeMethod {
			t.Fatalf("MergeMethod = %q, want %q", r.MergeMethod, config.DefaultMergeMethod)
		}
		if r.FocusSessionMinutes != config.DefaultFocusSessionMinutes {
			t.Fatalf("FocusSessionMinutes = %d, want %d", r.FocusSessionMinutes, config.DefaultFocusSessionMinutes)
		}
		if r.FocusBreakMinutes != config.DefaultFocusBreakMinutes {
			t.Fatalf("FocusBreakMinutes = %d, want %d", r.FocusBreakMinutes, config.DefaultFocusBreakMinutes)
		}
		if r.MaxConcurrentAgents != config.DefaultMaxConcurrentAgents {
			t.Fatalf("MaxConcurrentAgents = %d, want %d", r.MaxConcurrentAgents, config.DefaultMaxConcurrentAgents)
		}
		if r.PlanFirstEnabled != config.DefaultPlanFirstEnabled {
			t.Fatalf("PlanFirstEnabled = %v, want %v", r.PlanFirstEnabled, config.DefaultPlanFirstEnabled)
		}
		if r.PRDraftByDefault != config.DefaultPRDraftByDefault {
			t.Fatalf("PRDraftByDefault = %v, want %v", r.PRDraftByDefault, config.DefaultPRDraftByDefault)
		}
		if r.AutoOpenPRInBrowser != config.DefaultAutoOpenPRInBrowser {
			t.Fatalf("AutoOpenPRInBrowser = %v, want %v", r.AutoOpenPRInBrowser, config.DefaultAutoOpenPRInBrowser)
		}
	})
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
		if repo.BranchPrefix != nil && r.BranchPrefix != *repo.BranchPrefix {
			t.Fatalf("BranchPrefix: repo=%q resolved=%q", *repo.BranchPrefix, r.BranchPrefix)
		}
		if repo.AgentProgram != nil && r.AgentProgram != *repo.AgentProgram {
			t.Fatalf("AgentProgram: repo=%q resolved=%q", *repo.AgentProgram, r.AgentProgram)
		}
		if repo.WorktreeDir != nil && r.WorktreeDir != *repo.WorktreeDir {
			t.Fatalf("WorktreeDir: repo=%q resolved=%q", *repo.WorktreeDir, r.WorktreeDir)
		}
		if repo.PlanFirstEnabled != nil && r.PlanFirstEnabled != *repo.PlanFirstEnabled {
			t.Fatalf("PlanFirstEnabled: repo=%v resolved=%v", *repo.PlanFirstEnabled, r.PlanFirstEnabled)
		}
		// MergeMethod is normalized, so only check when the raw value is already valid.
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
