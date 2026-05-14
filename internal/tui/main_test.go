package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain redirects $HOME to a per-process temp dir so tests that exercise
// session creation (via agent.Manager) never touch the real ~/.refrain/ —
// otherwise every CreateSession call appends a row to the user's real
// setlist file. We also set explicit git author/committer env vars so the
// HOME redirect does not break tests that run `git commit` (which would
// otherwise fail with "Author identity unknown" when no global gitconfig
// is reachable, e.g. on CI runners).
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "refrain-tui-tests-*")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	for k, v := range map[string]string{
		"HOME":                tmp,
		"XDG_CONFIG_HOME":     filepath.Join(tmp, ".config"),
		"GIT_AUTHOR_NAME":     "Refrain Test",
		"GIT_AUTHOR_EMAIL":    "refrain-test@example.com",
		"GIT_COMMITTER_NAME":  "Refrain Test",
		"GIT_COMMITTER_EMAIL": "refrain-test@example.com",
	} {
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}
	os.Exit(m.Run())
}
