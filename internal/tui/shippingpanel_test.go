package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/baton/internal/agent"
	"github.com/devenjarvis/baton/internal/github"
)

func TestRenderShippingPanel_NilEntry(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "ship-it")
	out := renderShippingPanel(sess, nil, 100, 30, 0, 0, nil)
	if !strings.Contains(out, "SHIPPING") {
		t.Errorf("header missing: %q", out)
	}
	if !strings.Contains(ansi.Strip(out), "fetching PR status") {
		t.Errorf("loading message missing: %q", ansi.Strip(out))
	}
}

func TestRenderShippingPanel_WithEntry(t *testing.T) {
	sess := agent.NewSessionForTest("s2", "ship-it")
	entry := &prCacheEntry{
		pr: &github.PRState{
			Number:         42,
			Title:          "Add feature X",
			BaseBranch:     "main",
			MergeableState: "clean",
		},
		checks: &github.CheckStatus{
			State:  "failure",
			Total:  3,
			Failed: 1,
			Runs: []github.CheckRun{
				{Name: "test-suite", Status: "completed", Conclusion: "success", Duration: 90 * time.Second},
				{Name: "lint", Status: "completed", Conclusion: "failure", Duration: 30 * time.Second},
			},
		},
		reviews: &github.ReviewStatus{State: "changes_requested"},
		threads: []github.ReviewThread{
			{
				Reviewer: "alice",
				State:    "CHANGES_REQUESTED",
				Body:     "Please fix the tests.",
				Comments: []github.ReviewComment{
					{Path: "main.go", Body: "nit: rename this", Line: 10},
				},
			},
		},
	}

	out := renderShippingPanel(sess, entry, 100, 40, 0, 0, nil)
	stripped := ansi.Strip(out)

	if !strings.Contains(out, "#42") {
		t.Errorf("PR number missing: %q", out)
	}
	if !strings.Contains(stripped, "Add feature X") {
		t.Errorf("PR title missing: %q", stripped)
	}
	if !strings.Contains(stripped, "test-suite") {
		t.Errorf("check run name missing: %q", stripped)
	}
	if !strings.Contains(stripped, "lint") {
		t.Errorf("failing check run missing: %q", stripped)
	}
	if !strings.Contains(stripped, "alice") {
		t.Errorf("reviewer name missing: %q", stripped)
	}
	if !strings.Contains(stripped, "Please fix the tests") {
		t.Errorf("review body missing: %q", stripped)
	}
	if !strings.Contains(stripped, "main.go") {
		t.Errorf("comment path missing: %q", stripped)
	}
	if !strings.Contains(stripped, "merge") {
		t.Errorf("merge hint missing: %q", stripped)
	}
}

func TestRenderShippingPanel_MergeReady(t *testing.T) {
	sess := agent.NewSessionForTest("s3", "ship-it")
	entry := &prCacheEntry{
		pr:      &github.PRState{Number: 7, MergeableState: "clean"},
		checks:  &github.CheckStatus{State: "success", Total: 2, Passed: 2},
		reviews: &github.ReviewStatus{State: "approved", Approved: 1},
	}
	out := renderShippingPanel(sess, entry, 100, 30, 0, 0, nil)
	stripped := ansi.Strip(out)
	if !strings.Contains(stripped, "Ready") {
		t.Errorf("Ready phrase missing: %q", stripped)
	}
}

func TestRenderShippingPanel_UnknownMergeable(t *testing.T) {
	sess := agent.NewSessionForTest("s4", "ship-it")
	entry := &prCacheEntry{
		pr: &github.PRState{
			Number:         10,
			Title:          "WIP",
			BaseBranch:     "main",
			MergeableState: "unknown",
		},
	}
	out := renderShippingPanel(sess, entry, 100, 30, 0, 0, nil)
	stripped := ansi.Strip(out)
	if !strings.Contains(stripped, "checking") {
		t.Errorf("expected 'checking' for unknown mergeable state: %q", stripped)
	}
	if strings.Contains(stripped, "conflicts") {
		t.Errorf("unexpected 'conflicts' for unknown mergeable state: %q", stripped)
	}
}

func TestRenderShippingPanel_HintsListsTriageKeys(t *testing.T) {
	sess := agent.NewSessionForTest("s-hints", "ship-it")
	entry := &prCacheEntry{
		pr: &github.PRState{
			Number:         1,
			Title:          "T",
			BaseBranch:     "main",
			MergeableState: "clean",
		},
	}
	out := renderShippingPanel(sess, entry, 120, 30, 0, 0, nil)
	stripped := ansi.Strip(out)
	if !strings.Contains(stripped, "a — approve") {
		t.Errorf("hint 'a — approve' missing: %q", stripped)
	}
	if !strings.Contains(stripped, "x — disagree") {
		t.Errorf("hint 'x — disagree' missing: %q", stripped)
	}
	if !strings.Contains(stripped, "n — note") {
		t.Errorf("hint 'n — note' missing: %q", stripped)
	}
}

func TestRenderShippingPanel_TwoPaneFullBody(t *testing.T) {
	sess := agent.NewSessionForTest("s5", "ship-it")
	// 400-char body with no newlines — old truncateVisible would clip it.
	longBody := strings.Repeat("x", 50) + " " + strings.Repeat("y", 50) + " " +
		strings.Repeat("z", 50) + " " + strings.Repeat("a", 50) + " " +
		strings.Repeat("b", 50) + " " + strings.Repeat("c", 50) + " " +
		strings.Repeat("d", 50) + " " + strings.Repeat("e", 7)
	entry := &prCacheEntry{
		pr: &github.PRState{
			Number:         99,
			Title:          "Long body PR",
			BaseBranch:     "main",
			MergeableState: "clean",
		},
		threads: []github.ReviewThread{
			{
				Reviewer: "reviewer1",
				State:    "CHANGES_REQUESTED",
				Body:     longBody,
			},
		},
	}
	out := renderShippingPanel(sess, entry, 120, 40, 0, 0, nil)
	stripped := ansi.Strip(out)

	// Reviewer name appears in left pane.
	if !strings.Contains(stripped, "reviewer1") {
		t.Errorf("reviewer name missing from left pane: %q", stripped)
	}
	// A substring from the *end* of the body that truncateVisible would have dropped.
	// The old width-8 truncateVisible on a 100-wide panel would cut at ~92 chars.
	// We look for content from the last word (8 e's).
	if !strings.Contains(stripped, "eeeeeee") {
		t.Errorf("full body not shown — late content missing: %q", stripped)
	}
}

func TestFeedbackItems_FlattensThreadsAndComments(t *testing.T) {
	threads := []github.ReviewThread{
		{
			Reviewer: "alice",
			State:    "CHANGES_REQUESTED",
			Body:     "please fix",
			Comments: []github.ReviewComment{
				{ID: 101, Path: "a.go", Body: "nit one", Line: 5},
				{ID: 202, Path: "b.go", Body: "nit two", Line: 10},
			},
		},
	}
	items := feedbackItems(threads)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d: %+v", len(items), items)
	}
	// First: thread body item.
	if items[0].Body != "please fix" || items[0].IsInline || items[0].Reviewer != "alice" {
		t.Errorf("item[0] unexpected: %+v", items[0])
	}
	if k := feedbackItemKey(items[0]); k != "thread:alice" {
		t.Errorf("item[0] key = %q, want thread:alice", k)
	}
	// Second: inline comment 101.
	if !items[1].IsInline || items[1].CommentID != 101 {
		t.Errorf("item[1] unexpected: %+v", items[1])
	}
	if k := feedbackItemKey(items[1]); k != "comment:101" {
		t.Errorf("item[1] key = %q, want comment:101", k)
	}
	// Third: inline comment 202.
	if !items[2].IsInline || items[2].CommentID != 202 {
		t.Errorf("item[2] unexpected: %+v", items[2])
	}
	if k := feedbackItemKey(items[2]); k != "comment:202" {
		t.Errorf("item[2] key = %q, want comment:202", k)
	}
}

func TestFeedbackItems_SkipsEmptyThreadBody(t *testing.T) {
	threads := []github.ReviewThread{
		{
			Reviewer: "bob",
			State:    "APPROVED",
			Body:     "  ",
			Comments: []github.ReviewComment{
				{ID: 5, Path: "x.go", Body: "nice", Line: 1},
			},
		},
	}
	items := feedbackItems(threads)
	// Only the inline comment, not the whitespace-only body.
	if len(items) != 1 {
		t.Fatalf("expected 1 item (body skipped), got %d", len(items))
	}
	if items[0].CommentID != 5 {
		t.Errorf("expected inline comment ID 5, got %+v", items[0])
	}
}

func TestRenderCheckRow_DurationFormat(t *testing.T) {
	run := github.CheckRun{
		Name:       "my-check",
		Status:     "completed",
		Conclusion: "success",
		StartedAt:  time.Now().Add(-90 * time.Second),
		Duration:   90 * time.Second,
	}
	out := ansi.Strip(renderCheckRow(run, 80))
	if !strings.Contains(out, "1m 30s") {
		t.Errorf("duration format wrong: %q", out)
	}
}
