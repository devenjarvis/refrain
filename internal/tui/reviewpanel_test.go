package tui

import (
	"strings"
	"testing"

	"github.com/devenjarvis/baton/internal/agent"
	"github.com/devenjarvis/baton/internal/git"
)

func TestRenderReviewPanel_ShowsOriginalPrompt(t *testing.T) {
	sess := agent.NewSessionForTest("sess-1", "fix-auth")
	sess.SetOriginalPrompt("Fix the auth bug so tokens redirect to /login")
	sess.MarkDone()

	entry := &reviewDiffEntry{
		files: []git.FileStat{
			{Path: "middleware/auth.go", Status: "M", Insertions: 89, Deletions: 34},
			{Path: "middleware/auth_test.go", Status: "M", Insertions: 124, Deletions: 0},
		},
		aggregate: &git.DiffStats{Files: 2, Insertions: 213, Deletions: 34},
	}

	output := renderReviewPanel(sess, entry, 120, 40, 0)

	if !strings.Contains(output, "Fix the auth bug") {
		t.Error("review panel must show the original prompt")
	}
	if !strings.Contains(output, "middleware/auth.go") {
		t.Error("review panel must show top changed file")
	}
}

func TestRenderReviewPanel_NilDiffEntry(t *testing.T) {
	sess := agent.NewSessionForTest("sess-1", "fix-auth")
	sess.SetOriginalPrompt("Fix the auth bug")

	// nil entry — should not panic
	output := renderReviewPanel(sess, nil, 120, 40, 0)
	if !strings.Contains(output, "Fix the auth bug") {
		t.Error("must still show prompt even with nil diff entry")
	}
}

func TestClassifyFile(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"middleware/auth_test.go", "tests"},
		{"middleware/auth.go", "logic"},
		{".github/workflows/ci.yml", "config"},
		{"Makefile", "config"},
		{"cmd/root.go", "logic"},
		{"internal/config/settings.go", "logic"},
	}
	for _, tc := range cases {
		if got := classifyFile(tc.path); got != tc.want {
			t.Errorf("classifyFile(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestWrapText(t *testing.T) {
	lines := wrapText("hello world foo bar", 10)
	for _, l := range lines {
		if len(l) > 10 {
			t.Errorf("line %q exceeds maxWidth 10", l)
		}
	}
	if len(lines) < 2 {
		t.Error("expected wrapping to produce multiple lines")
	}
}

// TestRenderReviewPanel_FooterAdvertisesAllActions verifies that the action
// footer surfaces the new t/c keys alongside p/e/d/ESC. Without these hints,
// users who can't open a PR (no PR yet, design doc, etc.) have no visible
// path forward and may end up orphaning the session.
func TestRenderReviewPanel_FooterAdvertisesAllActions(t *testing.T) {
	sess := agent.NewSessionForTest("sess-1", "fix-auth")
	sess.SetOriginalPrompt("Fix the auth bug")
	sess.MarkDone()

	output := renderReviewPanel(sess, nil, 120, 40, 0)

	for _, want := range []string{
		"open PR",
		"open agent terminal",
		"mark complete",
		"open in editor",
		"defer",
		"back to focus",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("footer must advertise %q; got:\n%s", want, output)
		}
	}
}

// TestRenderReviewPanel_TaskListShown verifies that when the entry has plan
// tasks, the review panel renders a task list instead of the file-centric view.
func TestRenderReviewPanel_TaskListShown(t *testing.T) {
	sess := agent.NewSessionForTest("sess-1", "fix-auth")
	sess.SetOriginalPrompt("Fix the auth bug")
	sess.MarkDone()

	entry := &reviewDiffEntry{
		files:     []git.FileStat{{Path: "auth.go", Status: "M", Insertions: 10, Deletions: 2}},
		aggregate: &git.DiffStats{Files: 1, Insertions: 10, Deletions: 2},
		tasks: []agent.PlanTask{
			{Index: 1, Text: "Add auth middleware", Done: false},
			{Index: 2, Text: "Write tests", Done: false},
		},
		groups: []taskReviewGroup{
			{
				taskIndex: 1,
				commits:   []git.Commit{{Hash: "abc123", Subject: "[task 1] add middleware"}},
				stats:     &git.DiffStats{Files: 1, Insertions: 10, Deletions: 2},
			},
		},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictPass, Rationale: "looks good"}},
			2: {state: verdictPending},
		},
	}

	output := renderReviewPanel(sess, entry, 120, 40, 0)

	if !strings.Contains(output, "PLAN TASKS") {
		t.Error("task list view must show PLAN TASKS header")
	}
	if !strings.Contains(output, "Add auth middleware") {
		t.Error("must show task 1 text")
	}
	if !strings.Contains(output, "Write tests") {
		t.Error("must show task 2 text")
	}
	if !strings.Contains(output, "pass") {
		t.Error("must show verdict badge for task 1")
	}
}

// TestReviewTaskGroupAtCursor checks that the correct group is returned for
// each cursor position in the task list ordering.
func TestReviewTaskGroupAtCursor(t *testing.T) {
	g1 := taskReviewGroup{taskIndex: 1, commits: []git.Commit{{Hash: "a"}}}
	g2 := taskReviewGroup{taskIndex: 2, commits: []git.Commit{{Hash: "b"}}}
	gOther := taskReviewGroup{taskIndex: 0, commits: []git.Commit{{Hash: "c"}}}

	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{
			{Index: 1, Text: "Task one"},
			{Index: 2, Text: "Task two"},
		},
		groups: []taskReviewGroup{g1, g2, gOther},
	}

	tests := []struct {
		cursor    int
		wantIndex int
		wantNil   bool
	}{
		{0, 1, false},
		{1, 2, false},
		{2, 0, false}, // "Other changes"
		{3, 0, true},  // out of range
	}
	for _, tt := range tests {
		got := reviewTaskGroupAtCursor(entry, tt.cursor)
		if tt.wantNil {
			if got != nil {
				t.Errorf("cursor %d: want nil, got taskIndex=%d", tt.cursor, got.taskIndex)
			}
		} else {
			if got == nil {
				t.Errorf("cursor %d: want taskIndex=%d, got nil", tt.cursor, tt.wantIndex)
			} else if got.taskIndex != tt.wantIndex {
				t.Errorf("cursor %d: want taskIndex=%d, got %d", tt.cursor, tt.wantIndex, got.taskIndex)
			}
		}
	}
}

// TestRenderTaskList_NoDiffFoundBadge verifies that a plan task with no matching
// commit group renders a "no diff found" verdict badge instead of being silent,
// when other tasks do have commit groups (i.e. verdicts map is initialised).
func TestRenderTaskList_NoDiffFoundBadge(t *testing.T) {
	entry := &reviewDiffEntry{
		files:     []git.FileStat{{Path: "auth.go", Status: "M", Insertions: 5, Deletions: 1}},
		aggregate: &git.DiffStats{Files: 1, Insertions: 5, Deletions: 1},
		tasks: []agent.PlanTask{
			{Index: 1, Text: "Add auth middleware", Done: false},
			{Index: 2, Text: "Write tests", Done: false},
		},
		groups: []taskReviewGroup{
			{
				taskIndex: 1,
				commits:   []git.Commit{{Hash: "abc123", Subject: "[task 1] add middleware"}},
				stats:     &git.DiffStats{Files: 1, Insertions: 5, Deletions: 1},
			},
		},
		// task 2 has no group — verdictNoDiff should be auto-populated
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictPending},
			2: {state: verdictNoDiff},
		},
	}

	sess := agent.NewSessionForTest("sess-1", "fix-auth")
	sess.SetOriginalPrompt("Fix the auth bug")
	sess.MarkDone()

	output := renderReviewPanel(sess, entry, 120, 40, 0)

	if !strings.Contains(output, "Write tests") {
		t.Error("must render a row for task 2 even though it has no commits")
	}
	if !strings.Contains(output, "no diff found") {
		t.Error("task with no commit group must show 'no diff found' badge")
	}
}

// TestReviewTaskCount verifies task row counting including the "other" bucket.
func TestReviewTaskCount(t *testing.T) {
	tests := []struct {
		name  string
		entry *reviewDiffEntry
		want  int
	}{
		{"nil entry", nil, 0},
		{"no tasks no groups", &reviewDiffEntry{}, 0},
		{
			"two tasks no other",
			&reviewDiffEntry{
				tasks: []agent.PlanTask{{Index: 1}, {Index: 2}},
			},
			2,
		},
		{
			"two tasks with other",
			&reviewDiffEntry{
				tasks:  []agent.PlanTask{{Index: 1}, {Index: 2}},
				groups: []taskReviewGroup{{taskIndex: 0}},
			},
			3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := reviewTaskCount(tt.entry); got != tt.want {
				t.Errorf("reviewTaskCount = %d, want %d", got, tt.want)
			}
		})
	}
}
