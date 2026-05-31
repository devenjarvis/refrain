package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/github"
)

// TestPrPollInterval_BurstOverridesBaseline verifies the burst window shortens
// the poll interval to 2s regardless of the adaptive baseline.
func TestPrPollInterval_BurstOverridesBaseline(t *testing.T) {
	now := time.Now()
	if got := prPollInterval(now, now.Add(30*time.Second), nil, ""); got != 2*time.Second {
		t.Fatalf("burst interval = %v, want 2s", got)
	}
}

func TestPrPollInterval_ExpiredBurstFallsBackToBaseline(t *testing.T) {
	now := time.Now()
	if got := prPollInterval(now, now.Add(-5*time.Second), nil, ""); got != 30*time.Second {
		t.Fatalf("expired burst should use 30s baseline, got %v", got)
	}
}

// TestPrPollInterval_Matrix exercises the adaptive interval across the
// burst → CI-pending → after-push → stable progression. Since prPollInterval
// is a pure free function, each case is a direct call with explicit inputs.
func TestPrPollInterval_Matrix(t *testing.T) {
	now := time.Now()
	pendingChecks := &prCacheEntry{
		pr:     &github.PRState{Number: 1},
		checks: &github.CheckStatus{State: "pending"},
	}
	successChecks := &prCacheEntry{
		pr:     &github.PRState{Number: 1},
		checks: &github.CheckStatus{State: "success"},
	}
	noChecks := &prCacheEntry{pr: &github.PRState{Number: 1}}

	cases := []struct {
		name          string
		burstUntil    time.Time
		entry         *prCacheEntry
		lastRemoteSHA string
		want          time.Duration
	}{
		{"burst wins over pending PR", now.Add(30 * time.Second), pendingChecks, "abc", PRPollDuringBurst},
		{"no entry, no push → stable", time.Time{}, nil, "", PRPollStable},
		{"no entry, pushed → after-push", time.Time{}, nil, "abc", PRPollAfterPush},
		{"entry with nil pr, pushed → after-push", time.Time{}, &prCacheEntry{}, "abc", PRPollAfterPush},
		{"PR with pending CI → CI-pending", time.Time{}, pendingChecks, "abc", PRPollCIPending},
		{"PR with success CI → stable", time.Time{}, successChecks, "abc", PRPollStable},
		{"PR with no checks → stable", time.Time{}, noChecks, "abc", PRPollStable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := prPollInterval(now, tc.burstUntil, tc.entry, tc.lastRemoteSHA); got != tc.want {
				t.Fatalf("prPollInterval = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCachedPRNumberForFallback covers the Shipping-gated fallback-number
// extraction: only Shipping sessions with a cached open PR yield a number.
func TestCachedPRNumberForFallback(t *testing.T) {
	shipping := agent.NewSessionForTest("s1", "shipping")
	shipping.SetLifecyclePhase(agent.LifecycleShipping)
	building := agent.NewSessionForTest("s2", "building")
	building.SetLifecyclePhase(agent.LifecycleInProgress)
	withPR := &prCacheEntry{pr: &github.PRState{Number: 42}}

	cases := []struct {
		name  string
		sess  *agent.Session
		entry *prCacheEntry
		want  int
	}{
		{"shipping with cached PR", shipping, withPR, 42},
		{"shipping, nil entry", shipping, nil, 0},
		{"shipping, entry without pr", shipping, &prCacheEntry{}, 0},
		{"non-shipping with cached PR", building, withPR, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cachedPRNumberForFallback(tc.sess, tc.entry); got != tc.want {
				t.Fatalf("cachedPRNumberForFallback = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestBranchRenamedEventArmsBurst verifies that feeding an EventBranchRenamed
// via agentEventMsg sets burstUntil in the future and resets SHA/poll state
// so the next tick re-queries immediately.
func TestBranchRenamedEventArmsBurst(t *testing.T) {
	a := NewApp()
	const repo = "/repo"
	key := cacheKey(repo, "sess-1")
	// Seed prior state so we can verify the handler resets it.
	a.prPollStates[key] = &prSessionState{
		lastPoll:      time.Now(),
		lastSHACheck:  time.Now(),
		lastRemoteSHA: "oldsha",
	}

	model, _ := a.Update(agentEventMsg{
		repoPath: repo,
		event: agent.Event{
			Type:      agent.EventBranchRenamed,
			SessionID: "sess-1",
			Branch:    "refrain/new-name",
		},
	})
	got := model.(App).prPollStates[key]
	if got == nil {
		t.Fatal("prPollStates missing after event")
	}
	if !got.burstUntil.After(time.Now().Add(50 * time.Second)) {
		t.Errorf("burstUntil should be ~60s in the future, got %v", got.burstUntil)
	}
	if !got.lastPoll.IsZero() {
		t.Errorf("lastPoll should be reset, got %v", got.lastPoll)
	}
	if got.lastRemoteSHA != "" {
		t.Errorf("lastRemoteSHA should be cleared, got %q", got.lastRemoteSHA)
	}
}

// TestPrPollMsg_ErrorPreservesCache verifies that a fetch error does not
// clobber a previously-cached PR entry.
func TestPrPollMsg_ErrorPreservesCache(t *testing.T) {
	a := NewApp()
	const repo = "/repo"
	key := cacheKey(repo, "sess-1")
	a.prPollsInFlight = 1
	a.prPollStates[key] = &prSessionState{inFlight: true}
	prev := &prCacheEntry{}
	a.prCache[key] = prev

	model, _ := a.Update(prPollMsg{sessionID: "sess-1", repoPath: repo, err: errors.New("boom")})
	got := model.(App)
	if got.prCache[key] != prev {
		t.Errorf("cache entry was clobbered on error")
	}
	if got.prPollStates[key].inFlight {
		t.Errorf("inFlight should be cleared after poll result")
	}
	if got.prPollsInFlight != 0 {
		t.Errorf("prPollsInFlight = %d, want 0", got.prPollsInFlight)
	}
}

// TestPrPollMsg_NilGracePeriod verifies the 2-consecutive-nil grace period:
// the first nil preserves the cache entry, the second nil evicts it.
func TestPrPollMsg_NilGracePeriod(t *testing.T) {
	a := NewApp()
	const repo = "/repo"
	key := cacheKey(repo, "sess-1")
	a.prPollsInFlight = 1
	a.prPollStates[key] = &prSessionState{inFlight: true, lastCheckState: "success"}
	a.prCache[key] = &prCacheEntry{}

	// First nil: cache should be preserved.
	model, _ := a.Update(prPollMsg{sessionID: "sess-1", repoPath: repo})
	got := model.(App)
	if _, ok := got.prCache[key]; !ok {
		t.Errorf("cache entry should be preserved on first nil poll (grace period)")
	}
	if got.prPollStates[key].consecutiveNilPolls != 1 {
		t.Errorf("consecutiveNilPolls = %d, want 1", got.prPollStates[key].consecutiveNilPolls)
	}

	// Second nil: cache should be cleared.
	got.prPollsInFlight = 1
	got.prPollStates[key].inFlight = true
	model2, _ := got.Update(prPollMsg{sessionID: "sess-1", repoPath: repo})
	got2 := model2.(App)
	if _, ok := got2.prCache[key]; ok {
		t.Errorf("cache entry should be cleared after second consecutive nil poll")
	}
	if got2.prPollStates[key].lastCheckState != "" {
		t.Errorf("lastCheckState should reset, got %q", got2.prPollStates[key].lastCheckState)
	}
	if got2.prPollStates[key].consecutiveNilPolls != 0 {
		t.Errorf("consecutiveNilPolls should reset to 0 after eviction, got %d", got2.prPollStates[key].consecutiveNilPolls)
	}
}

// TestResolveMergedFallback verifies the six cases of the merged-fallback helper.
func TestResolveMergedFallback(t *testing.T) {
	ctx := context.Background()
	mergedPR := &github.PRState{State: "merged", Number: 42}
	closedPR := &github.PRState{State: "closed", Number: 43}
	openPR := &github.PRState{State: "open", Number: 44}

	tests := []struct {
		name           string
		cachedPRNumber int
		refreshReturn  *github.PRState
		refreshErr     error
		want           *github.PRState
		wantCalled     bool
	}{
		{
			name:           "zero cachedPRNumber skips refresh",
			cachedPRNumber: 0,
			wantCalled:     false,
			want:           nil,
		},
		{
			name:           "refresh error returns nil",
			cachedPRNumber: 42,
			refreshErr:     errors.New("network error"),
			wantCalled:     true,
			want:           nil,
		},
		{
			name:           "refresh returns nil PR returns nil",
			cachedPRNumber: 42,
			refreshReturn:  nil,
			wantCalled:     true,
			want:           nil,
		},
		{
			name:           "open PR returns nil",
			cachedPRNumber: 44,
			refreshReturn:  openPR,
			wantCalled:     true,
			want:           nil,
		},
		{
			name:           "merged PR is returned",
			cachedPRNumber: 42,
			refreshReturn:  mergedPR,
			wantCalled:     true,
			want:           mergedPR,
		},
		{
			name:           "closed PR is returned",
			cachedPRNumber: 43,
			refreshReturn:  closedPR,
			wantCalled:     true,
			want:           closedPR,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			refresh := func(_ context.Context, _, _ string, _ int) (*github.PRState, error) {
				called = true
				return tc.refreshReturn, tc.refreshErr
			}
			got := resolveMergedFallback(ctx, "owner", "repo", tc.cachedPRNumber, refresh)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
			if called != tc.wantCalled {
				t.Errorf("refresh called = %v, want %v", called, tc.wantCalled)
			}
		})
	}
}

// TestPrPollMsg_NilThenSuccessResetsCounter verifies that a successful poll
// after a nil resets the consecutiveNilPolls counter so the grace period
// starts fresh on the next nil.
func TestPrPollMsg_NilThenSuccessResetsCounter(t *testing.T) {
	a := NewApp()
	const repo = "/repo"
	key := cacheKey(repo, "sess-1")
	a.prPollsInFlight = 1
	a.prPollStates[key] = &prSessionState{inFlight: true}
	a.prCache[key] = &prCacheEntry{}

	// First nil: increments counter.
	model, _ := a.Update(prPollMsg{sessionID: "sess-1", repoPath: repo})
	got := model.(App)
	if got.prPollStates[key].consecutiveNilPolls != 1 {
		t.Fatalf("consecutiveNilPolls after first nil = %d, want 1", got.prPollStates[key].consecutiveNilPolls)
	}

	// Successful poll: counter resets.
	got.prPollsInFlight = 1
	got.prPollStates[key].inFlight = true
	model2, _ := got.Update(prPollMsg{sessionID: "sess-1", repoPath: repo, pr: &github.PRState{Number: 1}})
	got2 := model2.(App)
	if got2.prPollStates[key].consecutiveNilPolls != 0 {
		t.Errorf("consecutiveNilPolls should reset to 0 after success, got %d", got2.prPollStates[key].consecutiveNilPolls)
	}
}

// TestPrPollMsg_NilWithNoPriorCacheIsNoop verifies that a successful empty
// lookup for a session that never had a PR doesn't create spurious state.
func TestPrPollMsg_NilWithNoPriorCacheIsNoop(t *testing.T) {
	a := NewApp()
	const repo = "/repo"
	key := cacheKey(repo, "sess-1")
	a.prPollsInFlight = 1
	a.prPollStates[key] = &prSessionState{inFlight: true}

	model, _ := a.Update(prPollMsg{sessionID: "sess-1", repoPath: repo})
	got := model.(App)
	if _, ok := got.prCache[key]; ok {
		t.Errorf("no cache entry should exist")
	}
	if got.prPollStates[key].inFlight {
		t.Errorf("inFlight should be cleared")
	}
}

// TestPrIndicator_Stacked verifies the chain format and that prIndicatorWidth
// agrees with the rendered visible length.
func TestPrIndicator_Stacked(t *testing.T) {
	base := &prCacheEntry{
		pr:     &github.PRState{Number: 101},
		checks: &github.CheckStatus{State: "success"},
	}
	head := &prCacheEntry{
		pr:     &github.PRState{Number: 102},
		checks: &github.CheckStatus{State: "pending"},
		stack:  []*prCacheEntry{base},
	}

	indicator := prIndicator(head)
	if indicator == "" {
		t.Fatal("prIndicator returned empty string for stacked entry")
	}
	// Indicator must contain both PR numbers.
	if !strings.Contains(indicator, "101") {
		t.Errorf("indicator missing base PR number: %q", indicator)
	}
	if !strings.Contains(indicator, "102") {
		t.Errorf("indicator missing head PR number: %q", indicator)
	}

	// prIndicatorWidth must be > single-PR width.
	singleWidth := prIndicatorWidth(&prCacheEntry{
		pr:     &github.PRState{Number: 102},
		checks: &github.CheckStatus{State: "pending"},
	})
	stackedWidth := prIndicatorWidth(head)
	if stackedWidth <= singleWidth {
		t.Errorf("stacked width %d should be > single width %d", stackedWidth, singleWidth)
	}
}

// TestPrIndicator_NonStacked verifies that a non-stacked entry is unchanged.
func TestPrIndicator_NonStacked(t *testing.T) {
	entry := &prCacheEntry{
		pr:     &github.PRState{Number: 42},
		checks: &github.CheckStatus{State: "success"},
	}
	indicator := prIndicator(entry)
	if !strings.Contains(indicator, "42") {
		t.Errorf("single-PR indicator missing PR number: %q", indicator)
	}
	// No separator arrow in non-stacked case.
	if strings.Contains(indicator, "→") {
		t.Errorf("non-stacked indicator should not contain separator: %q", indicator)
	}
}

// TestRowStatePhrase verifies the state phrase selector for shipping row badges.
func TestRowStatePhrase(t *testing.T) {
	cases := []struct {
		name  string
		entry *prCacheEntry
		want  string
	}{
		{
			name:  "nil entry",
			entry: nil,
			want:  "",
		},
		{
			name:  "no pr",
			entry: &prCacheEntry{},
			want:  "",
		},
		{
			name: "merge ready",
			entry: &prCacheEntry{
				pr:      &github.PRState{MergeableState: "clean"},
				checks:  &github.CheckStatus{State: "success", Total: 1, Passed: 1},
				reviews: &github.ReviewStatus{State: "approved", Approved: 1},
			},
			want: "Ready",
		},
		{
			name: "conflicts",
			entry: &prCacheEntry{
				pr:      &github.PRState{MergeableState: "dirty"},
				checks:  &github.CheckStatus{State: "success", Total: 1, Passed: 1},
				reviews: &github.ReviewStatus{State: "approved", Approved: 1},
			},
			want: "Conflicts",
		},
		{
			name: "changes requested",
			entry: &prCacheEntry{
				pr:      &github.PRState{MergeableState: "clean"},
				reviews: &github.ReviewStatus{State: "changes_requested"},
			},
			want: "Changes requested",
		},
		{
			name: "ci failing",
			entry: &prCacheEntry{
				pr:     &github.PRState{MergeableState: "clean"},
				checks: &github.CheckStatus{State: "failure", Failed: 2, Total: 5},
			},
			want: "CI 2/5 failing",
		},
		{
			name: "waiting on ci",
			entry: &prCacheEntry{
				pr:     &github.PRState{MergeableState: "clean"},
				checks: &github.CheckStatus{State: "pending"},
			},
			want: "Waiting on CI",
		},
		{
			name: "unknown falls through to CI pending",
			entry: &prCacheEntry{
				pr:     &github.PRState{MergeableState: "unknown"},
				checks: &github.CheckStatus{State: "pending"},
			},
			want: "Waiting on CI",
		},
		{
			name: "unknown with no other signal",
			entry: &prCacheEntry{
				pr: &github.PRState{MergeableState: "unknown"},
			},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rowStatePhrase(tc.entry)
			if got != tc.want {
				t.Errorf("rowStatePhrase = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPrPollMsg_UnknownArmsBurst verifies that a prPollMsg with MergeableState
// "unknown" or "" arms the 15s burst window on the poll state.
func TestPrPollMsg_UnknownArmsBurst(t *testing.T) {
	cases := []struct {
		name           string
		mergeableState string
	}{
		{"unknown", "unknown"},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := NewApp()
			const repo = "/repo"
			key := cacheKey(repo, "s1")
			a.prPollsInFlight = 1
			a.prPollStates[key] = &prSessionState{inFlight: true}

			model, _ := a.Update(prPollMsg{
				sessionID: "s1",
				repoPath:  repo,
				pr:        &github.PRState{Number: 1, MergeableState: tc.mergeableState},
			})
			got := model.(App).prPollStates[key]
			if got == nil {
				t.Fatal("prPollStates missing after update")
			}
			if !got.burstUntil.After(time.Now().Add(14 * time.Second)) {
				t.Errorf("burstUntil should be >14s in the future, got %v", got.burstUntil)
			}
		})
	}
}

// TestPrPollMsg_KnownDoesNotArmBurst verifies that a prPollMsg with a known
// mergeable state (e.g. "clean") does not arm the burst window.
func TestPrPollMsg_KnownDoesNotArmBurst(t *testing.T) {
	a := NewApp()
	const repo = "/repo"
	key := cacheKey(repo, "s1")
	a.prPollsInFlight = 1
	a.prPollStates[key] = &prSessionState{inFlight: true}

	model, _ := a.Update(prPollMsg{
		sessionID: "s1",
		repoPath:  repo,
		pr:        &github.PRState{Number: 1, MergeableState: "clean"},
	})
	got := model.(App).prPollStates[key]
	if got == nil {
		t.Fatal("prPollStates missing after update")
	}
	if got.burstUntil.After(time.Now()) {
		t.Errorf("burstUntil should not be armed for known state, got %v", got.burstUntil)
	}
}

// TestPrIndicator_StatePhrase verifies that prIndicator surfaces the state phrase.
func TestPrIndicator_StatePhrase(t *testing.T) {
	entry := &prCacheEntry{
		pr:     &github.PRState{Number: 7, MergeableState: "clean"},
		checks: &github.CheckStatus{State: "failure", Failed: 1, Total: 3},
	}
	ind := prIndicator(entry)
	if !strings.Contains(ind, "CI 1/3 failing") {
		t.Errorf("prIndicator = %q, want to contain CI phrase", ind)
	}
}

// Ensure the test file participates in the package even when the above tests
// are filtered out via -run.
var _ = tea.Batch
