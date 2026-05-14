package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/devenjarvis/baton/internal/config"
)

func boolPtr(v bool) *bool    { return &v }
func strPtr(v string) *string { return &v }
func intPtr(v int) *int       { return &v }

// ---- Resolve ----

func TestResolve_AllDefaults(t *testing.T) {
	r := config.Resolve(nil, nil)

	if r.AudioEnabled != config.DefaultAudioEnabled {
		t.Errorf("AudioEnabled = %v, want %v", r.AudioEnabled, config.DefaultAudioEnabled)
	}
	if r.BypassPermissions != config.DefaultBypassPermissions {
		t.Errorf("BypassPermissions = %v, want %v", r.BypassPermissions, config.DefaultBypassPermissions)
	}
	if r.DefaultBranch != "" {
		t.Errorf("DefaultBranch = %q, want empty", r.DefaultBranch)
	}
	if r.BranchPrefix != config.DefaultBranchPrefix {
		t.Errorf("BranchPrefix = %q, want %q", r.BranchPrefix, config.DefaultBranchPrefix)
	}
	if r.BranchNamePrompt != config.DefaultBranchNamePrompt {
		t.Errorf("BranchNamePrompt = %q, want default", r.BranchNamePrompt)
	}
	if r.AgentProgram != config.DefaultAgentProgram {
		t.Errorf("AgentProgram = %q, want %q", r.AgentProgram, config.DefaultAgentProgram)
	}
	if r.WorktreeDir != config.DefaultWorktreeDir {
		t.Errorf("WorktreeDir = %q, want %q", r.WorktreeDir, config.DefaultWorktreeDir)
	}
}

func TestResolve_EmptyStructs(t *testing.T) {
	r := config.Resolve(&config.GlobalSettings{}, &config.RepoSettings{})

	if r.AudioEnabled != config.DefaultAudioEnabled {
		t.Errorf("AudioEnabled = %v, want %v", r.AudioEnabled, config.DefaultAudioEnabled)
	}
	if r.AgentProgram != config.DefaultAgentProgram {
		t.Errorf("AgentProgram = %q, want %q", r.AgentProgram, config.DefaultAgentProgram)
	}
}

func TestResolve_GlobalOverridesDefaults(t *testing.T) {
	g := &config.GlobalSettings{
		AudioEnabled:      boolPtr(false),
		BypassPermissions: boolPtr(false),
		BranchPrefix:      strPtr("custom/"),
		AgentProgram:      strPtr("my-claude"),
		DefaultBranch:     strPtr("develop"),
	}
	r := config.Resolve(g, nil)

	if r.AudioEnabled {
		t.Error("AudioEnabled should be false")
	}
	if r.BypassPermissions {
		t.Error("BypassPermissions should be false")
	}
	if r.BranchPrefix != "custom/" {
		t.Errorf("BranchPrefix = %q, want custom/", r.BranchPrefix)
	}
	if r.AgentProgram != "my-claude" {
		t.Errorf("AgentProgram = %q, want my-claude", r.AgentProgram)
	}
	if r.DefaultBranch != "develop" {
		t.Errorf("DefaultBranch = %q, want develop", r.DefaultBranch)
	}
	// WorktreeDir should still be default (global doesn't have it)
	if r.WorktreeDir != config.DefaultWorktreeDir {
		t.Errorf("WorktreeDir = %q, want %q", r.WorktreeDir, config.DefaultWorktreeDir)
	}
}

func TestResolve_RepoOverridesGlobal(t *testing.T) {
	g := &config.GlobalSettings{
		BypassPermissions: boolPtr(true),
		BranchPrefix:      strPtr("global/"),
		AgentProgram:      strPtr("global-claude"),
	}
	repo := &config.RepoSettings{
		BypassPermissions: boolPtr(false),
		BranchPrefix:      strPtr("repo/"),
		WorktreeDir:       strPtr("custom/worktrees"),
	}
	r := config.Resolve(g, repo)

	if r.BypassPermissions {
		t.Error("BypassPermissions should be false (repo override)")
	}
	if r.BranchPrefix != "repo/" {
		t.Errorf("BranchPrefix = %q, want repo/", r.BranchPrefix)
	}
	// AgentProgram not overridden in repo, should use global value
	if r.AgentProgram != "global-claude" {
		t.Errorf("AgentProgram = %q, want global-claude", r.AgentProgram)
	}
	if r.WorktreeDir != "custom/worktrees" {
		t.Errorf("WorktreeDir = %q, want custom/worktrees", r.WorktreeDir)
	}
}

func TestResolve_NilVsFalse(t *testing.T) {
	// nil BypassPermissions -> default (true)
	r1 := config.Resolve(&config.GlobalSettings{}, nil)
	if !r1.BypassPermissions {
		t.Error("nil BypassPermissions should default to true")
	}

	// Explicitly set to false -> false
	r2 := config.Resolve(&config.GlobalSettings{BypassPermissions: boolPtr(false)}, nil)
	if r2.BypassPermissions {
		t.Error("false BypassPermissions should be false")
	}
}

func TestResolve_BranchNamePrompt_GlobalOverride(t *testing.T) {
	custom := "my custom prompt {prompt}"
	r := config.Resolve(&config.GlobalSettings{BranchNamePrompt: strPtr(custom)}, nil)
	if r.BranchNamePrompt != custom {
		t.Errorf("BranchNamePrompt = %q, want %q", r.BranchNamePrompt, custom)
	}
}

func TestResolve_BranchNamePrompt_RepoOverridesGlobal(t *testing.T) {
	g := &config.GlobalSettings{BranchNamePrompt: strPtr("from-global {prompt}")}
	repo := &config.RepoSettings{BranchNamePrompt: strPtr("from-repo {prompt}")}
	r := config.Resolve(g, repo)
	if r.BranchNamePrompt != "from-repo {prompt}" {
		t.Errorf("BranchNamePrompt = %q, want repo override", r.BranchNamePrompt)
	}
}

// ---- Load/Save GlobalSettings ----

func TestLoadGlobalSettings_MissingFile(t *testing.T) {
	configDirInTmp(t)

	s, err := config.LoadGlobalSettings()
	if err != nil {
		t.Fatalf("LoadGlobalSettings() error = %v", err)
	}
	if s == nil {
		t.Fatal("LoadGlobalSettings() returned nil")
	}
	// All fields should be nil (unset)
	if s.AudioEnabled != nil {
		t.Error("AudioEnabled should be nil")
	}
}

func TestSaveLoadRoundTrip_Global(t *testing.T) {
	configDirInTmp(t)

	original := &config.GlobalSettings{
		AudioEnabled:      boolPtr(false),
		BypassPermissions: boolPtr(true),
		BranchPrefix:      strPtr("test/"),
		BranchNamePrompt:  strPtr("custom {prompt}"),
		AgentProgram:      strPtr("test-claude"),
		DefaultBranch:     strPtr("develop"),
	}

	if err := config.SaveGlobalSettings(original); err != nil {
		t.Fatalf("SaveGlobalSettings() error = %v", err)
	}

	loaded, err := config.LoadGlobalSettings()
	if err != nil {
		t.Fatalf("LoadGlobalSettings() error = %v", err)
	}

	if loaded.AudioEnabled == nil || *loaded.AudioEnabled != false {
		t.Error("AudioEnabled should be false after round-trip")
	}
	if loaded.BypassPermissions == nil || *loaded.BypassPermissions != true {
		t.Error("BypassPermissions should be true after round-trip")
	}
	if loaded.BranchPrefix == nil || *loaded.BranchPrefix != "test/" {
		t.Errorf("BranchPrefix = %v, want test/", loaded.BranchPrefix)
	}
	if loaded.BranchNamePrompt == nil || *loaded.BranchNamePrompt != "custom {prompt}" {
		t.Errorf("BranchNamePrompt = %v, want custom {prompt}", loaded.BranchNamePrompt)
	}
	if loaded.AgentProgram == nil || *loaded.AgentProgram != "test-claude" {
		t.Errorf("AgentProgram = %v, want test-claude", loaded.AgentProgram)
	}
	if loaded.DefaultBranch == nil || *loaded.DefaultBranch != "develop" {
		t.Errorf("DefaultBranch = %v, want develop", loaded.DefaultBranch)
	}
}

// ---- Load/Save RepoSettings ----

func TestLoadRepoSettings_MissingFile(t *testing.T) {
	repoPath := t.TempDir()

	s, err := config.LoadRepoSettings(repoPath)
	if err != nil {
		t.Fatalf("LoadRepoSettings() error = %v", err)
	}
	if s == nil {
		t.Fatal("LoadRepoSettings() returned nil")
	}
	if s.BypassPermissions != nil {
		t.Error("BypassPermissions should be nil")
	}
}

func TestSaveLoadRoundTrip_Repo(t *testing.T) {
	repoPath := t.TempDir()
	// Create .baton dir
	_ = os.MkdirAll(filepath.Join(repoPath, ".baton"), 0o755)

	original := &config.RepoSettings{
		BypassPermissions: boolPtr(false),
		BranchPrefix:      strPtr("repo/"),
		WorktreeDir:       strPtr("custom/wt"),
		AgentProgram:      strPtr("my-agent"),
		DefaultBranch:     strPtr("trunk"),
	}

	if err := config.SaveRepoSettings(repoPath, original); err != nil {
		t.Fatalf("SaveRepoSettings() error = %v", err)
	}

	loaded, err := config.LoadRepoSettings(repoPath)
	if err != nil {
		t.Fatalf("LoadRepoSettings() error = %v", err)
	}

	if loaded.BypassPermissions == nil || *loaded.BypassPermissions != false {
		t.Error("BypassPermissions should be false")
	}
	if loaded.BranchPrefix == nil || *loaded.BranchPrefix != "repo/" {
		t.Errorf("BranchPrefix = %v, want repo/", loaded.BranchPrefix)
	}
	if loaded.WorktreeDir == nil || *loaded.WorktreeDir != "custom/wt" {
		t.Errorf("WorktreeDir = %v, want custom/wt", loaded.WorktreeDir)
	}
	if loaded.AgentProgram == nil || *loaded.AgentProgram != "my-agent" {
		t.Errorf("AgentProgram = %v, want my-agent", loaded.AgentProgram)
	}
	if loaded.DefaultBranch == nil || *loaded.DefaultBranch != "trunk" {
		t.Errorf("DefaultBranch = %v, want trunk", loaded.DefaultBranch)
	}
}

// ---- LoadResolved ----

func TestLoadResolved_MergesGlobalAndRepo(t *testing.T) {
	dir := configDirInTmp(t)
	repoPath := t.TempDir()

	// Write global settings
	_ = os.MkdirAll(dir, 0o755)
	globalData, _ := json.Marshal(config.GlobalSettings{
		AudioEnabled: boolPtr(false),
		BranchPrefix: strPtr("global/"),
	})
	_ = os.WriteFile(filepath.Join(dir, "config.json"), globalData, 0o644)

	// Write repo settings
	_ = os.MkdirAll(filepath.Join(repoPath, ".baton"), 0o755)
	repoData, _ := json.Marshal(config.RepoSettings{
		BranchPrefix: strPtr("repo/"),
	})
	_ = os.WriteFile(filepath.Join(repoPath, ".baton", "config.json"), repoData, 0o644)

	r, err := config.LoadResolved(repoPath)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}

	if r.AudioEnabled {
		t.Error("AudioEnabled should be false (from global)")
	}
	if r.BranchPrefix != "repo/" {
		t.Errorf("BranchPrefix = %q, want repo/ (repo overrides global)", r.BranchPrefix)
	}
}

// ---- JSON omitempty behavior ----

func TestGlobalSettings_OmitsNilFields(t *testing.T) {
	s := &config.GlobalSettings{
		AudioEnabled: boolPtr(false),
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	_ = json.Unmarshal(data, &m)

	if _, ok := m["audio_enabled"]; !ok {
		t.Error("audio_enabled should be present")
	}
	if _, ok := m["bypass_permissions"]; ok {
		t.Error("bypass_permissions should be omitted when nil")
	}
	if _, ok := m["branch_prefix"]; ok {
		t.Error("branch_prefix should be omitted when nil")
	}
}

// ---- Migration ----

func TestMigrateBypassPermissions(t *testing.T) {
	dir := configDirInTmp(t)
	_ = os.MkdirAll(dir, 0o755)

	// Start with repos.json that has BypassPermissions set to false
	cfg := &config.Config{
		Repos:             []config.Repo{{Path: "/test", Name: "test"}},
		BypassPermissions: boolPtr(false),
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Run migration
	if err := config.MigrateBypassPermissions(cfg); err != nil {
		t.Fatalf("MigrateBypassPermissions() error = %v", err)
	}

	// cfg should have BypassPermissions cleared
	if cfg.BypassPermissions != nil {
		t.Error("BypassPermissions should be nil in Config after migration")
	}

	// Global settings should have it
	global, err := config.LoadGlobalSettings()
	if err != nil {
		t.Fatalf("LoadGlobalSettings() error = %v", err)
	}
	if global.BypassPermissions == nil || *global.BypassPermissions != false {
		t.Error("Global BypassPermissions should be false after migration")
	}
}

func TestMigrateBypassPermissions_NilIsNoop(t *testing.T) {
	configDirInTmp(t)

	cfg := &config.Config{
		Repos: []config.Repo{{Path: "/test", Name: "test"}},
	}

	// Should be a no-op when BypassPermissions is nil
	if err := config.MigrateBypassPermissions(cfg); err != nil {
		t.Fatalf("MigrateBypassPermissions() error = %v", err)
	}
}

func TestMigrateBypassPermissions_DoesNotOverwriteExistingGlobal(t *testing.T) {
	dir := configDirInTmp(t)
	_ = os.MkdirAll(dir, 0o755)

	// Pre-set global settings with BypassPermissions=true
	globalData, _ := json.Marshal(config.GlobalSettings{
		BypassPermissions: boolPtr(true),
	})
	_ = os.WriteFile(filepath.Join(dir, "config.json"), globalData, 0o644)

	// repos.json has BypassPermissions=false
	cfg := &config.Config{
		BypassPermissions: boolPtr(false),
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	if err := config.MigrateBypassPermissions(cfg); err != nil {
		t.Fatalf("MigrateBypassPermissions() error = %v", err)
	}

	// Global should still be true (not overwritten)
	global, err := config.LoadGlobalSettings()
	if err != nil {
		t.Fatal(err)
	}
	if global.BypassPermissions == nil || *global.BypassPermissions != true {
		t.Error("Global BypassPermissions should remain true (not overwritten)")
	}
}

// ---- Legacy migration ----

func TestLoad_MigratesFromLegacyXDGPath(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", base)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, ".config"))

	// Write repos.json to old XDG location
	oldDir := filepath.Join(base, ".config", "baton")
	_ = os.MkdirAll(oldDir, 0o755)
	data, _ := json.Marshal(config.Config{
		Repos: []config.Repo{{Path: "/legacy", Name: "legacy"}},
	})
	_ = os.WriteFile(filepath.Join(oldDir, "repos.json"), data, 0o644)

	// Load should find the legacy file and migrate
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Repos) != 1 || cfg.Repos[0].Path != "/legacy" {
		t.Errorf("Load() repos = %v, want one repo with path /legacy", cfg.Repos)
	}

	// New location should now exist
	newPath := filepath.Join(base, ".baton", "repos.json")
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("new path %s should exist after migration: %v", newPath, err)
	}

	// Old file should be removed
	oldPath := filepath.Join(oldDir, "repos.json")
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("old path %s should be removed after migration", oldPath)
	}
}

// ---- SidebarWidth ----

func TestResolve_SidebarWidth_Default(t *testing.T) {
	r := config.Resolve(nil, nil)
	if r.SidebarWidth != config.DefaultSidebarWidth {
		t.Errorf("SidebarWidth = %d, want %d", r.SidebarWidth, config.DefaultSidebarWidth)
	}
}

func TestResolve_SidebarWidth_GlobalOverride(t *testing.T) {
	r := config.Resolve(&config.GlobalSettings{SidebarWidth: intPtr(42)}, nil)
	if r.SidebarWidth != 42 {
		t.Errorf("SidebarWidth = %d, want 42", r.SidebarWidth)
	}
}

func TestResolve_SidebarWidth_ClampedOnLoad(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, config.MinSidebarWidth},
		{5, config.MinSidebarWidth},
		{config.MinSidebarWidth, config.MinSidebarWidth},
		{config.MaxSidebarWidth, config.MaxSidebarWidth},
		{999, config.MaxSidebarWidth},
	}
	for _, c := range cases {
		r := config.Resolve(&config.GlobalSettings{SidebarWidth: intPtr(c.in)}, nil)
		if r.SidebarWidth != c.want {
			t.Errorf("Resolve(SidebarWidth=%d).SidebarWidth = %d, want %d", c.in, r.SidebarWidth, c.want)
		}
	}
}

func TestClampSidebarWidth(t *testing.T) {
	if got := config.ClampSidebarWidth(10); got != config.MinSidebarWidth {
		t.Errorf("ClampSidebarWidth(10) = %d, want %d", got, config.MinSidebarWidth)
	}
	if got := config.ClampSidebarWidth(100); got != config.MaxSidebarWidth {
		t.Errorf("ClampSidebarWidth(100) = %d, want %d", got, config.MaxSidebarWidth)
	}
	if got := config.ClampSidebarWidth(35); got != 35 {
		t.Errorf("ClampSidebarWidth(35) = %d, want 35", got)
	}
}

// ---- Wellness defaults ----

func TestResolve_WellnessDefaults(t *testing.T) {
	r := config.Resolve(nil, nil)

	if r.FocusSessionMinutes != config.DefaultFocusSessionMinutes {
		t.Errorf("FocusSessionMinutes = %d, want %d", r.FocusSessionMinutes, config.DefaultFocusSessionMinutes)
	}
	if r.MaxConcurrentAgents != config.DefaultMaxConcurrentAgents {
		t.Errorf("MaxConcurrentAgents = %d, want %d", r.MaxConcurrentAgents, config.DefaultMaxConcurrentAgents)
	}
}

func TestResolve_WellnessGlobalOverride(t *testing.T) {
	minutes := 60
	maxAgents := 5
	g := &config.GlobalSettings{
		FocusSessionMinutes: &minutes,
		MaxConcurrentAgents: &maxAgents,
	}
	r := config.Resolve(g, nil)

	if r.FocusSessionMinutes != 60 {
		t.Errorf("FocusSessionMinutes = %d, want 60", r.FocusSessionMinutes)
	}
	if r.MaxConcurrentAgents != 5 {
		t.Errorf("MaxConcurrentAgents = %d, want 5", r.MaxConcurrentAgents)
	}
}

func TestResolve_WellnessNotInRepoSettings(t *testing.T) {
	// Wellness fields are global-only; repo settings should not override them.
	g := &config.GlobalSettings{
		FocusSessionMinutes: intPtr(45),
	}
	// RepoSettings has no wellness fields by design.
	r := config.Resolve(g, &config.RepoSettings{})
	if r.FocusSessionMinutes != 45 {
		t.Errorf("FocusSessionMinutes = %d, want 45 (from global)", r.FocusSessionMinutes)
	}
}

func TestMaxReviewBacklog_Default(t *testing.T) {
	resolved := config.Resolve(nil, nil)
	if resolved.MaxReviewBacklog != config.DefaultMaxReviewBacklog {
		t.Errorf("MaxReviewBacklog default = %d, want %d", resolved.MaxReviewBacklog, config.DefaultMaxReviewBacklog)
	}
}

func TestMaxReviewBacklog_GlobalOverride(t *testing.T) {
	two := 2
	global := &config.GlobalSettings{MaxReviewBacklog: &two}
	resolved := config.Resolve(global, nil)
	if resolved.MaxReviewBacklog != 2 {
		t.Errorf("MaxReviewBacklog = %d, want 2", resolved.MaxReviewBacklog)
	}
}

func TestResolve_PlanFirst_Defaults(t *testing.T) {
	r := config.Resolve(nil, nil)
	if r.PlanFirstEnabled != config.DefaultPlanFirstEnabled {
		t.Errorf("PlanFirstEnabled = %v, want %v", r.PlanFirstEnabled, config.DefaultPlanFirstEnabled)
	}
	if r.BuildFromPlanPrompt != config.DefaultBuildFromPlanPrompt {
		t.Errorf("BuildFromPlanPrompt = %q, want default", r.BuildFromPlanPrompt)
	}
}

func TestResolve_PlanFirst_GlobalOverride(t *testing.T) {
	g := &config.GlobalSettings{
		PlanFirstEnabled:    boolPtr(true),
		BuildFromPlanPrompt: strPtr("custom plan prompt"),
	}
	r := config.Resolve(g, nil)
	if !r.PlanFirstEnabled {
		t.Error("PlanFirstEnabled should be true")
	}
	if r.BuildFromPlanPrompt != "custom plan prompt" {
		t.Errorf("BuildFromPlanPrompt = %q, want %q", r.BuildFromPlanPrompt, "custom plan prompt")
	}
}

func TestResolve_Models_Defaults(t *testing.T) {
	r := config.Resolve(nil, nil)
	if r.PlanModel != config.DefaultPlanModel {
		t.Errorf("PlanModel = %q, want %q", r.PlanModel, config.DefaultPlanModel)
	}
	if r.AgentModel != "" {
		t.Errorf("AgentModel = %q, want empty (no --model flag)", r.AgentModel)
	}
}

func TestResolve_Models_GlobalOverride(t *testing.T) {
	g := &config.GlobalSettings{
		PlanModel:  strPtr("claude-haiku-4-5"),
		AgentModel: strPtr("claude-opus-4-7"),
	}
	r := config.Resolve(g, nil)
	if r.PlanModel != "claude-haiku-4-5" {
		t.Errorf("PlanModel = %q, want claude-haiku-4-5", r.PlanModel)
	}
	if r.AgentModel != "claude-opus-4-7" {
		t.Errorf("AgentModel = %q, want claude-opus-4-7", r.AgentModel)
	}
}

func TestResolve_Models_RepoOverridesGlobal(t *testing.T) {
	g := &config.GlobalSettings{
		PlanModel:  strPtr("global-plan"),
		AgentModel: strPtr("global-agent"),
	}
	repo := &config.RepoSettings{
		PlanModel:  strPtr("repo-plan"),
		AgentModel: strPtr("repo-agent"),
	}
	r := config.Resolve(g, repo)
	if r.PlanModel != "repo-plan" {
		t.Errorf("PlanModel = %q, want repo-plan", r.PlanModel)
	}
	if r.AgentModel != "repo-agent" {
		t.Errorf("AgentModel = %q, want repo-agent", r.AgentModel)
	}
}

func TestGlobalSettings_Models_OmitsNilFields(t *testing.T) {
	s := &config.GlobalSettings{}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	if _, ok := m["plan_model"]; ok {
		t.Error("plan_model should be omitted when nil")
	}
	if _, ok := m["agent_model"]; ok {
		t.Error("agent_model should be omitted when nil")
	}
}

func TestResolve_PlanFirst_RepoOverridesGlobal(t *testing.T) {
	g := &config.GlobalSettings{
		PlanFirstEnabled:    boolPtr(true),
		BuildFromPlanPrompt: strPtr("global"),
	}
	repo := &config.RepoSettings{
		PlanFirstEnabled:    boolPtr(false),
		BuildFromPlanPrompt: strPtr("repo"),
	}
	r := config.Resolve(g, repo)
	if r.PlanFirstEnabled {
		t.Error("PlanFirstEnabled should be false (repo override)")
	}
	if r.BuildFromPlanPrompt != "repo" {
		t.Errorf("BuildFromPlanPrompt = %q, want repo", r.BuildFromPlanPrompt)
	}
}

func TestResolve_BuildSystemPrompt_Default(t *testing.T) {
	r := config.Resolve(nil, nil)
	if r.BuildSystemPrompt != config.DefaultBuildSystemPrompt {
		t.Errorf("BuildSystemPrompt = %q, want default", r.BuildSystemPrompt)
	}
}

func TestResolve_BuildSystemPrompt_GlobalOverride(t *testing.T) {
	g := &config.GlobalSettings{BuildSystemPrompt: strPtr("custom")}
	r := config.Resolve(g, nil)
	if r.BuildSystemPrompt != "custom" {
		t.Errorf("BuildSystemPrompt = %q, want %q", r.BuildSystemPrompt, "custom")
	}
}

func TestResolve_BuildSystemPrompt_RepoOverridesGlobal(t *testing.T) {
	g := &config.GlobalSettings{BuildSystemPrompt: strPtr("global")}
	repo := &config.RepoSettings{BuildSystemPrompt: strPtr("repo")}
	r := config.Resolve(g, repo)
	if r.BuildSystemPrompt != "repo" {
		t.Errorf("BuildSystemPrompt = %q, want repo", r.BuildSystemPrompt)
	}
}

// An empty-string override is honored as a deliberate disable. The build
// path keys flag emission off `BuildSystemPrompt != ""`, so this lets users
// opt out by setting "build_system_prompt": "" in their config without
// requiring a separate boolean toggle.
func TestResolve_BuildSystemPrompt_EmptyStringDisables(t *testing.T) {
	g := &config.GlobalSettings{BuildSystemPrompt: strPtr("")}
	r := config.Resolve(g, nil)
	if r.BuildSystemPrompt != "" {
		t.Errorf("BuildSystemPrompt = %q, want empty (disabled)", r.BuildSystemPrompt)
	}
}

// TestResolve_MergeMethod_NormalizesAndValidates pins M4: the resolver must
// lowercase and whitelist MergeMethod against {"merge","squash","rebase"} and
// fall back to the default for anything else. Without this, a typo like
// "Squash" or "ff-only" silently reaches the API client where it's coerced
// to "squash" without telling the user — which makes the merge button's
// behavior surprising.
func TestResolve_MergeMethod_NormalizesAndValidates(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"squash", "squash"},
		{"merge", "merge"},
		{"rebase", "rebase"},
		{"Squash", "squash"},     // capital letter → lowercased
		{"  rebase  ", "rebase"}, // whitespace trimmed
		{"ff-only", config.DefaultMergeMethod},
		{"yolo", config.DefaultMergeMethod},
		{"", config.DefaultMergeMethod},
	}
	for _, tc := range cases {
		g := &config.GlobalSettings{MergeMethod: strPtr(tc.input)}
		r := config.Resolve(g, nil)
		if r.MergeMethod != tc.want {
			t.Errorf("MergeMethod(%q) = %q, want %q", tc.input, r.MergeMethod, tc.want)
		}
	}
}
