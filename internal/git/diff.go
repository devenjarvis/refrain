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
	out, err := runGit(wt.Path, "log",
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
	var commits []Commit
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
// are assumed to be in oldest-first order and to form a contiguous run on their
// branch; the diff is computed as `git diff <first>^..<last>` so intermediate
// commits collapse into a single coherent change set. An empty slice returns
// zero values. A single hash uses `<hash>^..<hash>` which equals `git show`.
func DiffForCommits(wt *WorktreeInfo, hashes []string) ([]FileStat, *DiffStats, string, error) {
	if len(hashes) == 0 {
		return nil, &DiffStats{}, "", nil
	}
	first, last := hashes[0], hashes[len(hashes)-1]
	rangeSpec := first + "^.." + last

	rawDiff, err := runGitRaw(wt.Path, "diff", rangeSpec)
	if err != nil {
		return nil, nil, "", fmt.Errorf("diff for commits: %w", err)
	}

	numstatOut, err := runGit(wt.Path, "diff", "--numstat", "--diff-filter=AMD", rangeSpec)
	if err != nil {
		return nil, nil, "", fmt.Errorf("diff numstat for commits: %w", err)
	}
	nameStatusOut, err := runGit(wt.Path, "diff", "--name-status", "--diff-filter=AMD", rangeSpec)
	if err != nil {
		return nil, nil, "", fmt.Errorf("diff name-status for commits: %w", err)
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

	return result, agg, rawDiff, nil
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

// DiffFile holds the parsed diff for a single file.
type DiffFile struct {
	Status     string   // "M", "A", or "D"
	Path       string   // file path from b/... header
	Lines      []string // all diff lines for this file including headers
	Insertions int      // count of added lines (excluding +++ header)
	Deletions  int      // count of deleted lines (excluding --- header)
}

// HunkLineKind distinguishes context, addition, and deletion lines within a hunk.
type HunkLineKind int

const (
	// HunkLineContext is an unchanged context line.
	HunkLineContext HunkLineKind = iota
	// HunkLineAddition is an added line (prefix '+').
	HunkLineAddition
	// HunkLineDeletion is a deleted line (prefix '-').
	HunkLineDeletion
)

// HunkLine is a single line within a diff hunk.
type HunkLine struct {
	Kind HunkLineKind
	// Text is the literal line content with the leading '+', '-', or ' ' prefix stripped.
	Text string
}

// Hunk is a contiguous block of changes within a file's diff.
type Hunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Header   string // raw @@ ... @@ line
	Lines    []HunkLine
}

// ParseDiffFiles splits a raw unified diff into per-file chunks.
func ParseDiffFiles(rawDiff string) []DiffFile {
	if rawDiff == "" {
		return []DiffFile{}
	}

	var files []DiffFile
	var current *DiffFile

	for _, line := range strings.Split(rawDiff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			// Flush the previous file.
			if current != nil {
				files = append(files, *current)
			}
			// Extract path from b/... part.
			path := ""
			fields := strings.Fields(line)
			// fields: ["diff", "--git", "a/foo", "b/foo"]
			if len(fields) >= 4 {
				bPart := fields[3]
				if strings.HasPrefix(bPart, "b/") {
					path = bPart[2:]
				} else {
					path = bPart
				}
			}
			current = &DiffFile{
				Status: "M",
				Path:   path,
				Lines:  []string{line},
			}
			continue
		}
		if current == nil {
			continue
		}
		if line == "new file mode" || strings.HasPrefix(line, "new file mode ") {
			current.Status = "A"
		} else if line == "deleted file mode" || strings.HasPrefix(line, "deleted file mode ") {
			current.Status = "D"
		}
		// Count insertions/deletions, skipping the +++/--- file header lines.
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			// file header; do not count
		} else if strings.HasPrefix(line, "+") {
			current.Insertions++
		} else if strings.HasPrefix(line, "-") {
			current.Deletions++
		}
		current.Lines = append(current.Lines, line)
	}

	// Flush the last file.
	if current != nil {
		files = append(files, *current)
	}

	return files
}

// ParseHunks extracts the structured hunks from a DiffFile. Returns an empty
// slice for binary files or files with no hunks.
func ParseHunks(f DiffFile) []Hunk {
	var hunks []Hunk
	var current *Hunk

	for _, line := range f.Lines {
		// Skip file-level preamble lines.
		if strings.HasPrefix(line, "diff --git ") ||
			strings.HasPrefix(line, "index ") ||
			strings.HasPrefix(line, "new file mode") ||
			strings.HasPrefix(line, "deleted file mode") ||
			strings.HasPrefix(line, "old mode") ||
			strings.HasPrefix(line, "new mode") ||
			strings.HasPrefix(line, "similarity index") ||
			strings.HasPrefix(line, "rename from") ||
			strings.HasPrefix(line, "rename to") ||
			strings.HasPrefix(line, "copy from") ||
			strings.HasPrefix(line, "copy to") ||
			strings.HasPrefix(line, "Binary files") ||
			strings.HasPrefix(line, "--- ") ||
			strings.HasPrefix(line, "+++ ") ||
			line == "---" ||
			line == "+++" {
			continue
		}
		if strings.HasPrefix(line, "@@") {
			// Flush previous.
			if current != nil {
				hunks = append(hunks, *current)
			}
			h := parseHunkHeader(line)
			current = &h
			continue
		}
		if current == nil {
			continue
		}
		// "\ No newline at end of file" is a meta line; skip it.
		if strings.HasPrefix(line, "\\") {
			continue
		}
		// In unified diff format, every line inside a hunk has a leading
		// '+', '-', or ' '. A bare empty line therefore means we've walked
		// off the end of the hunk content (trailing newline in the raw diff).
		switch {
		case strings.HasPrefix(line, "+"):
			current.Lines = append(current.Lines, HunkLine{
				Kind: HunkLineAddition,
				Text: line[1:],
			})
		case strings.HasPrefix(line, "-"):
			current.Lines = append(current.Lines, HunkLine{
				Kind: HunkLineDeletion,
				Text: line[1:],
			})
		case strings.HasPrefix(line, " "):
			current.Lines = append(current.Lines, HunkLine{
				Kind: HunkLineContext,
				Text: line[1:],
			})
		}
	}

	if current != nil {
		hunks = append(hunks, *current)
	}
	return hunks
}

// parseHunkHeader parses a line like "@@ -1,3 +1,4 @@ optional section" into a Hunk.
func parseHunkHeader(line string) Hunk {
	h := Hunk{Header: line, OldCount: 1, NewCount: 1}
	// Expected form: @@ -oldStart[,oldCount] +newStart[,newCount] @@ ...
	rest := strings.TrimPrefix(line, "@@")
	// Find the closing "@@".
	end := strings.Index(rest, "@@")
	if end < 0 {
		return h
	}
	spec := strings.TrimSpace(rest[:end])
	parts := strings.Fields(spec)
	for _, p := range parts {
		if strings.HasPrefix(p, "-") {
			h.OldStart, h.OldCount = parseRange(p[1:])
		} else if strings.HasPrefix(p, "+") {
			h.NewStart, h.NewCount = parseRange(p[1:])
		}
	}
	return h
}

// parseRange parses "start,count" or just "start", returning start and count (default 1).
func parseRange(s string) (int, int) {
	start, count := 0, 1
	comma := strings.Index(s, ",")
	if comma < 0 {
		if n, err := strconv.Atoi(s); err == nil {
			start = n
		}
		return start, count
	}
	if n, err := strconv.Atoi(s[:comma]); err == nil {
		start = n
	}
	if n, err := strconv.Atoi(s[comma+1:]); err == nil {
		count = n
	}
	return start, count
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
