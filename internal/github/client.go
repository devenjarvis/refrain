package github

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sort"
	"strconv"
	"time"

	gh "github.com/google/go-github/v69/github"
)

// Client wraps the GitHub API client with methods for PR and check operations.
type Client struct {
	gh *gh.Client
	// sleep is injected for testability. Defaults to time.After-based sleep
	// that honors ctx cancellation.
	sleep func(ctx context.Context, d time.Duration) error
}

// NewClient creates a new GitHub API client using a token from GetToken().
func NewClient() (*Client, error) {
	token, err := GetToken()
	if err != nil {
		return nil, err
	}

	client := gh.NewClient(nil).WithAuthToken(token)
	return &Client{gh: client, sleep: ctxSleep}, nil
}

// ctxSleep blocks for d or until ctx is cancelled, whichever is first.
func ctxSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// retry budget: 1 initial attempt + up to 2 retries.
var defaultRetryBackoffs = []time.Duration{500 * time.Millisecond, 2 * time.Second}

// maxRetryWait caps server-suggested Retry-After values so an egregious
// Retry-After header can't hang a poll for minutes.
const maxRetryWait = 30 * time.Second

// doWithRetry invokes op, retrying on 5xx, 429, and typed rate-limit errors.
// It honors Retry-After for 429 responses and the typed errors' hints,
// applies a small jitter to backoff, and respects ctx cancellation.
// 4xx (other than 429) and 422 are not retried.
func (c *Client) doWithRetry(ctx context.Context, op func() (*gh.Response, error)) error {
	var lastErr error
	for attempt := 0; attempt <= len(defaultRetryBackoffs); attempt++ {
		resp, err := op()
		if err == nil {
			return nil
		}
		lastErr = err

		retriable, wait := classifyRetry(resp, err)
		if !retriable || attempt == len(defaultRetryBackoffs) {
			return err
		}

		if wait == 0 {
			wait = defaultRetryBackoffs[attempt]
		}
		// ±100ms jitter (bounded to wait).
		jitter := time.Duration(rand.Int64N(int64(200*time.Millisecond))) - 100*time.Millisecond
		wait += jitter
		if wait < 0 {
			wait = 0
		}
		if wait > maxRetryWait {
			wait = maxRetryWait
		}
		if err := c.sleep(ctx, wait); err != nil {
			return err
		}
	}
	return lastErr
}

// classifyRetry returns whether the error is retriable and an optional
// server-suggested wait duration.
func classifyRetry(resp *gh.Response, err error) (retriable bool, wait time.Duration) {
	var rate *gh.RateLimitError
	if errors.As(err, &rate) {
		if !rate.Rate.Reset.IsZero() {
			wait = time.Until(rate.Rate.Reset.Time)
			if wait < 0 {
				wait = 0
			}
		}
		return true, wait
	}
	var abuse *gh.AbuseRateLimitError
	if errors.As(err, &abuse) {
		if abuse.RetryAfter != nil {
			wait = *abuse.RetryAfter
		}
		return true, wait
	}
	if resp == nil || resp.Response == nil {
		// Transport-level failure (no HTTP response) — retry once.
		return true, 0
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, perr := strconv.Atoi(ra); perr == nil {
				wait = time.Duration(secs) * time.Second
			}
		}
		return true, wait
	case http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true, 0
	}
	return false, 0
}

// isNotFoundOrUnprocessable returns true for 404/422 responses — used by
// SHA-based PR lookup to treat a pre-push SHA as "no PR", not an error.
func isNotFoundOrUnprocessable(resp *gh.Response) bool {
	if resp == nil || resp.Response == nil {
		return false
	}
	return resp.StatusCode == http.StatusNotFound ||
		resp.StatusCode == http.StatusUnprocessableEntity
}

// getPRDetail fetches the singular PR endpoint for the given number, which
// populates mergeable_state (always null from list and create endpoints).
// Returns an error if the fetch fails after retries; callers fall back to
// their list/create result with MergeableState == "".
func (c *Client) getPRDetail(ctx context.Context, owner, repo string, number int) (*gh.PullRequest, error) {
	var pr *gh.PullRequest
	err := c.doWithRetry(ctx, func() (*gh.Response, error) {
		var resp *gh.Response
		var err error
		pr, resp, err = c.gh.PullRequests.Get(ctx, owner, repo, number)
		return resp, err
	})
	if err != nil {
		return nil, err
	}
	return pr, nil
}

// GetPR finds an open pull request for the given head branch.
// Returns nil (not an error) if no open PR exists for the branch.
func (c *Client) GetPR(ctx context.Context, owner, repo, branch string) (*PRState, error) {
	var prs []*gh.PullRequest
	err := c.doWithRetry(ctx, func() (*gh.Response, error) {
		var resp *gh.Response
		var err error
		prs, resp, err = c.gh.PullRequests.List(ctx, owner, repo, &gh.PullRequestListOptions{
			Head:  owner + ":" + branch,
			State: "open",
			ListOptions: gh.ListOptions{
				PerPage: 1,
			},
		})
		return resp, err
	})
	if err != nil {
		return nil, fmt.Errorf("listing PRs: %w", err)
	}

	if len(prs) == 0 {
		return nil, nil
	}

	if detail, err := c.getPRDetail(ctx, owner, repo, prs[0].GetNumber()); err == nil {
		return prToState(detail), nil
	}
	return prToState(prs[0]), nil
}

// GetPRBySHA finds the open PR associated with a commit SHA. This is invariant
// to branch renames: after a local `git branch -m`, the commit's association
// with its PR on GitHub is preserved, so SHA lookup still finds it.
//
// Semantics:
//   - fork PRs (head repo != owner/repo) are filtered out.
//   - only open PRs are returned; closed/merged PRs are ignored.
//   - 404/422 (commit not yet pushed to GitHub) return (nil, nil), not an error.
func (c *Client) GetPRBySHA(ctx context.Context, owner, repo, sha string) (*PRState, error) {
	if sha == "" {
		return nil, nil
	}
	var prs []*gh.PullRequest
	var finalResp *gh.Response
	err := c.doWithRetry(ctx, func() (*gh.Response, error) {
		var resp *gh.Response
		var err error
		prs, resp, err = c.gh.PullRequests.ListPullRequestsWithCommit(ctx, owner, repo, sha, &gh.ListOptions{
			PerPage: 10,
		})
		finalResp = resp
		return resp, err
	})
	if err != nil {
		if isNotFoundOrUnprocessable(finalResp) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing PRs for commit: %w", err)
	}

	headRepo := owner + "/" + repo
	for _, pr := range prs {
		// Drop PRs whose head is in a fork repo sharing the SHA.
		if pr.GetHead().GetRepo().GetFullName() != headRepo {
			continue
		}
		if pr.GetState() == "open" {
			if detail, err := c.getPRDetail(ctx, owner, repo, pr.GetNumber()); err == nil {
				return prToState(detail), nil
			}
			return prToState(pr), nil
		}
	}
	return nil, nil
}

// ListPRs returns open pull requests for the given repository (up to 100).
func (c *Client) ListPRs(ctx context.Context, owner, repo string) ([]*PRState, error) {
	var prs []*gh.PullRequest
	err := c.doWithRetry(ctx, func() (*gh.Response, error) {
		var resp *gh.Response
		var err error
		prs, resp, err = c.gh.PullRequests.List(ctx, owner, repo, &gh.PullRequestListOptions{
			State: "open",
			ListOptions: gh.ListOptions{
				PerPage: 100,
			},
		})
		return resp, err
	})
	if err != nil {
		return nil, fmt.Errorf("listing PRs: %w", err)
	}

	states := make([]*PRState, len(prs))
	for i, pr := range prs {
		states[i] = prToState(pr)
	}
	return states, nil
}

// GetChecks returns the combined check status for the given git ref (SHA or branch).
func (c *Client) GetChecks(ctx context.Context, owner, repo, ref string) (*CheckStatus, error) {
	var result *gh.ListCheckRunsResults
	err := c.doWithRetry(ctx, func() (*gh.Response, error) {
		var resp *gh.Response
		var err error
		result, resp, err = c.gh.Checks.ListCheckRunsForRef(ctx, owner, repo, ref, &gh.ListCheckRunsOptions{
			ListOptions: gh.ListOptions{
				PerPage: 100,
			},
		})
		return resp, err
	})
	if err != nil {
		return nil, fmt.Errorf("listing check runs: %w", err)
	}

	status := &CheckStatus{
		Total: result.GetTotal(),
	}

	for _, run := range result.CheckRuns {
		conclusion := run.GetConclusion()
		switch {
		case run.GetStatus() != "completed":
			status.Pending++
		case conclusion == "success" || conclusion == "skipped" || conclusion == "neutral":
			status.Passed++
		default:
			status.Failed++
		}

		cr := CheckRun{
			Name:       run.GetName(),
			Status:     run.GetStatus(),
			Conclusion: conclusion,
			URL:        run.GetHTMLURL(),
		}
		if run.StartedAt != nil {
			cr.StartedAt = run.StartedAt.Time
			if run.CompletedAt != nil {
				cr.Duration = run.CompletedAt.Sub(run.StartedAt.Time)
			} else {
				cr.Duration = time.Since(run.StartedAt.Time)
			}
		}
		status.Runs = append(status.Runs, cr)
	}

	switch {
	case status.Failed > 0:
		status.State = "failure"
	case status.Pending > 0:
		status.State = "pending"
	default:
		status.State = "success"
	}

	return status, nil
}

// listAllReviews fetches all reviews for a pull request, handling pagination.
func (c *Client) listAllReviews(ctx context.Context, owner, repo string, number int) ([]*gh.PullRequestReview, error) {
	var all []*gh.PullRequestReview
	opts := &gh.ListOptions{PerPage: 100}
	for {
		var reviews []*gh.PullRequestReview
		var resp *gh.Response
		err := c.doWithRetry(ctx, func() (*gh.Response, error) {
			var err error
			reviews, resp, err = c.gh.PullRequests.ListReviews(ctx, owner, repo, number, opts)
			return resp, err
		})
		if err != nil {
			return nil, fmt.Errorf("listing reviews: %w", err)
		}
		all = append(all, reviews...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return all, nil
}

// GetReviews returns the aggregated review status for a pull request.
// It deduplicates by user, keeping only the latest review per reviewer.
func (c *Client) GetReviews(ctx context.Context, owner, repo string, number int) (*ReviewStatus, error) {
	allReviews, err := c.listAllReviews(ctx, owner, repo, number)
	if err != nil {
		return nil, err
	}

	// Deduplicate: latest review per user wins.
	latestByUser := make(map[int64]*gh.PullRequestReview)
	for _, r := range allReviews {
		uid := r.GetUser().GetID()
		// COMMENTED state means review was started but not submitted — skip.
		if r.GetState() == "COMMENTED" {
			continue
		}
		if existing, ok := latestByUser[uid]; !ok || r.GetSubmittedAt().After(existing.GetSubmittedAt().Time) {
			latestByUser[uid] = r
		}
	}

	status := &ReviewStatus{}
	for _, r := range latestByUser {
		switch r.GetState() {
		case "APPROVED":
			status.Approved++
		case "CHANGES_REQUESTED":
			status.ChangesRequested++
		default:
			status.Pending++
		}
	}

	switch {
	case status.ChangesRequested > 0:
		status.State = "changes_requested"
	case status.Approved > 0 && status.Pending == 0:
		status.State = "approved"
	default:
		status.State = "pending"
	}

	return status, nil
}

// CreatePR opens a new pull request on GitHub. head is the branch to merge,
// base is the target branch. If draft is true the PR is created as a draft.
// Returns the newly created PRState on success.
func (c *Client) CreatePR(ctx context.Context, owner, repo, head, base, title, body string, draft bool) (*PRState, error) {
	var pr *gh.PullRequest
	err := c.doWithRetry(ctx, func() (*gh.Response, error) {
		var resp *gh.Response
		var err error
		pr, resp, err = c.gh.PullRequests.Create(ctx, owner, repo, &gh.NewPullRequest{
			Title: gh.Ptr(title),
			Body:  gh.Ptr(body),
			Head:  gh.Ptr(head),
			Base:  gh.Ptr(base),
			Draft: gh.Ptr(draft),
		})
		return resp, err
	})
	if err != nil {
		return nil, fmt.Errorf("creating PR: %w", err)
	}
	if detail, err := c.getPRDetail(ctx, owner, repo, pr.GetNumber()); err == nil {
		return prToState(detail), nil
	}
	return prToState(pr), nil
}

// GetReviewThreads returns review threads for a PR grouped by reviewer.
// Each thread combines the reviewer's overall state (APPROVED /
// CHANGES_REQUESTED / COMMENTED) with their inline file-level comments.
func (c *Client) GetReviewThreads(ctx context.Context, owner, repo string, number int) ([]ReviewThread, error) {
	allReviews, err := c.listAllReviews(ctx, owner, repo, number)
	if err != nil {
		return nil, err
	}

	// Determine the latest actionable review per user, keeping COMMENTED only
	// as a fallback for users who haven't formally reviewed.
	latestByUID := make(map[int64]*gh.PullRequestReview)
	for _, r := range allReviews {
		uid := r.GetUser().GetID()
		state := r.GetState()
		existing, ok := latestByUID[uid]
		if !ok {
			latestByUID[uid] = r
			continue
		}
		// Prefer non-COMMENTED states; within the same class keep the latest.
		existingCommented := existing.GetState() == "COMMENTED"
		thisCommented := state == "COMMENTED"
		if existingCommented && !thisCommented {
			latestByUID[uid] = r
			continue
		}
		if !existingCommented && thisCommented {
			continue
		}
		if r.GetSubmittedAt().After(existing.GetSubmittedAt().Time) {
			latestByUID[uid] = r
		}
	}

	// Fetch inline review comments.
	var allComments []*gh.PullRequestComment
	commentOpts := &gh.PullRequestListCommentsOptions{
		ListOptions: gh.ListOptions{PerPage: 100},
	}
	for {
		var comments []*gh.PullRequestComment
		var resp *gh.Response
		err := c.doWithRetry(ctx, func() (*gh.Response, error) {
			var err error
			comments, resp, err = c.gh.PullRequests.ListComments(ctx, owner, repo, number, commentOpts)
			return resp, err
		})
		if err != nil {
			return nil, fmt.Errorf("listing review comments: %w", err)
		}
		allComments = append(allComments, comments...)
		if resp.NextPage == 0 {
			break
		}
		commentOpts.Page = resp.NextPage
	}

	// Group inline comments by reviewer login.
	commentsByLogin := make(map[string][]ReviewComment)
	for _, comment := range allComments {
		login := comment.GetUser().GetLogin()
		rc := ReviewComment{
			ID:   comment.GetID(),
			Path: comment.GetPath(),
			Body: comment.GetBody(),
			Line: comment.GetLine(),
		}
		commentsByLogin[login] = append(commentsByLogin[login], rc)
	}

	// Build threads — one per reviewer.
	seen := make(map[string]bool)
	threads := make([]ReviewThread, 0, len(latestByUID))
	for _, r := range latestByUID {
		login := r.GetUser().GetLogin()
		seen[login] = true
		threads = append(threads, ReviewThread{
			Reviewer: login,
			State:    r.GetState(),
			Body:     r.GetBody(),
			Comments: commentsByLogin[login],
		})
	}
	// Include comment-only reviewers not captured via the review list.
	for login, comments := range commentsByLogin {
		if !seen[login] {
			threads = append(threads, ReviewThread{
				Reviewer: login,
				State:    "COMMENTED",
				Comments: comments,
			})
		}
	}
	sort.Slice(threads, func(i, j int) bool {
		return threads[i].Reviewer < threads[j].Reviewer
	})
	return threads, nil
}

// RefreshPR fetches the latest PR state by number, including mergeable_state
// (always populated by the singular PR endpoint, unlike List/Create). Used to
// re-validate a PR is still mergeable immediately before MergePR — the
// pr-poller's cached state can be seconds stale, and CI/conflicts can change
// in that window. Returns (nil, nil) if the PR no longer exists.
func (c *Client) RefreshPR(ctx context.Context, owner, repo string, number int) (*PRState, error) {
	pr, err := c.getPRDetail(ctx, owner, repo, number)
	if err != nil {
		return nil, err
	}
	if pr == nil {
		return nil, nil
	}
	return prToState(pr), nil
}

// MergePR merges the given pull request using the specified method.
// method must be one of "merge", "squash", or "rebase". Defaults to "squash".
// Merge is not idempotent so this bypasses doWithRetry to avoid a false-failure
// when a transport error after a successful merge triggers a retry that gets 405.
func (c *Client) MergePR(ctx context.Context, owner, repo string, number int, method string) error {
	switch method {
	case "merge", "squash", "rebase":
	default:
		method = "squash"
	}
	_, _, err := c.gh.PullRequests.Merge(ctx, owner, repo, number, "", &gh.PullRequestOptions{
		MergeMethod: method,
	})
	return err
}

// prToState converts a GitHub API PullRequest to our PRState type.
func prToState(pr *gh.PullRequest) *PRState {
	state := pr.GetState()
	if pr.GetMerged() {
		state = "merged"
	}

	return &PRState{
		Number:         pr.GetNumber(),
		Title:          pr.GetTitle(),
		URL:            pr.GetHTMLURL(),
		State:          state,
		MergeableState: pr.GetMergeableState(),
		Draft:          pr.GetDraft(),
		HeadBranch:     pr.GetHead().GetRef(),
		BaseBranch:     pr.GetBase().GetRef(),
	}
}
