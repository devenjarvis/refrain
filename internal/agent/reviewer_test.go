package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/devenjarvis/refrain/internal/git"
)

// TestParsePlanTasks verifies that ParsePlanTasks extracts tasks with correct
// indices, text, and done flags using the same counting rules as planTaskCounts.
func TestParsePlanTasks(t *testing.T) {
	plan := `# Goal
Do a thing.

## Tasks
- [ ] First task
- [x] Second task (done)
- [ ] Third task

## Not in scope
Nothing.
`
	tasks := ParsePlanTasks(plan)
	if len(tasks) != 3 {
		t.Fatalf("want 3 tasks, got %d", len(tasks))
	}
	tests := []struct {
		idx  int
		text string
		done bool
	}{
		{1, "First task", false},
		{2, "Second task (done)", true},
		{3, "Third task", false},
	}
	for i, tt := range tests {
		got := tasks[i]
		if got.Index != tt.idx {
			t.Errorf("task %d: Index = %d, want %d", i, got.Index, tt.idx)
		}
		if got.Text != tt.text {
			t.Errorf("task %d: Text = %q, want %q", i, got.Text, tt.text)
		}
		if got.Done != tt.done {
			t.Errorf("task %d: Done = %v, want %v", i, got.Done, tt.done)
		}
	}
}

// TestParsePlanTasks_EmptyPlan verifies no crash or panic on empty input.
func TestParsePlanTasks_EmptyPlan(t *testing.T) {
	tasks := ParsePlanTasks("")
	if len(tasks) != 0 {
		t.Errorf("empty plan: want 0 tasks, got %d", len(tasks))
	}
}

// TestParsePlanTasks_IgnoresCheckboxesOutsideTasks pins the section-scoping
// fix: when a "## Tasks" heading is present, checkboxes in other sections
// (Spec, Verification, sub-bullets) must not be counted because the
// "[task N]" commit prefix depends on the in-section ordering.
func TestParsePlanTasks_IgnoresCheckboxesOutsideTasks(t *testing.T) {
	plan := `# Goal
Add a feature.

## Spec
- [ ] this is a stray checkbox the drafter accidentally wrote in Spec
- [ ] another one

## Tasks
- [ ] real task one
- [x] real task two

## Verification
- [ ] not a task, this is a verification step
`
	tasks := ParsePlanTasks(plan)
	if len(tasks) != 2 {
		t.Fatalf("want 2 tasks (only those under ## Tasks), got %d", len(tasks))
	}
	if tasks[0].Index != 1 || tasks[0].Text != "real task one" || tasks[0].Done {
		t.Errorf("tasks[0] = %+v, want {1 'real task one' false}", tasks[0])
	}
	if tasks[1].Index != 2 || tasks[1].Text != "real task two" || !tasks[1].Done {
		t.Errorf("tasks[1] = %+v, want {2 'real task two' true}", tasks[1])
	}
}

// TestParsePlanTasks_NoTasksHeadingFallback verifies plans without a
// "## Tasks" heading still report a count (whole-document scope).
func TestParsePlanTasks_NoTasksHeadingFallback(t *testing.T) {
	plan := "freeform plan\n- [ ] one\n- [x] two\n"
	tasks := ParsePlanTasks(plan)
	if len(tasks) != 2 {
		t.Fatalf("freeform plan: want 2 tasks, got %d", len(tasks))
	}
}

// TestParsePlanTasks_WithSubBullets verifies that sub-bullet lines following
// each checkbox are collected into Body, and the checkbox line itself is not
// included in Body.
func TestParsePlanTasks_WithSubBullets(t *testing.T) {
	plan := `# Goal
Test sub-bullets.

## Tasks
- [ ] First task
  - Files: internal/agent/session.go
  - Implement: add the field
- [ ] Second task
  - Test first: write TestFoo
  - Implement: do the thing
  - Verify: go test ./...

## Not in scope
Nothing.
`
	tasks := ParsePlanTasks(plan)
	if len(tasks) != 2 {
		t.Fatalf("want 2 tasks, got %d", len(tasks))
	}

	if tasks[0].Body == "" {
		t.Error("tasks[0].Body is empty, want sub-bullet content")
	}
	if !strings.Contains(tasks[0].Body, "Files: internal/agent/session.go") {
		t.Errorf("tasks[0].Body missing Files line, got: %q", tasks[0].Body)
	}
	if !strings.Contains(tasks[0].Body, "Implement: add the field") {
		t.Errorf("tasks[0].Body missing Implement line, got: %q", tasks[0].Body)
	}
	if strings.Contains(tasks[0].Body, "- [ ]") || strings.Contains(tasks[0].Body, "First task") {
		t.Errorf("tasks[0].Body must not include the checkbox line, got: %q", tasks[0].Body)
	}

	if tasks[1].Body == "" {
		t.Error("tasks[1].Body is empty, want sub-bullet content")
	}
	if !strings.Contains(tasks[1].Body, "Test first: write TestFoo") {
		t.Errorf("tasks[1].Body missing Test first line, got: %q", tasks[1].Body)
	}
	if !strings.Contains(tasks[1].Body, "Implement: do the thing") {
		t.Errorf("tasks[1].Body missing Implement line, got: %q", tasks[1].Body)
	}
	if !strings.Contains(tasks[1].Body, "Verify: go test ./...") {
		t.Errorf("tasks[1].Body missing Verify line, got: %q", tasks[1].Body)
	}
	if strings.Contains(tasks[1].Body, "- [ ]") || strings.Contains(tasks[1].Body, "Second task") {
		t.Errorf("tasks[1].Body must not include the checkbox line, got: %q", tasks[1].Body)
	}
}

// TestParsePlanTaskIndex verifies the Plan-Task trailer parser.
func TestParsePlanTaskIndex(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{
			name: "bare trailer",
			body: "Plan-Task: 3",
			want: 3,
		},
		{
			name: "indented trailer",
			body: "  Plan-Task: 3  ",
			want: 3,
		},
		{
			name: "no trailer",
			body: "Implements the widget.",
			want: 0,
		},
		{
			name: "multiple trailers first wins",
			body: "Implements widget.\n\nPlan-Task: 2\nPlan-Task: 5",
			want: 2,
		},
		{
			name: "surrounding text trailer at end",
			body: "Implements the widget.\n\nCo-authored-by: Alice <alice@example.com>\nPlan-Task: 7",
			want: 7,
		},
		{
			name: "case insensitive key",
			body: "plan-task: 4",
			want: 4,
		},
		{
			name: "mixed case key",
			body: "PLAN-TASK: 9",
			want: 9,
		},
		{
			name: "zero is not a valid task index",
			body: "Plan-Task: 0",
			want: 0,
		},
		{
			name: "empty body",
			body: "",
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parsePlanTaskIndex(tc.body)
			if got != tc.want {
				t.Errorf("parsePlanTaskIndex(%q) = %d, want %d", tc.body, got, tc.want)
			}
		})
	}
}

// TestGroupCommitsByTask verifies that commits are bucketed correctly by
// Plan-Task body trailer, with the "other" bucket receiving untagged commits.
func TestGroupCommitsByTask(t *testing.T) {
	commits := []git.Commit{
		{Hash: "aaa", Subject: "feat: add widget", Body: "Implements the widget.\n\nPlan-Task: 2"},
		{Hash: "bbb", Subject: "feat: scaffold", Body: "Initial scaffold.\n\nPlan-Task: 1"},
		{Hash: "ccc", Subject: "fixup: typo", Body: ""},
		{Hash: "ddd", Subject: "test: add widget tests", Body: "Test coverage.\n\nPlan-Task: 2"},
		{Hash: "eee", Subject: "chore: final polish", Body: "Polish.\n\nPlan-Task: 3"},
	}
	groups := GroupCommitsByTask(commits)

	// Expect 3 task groups + 1 "other" group = 4 total.
	if len(groups) != 4 {
		t.Fatalf("want 4 groups, got %d: %v", len(groups), groups)
	}

	// Groups are sorted: task 1, task 2, task 3, other (index 0) last.
	byIndex := make(map[int]CommitGroup, len(groups))
	for _, g := range groups {
		byIndex[g.TaskIndex] = g
	}

	if len(byIndex[1].Commits) != 1 {
		t.Errorf("task 1: want 1 commit, got %d", len(byIndex[1].Commits))
	}
	if len(byIndex[2].Commits) != 2 {
		t.Errorf("task 2: want 2 commits, got %d", len(byIndex[2].Commits))
	}
	if len(byIndex[3].Commits) != 1 {
		t.Errorf("task 3: want 1 commit, got %d", len(byIndex[3].Commits))
	}
	if len(byIndex[0].Commits) != 1 || byIndex[0].Commits[0].Subject != "fixup: typo" {
		t.Errorf("other: want 1 commit with subject 'fixup: typo', got %v", byIndex[0].Commits)
	}

	// Verify ordering: last group must be "other".
	if groups[len(groups)-1].TaskIndex != 0 {
		t.Errorf("other group must be last, got taskIndex=%d", groups[len(groups)-1].TaskIndex)
	}
}

// TestGroupCommitsByTask_NoOtherBucket verifies that no extra group is created
// when every commit has a Plan-Task trailer.
func TestGroupCommitsByTask_NoOtherBucket(t *testing.T) {
	commits := []git.Commit{
		{Hash: "aaa", Subject: "feat: scaffold", Body: "Plan-Task: 1"},
		{Hash: "bbb", Subject: "test: scaffold tests", Body: "Plan-Task: 1"},
	}
	groups := GroupCommitsByTask(commits)
	for _, g := range groups {
		if g.TaskIndex == 0 {
			t.Errorf("unexpected 'other' group: %v", g.Commits)
		}
	}
}

// TestBuildReviewPrompt verifies the reviewer prompt is assembled correctly
// depending on which optional fields are populated.
func TestBuildReviewPrompt(t *testing.T) {
	base := ReviewRequest{
		TaskIndex:      2,
		TaskText:       "Add widget",
		TaskDiff:       "diff --git a/widget.go\n+func NewWidget() {}",
		OriginalPrompt: "Build a widget system",
	}

	tests := []struct {
		name             string
		req              ReviewRequest
		wantTaskDetail   bool
		wantChangedFiles bool
		detailSnippet    string
		filesSnippet     string
	}{
		{
			name: "all fields populated",
			req: func() ReviewRequest {
				r := base
				r.TaskDetail = "  - Files: internal/widget.go\n  - Implement: add NewWidget"
				r.ChangedFiles = []string{"internal/widget.go", "internal/widget_test.go"}
				return r
			}(),
			wantTaskDetail:   true,
			wantChangedFiles: true,
			detailSnippet:    "Files: internal/widget.go",
			filesSnippet:     "internal/widget_test.go",
		},
		{
			name: "empty TaskDetail omits section",
			req: func() ReviewRequest {
				r := base
				r.TaskDetail = ""
				r.ChangedFiles = []string{"internal/widget.go"}
				return r
			}(),
			wantTaskDetail:   false,
			wantChangedFiles: true,
			filesSnippet:     "internal/widget.go",
		},
		{
			name: "empty ChangedFiles omits section",
			req: func() ReviewRequest {
				r := base
				r.TaskDetail = "  - Files: internal/widget.go"
				r.ChangedFiles = nil
				return r
			}(),
			wantTaskDetail:   true,
			wantChangedFiles: false,
			detailSnippet:    "Files: internal/widget.go",
		},
		{
			name:             "both empty - plain format",
			req:              base,
			wantTaskDetail:   false,
			wantChangedFiles: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildReviewPrompt(tt.req)

			if tt.wantTaskDetail {
				if !strings.Contains(got, "TASK DETAIL:") {
					t.Error("expected TASK DETAIL section, not found")
				}
				if tt.detailSnippet != "" && !strings.Contains(got, tt.detailSnippet) {
					t.Errorf("expected detail snippet %q in prompt, got:\n%s", tt.detailSnippet, got)
				}
			} else {
				if strings.Contains(got, "TASK DETAIL:") {
					t.Error("TASK DETAIL section must be absent when TaskDetail is empty")
				}
			}

			if tt.wantChangedFiles {
				if !strings.Contains(got, "CHANGED FILES:") {
					t.Error("expected CHANGED FILES section, not found")
				}
				if tt.filesSnippet != "" && !strings.Contains(got, tt.filesSnippet) {
					t.Errorf("expected files snippet %q in prompt, got:\n%s", tt.filesSnippet, got)
				}
			} else {
				if strings.Contains(got, "CHANGED FILES:") {
					t.Error("CHANGED FILES section must be absent when ChangedFiles is empty")
				}
			}

			// Core sections always present.
			if !strings.Contains(got, "ORIGINAL INTENT:") {
				t.Error("ORIGINAL INTENT section missing")
			}
			if !strings.Contains(got, "TASK #2:") {
				t.Error("TASK #N section missing")
			}
			if !strings.Contains(got, "Add widget") {
				t.Error("task text missing")
			}
			if !strings.Contains(got, "DIFF:") {
				t.Error("DIFF section missing")
			}
		})
	}
}

// TestBuildReviewerArgs_NoBare verifies --bare is absent without an API key.
func TestBuildReviewerArgs_NoBare(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	args := buildReviewerArgs("claude-sonnet-4-6")
	for _, a := range args {
		if a == "--bare" {
			t.Error("--bare must not be present when ANTHROPIC_API_KEY is unset")
		}
	}
}

// TestBuildReviewerArgs_BareWithKey verifies --bare is present when ANTHROPIC_API_KEY is set.
func TestBuildReviewerArgs_BareWithKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	args := buildReviewerArgs("claude-sonnet-4-6")
	hasBare := false
	for _, a := range args {
		if a == "--bare" {
			hasBare = true
		}
	}
	if !hasBare {
		t.Error("expected --bare when ANTHROPIC_API_KEY is set")
	}
}

// TestBuildReviewerArgs_RequiredFlags verifies the read-only tool flags and
// no-session-persistence are always present.
func TestBuildReviewerArgs_RequiredFlags(t *testing.T) {
	args := buildReviewerArgs("claude-sonnet-4-6")
	want := map[string]bool{
		"--no-session-persistence": false,
		"--tools":                  false,
		"--allowedTools":           false,
	}
	for _, a := range args {
		if _, ok := want[a]; ok {
			want[a] = true
		}
	}
	for flag, found := range want {
		if !found {
			t.Errorf("expected flag %q in buildReviewerArgs output", flag)
		}
	}
}

// TestParseReviewerOutput covers the structured VERDICT/RATIONALE format.
func TestParseReviewerOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantKind VerdictKind
		wantRat  string
		wantErr  bool
	}{
		{
			name:     "pass verdict",
			input:    "VERDICT: pass\nRATIONALE: All good.",
			wantKind: VerdictPass,
			wantRat:  "All good.",
		},
		{
			name:     "concerns verdict",
			input:    "VERDICT: concerns\nRATIONALE: Missing edge case.",
			wantKind: VerdictConcerns,
			wantRat:  "Missing edge case.",
		},
		{
			name:     "fail verdict",
			input:    "VERDICT: fail\nRATIONALE: Not implemented.",
			wantKind: VerdictFail,
			wantRat:  "Not implemented.",
		},
		{
			name:     "case insensitive verdict",
			input:    "VERDICT: PASS\nRATIONALE: OK.",
			wantKind: VerdictPass,
			wantRat:  "OK.",
		},
		{
			name:     "unknown verdict falls back to concerns",
			input:    "VERDICT: maybe\nRATIONALE: Unsure.",
			wantKind: VerdictConcerns,
			wantRat:  "Unsure.",
		},
		{
			name:    "empty output returns error",
			input:   "",
			wantErr: true,
		},
		{
			name:     "unstructured output returns concerns with raw text",
			input:    "something went wrong",
			wantKind: VerdictConcerns,
			wantRat:  "something went wrong",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := parseReviewerOutput(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v.Kind != tt.wantKind {
				t.Errorf("Kind = %q, want %q", v.Kind, tt.wantKind)
			}
			if v.Rationale != tt.wantRat {
				t.Errorf("Rationale = %q, want %q", v.Rationale, tt.wantRat)
			}
		})
	}
}

// fakeReviewerAgent is a ReviewerAgent stub for tests.
type fakeReviewerAgent struct {
	verdict ReviewVerdict
	err     error
}

func (f *fakeReviewerAgent) Review(_ context.Context, _ ReviewRequest) (ReviewVerdict, error) {
	return f.verdict, f.err
}

// TestManagerSetReviewerAgent verifies that SetReviewerAgent swaps the agent
// visible via ReviewerAgent() and that a custom stub can be injected.
func TestManagerSetReviewerAgent(t *testing.T) {
	repo := setupTestRepo(t)
	mgr := NewManager(repo, defaultTestSettings())
	defer mgr.Shutdown()

	stub := &fakeReviewerAgent{verdict: ReviewVerdict{Kind: VerdictPass, Rationale: "stub"}}
	mgr.SetReviewerAgent(stub)

	got := mgr.ReviewerAgent()
	if got != stub {
		t.Error("ReviewerAgent() did not return the injected stub")
	}

	ctx := context.Background()
	v, err := got.Review(ctx, ReviewRequest{TaskIndex: 1, TaskText: "anything"})
	if err != nil {
		t.Fatalf("stub.Review error: %v", err)
	}
	if v.Kind != VerdictPass {
		t.Errorf("stub verdict Kind = %q, want pass", v.Kind)
	}
}
