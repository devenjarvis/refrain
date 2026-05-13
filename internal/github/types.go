package github

import "time"

// PRState holds the state of a GitHub pull request.
type PRState struct {
	Number         int
	Title          string
	URL            string
	State          string // "open", "closed", "merged"
	MergeableState string // "clean", "dirty", "blocked", "behind", "unstable", "draft", "unknown", or ""
	Draft          bool
	HeadBranch     string // branch the PR is from
	BaseBranch     string // branch the PR targets
}

// CheckStatus holds the combined check/CI status for a git ref.
type CheckStatus struct {
	State   string // "success", "failure", "pending"
	Total   int
	Passed  int
	Failed  int
	Pending int
	Runs    []CheckRun
}

// CheckRun holds details about a single check run.
type CheckRun struct {
	Name       string
	Status     string // "queued", "in_progress", "completed"
	Conclusion string // "success", "failure", "cancelled", "skipped", etc.
	URL        string // GitHub URL for the check run detail page
	StartedAt  time.Time
	Duration   time.Duration
}

// ReviewStatus holds the aggregated review status for a PR.
type ReviewStatus struct {
	State            string // "approved", "changes_requested", "pending"
	Approved         int
	Pending          int
	ChangesRequested int
}

// ReviewComment is a single inline comment on a pull request.
type ReviewComment struct {
	Path string
	Body string
	Line int
}

// ReviewThread groups a reviewer's overall state and their inline comments.
// State is one of "APPROVED", "CHANGES_REQUESTED", "COMMENTED", "PENDING".
type ReviewThread struct {
	Reviewer string
	State    string
	Body     string          // review-level comment body (may be empty)
	Comments []ReviewComment // inline file-level comments
}
