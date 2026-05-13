package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/baton/internal/agent"
	"github.com/devenjarvis/baton/internal/git"
)

// TestRenderReviewHeader_TwoLineIntentCap checks that a long prompt is capped to
// two intent lines (with trailing …) giving title+2 intent+divider = 4 lines total,
// and that every line fits within width-4 visible cells.
func TestRenderReviewHeader_TwoLineIntentCap(t *testing.T) {
	const width = 120
	sess := agent.NewSessionForTest("sess-1", "fix-auth")
	// 400-character prompt with spaces so wrapText can break lines.
	longPrompt := strings.Repeat("Fix authentication tokens so they properly redirect to the login page when expired. ", 5)
	sess.SetOriginalPrompt(longPrompt)
	sess.MarkDone()

	lines := renderReviewHeader(sess, width)

	// Expect: title row + exactly 2 intent rows (2nd ending with …) + divider = 4 total.
	if len(lines) != 4 {
		t.Errorf("renderReviewHeader returned %d lines, want 4 (title+2 intent+divider); got:\n%s",
			len(lines), strings.Join(lines, "\n"))
	}
	if len(lines) < 4 {
		t.Fatal("too few lines to inspect")
	}
	// Title line must contain REVIEW.
	if !strings.Contains(lines[0], "REVIEW") {
		t.Errorf("first line must contain REVIEW, got: %q", lines[0])
	}
	// Intent lines (lines[1] and lines[2]) must fit within width-4 visible cells.
	for i := 1; i <= 2; i++ {
		if vw := ansi.StringWidth(lines[i]); vw > width-4 {
			t.Errorf("intent line %d visible width %d exceeds %d: %q", i, vw, width-4, lines[i])
		}
	}
	// Second intent line (lines[2]) must end with ellipsis.
	if !strings.HasSuffix(strings.TrimRight(lines[2], " "), "…") {
		t.Errorf("second intent line must end with '…', got: %q", lines[2])
	}
	// Last line must be the divider.
	last := lines[len(lines)-1]
	if !strings.Contains(ansi.Strip(last), "─") {
		t.Errorf("last line must be the horizontal divider, got: %q", last)
	}
}

// TestRenderTaskListPane_RowFormat verifies the compact left-pane row structure.
func TestRenderTaskListPane_RowFormat(t *testing.T) {
	entry := &reviewDiffEntry{
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
			{
				taskIndex: 0,
				commits:   []git.Commit{{Hash: "def456", Subject: "other commit"}},
				stats:     &git.DiffStats{Files: 1, Insertions: 3, Deletions: 1},
			},
		},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictPass}},
			2: {state: verdictPending},
		},
	}

	const width, height, cursor = 40, 10, 0
	lines := renderTaskListPane(entry, width, height, cursor)
	out := strings.Join(lines, "\n")

	if !strings.Contains(out, "PLAN TASKS") {
		t.Error("must contain PLAN TASKS header")
	}

	// Row 0 (task 1): pass icon, [1], task text.
	found1 := false
	for _, l := range lines {
		if strings.Contains(l, "✓") && strings.Contains(l, "[1]") && strings.Contains(l, "Add auth") {
			found1 = true
			break
		}
	}
	if !found1 {
		t.Errorf("row 0 must contain ✓, [1], and 'Add auth'; got:\n%s", out)
	}

	// Row 1 (task 2): pending icon, [2].
	found2 := false
	for _, l := range lines {
		if strings.Contains(l, "⋯") && strings.Contains(l, "[2]") {
			found2 = true
			break
		}
	}
	if !found2 {
		t.Errorf("row 1 must contain ⋯ and [2]; got:\n%s", out)
	}

	// "Other changes" row contains [?].
	if !strings.Contains(out, "[?]") {
		t.Errorf("Other changes row must contain [?]; got:\n%s", out)
	}

	// No line exceeds width visible cells.
	for i, l := range lines {
		if vw := ansi.StringWidth(l); vw > width {
			t.Errorf("line %d width %d exceeds %d: %q", i, vw, width, l)
		}
	}
}

// TestRenderTaskDetailPane_FullRationaleNoTruncation verifies that a 250-char
// rationale renders in full without any ellipsis truncation.
func TestRenderTaskDetailPane_FullRationaleNoTruncation(t *testing.T) {
	rationale := strings.Repeat("Great implementation. ", 12)[:250]
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 1, Text: "Add auth middleware"}},
		groups: []taskReviewGroup{{
			taskIndex: 1,
			commits:   []git.Commit{{Hash: "abc1234", Subject: "add middleware"}},
			stats:     &git.DiffStats{Files: 1, Insertions: 5, Deletions: 1},
		}},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictPass, Rationale: rationale}},
		},
	}

	const width, height, cursor = 60, 20, 0
	lines := renderTaskDetailPane(entry, cursor, width, height)
	out := strings.Join(lines, "\n")

	// Full rationale must appear — check a phrase that fits within one wrapped line.
	if !strings.Contains(out, "Great implementation.") {
		t.Error("full rationale must be present ('Great implementation.' not found)")
	}
	// Rationale is long enough to span multiple lines; verify last word also present.
	if !strings.Contains(out, rationale[200:220]) {
		t.Error("tail of rationale must also be present (rationale truncated)")
	}
	if strings.Contains(out, "…") {
		t.Error("rationale must not be truncated with …")
	}

	// No individual rendered line exceeds width-2 visible cells.
	for i, l := range lines {
		if vw := ansi.StringWidth(l); vw > width-2 {
			t.Errorf("line %d visible width %d exceeds %d: %q", i, vw, width-2, l)
		}
	}
}

// TestRenderTaskDetailPane_VerdictStatesRender verifies each verdict state produces
// the expected label string in the detail pane.
func TestRenderTaskDetailPane_VerdictStatesRender(t *testing.T) {
	tests := []struct {
		name      string
		rec       *taskVerdictRecord
		wantLabel string
	}{
		{"pass", &taskVerdictRecord{state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictPass}}, "pass"},
		{"concerns", &taskVerdictRecord{state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictConcerns}}, "concerns"},
		{"fail", &taskVerdictRecord{state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictFail}}, "fail"},
		{"pending", &taskVerdictRecord{state: verdictPending}, "Pending"},
		{"running", &taskVerdictRecord{state: verdictRunning}, "Reviewing…"},
		{"err", &taskVerdictRecord{state: verdictErr}, "error"},
		{"noDiff", &taskVerdictRecord{state: verdictNoDiff}, "no diff found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := &reviewDiffEntry{
				tasks:    []agent.PlanTask{{Index: 1, Text: "Do something"}},
				groups:   []taskReviewGroup{{taskIndex: 1, commits: []git.Commit{{Hash: "abc1234"}}}},
				verdicts: map[int]*taskVerdictRecord{1: tt.rec},
			}
			lines := renderTaskDetailPane(entry, 0, 80, 20)
			out := strings.Join(lines, "\n")
			if !strings.Contains(out, tt.wantLabel) {
				t.Errorf("verdict state %s: want label %q in output; got:\n%s", tt.name, tt.wantLabel, out)
			}
		})
	}
}

// TestRenderTaskDetailPane_ShowsFilesAndCommits verifies that file paths, +X -Y
// stats, and commit subjects all appear in the detail pane.
func TestRenderTaskDetailPane_ShowsFilesAndCommits(t *testing.T) {
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 1, Text: "Implement feature"}},
		groups: []taskReviewGroup{{
			taskIndex: 1,
			commits: []git.Commit{
				{Hash: "abc1234def", Subject: "feat: add new handler"},
				{Hash: "fff5678abc", Subject: "test: add handler tests"},
			},
			files: []git.FileStat{
				{Path: "internal/handler.go", Insertions: 42, Deletions: 3},
				{Path: "internal/handler_test.go", Insertions: 80, Deletions: 0},
			},
			stats: &git.DiffStats{Files: 2, Insertions: 122, Deletions: 3},
		}},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictPass}},
		},
	}

	lines := renderTaskDetailPane(entry, 0, 80, 30)
	out := strings.Join(lines, "\n")

	for _, want := range []string{
		"internal/handler.go", "+42", "-3",
		"internal/handler_test.go", "+80",
		"abc1234", "feat: add new handler",
		"fff5678", "test: add handler tests",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("detail pane must contain %q; got:\n%s", want, out)
		}
	}
}

// TestRenderReviewPanel_TwoPaneLayout verifies that wide rendering composes both
// panes side-by-side: left pane task list and right pane task detail.
func TestRenderReviewPanel_TwoPaneLayout(t *testing.T) {
	rationale := strings.Repeat("This implementation is well structured and correct. ", 4)
	sess := agent.NewSessionForTest("sess-1", "fix-auth")
	sess.SetOriginalPrompt("Fix auth")
	sess.MarkDone()
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 1, Text: "Add auth middleware"}},
		groups: []taskReviewGroup{{
			taskIndex: 1,
			commits:   []git.Commit{{Hash: "abc1234", Subject: "add middleware"}},
			stats:     &git.DiffStats{Files: 1, Insertions: 5, Deletions: 1},
		}},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictPass, Rationale: rationale}},
		},
	}

	output := renderReviewPanel(sess, entry, 140, 30, 0, false, 0)

	if !strings.Contains(output, "PLAN TASKS") {
		t.Error("must contain PLAN TASKS from left pane")
	}
	if !strings.Contains(output, "Task 1:") {
		t.Error("must contain 'Task 1:' from right pane detail heading")
	}
	if !strings.Contains(output, "[1]") {
		t.Error("must contain [1] task index from left pane row")
	}
	if !strings.Contains(output, "This implementation") {
		t.Error("must contain rationale text from right pane")
	}
	if !strings.Contains(output, "create or open PR") {
		t.Error("footer must still advertise 'create or open PR'")
	}
}

// TestRenderReviewPanel_NarrowWidthStacks verifies that at <80 cols, panes stack
// vertically (list above detail) rather than side by side.
func TestRenderReviewPanel_NarrowWidthStacks(t *testing.T) {
	sess := agent.NewSessionForTest("sess-1", "fix-auth")
	sess.SetOriginalPrompt("Fix auth")
	sess.MarkDone()
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 1, Text: "Add auth middleware"}},
		groups: []taskReviewGroup{{
			taskIndex: 1,
			commits:   []git.Commit{{Hash: "abc1234", Subject: "add middleware"}},
			stats:     &git.DiffStats{Files: 1, Insertions: 5, Deletions: 1},
		}},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictPass, Rationale: "Looks good."}},
		},
	}

	output := renderReviewPanel(sess, entry, 70, 30, 0, false, 0)

	if !strings.Contains(output, "PLAN TASKS") {
		t.Error("must contain PLAN TASKS from list pane")
	}
	if !strings.Contains(output, "Task 1:") {
		t.Error("must contain 'Task 1:' from detail pane")
	}

	// In stacked mode, no single line should contain both PLAN TASKS and Task 1:.
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "PLAN TASKS") && strings.Contains(line, "Task 1:") {
			t.Errorf("in narrow mode panes must be stacked, not side-by-side; found both on one line: %q", line)
		}
	}
}

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

	output := renderReviewPanel(sess, entry, 120, 40, 0, false, 0)

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
	output := renderReviewPanel(sess, nil, 120, 40, 0, false, 0)
	if !strings.Contains(output, "Fix the auth bug") {
		t.Error("must still show prompt even with nil diff entry")
	}
}

// TestRenderReviewPanel_UsesThemeColors verifies that the panel uses the theme's
// ColorSuccess ANSI escape rather than a hard-coded hex green for a pass verdict.
func TestRenderReviewPanel_UsesThemeColors(t *testing.T) {
	sess := agent.NewSessionForTest("sess-1", "fix-auth")
	sess.SetOriginalPrompt("Fix auth")
	sess.MarkDone()
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 1, Text: "Add auth middleware"}},
		groups: []taskReviewGroup{{
			taskIndex: 1,
			commits:   []git.Commit{{Hash: "abc1234", Subject: "add middleware"}},
			stats:     &git.DiffStats{Files: 1, Insertions: 10, Deletions: 2},
		}},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictPass}},
		},
	}

	output := renderReviewPanel(sess, entry, 120, 30, 0, false, 0)

	// The pass verdict icon should carry the ColorSuccess ANSI escape.
	expectedPrefix := StyleSuccess.Render("✓")
	if !strings.Contains(output, expectedPrefix) {
		t.Errorf("pass verdict must use StyleSuccess ANSI color; styled icon %q not found in output", expectedPrefix)
	}
}

// TestReviewListPaneRowAt verifies the click hit-test for the left task list pane.
func TestReviewListPaneRowAt(t *testing.T) {
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{
			{Index: 1, Text: "Task one"},
			{Index: 2, Text: "Task two"},
			{Index: 3, Text: "Task three"},
		},
		groups: []taskReviewGroup{},
	}

	const paneTop, paneLeft, paneWidth = 4, 0, 40

	tests := []struct {
		name    string
		mouseX  int
		mouseY  int
		wantRow int
	}{
		{"click row 0", 5, paneTop + 2, 0},
		{"click row 1", 5, paneTop + 3, 1},
		{"click row 2", 5, paneTop + 4, 2},
		{"Y below rows", 5, paneTop + 5, -1},
		{"in PLAN TASKS header", 5, paneTop, -1},
		{"in blank header line", 5, paneTop + 1, -1},
		{"X past right edge", paneLeft + paneWidth, paneTop + 2, -1},
		{"X before left edge", paneLeft - 1, paneTop + 2, -1},
		{"Y above pane", 5, paneTop - 1, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reviewListPaneRowAt(entry, tt.mouseX, tt.mouseY, paneTop, paneLeft, paneWidth)
			if got != tt.wantRow {
				t.Errorf("reviewListPaneRowAt(x=%d, y=%d) = %d, want %d", tt.mouseX, tt.mouseY, got, tt.wantRow)
			}
		})
	}
}

// TestRenderReviewPanel_NoPlanShowsOverview verifies the no-plan (overview) path.
func TestRenderReviewPanel_NoPlanShowsOverview(t *testing.T) {
	sess := agent.NewSessionForTest("sess-1", "fix-auth")
	sess.SetOriginalPrompt("Fix the auth bug")
	sess.MarkDone()

	entry := &reviewDiffEntry{
		files: []git.FileStat{
			{Path: "auth.go", Status: "M", Insertions: 10, Deletions: 2},
			{Path: "auth_test.go", Status: "M", Insertions: 20, Deletions: 0},
		},
		aggregate: &git.DiffStats{Files: 2, Insertions: 30, Deletions: 2},
	}

	output := renderReviewPanel(sess, entry, 120, 30, 0, false, 0)

	if !strings.Contains(output, "Overview") {
		t.Error("must show 'Overview' row in list pane for no-plan session")
	}
	if !strings.Contains(output, "auth.go") {
		t.Error("must show auth.go in detail pane aggregate view")
	}
	if !strings.Contains(output, "+10") {
		t.Error("must show +10 insertions for auth.go")
	}
	if strings.Contains(output, "REVIEW SHAPE") {
		t.Error("must not show legacy 'REVIEW SHAPE' string")
	}
	if strings.Contains(output, "FOCUS HERE FIRST") {
		t.Error("must not show legacy 'FOCUS HERE FIRST' string")
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

	output := renderReviewPanel(sess, nil, 120, 40, 0, false, 0)

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

	output := renderReviewPanel(sess, entry, 120, 40, 0, false, 0)

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

// TestReviewTaskIndexAtCursor checks that the index resolves correctly for the
// f-flag path, including plan tasks that have no commit group (which is the
// case the cursor helper used to silently skip).
func TestReviewTaskIndexAtCursor(t *testing.T) {
	// Plan with two tasks; only task 2 has commits. Task 1 has no group — the
	// reviewer might want to flag it as "never started".
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{
			{Index: 1, Text: "Add metrics"},
			{Index: 2, Text: "Wire dashboard"},
		},
		groups: []taskReviewGroup{
			{taskIndex: 2, commits: []git.Commit{{Hash: "b"}}},
			{taskIndex: 0, commits: []git.Commit{{Hash: "c"}}},
		},
	}
	tests := []struct {
		name    string
		cursor  int
		wantIdx int
		wantOk  bool
	}{
		{"task 1 with no commits resolves", 0, 1, true},
		{"task 2 with commits resolves", 1, 2, true},
		{"other-changes row resolves to 0", 2, 0, true},
		{"out of range returns false", 3, 0, false},
		{"negative cursor returns false", -1, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, ok := reviewTaskIndexAtCursor(entry, tt.cursor)
			if ok != tt.wantOk {
				t.Errorf("ok = %v, want %v", ok, tt.wantOk)
			}
			if idx != tt.wantIdx {
				t.Errorf("idx = %d, want %d", idx, tt.wantIdx)
			}
		})
	}

	// No-plan synthetic Overview row: no tasks, no groups → never resolves.
	emptyEntry := &reviewDiffEntry{}
	if _, ok := reviewTaskIndexAtCursor(emptyEntry, 0); ok {
		t.Error("expected !ok for no-plan synthetic Overview row")
	}
	if _, ok := reviewTaskIndexAtCursor(nil, 0); ok {
		t.Error("expected !ok for nil entry")
	}
}

// TestPopulateNoDiffVerdicts verifies the detection logic that stamps verdictNoDiff
// on plan tasks with no matching commit group, leaving matched tasks untouched.
func TestPopulateNoDiffVerdicts(t *testing.T) {
	tests := []struct {
		name          string
		tasks         []agent.PlanTask
		verdicts      map[int]*taskVerdictRecord // pre-populated (matched tasks)
		wantNoDiff    []int                      // task indices that should get verdictNoDiff
		wantUntouched []int                      // task indices that must keep their original state
	}{
		{
			name: "one matched one unmatched",
			tasks: []agent.PlanTask{
				{Index: 1, Text: "task one"},
				{Index: 2, Text: "task two"},
			},
			verdicts:      map[int]*taskVerdictRecord{1: {state: verdictPending}},
			wantNoDiff:    []int{2},
			wantUntouched: []int{1},
		},
		{
			name: "all matched — nothing stamped",
			tasks: []agent.PlanTask{
				{Index: 1, Text: "task one"},
			},
			verdicts:      map[int]*taskVerdictRecord{1: {state: verdictPending}},
			wantNoDiff:    nil,
			wantUntouched: []int{1},
		},
		{
			name: "all unmatched",
			tasks: []agent.PlanTask{
				{Index: 1, Text: "task one"},
				{Index: 2, Text: "task two"},
			},
			verdicts:      map[int]*taskVerdictRecord{},
			wantNoDiff:    []int{1, 2},
			wantUntouched: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := &reviewDiffEntry{
				tasks:    tt.tasks,
				verdicts: tt.verdicts,
			}
			populateNoDiffVerdicts(entry)

			for _, idx := range tt.wantNoDiff {
				v, ok := entry.verdicts[idx]
				if !ok {
					t.Errorf("task %d: expected verdictNoDiff entry, got nothing", idx)
					continue
				}
				if v.state != verdictNoDiff {
					t.Errorf("task %d: want verdictNoDiff, got state %d", idx, v.state)
				}
			}
			for _, idx := range tt.wantUntouched {
				v, ok := entry.verdicts[idx]
				if !ok {
					t.Errorf("task %d: entry disappeared after populate", idx)
					continue
				}
				if v.state == verdictNoDiff {
					t.Errorf("task %d: should not have been stamped verdictNoDiff", idx)
				}
			}
		})
	}
}

// TestRenderReviewPanel_NoDiffFoundBadge verifies that a plan task with no matching
// commit group renders a "no diff found" verdict badge instead of being silent,
// when other tasks do have commit groups (i.e. verdicts map is initialised).
func TestRenderReviewPanel_NoDiffFoundBadge(t *testing.T) {
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

	output := renderReviewPanel(sess, entry, 120, 40, 0, false, 0)

	if !strings.Contains(output, "Write tests") {
		t.Error("must render a row for task 2 even though it has no commits")
	}
	if !strings.Contains(output, "no diff found") {
		t.Error("task with no commit group must show 'no diff found' badge")
	}
	if strings.Contains(output, "no commits") {
		t.Error("'no diff found' badge must replace 'no commits' label, not appear alongside it")
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
		{
			"no tasks no groups with aggregate (no-plan session)",
			&reviewDiffEntry{
				aggregate: &git.DiffStats{Files: 2, Insertions: 30, Deletions: 2},
			},
			1,
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

// TestVerdictBadge verifies the icon, label, and style returned for each verdict state.
func TestVerdictBadge(t *testing.T) {
	spinnerChars := map[string]bool{}
	for _, f := range spinnerFrames {
		spinnerChars[f] = true
	}

	tests := []struct {
		name      string
		rec       *taskVerdictRecord
		wantIcon  func(string) bool
		wantLabel string
	}{
		{
			name:      "nil record",
			rec:       nil,
			wantIcon:  func(s string) bool { return s == "⋯" },
			wantLabel: "Pending",
		},
		{
			name:      "verdictPending",
			rec:       &taskVerdictRecord{state: verdictPending},
			wantIcon:  func(s string) bool { return s == "⋯" },
			wantLabel: "Pending",
		},
		{
			name:      "verdictRunning",
			rec:       &taskVerdictRecord{state: verdictRunning},
			wantIcon:  func(s string) bool { return spinnerChars[s] },
			wantLabel: "Reviewing…",
		},
		{
			name:      "verdictDone Pass",
			rec:       &taskVerdictRecord{state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictPass}},
			wantIcon:  func(s string) bool { return s == "✓" },
			wantLabel: "pass",
		},
		{
			name:      "verdictDone Concerns",
			rec:       &taskVerdictRecord{state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictConcerns}},
			wantIcon:  func(s string) bool { return s == "!" },
			wantLabel: "concerns",
		},
		{
			name:      "verdictDone Fail",
			rec:       &taskVerdictRecord{state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictFail}},
			wantIcon:  func(s string) bool { return s == "✗" },
			wantLabel: "fail",
		},
		{
			name:      "verdictErr",
			rec:       &taskVerdictRecord{state: verdictErr},
			wantIcon:  func(s string) bool { return s == "✗" },
			wantLabel: "error",
		},
		{
			name:      "verdictNoDiff",
			rec:       &taskVerdictRecord{state: verdictNoDiff},
			wantIcon:  func(s string) bool { return s == "⊘" },
			wantLabel: "no diff found",
		},
		{
			name:      "userFlagged overrides pending",
			rec:       &taskVerdictRecord{state: verdictPending, userFlagged: true},
			wantIcon:  func(s string) bool { return s == "⚑" },
			wantLabel: "flagged",
		},
		{
			// Documents the intentional precedence: a human flag wins over the
			// AI reviewer's verdict, even a passing one.
			name:      "userFlagged overrides verdictDone pass",
			rec:       &taskVerdictRecord{state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictPass}, userFlagged: true},
			wantIcon:  func(s string) bool { return s == "⚑" },
			wantLabel: "flagged",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			icon, label, _ := verdictBadge(tt.rec)
			if !tt.wantIcon(icon) {
				t.Errorf("verdictBadge icon = %q, unexpected for %s", icon, tt.name)
			}
			if label != tt.wantLabel {
				t.Errorf("verdictBadge label = %q, want %q", label, tt.wantLabel)
			}
		})
	}
}

// TestRenderReviewHeader_GoalLineFromPlan verifies that the review header includes
// a Goal: line populated from the plan's # Goal section when the session has a plan.
func TestRenderReviewHeader_GoalLineFromPlan(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSessionForTestWithPath("sess-goal", "fix-auth", dir)
	sess.SetOriginalPrompt("Fix the auth redirect bug")
	sess.MarkDone()

	plan := "# Goal\nFix the auth redirect bug.\n\n## Spec\n1. Users redirect correctly.\n"
	if err := sess.WritePlan(plan); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}

	lines := renderReviewHeader(sess, 120)
	out := strings.Join(lines, "\n")

	foundGoalLine := false
	for _, l := range lines {
		stripped := ansi.Strip(l)
		if strings.HasPrefix(strings.TrimSpace(stripped), "Goal:") {
			foundGoalLine = true
			if !strings.Contains(stripped, "Fix the auth redirect bug.") {
				t.Errorf("Goal: line does not contain goal text, got: %q", stripped)
			}
			break
		}
	}
	if !foundGoalLine {
		t.Errorf("renderReviewHeader must include a 'Goal:' line when session has a plan; got:\n%s", out)
	}
}

// TestRenderReviewHeader_NoGoalWhenNoPlan verifies that the review header has no
// Goal: line when the session has no plan.
func TestRenderReviewHeader_NoGoalWhenNoPlan(t *testing.T) {
	sess := agent.NewSessionForTest("sess-noplan", "fix-auth")
	sess.SetOriginalPrompt("Fix the auth redirect bug")
	sess.MarkDone()

	lines := renderReviewHeader(sess, 120)
	out := strings.Join(lines, "\n")

	for _, l := range lines {
		if strings.Contains(ansi.Strip(l), "Goal:") {
			t.Errorf("renderReviewHeader must not include a 'Goal:' line when session has no plan; got:\n%s", out)
		}
	}
}

// TestRenderReviewPanel_PRDraftInFlight verifies that the spinner status line
// and disabled p hint appear when prDraftInFlight is true.
func TestRenderReviewPanel_PRDraftInFlight(t *testing.T) {
	sess := agent.NewSessionForTest("sess-1", "fix-auth")
	sess.SetOriginalPrompt("Fix the auth bug")
	sess.MarkDone()

	output := renderReviewPanel(sess, nil, 120, 40, 0, true, 0)

	if !strings.Contains(output, "Pushing branch and drafting PR") {
		t.Error("in-flight state must show draft spinner line")
	}
	if !strings.Contains(output, "in progress") {
		t.Error("in-flight state must show disabled p hint")
	}
	if strings.Contains(output, "create or open PR") {
		t.Error("in-flight state must not show normal p hint")
	}
}

// TestRenderReviewPanel_InlineDiffPresent verifies that when a task group has a
// non-empty rawDiff, the wide-mode review panel renders an inline diff showing
// both the verdict label and a diff hunk header.
func TestRenderReviewPanel_InlineDiffPresent(t *testing.T) {
	rawDiff := "diff --git a/internal/auth/handler.go b/internal/auth/handler.go\n" +
		"index 1234567..abcdefg 100644\n" +
		"--- a/internal/auth/handler.go\n" +
		"+++ b/internal/auth/handler.go\n" +
		"@@ -10,5 +10,6 @@ func Handle(w http.ResponseWriter, r *http.Request) {\n" +
		" \ttoken := r.Header.Get(\"Authorization\")\n" +
		" \tif token == \"\" {\n" +
		" \t\thttp.Redirect(w, r, \"/login\", http.StatusFound)\n" +
		"+\t\treturn\n" +
		" \t}\n" +
		" }\n"
	sess := agent.NewSessionForTest("sess-diff", "fix-auth")
	sess.SetOriginalPrompt("Fix auth redirect")
	sess.MarkDone()

	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 1, Text: "Fix handler", Done: false}},
		groups: []taskReviewGroup{{
			taskIndex: 1,
			commits:   []git.Commit{{Hash: "abc1234", Subject: "[task 1] fix handler"}},
			stats:     &git.DiffStats{Files: 1, Insertions: 1, Deletions: 0},
			rawDiff:   rawDiff,
		}},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictPass}},
		},
	}

	output := renderReviewPanel(sess, entry, 140, 40, 0, false, 0)

	if !strings.Contains(output, "pass") {
		t.Error("must contain verdict label 'pass'")
	}
	if !strings.Contains(output, "@@") {
		t.Errorf("inline diff must contain hunk header '@@'; got:\n%s", output)
	}
}

// TestRenderReviewPanel_NoDiffPlaceholder verifies that when a task group has an
// empty rawDiff, the inline diff area shows the "(no diff for this task)" placeholder.
func TestRenderReviewPanel_NoDiffPlaceholder(t *testing.T) {
	sess := agent.NewSessionForTest("sess-nodiff", "fix-auth")
	sess.SetOriginalPrompt("Fix auth redirect")
	sess.MarkDone()

	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 1, Text: "Fix handler", Done: false}},
		groups: []taskReviewGroup{{
			taskIndex: 1,
			commits:   []git.Commit{{Hash: "abc1234", Subject: "[task 1] fix handler"}},
			stats:     &git.DiffStats{Files: 0, Insertions: 0, Deletions: 0},
			rawDiff:   "",
		}},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictNoDiff},
		},
	}

	// Must not panic and must show the placeholder.
	output := renderReviewPanel(sess, entry, 140, 40, 0, false, 0)
	if !strings.Contains(output, "(no diff for this task)") {
		t.Errorf("must show '(no diff for this task)' placeholder; got:\n%s", output)
	}
}
