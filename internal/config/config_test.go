package config_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/devenjarvis/refrain/internal/config"
)

// initTestRepo creates a temporary git repository with an initial commit.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "checkout", "-b", "main"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup %v: %v\n%s", args, err, out)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "initial commit"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// configDirInTmp redirects $HOME into a temp directory so tests never touch
// the real ~/.refrain/.  It also sets $XDG_CONFIG_HOME so legacy migration
// tests can exercise the old path.  Returns the ~/.refrain directory.
func configDirInTmp(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("HOME", base)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, ".config"))
	return filepath.Join(base, ".refrain")
}

// ---- Load ----

func TestLoad_MissingFile_ReturnsEmpty(t *testing.T) {
	configDirInTmp(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg == nil {
		t.Fatal("Load() returned nil config")
	}
	if len(cfg.Repos) != 0 {
		t.Errorf("Load() repos = %v, want empty", cfg.Repos)
	}
}

func TestLoad_ExistingFile_ReturnsRepos(t *testing.T) {
	dir := configDirInTmp(t)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Second)
	data, _ := json.Marshal(config.Config{
		Repos: []config.Repo{
			{Path: "/some/path", Name: "path", AddedAt: now},
		},
	})
	if err := os.WriteFile(filepath.Join(dir, "repos.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Repos) != 1 {
		t.Fatalf("Load() got %d repos, want 1", len(cfg.Repos))
	}
	if cfg.Repos[0].Path != "/some/path" {
		t.Errorf("Load() path = %q, want /some/path", cfg.Repos[0].Path)
	}
}

// ---- Save ----

func TestSave_CreatesFileAndDir(t *testing.T) {
	dir := configDirInTmp(t)

	cfg := &config.Config{
		Repos: []config.Repo{
			{Path: "/foo", Name: "foo", AddedAt: time.Now()},
		},
	}

	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "repos.json"))
	if err != nil {
		t.Fatalf("reading saved file: %v", err)
	}

	var got config.Config
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal saved file: %v", err)
	}
	if len(got.Repos) != 1 || got.Repos[0].Path != "/foo" {
		t.Errorf("saved config = %+v, want 1 repo with path /foo", got)
	}
}

func TestSave_RoundTrip(t *testing.T) {
	configDirInTmp(t)

	now := time.Now().Truncate(time.Second)
	original := &config.Config{
		Repos: []config.Repo{
			{Path: "/a", Name: "a", AddedAt: now},
			{Path: "/b", Name: "b", AddedAt: now},
		},
	}

	if err := config.Save(original); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded.Repos) != 2 {
		t.Fatalf("Load() got %d repos, want 2", len(loaded.Repos))
	}
}

// ---- AddRepo ----

func TestAddRepo_HappyPath(t *testing.T) {
	configDirInTmp(t)
	repoPath := initTestRepo(t)

	cfg := &config.Config{}
	if err := config.AddRepo(cfg, repoPath); err != nil {
		t.Fatalf("AddRepo() error = %v", err)
	}

	if len(cfg.Repos) != 1 {
		t.Fatalf("got %d repos, want 1", len(cfg.Repos))
	}
	if cfg.Repos[0].Path != repoPath {
		t.Errorf("path = %q, want %q", cfg.Repos[0].Path, repoPath)
	}
	if cfg.Repos[0].Name != filepath.Base(repoPath) {
		t.Errorf("name = %q, want %q", cfg.Repos[0].Name, filepath.Base(repoPath))
	}
	if cfg.Repos[0].AddedAt.IsZero() {
		t.Error("AddedAt is zero")
	}
}

func TestAddRepo_DotResolvesToAbsolute(t *testing.T) {
	configDirInTmp(t)
	repoPath := initTestRepo(t)

	// Change working directory to the repo so "." resolves to it.
	orig, _ := os.Getwd()
	defer func() { _ = os.Chdir(orig) }()
	if err := os.Chdir(repoPath); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	if err := config.AddRepo(cfg, "."); err != nil {
		t.Fatalf("AddRepo(\".\") error = %v", err)
	}

	if len(cfg.Repos) != 1 {
		t.Fatalf("got %d repos, want 1", len(cfg.Repos))
	}
	if cfg.Repos[0].Path == "." {
		t.Error("path was not resolved to absolute; still \".\"")
	}
}

func TestAddRepo_DuplicateError(t *testing.T) {
	configDirInTmp(t)
	repoPath := initTestRepo(t)

	cfg := &config.Config{}
	if err := config.AddRepo(cfg, repoPath); err != nil {
		t.Fatalf("first AddRepo() error = %v", err)
	}
	if err := config.AddRepo(cfg, repoPath); err == nil {
		t.Error("second AddRepo() expected error for duplicate, got nil")
	}
}

func TestAddRepo_NonGitRepoError(t *testing.T) {
	configDirInTmp(t)
	plain := t.TempDir() // not a git repo

	cfg := &config.Config{}
	if err := config.AddRepo(cfg, plain); err == nil {
		t.Error("AddRepo() expected error for non-git-repo, got nil")
	}
}

// ---- RemoveRepo ----

func TestRemoveRepo_HappyPath(t *testing.T) {
	configDirInTmp(t)
	repoPath := initTestRepo(t)

	cfg := &config.Config{}
	if err := config.AddRepo(cfg, repoPath); err != nil {
		t.Fatalf("AddRepo() error = %v", err)
	}

	if err := config.RemoveRepo(cfg, repoPath); err != nil {
		t.Fatalf("RemoveRepo() error = %v", err)
	}

	if len(cfg.Repos) != 0 {
		t.Errorf("after RemoveRepo got %d repos, want 0", len(cfg.Repos))
	}
}

func TestRemoveRepo_NotFound(t *testing.T) {
	configDirInTmp(t)

	cfg := &config.Config{}
	if err := config.RemoveRepo(cfg, "/nonexistent"); err == nil {
		t.Error("RemoveRepo() expected error for missing path, got nil")
	}
}

// ---- BypassPermissions ----

func TestLoad_MissingFile_DefaultsBypassPermissionsTrue(t *testing.T) {
	configDirInTmp(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.GetBypassPermissions() {
		t.Error("Load() BypassPermissions should default to true when file missing")
	}
}

func TestLoad_ExistingFileWithoutBypassField_DefaultsTrue(t *testing.T) {
	dir := configDirInTmp(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write config without bypass_permissions field
	data := []byte(`{"repos":[]}`)
	if err := os.WriteFile(filepath.Join(dir, "repos.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.GetBypassPermissions() {
		t.Error("Load() BypassPermissions should default to true when field missing from JSON")
	}
}

func TestLoad_ExistingFileWithBypassFalse_ReturnsFalse(t *testing.T) {
	dir := configDirInTmp(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"repos":[],"bypass_permissions":false}`)
	if err := os.WriteFile(filepath.Join(dir, "repos.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.GetBypassPermissions() {
		t.Error("Load() BypassPermissions should be false when explicitly set to false")
	}
}

func TestGetBypassPermissions_NilPointer_ReturnsTrue(t *testing.T) {
	cfg := &config.Config{}
	if !cfg.GetBypassPermissions() {
		t.Error("GetBypassPermissions() should return true when BypassPermissions is nil")
	}
}

// ---- Repo.DisplayName ----

func TestRepo_DisplayName_NoAlias(t *testing.T) {
	r := config.Repo{Name: "my-company-super-long-repo-name", Path: "/x"}
	if got := r.DisplayName(); got != r.Name {
		t.Errorf("DisplayName() = %q, want %q", got, r.Name)
	}
}

func TestRepo_DisplayName_AliasWins(t *testing.T) {
	r := config.Repo{Name: "my-company-super-long-repo-name", Alias: "svc", Path: "/x"}
	if got := r.DisplayName(); got != "svc" {
		t.Errorf("DisplayName() = %q, want %q", got, "svc")
	}
}

// ---- SetRepoAlias ----

func TestSetRepoAlias_PersistsAndRoundTrips(t *testing.T) {
	configDirInTmp(t)
	repoPath := initTestRepo(t)

	cfg := &config.Config{}
	if err := config.AddRepo(cfg, repoPath); err != nil {
		t.Fatalf("AddRepo() error = %v", err)
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if err := config.SetRepoAlias(repoPath, "svc"); err != nil {
		t.Fatalf("SetRepoAlias() error = %v", err)
	}

	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded.Repos) != 1 {
		t.Fatalf("got %d repos, want 1", len(loaded.Repos))
	}
	if loaded.Repos[0].Alias != "svc" {
		t.Errorf("Alias = %q, want %q", loaded.Repos[0].Alias, "svc")
	}
	if got := loaded.Repos[0].DisplayName(); got != "svc" {
		t.Errorf("DisplayName() after reload = %q, want %q", got, "svc")
	}

	// Clearing should revert DisplayName to Name.
	if err := config.SetRepoAlias(repoPath, ""); err != nil {
		t.Fatalf("SetRepoAlias(\"\") error = %v", err)
	}
	loaded, err = config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Repos[0].Alias != "" {
		t.Errorf("Alias = %q, want empty after clear", loaded.Repos[0].Alias)
	}
	if got := loaded.Repos[0].DisplayName(); got != loaded.Repos[0].Name {
		t.Errorf("DisplayName() after clear = %q, want %q", got, loaded.Repos[0].Name)
	}
}

func TestSetRepoAlias_UnregisteredReturnsError(t *testing.T) {
	configDirInTmp(t)
	if err := config.SetRepoAlias(t.TempDir(), "x"); err == nil {
		t.Error("SetRepoAlias() expected error for unregistered repo, got nil")
	}
}
