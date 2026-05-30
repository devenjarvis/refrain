package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/devenjarvis/refrain/internal/github"
)

// rowStatePhrase returns a human-readable badge for a session's shipping row.
// Priority: merge-ready > conflicts > changes-requested > CI failure > CI pending.
func rowStatePhrase(entry *prCacheEntry) string {
	if entry == nil || entry.pr == nil {
		return ""
	}
	if isMergeReady(entry) {
		return "Ready"
	}
	if entry.pr.MergeableState == "dirty" {
		return "Conflicts"
	}
	if entry.reviews != nil && entry.reviews.State == "changes_requested" {
		return "Changes requested"
	}
	if entry.checks != nil {
		switch entry.checks.State {
		case "failure":
			return fmt.Sprintf("CI %d/%d failing", entry.checks.Failed, entry.checks.Total)
		case "pending":
			return "Waiting on CI"
		}
	}
	return ""
}

// prPollMsg carries the result of an async PR status poll.
//
// Three result shapes are possible and must be distinguished by the handler:
//   - err != nil: the fetch failed (transient). Preserve cache; shorten next poll.
//   - err == nil, pr == nil: the lookup succeeded and no open PR exists
//     (newly opened session, or PR was closed/merged).
//   - err == nil, pr != nil: update cache.
type prPollMsg struct {
	sessionID string
	repoPath  string
	pr        *github.PRState
	checks    *github.CheckStatus
	reviews   *github.ReviewStatus
	threads   []github.ReviewThread
	// stack holds base PRs for stacked-branch workflows, ordered from
	// immediate parent (index 0) to root. Empty for non-stacked sessions.
	stack []*prCacheEntry
	err   error
}

// prCacheEntry holds cached PR and check status for a session.
type prCacheEntry struct {
	pr      *github.PRState
	checks  *github.CheckStatus
	reviews *github.ReviewStatus
	threads []github.ReviewThread
	// stack holds base PRs for stacked-branch workflows, ordered from
	// immediate parent (index 0) to root. Nil for non-stacked sessions.
	// All code checking entry.pr != nil continues to work unchanged.
	stack []*prCacheEntry
}

// prSessionState tracks per-session polling state for adaptive PR polling.
type prSessionState struct {
	lastPoll       time.Time
	lastSHACheck   time.Time
	lastCheckState string // "success", "failure", "pending", ""
	lastRemoteSHA  string
	flashUntil     time.Time
	flashColor     string // "success" or "error"
	inFlight       bool
	// burstUntil, when set in the future, causes prPollInterval to return a
	// short (~2s) cadence so events like branch rename or new push are picked
	// up quickly. Writes happen only from the Bubble Tea Update goroutine;
	// no locking required.
	burstUntil time.Time
	// consecutiveNilPolls counts how many back-to-back polls returned nil (no
	// open PR). A cached entry is only evicted after 2 consecutive nils so a
	// single nil during the rename-gap or a rapid force-push window does not
	// blank the UI.
	consecutiveNilPolls int
}

// isMergeReady returns true when all conditions for merge readiness are met.
// Requires at least one approved review — repos with no required reviewers will
// never show "Ready" and must use force-merge (M) instead of gated merge (m).
func isMergeReady(entry *prCacheEntry) bool {
	if entry == nil || entry.pr == nil {
		return false
	}
	// Require at least one check to prevent premature "Ready" display while CI
	// is still initializing (API may briefly return zero check runs).
	checksOK := entry.checks != nil && entry.checks.State == "success" && entry.checks.Total > 0
	reviewsOK := entry.reviews != nil && entry.reviews.State == "approved"
	mergeable := entry.pr.MergeableState == "clean"
	return checksOK && reviewsOK && mergeable
}

// checkSymbolFor returns the colored check symbol for a checks state.
func checkSymbolFor(checks *github.CheckStatus) string {
	if checks == nil {
		return ""
	}
	var sym string
	var style lipgloss.Style
	switch checks.State {
	case "success":
		sym = "\u2713"
		style = StyleSuccess
	case "failure":
		sym = "\u2717"
		style = StyleError
	case "pending":
		sym = "\u25cb"
		style = StyleWarning
	default:
		sym = "?"
		style = StyleSubtle
	}
	return style.Render(sym)
}

// prIndicator returns a compact colored string for the session row.
// For stacked PRs it emits a chain format: "#101 \u2713 \u2192 #102 \u25cb".
// Returns empty string if no PR data exists.
func prIndicator(entry *prCacheEntry) string {
	if entry == nil || entry.pr == nil {
		return ""
	}

	// Non-stacked: simple "#N symbol [phrase]" format.
	if len(entry.stack) == 0 {
		prNum := StyleLink.Render(fmt.Sprintf("#%d", entry.pr.Number))
		sym := checkSymbolFor(entry.checks)
		result := prNum
		if sym != "" {
			result += " " + sym
		}
		if phrase := rowStatePhrase(entry); phrase != "" {
			result += " " + statePhraseStyle(phrase).Render(phrase)
		}
		return result
	}

	// Stacked: emit base-first chain. entry.stack is ordered immediate-parent
	// (index 0) to root, so reversing gives root-to-head order.
	sep := StyleSubtle.Render(" \u2192 ")
	var parts []string
	for i := len(entry.stack) - 1; i >= 0; i-- {
		base := entry.stack[i]
		if base.pr == nil {
			continue
		}
		num := StyleLink.Render(fmt.Sprintf("#%d", base.pr.Number))
		sym := checkSymbolFor(base.checks)
		if sym != "" {
			parts = append(parts, num+" "+sym)
		} else {
			parts = append(parts, num)
		}
	}
	// Append the head PR.
	headNum := StyleLink.Render(fmt.Sprintf("#%d", entry.pr.Number))
	headSym := checkSymbolFor(entry.checks)
	if headSym != "" {
		parts = append(parts, headNum+" "+headSym)
	} else {
		parts = append(parts, headNum)
	}

	result := strings.Join(parts, sep)
	if phrase := rowStatePhrase(entry); phrase != "" {
		result += " " + statePhraseStyle(phrase).Render(phrase)
	}
	return result
}

// statePhraseStyle returns a lipgloss style appropriate for a row state phrase.
func statePhraseStyle(phrase string) lipgloss.Style {
	switch phrase {
	case "Ready":
		return StyleSuccess
	case "Conflicts", "Changes requested":
		return StyleError
	case "Waiting on CI":
		return StyleWarning
	default: // "CI N/M failing"
		return StyleError
	}
}

// resolveMergedFallback fetches the authoritative PR state for a cached PR when
// the open-only poll returns nil. It is only called for Shipping sessions so that
// Building/Reviewing sessions retain today's 2-nil eviction behaviour.
// Returns the PR if State is "merged" or "closed"; otherwise returns nil.
func resolveMergedFallback(ctx context.Context, owner, repo string, cachedPRNumber int, refresh func(context.Context, string, string, int) (*github.PRState, error)) *github.PRState {
	if cachedPRNumber <= 0 {
		return nil
	}
	pr, err := refresh(ctx, owner, repo, cachedPRNumber)
	if err != nil || pr == nil {
		return nil
	}
	if pr.State == "merged" || pr.State == "closed" {
		return pr
	}
	return nil
}

// prIndicatorWidth returns the approximate visible width of prIndicator output
// for the given entry. Used for layout calculations in dashboard rendering.
func prIndicatorWidth(entry *prCacheEntry) int {
	if entry == nil || entry.pr == nil {
		return 0
	}
	// Head PR: "#N" + optional " symbol" + optional " phrase"
	w := 1 + len(fmt.Sprintf("%d", entry.pr.Number))
	if entry.checks != nil {
		w += 2 // " symbol"
	}
	if phrase := rowStatePhrase(entry); phrase != "" {
		w += 1 + len(phrase)
	}
	// Base levels: each adds " \u2192 #N" + optional " symbol"
	for _, base := range entry.stack {
		if base.pr == nil {
			continue
		}
		w += 3 // " \u2192 "
		w += 1 + len(fmt.Sprintf("%d", base.pr.Number))
		if base.checks != nil {
			w += 2
		}
	}
	return w
}
