package git

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Commit holds metadata for a single git commit.
type Commit struct {
	Hash    string
	Subject string
	Body    string
}

// LogCommitsAgainstBase returns commits reachable from HEAD but not from
// BaseBranch, ordered oldest-first (natural review order). The worktree path
// is used as the working directory so the command runs in the right context
// even when the repo root is elsewhere.
func LogCommitsAgainstBase(wt *WorktreeInfo) ([]Commit, error) {
	// %x1f separates fields within one record; %x1e terminates each record.
	// This avoids ambiguity with newlines in commit bodies.
	out, err := runGit(
		wt.Path, "log",
		"--format=format:%H%x1f%s%x1f%b%x1e",
		"--reverse",
		wt.BaseBranch+"..HEAD",
	)
	if err != nil {
		return nil, fmt.Errorf("log commits: %w", err)
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	commits := make([]Commit, 0, 32)
	for _, record := range strings.Split(out, "\x1e") {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		parts := strings.SplitN(record, "\x1f", 3)
		c := Commit{}
		if len(parts) > 0 {
			c.Hash = strings.TrimSpace(parts[0])
		}
		if len(parts) > 1 {
			c.Subject = strings.TrimSpace(parts[1])
		}
		if len(parts) > 2 {
			c.Body = strings.TrimSpace(parts[2])
		}
		if c.Hash == "" {
			continue
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// DiffForCommits returns per-file stats, aggregate stats, and raw unified diff
// for an arbitrary slice of commit hashes in the given worktree. The hashes
// must be in oldest-first order but need not be contiguous — each commit is
// diffed individually (hash^..hash) and the results are concatenated. This
// ensures correctness when task commits are interleaved with other tasks'
// commits in the log.
func DiffForCommits(wt *WorktreeInfo, hashes []string) ([]FileStat, *DiffStats, string, error) {
	if len(hashes) == 0 {
		return nil, &DiffStats{}, "", nil
	}

	var rawDiffSb strings.Builder
	fileMap := make(map[string]*FileStat)

	for _, h := range hashes {
		rangeSpec := h + "^.." + h

		raw, err := runGitRaw(wt.Path, "diff", rangeSpec)
		if err != nil {
			return nil, nil, "", fmt.Errorf("diff for commits: %w", err)
		}
		rawDiffSb.WriteString(raw)

		numstatOut, err := runGit(wt.Path, "diff", "--numstat", "--diff-filter=AMD", rangeSpec)
		if err != nil {
			return nil, nil, "", fmt.Errorf("diff numstat for commits: %w", err)
		}
		nameStatusOut, err := runGit(wt.Path, "diff", "--name-status", "--diff-filter=AMD", rangeSpec)
		if err != nil {
			return nil, nil, "", fmt.Errorf("diff name-status for commits: %w", err)
		}
		parseNumstat(numstatOut, fileMap)
		parseNameStatus(nameStatusOut, fileMap)
	}

	result := make([]FileStat, 0, len(fileMap))
	agg := &DiffStats{}
	for _, fs := range fileMap {
		result = append(result, *fs)
		agg.Files++
		agg.Insertions += fs.Insertions
		agg.Deletions += fs.Deletions
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })

	return result, agg, rawDiffSb.String(), nil
}

// DiffForRange returns per-file stats, aggregate stats, and the raw unified
// diff for the merge-base range from...to in the given worktree. The review
// ledger uses it for the aggregate "earlier changes" card on long-lived
// branches, where one card per commit would make the ledger unusable. The
// three-dot form diffs against the merge base so commits that landed on the
// base branch after the fork point don't show up as reverse changes.
func DiffForRange(wt *WorktreeInfo, from, to string) ([]FileStat, *DiffStats, string, error) {
	rangeSpec := from + "..." + to

	raw, err := runGitRaw(wt.Path, "diff", rangeSpec)
	if err != nil {
		return nil, nil, "", fmt.Errorf("diff for range: %w", err)
	}
	numstatOut, err := runGit(wt.Path, "diff", "--numstat", "--diff-filter=AMD", rangeSpec)
	if err != nil {
		return nil, nil, "", fmt.Errorf("diff numstat for range: %w", err)
	}
	nameStatusOut, err := runGit(wt.Path, "diff", "--name-status", "--diff-filter=AMD", rangeSpec)
	if err != nil {
		return nil, nil, "", fmt.Errorf("diff name-status for range: %w", err)
	}

	fileMap := make(map[string]*FileStat)
	parseNumstat(numstatOut, fileMap)
	parseNameStatus(nameStatusOut, fileMap)

	result := make([]FileStat, 0, len(fileMap))
	agg := &DiffStats{}
	for _, fs := range fileMap {
		result = append(result, *fs)
		agg.Files++
		agg.Insertions += fs.Insertions
		agg.Deletions += fs.Deletions
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })

	return result, agg, raw, nil
}

// SplitDiffByFile splits a raw unified diff into standalone per-file chunks
// keyed by the file's post-image path (pre-image path for deletions). Each
// chunk starts at its own "diff --git" header, so it parses on its own. Chunks
// whose path cannot be determined (e.g. binary files with no ---/+++ lines and
// an ambiguous header) are dropped rather than misattributed.
func SplitDiffByFile(raw string) map[string]string {
	result := make(map[string]string)
	if raw == "" {
		return result
	}
	lines := strings.SplitAfter(raw, "\n")
	chunk := make([]string, 0, len(lines))
	flush := func() {
		if len(chunk) == 0 {
			return
		}
		if path := diffChunkPath(chunk); path != "" {
			result[path] = strings.Join(chunk, "")
		}
		chunk = chunk[:0]
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "diff --git ") {
			flush()
		}
		chunk = append(chunk, l)
	}
	flush()
	return result
}

// diffChunkPath extracts the file path from one per-file diff chunk. It
// prefers the "+++ b/<path>" line, falls back to "--- a/<path>" for deleted
// files, and returns "" when neither yields a usable path.
func diffChunkPath(lines []string) string {
	var minus string
	for _, l := range lines {
		l = strings.TrimRight(l, "\n")
		if strings.HasPrefix(l, "@@") {
			break // hunks start; header lines are over
		}
		switch {
		case strings.HasPrefix(l, "--- "):
			minus = diffPathToken(l[len("--- "):], "a/")
		case strings.HasPrefix(l, "+++ "):
			if plus := diffPathToken(l[len("+++ "):], "b/"); plus != "" {
				return plus
			}
			return minus // "+++ /dev/null": deleted file
		}
	}
	return ""
}

// diffPathToken strips an optional surrounding quote pair and the given
// "a/"/"b/" prefix from a diff header path token. Returns "" for /dev/null or
// a token without the expected prefix.
func diffPathToken(tok, prefix string) string {
	tok = strings.TrimSpace(tok)
	if len(tok) >= 2 && strings.HasPrefix(tok, `"`) && strings.HasSuffix(tok, `"`) {
		tok = tok[1 : len(tok)-1]
	}
	if tok == "/dev/null" || !strings.HasPrefix(tok, prefix) {
		return ""
	}
	return strings.TrimPrefix(tok, prefix)
}

// DiffStats holds summary statistics for a diff.
type DiffStats struct {
	Files      int
	Insertions int
	Deletions  int
}

// Diff returns the full unified diff between the base branch and the worktree's
// current state (committed + staged + unstaged changes). It runs from inside
// the worktree so uncommitted work is included — the repoPath parameter is
// accepted for API compatibility but unused. Uses --diff-filter=AMD so the file
// list matches GetPerFileDiffStats.
func Diff(_ string, wt *WorktreeInfo) (string, error) {
	out, err := runGitRaw(wt.Path, "diff", "--diff-filter=AMD", wt.BaseBranch)
	if err != nil {
		return "", fmt.Errorf("getting diff: %w", err)
	}
	return out, nil
}

// GetDiffStats returns summary statistics for the diff between the base and worktree branches.
func GetDiffStats(repoPath string, wt *WorktreeInfo) (*DiffStats, error) {
	out, err := runGit(repoPath, "diff", "--numstat", wt.BaseBranch+"..."+wt.Branch)
	if err != nil {
		return nil, fmt.Errorf("getting diff stats: %w", err)
	}

	stats := &DiffStats{}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		// Binary files show "-" for insertions/deletions.
		if fields[0] != "-" {
			if n, err := strconv.Atoi(fields[0]); err == nil {
				stats.Insertions += n
			}
		}
		if fields[1] != "-" {
			if n, err := strconv.Atoi(fields[1]); err == nil {
				stats.Deletions += n
			}
		}
		stats.Files++
	}

	return stats, nil
}

// FileStat holds per-file diff statistics.
type FileStat struct {
	Path       string // file path
	Status     string // "A", "M", or "D"
	Insertions int
	Deletions  int
}

// GetPerFileDiffStats returns per-file insertion/deletion counts and statuses,
// including uncommitted working tree changes. It also returns aggregate DiffStats.
// It diffs the worktree's current state (committed + uncommitted) against the base
// branch in a single pass to avoid double-counting.
func GetPerFileDiffStats(repoPath string, wt *WorktreeInfo) ([]FileStat, *DiffStats, error) {
	// Run from the worktree directory so that both committed and uncommitted
	// changes are captured in a single diff against the base branch.
	numstatOut, err := runGit(wt.Path, "diff", "--numstat", "--diff-filter=AMD", wt.BaseBranch)
	if err != nil {
		return nil, nil, fmt.Errorf("getting per-file numstat: %w", err)
	}

	nameStatusOut, err := runGit(wt.Path, "diff", "--name-status", "--diff-filter=AMD", wt.BaseBranch)
	if err != nil {
		return nil, nil, fmt.Errorf("getting per-file name-status: %w", err)
	}

	fileMap := make(map[string]*FileStat)
	parseNumstat(numstatOut, fileMap)
	parseNameStatus(nameStatusOut, fileMap)

	// Build result slice and aggregate stats.
	result := make([]FileStat, 0, len(fileMap))
	agg := &DiffStats{}
	for _, fs := range fileMap {
		result = append(result, *fs)
		agg.Files++
		agg.Insertions += fs.Insertions
		agg.Deletions += fs.Deletions
	}

	// Sort by path for stable display order.
	sort.Slice(result, func(i, j int) bool {
		return result[i].Path < result[j].Path
	})

	return result, agg, nil
}

// parseNumstat parses `git diff --numstat` output and merges into fileMap.
// For existing entries, insertions and deletions are added (merged).
func parseNumstat(output string, fileMap map[string]*FileStat) {
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		path := fields[2]
		ins := 0
		del := 0
		if fields[0] != "-" {
			if n, err := strconv.Atoi(fields[0]); err == nil {
				ins = n
			}
		}
		if fields[1] != "-" {
			if n, err := strconv.Atoi(fields[1]); err == nil {
				del = n
			}
		}

		if existing, ok := fileMap[path]; ok {
			existing.Insertions += ins
			existing.Deletions += del
		} else {
			fileMap[path] = &FileStat{
				Path:       path,
				Insertions: ins,
				Deletions:  del,
			}
		}
	}
}

// parseNameStatus parses `git diff --name-status` output and sets statuses in fileMap.
// Later calls overwrite earlier statuses (uncommitted status takes precedence).
func parseNameStatus(output string, fileMap map[string]*FileStat) {
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		status := fields[0]
		path := fields[1]

		if existing, ok := fileMap[path]; ok {
			existing.Status = status
		} else {
			fileMap[path] = &FileStat{
				Path:   path,
				Status: status,
			}
		}
	}
}
