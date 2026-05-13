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
	out := renderShippingPanel(sess, nil, 100, 30)
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

	out := renderShippingPanel(sess, entry, 100, 40)
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
	out := renderShippingPanel(sess, entry, 100, 30)
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
	out := renderShippingPanel(sess, entry, 100, 30)
	stripped := ansi.Strip(out)
	if !strings.Contains(stripped, "checking") {
		t.Errorf("expected 'checking' for unknown mergeable state: %q", stripped)
	}
	if strings.Contains(stripped, "conflicts") {
		t.Errorf("unexpected 'conflicts' for unknown mergeable state: %q", stripped)
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
