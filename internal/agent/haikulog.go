package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// haikuLogPath returns the diagnostic log path for the given repo. The log
// lives under <repoPath>/.refrain/logs/ alongside the hook socket.
func haikuLogPath(repoPath string) string {
	return filepath.Join(repoPath, ".refrain", "logs", "haiku.log")
}

// haikuLogMaxBytes is the size threshold past which haikuLog truncates the
// file. The log is diagnostic, not audit; losing history on truncation is
// acceptable in exchange for bounded disk use.
const haikuLogMaxBytes int64 = 1 << 20 // 1 MiB

var haikuLogMu sync.Mutex

// haikuLog appends a single diagnostic line about the branch-namer flow to
// <repoPath>/.refrain/logs/haiku.log. Best-effort: any I/O error is silently
// dropped so logging never disrupts the TUI. The line is suffixed with "\n"
// if it doesn't already end in one.
//
// When the log exceeds haikuLogMaxBytes, the file is truncated before the
// next write — the log is for diagnosing the most recent failures, not for
// long-term audit, so a hard truncate is simpler than rotating files.
func haikuLog(repoPath, line string) {
	if repoPath == "" {
		return
	}
	haikuLogMu.Lock()
	defer haikuLogMu.Unlock()

	path := haikuLogPath(repoPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}

	if info, err := os.Stat(path); err == nil && info.Size() > haikuLogMaxBytes {
		_ = os.Truncate(path, 0)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	if line == "" || line[len(line)-1] != '\n' {
		line += "\n"
	}
	_, _ = f.WriteString(line)
}

// Haiku flow kinds used as the `kind=` field in diagnostic log lines so a
// shared haiku.log distinguishes branch-naming from task-summary attempts.
const (
	haikuKindBranch  = "branch"
	haikuKindSummary = "summary"
)

// haikuLogAttempt formats and writes one per-attempt diagnostic line.
// kind names which haiku flow produced the line (haikuKindBranch /
// haikuKindSummary). For branch flows the result column is labeled `suffix`;
// for summaries it is labeled `result`.
func haikuLogAttempt(repoPath, sessionID, kind string, attempt int, result string, err error, took time.Duration) {
	resultKey := haikuResultKey(kind)
	status := "err"
	detail := ""
	switch {
	case err != nil:
		detail = fmt.Sprintf(" err=%q", err.Error())
	case result == "":
		detail = fmt.Sprintf(" err=\"empty %s\"", resultKey)
	default:
		status = "ok"
		detail = fmt.Sprintf(" %s=%s", resultKey, result)
	}
	haikuLog(repoPath, fmt.Sprintf(
		"%s session=%s kind=%s attempt=%d status=%s took=%s%s",
		time.Now().UTC().Format(time.RFC3339),
		sessionID, kind, attempt, status, took.Round(time.Millisecond), detail,
	))
}

// haikuLogOutcome formats and writes one final-outcome diagnostic line for
// the whole haiku sequence (across all retries). See haikuLogAttempt for
// the meaning of kind.
func haikuLogOutcome(repoPath, sessionID, kind, result string, err error, took time.Duration) {
	resultKey := haikuResultKey(kind)
	if err == nil && result != "" {
		haikuLog(repoPath, fmt.Sprintf(
			"%s session=%s kind=%s status=ok %s=%s took=%s",
			time.Now().UTC().Format(time.RFC3339),
			sessionID, kind, resultKey, result, took.Round(time.Millisecond),
		))
		return
	}
	detail := "unknown"
	if err != nil {
		detail = err.Error()
	}
	haikuLog(repoPath, fmt.Sprintf(
		"%s session=%s kind=%s status=fail err=%q took=%s",
		time.Now().UTC().Format(time.RFC3339),
		sessionID, kind, detail, took.Round(time.Millisecond),
	))
}

// haikuResultKey picks the result-column label for the given kind so log
// lines stay greppable per flow ("suffix=" for branches, "result=" for
// summaries which can contain spaces).
func haikuResultKey(kind string) string {
	if kind == haikuKindBranch {
		return "suffix"
	}
	return "result"
}
