package config_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/refrain/internal/config"
)

func TestExpandBranchPrefix_NoBraces(t *testing.T) {
	if got := config.ExpandBranchPrefix("refrain/"); got != "refrain/" {
		t.Errorf("ExpandBranchPrefix(%q) = %q, want %q", "refrain/", got, "refrain/")
	}
	if got := config.ExpandBranchPrefix(""); got != "" {
		t.Errorf("ExpandBranchPrefix(%q) = %q, want empty", "", got)
	}
}

func TestExpandBranchPrefix_UnknownTokenLeftLiteral(t *testing.T) {
	if got := config.ExpandBranchPrefix("{foo}/"); got != "{foo}/" {
		t.Errorf("ExpandBranchPrefix(%q) = %q, want %q", "{foo}/", got, "{foo}/")
	}
}

func TestExpandBranchPrefix_Date(t *testing.T) {
	got := config.ExpandBranchPrefix("{date}/")
	want := time.Now().Format("2006-01-02") + "/"
	if got != want {
		t.Errorf("ExpandBranchPrefix(%q) = %q, want %q", "{date}/", got, want)
	}
}

// sandboxGitConfig isolates git's global config lookup so tests cannot read
// or mutate the developer's real ~/.gitconfig. HOME + XDG_CONFIG_HOME handle
// most setups; GIT_CONFIG_GLOBAL=/dev/null is a belt-and-braces override in
// case the caller's env already set it to a non-default path.
func sandboxGitConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
}

func TestExpandBranchPrefix_User(t *testing.T) {
	sandboxGitConfig(t)
	t.Setenv("USER", "fallback-user")
	// GIT_CONFIG_GLOBAL=/dev/null makes `git config --global user.name` a no-op
	// sink, so we set user.name via GIT_AUTHOR_NAME / GIT_COMMITTER_NAME-style
	// overrides. Instead, point GIT_CONFIG_GLOBAL at a real file we control.
	cfg := sandboxGitConfigFile(t, "[user]\n\tname = Deven Jarvis\n")
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)

	got := config.ExpandBranchPrefix("{user}/")
	if got != "deven-jarvis/" {
		t.Errorf("ExpandBranchPrefix(%q) = %q, want %q", "{user}/", got, "deven-jarvis/")
	}
}

func TestExpandBranchPrefix_UserAndDate(t *testing.T) {
	sandboxGitConfig(t)
	t.Setenv("USER", "fallback")
	cfg := sandboxGitConfigFile(t, "[user]\n\tname = dj\n")
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)

	got := config.ExpandBranchPrefix("{user}/{date}/")
	date := time.Now().Format("2006-01-02")
	want := "dj/" + date + "/"
	if got != want {
		t.Errorf("ExpandBranchPrefix(%q) = %q, want %q", "{user}/{date}/", got, want)
	}
}

func TestExpandBranchPrefix_UserFallsBackToEnv(t *testing.T) {
	sandboxGitConfig(t)
	t.Setenv("USER", "alice")
	// GIT_CONFIG_GLOBAL=/dev/null leaves git with no user.name, so {user}
	// falls back to $USER.

	got := config.ExpandBranchPrefix("{user}/")
	if got != "alice/" {
		t.Errorf("ExpandBranchPrefix(%q) = %q, want %q", "{user}/", got, "alice/")
	}
}

// TestExpandBranchPrefix_UserEmptyFallsBackToLiteral pins M7: when both git
// user.name and $USER are empty (CI containers, minimal sandboxes), {user}
// must expand to a non-empty literal so the resulting branch path stays
// well-formed. Dropping to empty produced "refrain//date-suffix", which git
// rejects as an invalid branch name.
func TestExpandBranchPrefix_UserEmptyFallsBackToLiteral(t *testing.T) {
	sandboxGitConfig(t)
	t.Setenv("USER", "")

	got := config.ExpandBranchPrefix("{user}/")
	if got != "user/" {
		t.Errorf("ExpandBranchPrefix(%q) = %q, want %q", "{user}/", got, "user/")
	}
}

func TestExpandBranchPrefix_UserSlugifiesSpecialChars(t *testing.T) {
	sandboxGitConfig(t)
	t.Setenv("USER", "fallback")
	cfg := sandboxGitConfigFile(t, "[user]\n\tname = Alice O'Hare\n")
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)

	got := config.ExpandBranchPrefix("{user}/")
	if !strings.HasPrefix(got, "alice-o-hare") {
		t.Errorf("ExpandBranchPrefix(%q) = %q, want prefix %q", "{user}/", got, "alice-o-hare")
	}
}

// sandboxGitConfigFile writes the given contents to a temp file and returns
// the path, suitable for GIT_CONFIG_GLOBAL.
func sandboxGitConfigFile(t *testing.T, contents string) string {
	t.Helper()
	path := t.TempDir() + "/.gitconfig"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write gitconfig: %v", err)
	}
	return path
}
