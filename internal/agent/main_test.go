package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMain redirects $HOME to a per-process temp dir so tests that exercise
// session creation never touch the real ~/.refrain/setlist.jsonl. We also set
// explicit git author/committer env vars so the HOME redirect does not break
// tests that run `git commit` (which would otherwise fail with "Author
// identity unknown" when no global gitconfig is reachable, e.g. on CI
// runners).
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "refrain-agent-tests-*")
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

	// Production retry config (45s per attempt, 140s overall, 1s+3s backoff)
	// is realistic for a `claude -p` cold start but glacial in unit tests.
	// Shrink to fast-but-still-realistic values so tests still exercise the
	// retry path without padding wall-clock time. Unit tests of the wrapper
	// itself (TestCallNamerWithRetry_*) override these inline as needed.
	haikuNamePerAttemptTimeout = 500 * time.Millisecond
	haikuNameOverallTimeout = 3 * time.Second
	haikuNameBackoff = []time.Duration{}

	// Same shrink for the task-summary retry budget so the integration tests
	// don't sit through real-world backoffs.
	haikuSummaryPerAttemptTimeout = 500 * time.Millisecond
	haikuSummaryOverallTimeout = 3 * time.Second
	haikuSummaryBackoff = []time.Duration{}

	os.Exit(m.Run())
}
