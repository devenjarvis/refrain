package agent

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHaikuLog_AppendsAndCreates(t *testing.T) {
	repo := t.TempDir()
	haikuLog(repo, "first")
	haikuLog(repo, "second")

	body, err := os.ReadFile(haikuLogPath(repo))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "first\n") || !strings.Contains(got, "second\n") {
		t.Errorf("log missing entries: %q", got)
	}
}

func TestHaikuLog_NoRepoIsNoOp(t *testing.T) {
	// No path = no panic, no file. Just verify it doesn't crash.
	haikuLog("", "anything")
}

func TestHaikuLog_TruncatesPastSizeLimit(t *testing.T) {
	repo := t.TempDir()
	path := haikuLogPath(repo)
	if err := os.MkdirAll(strings.TrimSuffix(path, "/haiku.log"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Pre-seed a file larger than the truncation threshold.
	huge := make([]byte, haikuLogMaxBytes+100)
	for i := range huge {
		huge[i] = 'X'
	}
	if err := os.WriteFile(path, huge, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	haikuLog(repo, "fresh-line")

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if int64(len(body)) >= haikuLogMaxBytes {
		t.Errorf("log not truncated; size = %d, max = %d", len(body), haikuLogMaxBytes)
	}
	if !strings.Contains(string(body), "fresh-line") {
		t.Errorf("log missing fresh entry after truncate: %q", string(body))
	}
	if strings.Contains(string(body), "XXXXXXXXXX") {
		t.Errorf("log still contains pre-truncate content: %q", string(body)[:50])
	}
}

func TestHaikuLog_ConcurrentWritesNoInterleaving(t *testing.T) {
	repo := t.TempDir()
	const goroutines = 16
	const perGoroutine = 50

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				haikuLog(repo, "tag-XYZ-marker")
			}
		}(g)
	}
	wg.Wait()

	body, err := os.ReadFile(haikuLogPath(repo))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if got, want := len(lines), goroutines*perGoroutine; got != want {
		t.Errorf("line count = %d, want %d", got, want)
	}
	for _, line := range lines {
		if line != "tag-XYZ-marker" {
			t.Errorf("interleaved/corrupt line: %q", line)
			break
		}
	}
}

func TestHaikuLogAttempt_FormatsOkAndErr(t *testing.T) {
	repo := t.TempDir()
	haikuLogAttempt(repo, "sess-1", haikuKindBranch, 1, "", errors.New("boom"), 100*time.Millisecond)
	haikuLogAttempt(repo, "sess-1", haikuKindBranch, 2, "good-slug", nil, 200*time.Millisecond)

	body, err := os.ReadFile(haikuLogPath(repo))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "session=sess-1 kind=branch attempt=1 status=err") {
		t.Errorf("missing err line: %q", got)
	}
	if !strings.Contains(got, `err="boom"`) {
		t.Errorf("missing quoted err detail: %q", got)
	}
	if !strings.Contains(got, "session=sess-1 kind=branch attempt=2 status=ok took=200ms suffix=good-slug") {
		t.Errorf("missing ok line: %q", got)
	}
}

func TestHaikuLogAttempt_SummaryKindUsesResultLabel(t *testing.T) {
	repo := t.TempDir()
	haikuLogAttempt(repo, "sess-x", haikuKindSummary, 1, "fix the broken login", nil, 50*time.Millisecond)
	haikuLogAttempt(repo, "sess-x", haikuKindSummary, 2, "", errors.New("nope"), 50*time.Millisecond)

	body, err := os.ReadFile(haikuLogPath(repo))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "kind=summary attempt=1 status=ok took=50ms result=fix the broken login") {
		t.Errorf("missing summary ok line: %q", got)
	}
	if !strings.Contains(got, "kind=summary attempt=2 status=err") {
		t.Errorf("missing summary err line: %q", got)
	}
}

func TestHaikuLogOutcome_FormatsBothPaths(t *testing.T) {
	repo := t.TempDir()
	haikuLogOutcome(repo, "sess-2", haikuKindBranch, "", errors.New("nope"), 500*time.Millisecond)
	haikuLogOutcome(repo, "sess-2", haikuKindBranch, "name", nil, 1500*time.Millisecond)

	body, err := os.ReadFile(haikuLogPath(repo))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, `kind=branch status=fail err="nope" took=500ms`) {
		t.Errorf("missing fail line: %q", got)
	}
	if !strings.Contains(got, "kind=branch status=ok suffix=name took=1.5s") {
		t.Errorf("missing ok line: %q", got)
	}
}

func TestHaikuLogOutcome_SummaryKind(t *testing.T) {
	repo := t.TempDir()
	haikuLogOutcome(repo, "sess-3", haikuKindSummary, "", errors.New("dead"), 25*time.Millisecond)
	haikuLogOutcome(repo, "sess-3", haikuKindSummary, "do the thing", nil, 75*time.Millisecond)

	body, err := os.ReadFile(haikuLogPath(repo))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, `kind=summary status=fail err="dead" took=25ms`) {
		t.Errorf("missing summary fail line: %q", got)
	}
	if !strings.Contains(got, "kind=summary status=ok result=do the thing took=75ms") {
		t.Errorf("missing summary ok line: %q", got)
	}
}
