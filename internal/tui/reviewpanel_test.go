package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/config"
	"github.com/devenjarvis/refrain/internal/git"
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

	// No individual rendered line exceeds width visible cells.
	// Rationale is now rendered at measure=width (when width<reviewDetailMaxMeasure),
	// so lines may be up to width wide rather than the old width-2.
	for i, l := range lines {
		if vw := ansi.StringWidth(l); vw > width {
			t.Errorf("line %d visible width %d exceeds %d: %q", i, vw, width, l)
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

	output := renderReviewPanel(sess, entry, 140, 30, 0, false, 0, nil)

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

// TestRenderReviewPanel_FullWidthStack verifies that at width=120 (wide terminal)
// the review panel uses a vertical stack — panes are never side-by-side.
func TestRenderReviewPanel_FullWidthStack(t *testing.T) {
	sess := agent.NewSessionForTest("sess-wide", "fix-auth")
	sess.SetOriginalPrompt("Fix auth")
	sess.MarkDone()
	// Long task text that would be truncated in the old 40%-width left pane but
	// must appear in full in the new full-width stacked layout.
	longTaskText := "Add authentication middleware with token validation and redirect"
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 1, Text: longTaskText}},
		groups: []taskReviewGroup{{
			taskIndex: 1,
			commits:   []git.Commit{{Hash: "abc1234", Subject: "add middleware"}},
			stats:     &git.DiffStats{Files: 1, Insertions: 5, Deletions: 1},
		}},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictPending},
		},
	}

	output := renderReviewPanel(sess, entry, 120, 40, 0, false, 0, nil)

	if !strings.Contains(output, "PLAN TASKS") {
		t.Error("must contain PLAN TASKS from list pane")
	}
	if !strings.Contains(output, "Task 1:") {
		t.Error("must contain 'Task 1:' from detail pane")
	}
	// In stacked mode, PLAN TASKS and Task 1: are never on the same line.
	for _, line := range strings.Split(output, "\n") {
		stripped := ansi.Strip(line)
		if strings.Contains(stripped, "PLAN TASKS") && strings.Contains(stripped, "Task 1:") {
			t.Errorf("in stacked mode panes must not be side-by-side; found both on one line: %q", line)
		}
	}
	// Full-width: the long task name must not be truncated. In the old 40%-width
	// left pane, text beyond ~48 chars was cut. At width=120 the list pane is
	// now 118 chars wide, so the full task text must appear.
	if !strings.Contains(ansi.Strip(output), "token validation and redirect") {
		t.Errorf("full-width stacked layout must not truncate long task text; got:\n%s", output)
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

	output := renderReviewPanel(sess, entry, 70, 30, 0, false, 0, nil)

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

	output := renderReviewPanel(sess, entry, 120, 40, 0, false, 0, nil)

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
	output := renderReviewPanel(sess, nil, 120, 40, 0, false, 0, nil)
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

	output := renderReviewPanel(sess, entry, 120, 30, 0, false, 0, nil)

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

	output := renderReviewPanel(sess, entry, 120, 30, 0, false, 0, nil)

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

	output := renderReviewPanel(sess, nil, 120, 40, 0, false, 0, nil)

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

// TestRenderReviewPanel_ChecksTabFooter verifies that when activeTab is Checks,
// the footer shows the r-run hint and does not contain the flag-task hint.
func TestRenderReviewPanel_ChecksTabFooter(t *testing.T) {
	sess := agent.NewSessionForTest("sess-1", "fix-auth")
	sess.SetOriginalPrompt("Fix auth")
	sess.MarkDone()

	output := renderReviewPanel(sess, nil, 120, 40, 0, false, reviewTabChecks, nil)
	stripped := ansi.Strip(output)

	if !strings.Contains(stripped, "run checks") {
		t.Errorf("Checks tab footer must contain 'run checks'; got:\n%s", stripped)
	}
	if strings.Contains(stripped, "flag task") {
		t.Errorf("Checks tab footer must not contain 'flag task'; got:\n%s", stripped)
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

	output := renderReviewPanel(sess, entry, 120, 40, 0, false, 0, nil)

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

	output := renderReviewPanel(sess, entry, 120, 40, 0, false, 0, nil)

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

// TestRenderReviewPanel_HintsIncludeSpecAndEnter verifies that the footer hints
// include ? (spec) and enter (open task diff), and no longer show "view task diff"
// or "pgdn/pgup" (the inline diff viewport was removed).
func TestRenderReviewPanel_HintsIncludeSpecAndEnter(t *testing.T) {
	sess := agent.NewSessionForTest("sess-hints", "fix-auth")
	sess.SetOriginalPrompt("Fix auth")
	sess.MarkDone()

	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 1, Text: "Fix handler"}},
		groups: []taskReviewGroup{{
			taskIndex: 1,
			commits:   []git.Commit{{Hash: "abc1234", Subject: "[task 1] fix"}},
			rawDiff:   "diff --git a/a.go b/a.go\nindex 1234567..abcdefg 100644\n--- a/a.go\n+++ b/a.go\n@@ -1,2 +1,3 @@\n package main\n+// marker\n func A() {}\n",
		}},
		verdicts: map[int]*taskVerdictRecord{1: {state: verdictPending}},
	}

	output := renderReviewPanel(sess, entry, 140, 40, 0, false, 0, nil)
	stripped := ansi.Strip(output)

	if !strings.Contains(stripped, "?") {
		t.Error("footer must include '?' hint for spec overlay")
	}
	if !strings.Contains(stripped, "enter") {
		t.Error("footer must include 'enter' hint for opening task diff")
	}
	if strings.Contains(stripped, "pgdn") {
		t.Error("footer must not include 'pgdn' hint (inline viewport removed)")
	}
	if strings.Contains(output, "view task diff") {
		t.Error("footer must not include 'view task diff' hint (removed in favor of full-screen diff viewer)")
	}
}

// TestRenderReviewSpecOverlay_ContainsAllSections verifies that the spec overlay
// renders Goal, Spec, Verification, and Not in scope section content.
func TestRenderReviewSpecOverlay_ContainsAllSections(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSessionForTestWithPath("spec-sess", "fix-auth", dir)
	sess.SetOriginalPrompt("Fix auth")

	plan := `# Goal
Fix the auth redirect bug.

## Spec
1. Users redirect correctly.
2. Tokens validated.

## Context
internal/auth/handler.go:42

## Tasks
- [ ] write test
- [ ] implement

## Verification
go test -race ./internal/auth

## Not in scope
OAuth2 support`

	if err := sess.WritePlan(plan); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	planContent, _ := sess.CachedPlan()

	output := renderReviewSpecOverlay(sess, planContent, 0, 120, 40)

	for _, want := range []string{"Goal", "Spec", "Verification", "Not in scope"} {
		if !strings.Contains(output, want) {
			t.Errorf("overlay must contain %q; got:\n%s", want, output)
		}
	}
	if !strings.Contains(output, "Fix the auth redirect bug.") {
		t.Errorf("overlay must contain Goal body text; got:\n%s", output)
	}
	if !strings.Contains(output, "go test -race") {
		t.Errorf("overlay must contain Verification body text; got:\n%s", output)
	}
	if !strings.Contains(output, "OAuth2 support") {
		t.Errorf("overlay must contain Not in scope body text; got:\n%s", output)
	}
}

// TestRenderReviewPanel_PRDraftInFlight verifies that the spinner status line
// and disabled p hint appear when prDraftInFlight is true.
func TestRenderReviewPanel_PRDraftInFlight(t *testing.T) {
	sess := agent.NewSessionForTest("sess-1", "fix-auth")
	sess.SetOriginalPrompt("Fix the auth bug")
	sess.MarkDone()

	output := renderReviewPanel(sess, nil, 120, 40, 0, true, 0, nil)

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

// TestRenderReviewPanel_TaskHasDiff verifies that when a task group has a
// non-empty rawDiff, the stacked review panel shows the verdict label.
// The diff itself is no longer shown inline — enter opens the full-screen viewer.
func TestRenderReviewPanel_TaskHasDiff(t *testing.T) {
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

	output := renderReviewPanel(sess, entry, 140, 40, 0, false, 0, nil)

	if !strings.Contains(output, "pass") {
		t.Error("must contain verdict label 'pass'")
	}
	// Diff hunk header should NOT appear inline — the diff is accessed via enter.
	if strings.Contains(output, "@@") {
		t.Errorf("inline diff must NOT appear in stacked layout; got:\n%s", output)
	}
}

// TestReviewPanel_CursorMoveSwapsDetail verifies that moving the cursor from task 1
// to task 2 causes the detail pane to show task 2's heading and commits.
// Diffs are no longer shown inline — they open via enter.
func TestReviewPanel_CursorMoveSwapsDetail(t *testing.T) {
	sess := agent.NewSessionForTest("sess-cursor", "fix-auth")
	sess.SetOriginalPrompt("Fix auth")
	sess.MarkDone()

	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{
			{Index: 1, Text: "Task one", Done: false},
			{Index: 2, Text: "Task two", Done: false},
		},
		groups: []taskReviewGroup{
			{taskIndex: 1, commits: []git.Commit{{Hash: "aaa1111"}}, rawDiff: ""},
			{taskIndex: 2, commits: []git.Commit{{Hash: "bbb2222"}}, rawDiff: ""},
		},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictPending},
			2: {state: verdictPending},
		},
	}

	// cursor=0 → task 1 heading in detail pane
	out0 := renderReviewPanel(sess, entry, 140, 40, 0, false, 0, nil)
	if !strings.Contains(out0, "Task 1:") {
		t.Errorf("cursor=0: expected 'Task 1:' in detail pane; got:\n%s", out0)
	}
	if strings.Contains(out0, "Task 2:") {
		t.Errorf("cursor=0: must not contain 'Task 2:'; got:\n%s", out0)
	}

	// cursor=1 → task 2 heading in detail pane
	out1 := renderReviewPanel(sess, entry, 140, 40, 1, false, 0, nil)
	if !strings.Contains(out1, "Task 2:") {
		t.Errorf("cursor=1: expected 'Task 2:' in detail pane; got:\n%s", out1)
	}
	if strings.Contains(out1, "Task 1:") {
		t.Errorf("cursor=1: must not contain 'Task 1:'; got:\n%s", out1)
	}
}

// TestRenderTaskDetailPane_ContentWidthCapped asserts that on a wide pane, content
// is capped at reviewDetailMaxMeasure rather than filling the full pane width.
func TestRenderTaskDetailPane_ContentWidthCapped(t *testing.T) {
	rationale := strings.Repeat("This is a long rationale that should be wrapped correctly. ", 5)
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 1, Text: "Add auth middleware"}},
		groups: []taskReviewGroup{{
			taskIndex: 1,
			commits:   []git.Commit{{Hash: "abc1234", Subject: "add middleware"}},
			files:     []git.FileStat{{Path: "internal/auth.go", Insertions: 5, Deletions: 1}},
			stats:     &git.DiffStats{Files: 1, Insertions: 5, Deletions: 1},
		}},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictPass, Rationale: rationale}},
		},
	}

	const width = 120
	lines := renderTaskDetailPane(entry, 0, width, 30)

	for i, l := range lines {
		if l == "" {
			continue
		}
		// Strip centering padding (leading spaces added for centering) then check
		// that no content line exceeds the max measure.
		stripped := strings.TrimLeft(l, " ")
		cw := ansi.StringWidth(stripped)
		if cw > reviewDetailMaxMeasure {
			t.Errorf("line %d content width %d exceeds reviewDetailMaxMeasure %d: %q",
				i, cw, reviewDetailMaxMeasure, l)
		}
	}
}

// TestRenderTaskDetailPane_RationaleHasANSIStyling asserts that rationale text is
// rendered via reviewRenderer.RenderLines(measure=72) rather than wrapText(maxW=70).
// A 72-char string that fits in measure=72 but not maxW=70 is used as the probe:
// wrapText(70) would split "token refresh!!" across lines, while RenderLines(72)
// keeps it on one line. This test works in no-color environments.
func TestRenderTaskDetailPane_RationaleHasANSIStyling(t *testing.T) {
	// Exactly 72 chars — fits in measure=72, does NOT fit in maxW=70.
	rationale := "This implementation is correct and handles auth redirect token refresh!!"
	if len(rationale) != 72 {
		t.Fatalf("test string must be 72 chars, got %d", len(rationale))
	}
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

	// width=80 → measure=72 (capped), maxW=70. If wrapText(70) were used, "refresh!!"
	// would land on its own line; RenderLines(72) keeps "token refresh!!" together.
	lines := renderTaskDetailPane(entry, 0, 80, 30)
	out := strings.Join(lines, "\n")

	if !strings.Contains(ansi.Strip(out), "token refresh!!") {
		t.Error("72-char rationale must not be split: 'token refresh!!' should appear on one line " +
			"(verifies RenderLines(measure=72) is used, not wrapText(maxW=70))")
	}
}

// TestRenderTaskDetailPane_HeadingHasUnderline asserts that the line immediately
// following the "Task N:" heading contains the ─ underline character.
func TestRenderTaskDetailPane_HeadingHasUnderline(t *testing.T) {
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 1, Text: "Add auth middleware"}},
		groups: []taskReviewGroup{{
			taskIndex: 1,
			commits:   []git.Commit{{Hash: "abc1234", Subject: "add middleware"}},
			stats:     &git.DiffStats{Files: 1, Insertions: 5, Deletions: 1},
		}},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictPass}},
		},
	}

	lines := renderTaskDetailPane(entry, 0, 80, 20)

	headingIdx := -1
	for i, l := range lines {
		if strings.Contains(ansi.Strip(l), "Task 1:") {
			headingIdx = i
			break
		}
	}
	if headingIdx < 0 {
		t.Fatal("heading line 'Task 1:' not found")
	}
	if headingIdx+1 >= len(lines) {
		t.Fatal("no line after heading")
	}
	if !strings.Contains(lines[headingIdx+1], "─") {
		t.Errorf("line after heading must contain '─' underline; got: %q", lines[headingIdx+1])
	}
}

// TestRenderReviewPanel_FooterUsesThemeStyles asserts that footer key names use
// StyleActive. In environments with color output the ANSI-coded styled key must
// appear; in no-color environments the test verifies structural completeness.
func TestRenderReviewPanel_FooterUsesThemeStyles(t *testing.T) {
	sess := agent.NewSessionForTest("sess-1", "fix-auth")
	sess.SetOriginalPrompt("Fix auth")
	sess.MarkDone()

	output := renderReviewPanel(sess, nil, 120, 40, 0, false, 0, nil)

	// StyleActive.Render("p") — carries ANSI codes in color mode, is "p" otherwise.
	// In both cases the rendered footer must contain it.
	styledP := StyleActive.Render("p")
	if !strings.Contains(output, styledP) {
		t.Errorf("footer must use StyleActive for 'p' key; %q not found in output", styledP)
	}

	// In color mode, verify the 't' key is also styled via StyleActive (not the old
	// ad-hoc lighter-cyan #7ec8e3).
	styledT := StyleActive.Render("t")
	if !strings.Contains(output, styledT) {
		t.Errorf("footer must use StyleActive for 't' key; %q not found in output", styledT)
	}
}

// TestRenderTaskListPane_HeaderUsesHeadingColor asserts that "PLAN TASKS" is
// followed by a ─ underline (structural change from the old blank-line separator).
func TestRenderTaskListPane_HeaderUsesHeadingColor(t *testing.T) {
	entry := &reviewDiffEntry{
		tasks:  []agent.PlanTask{{Index: 1, Text: "Add auth middleware"}},
		groups: []taskReviewGroup{},
	}

	lines := renderTaskListPane(entry, 40, 10, 0)
	out := strings.Join(lines, "\n")

	if !strings.Contains(out, "PLAN TASKS") {
		t.Error("header must contain PLAN TASKS")
	}

	headerIdx := -1
	for i, l := range lines {
		if strings.Contains(l, "PLAN TASKS") {
			headerIdx = i
			break
		}
	}
	if headerIdx < 0 {
		t.Fatal("no line containing PLAN TASKS found")
	}

	// The line immediately after PLAN TASKS must be the ─ underline (not a blank line).
	if headerIdx+1 >= len(lines) {
		t.Fatal("no line after PLAN TASKS header")
	}
	if !strings.Contains(lines[headerIdx+1], "─") {
		t.Errorf("line after PLAN TASKS must contain '─' underline; got: %q", lines[headerIdx+1])
	}
}

// TestRenderReviewPanel_ShowsTabBar verifies that renderReviewPanel includes a
// tab bar containing all three tab labels.
func TestRenderReviewPanel_ShowsTabBar(t *testing.T) {
	sess := agent.NewSessionForTest("sess-tabs", "fix-auth")
	sess.SetOriginalPrompt("Fix auth")
	sess.MarkDone()
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 1, Text: "Add auth middleware"}},
		groups: []taskReviewGroup{{
			taskIndex: 1,
			commits:   []git.Commit{{Hash: "abc1234", Subject: "add middleware"}},
		}},
		verdicts: map[int]*taskVerdictRecord{1: {state: verdictPending}},
	}

	output := renderReviewPanel(sess, entry, 120, 40, 0, false, 0, nil)

	for _, tab := range []string{"Tasks", "Diff", "Checks"} {
		if !strings.Contains(ansi.Strip(output), tab) {
			t.Errorf("panel must contain tab label %q; got:\n%s", tab, output)
		}
	}
}

// TestRenderReviewPanel_DiffTabPlaceholder verifies that when activeTab=1 (Diff)
// the panel output contains the placeholder text and not the task list header.
func TestRenderReviewPanel_DiffTabPlaceholder(t *testing.T) {
	sess := agent.NewSessionForTest("sess-difftab", "fix-auth")
	sess.SetOriginalPrompt("Fix auth")
	sess.MarkDone()
	entry := &reviewDiffEntry{
		tasks:    []agent.PlanTask{{Index: 1, Text: "task one"}},
		verdicts: map[int]*taskVerdictRecord{1: {state: verdictPending}},
	}

	output := renderReviewPanel(sess, entry, 120, 40, 0, false, reviewTabDiff, nil)
	stripped := ansi.Strip(output)

	if !strings.Contains(stripped, "full diff browser coming soon") {
		t.Errorf("Diff tab must show placeholder; got:\n%s", stripped)
	}
	if strings.Contains(stripped, "PLAN TASKS") {
		t.Errorf("Diff tab must not show task list header; got:\n%s", stripped)
	}
}

// TestRenderReviewPanel_TasksTabStillWorks verifies that activeTab=0 (Tasks)
// still renders the task list as a regression guard.
func TestRenderReviewPanel_TasksTabStillWorks(t *testing.T) {
	sess := agent.NewSessionForTest("sess-taskstab", "fix-auth")
	sess.SetOriginalPrompt("Fix auth")
	sess.MarkDone()
	entry := &reviewDiffEntry{
		tasks:    []agent.PlanTask{{Index: 1, Text: "task one"}},
		verdicts: map[int]*taskVerdictRecord{1: {state: verdictPending}},
	}

	output := renderReviewPanel(sess, entry, 120, 40, 0, false, reviewTabTasks, nil)
	stripped := ansi.Strip(output)

	if !strings.Contains(stripped, "PLAN TASKS") {
		t.Errorf("Tasks tab must show task list header; got:\n%s", stripped)
	}
}

// TestRenderReviewPanel_FooterShowsEnterHint verifies that the footer hint
// says "enter — open task diff" instead of the old "pgdn/pgup — scroll diff".
func TestRenderReviewPanel_FooterShowsEnterHint(t *testing.T) {
	sess := agent.NewSessionForTest("sess-footer", "fix-auth")
	sess.SetOriginalPrompt("Fix auth")
	sess.MarkDone()
	entry := &reviewDiffEntry{
		tasks:    []agent.PlanTask{{Index: 1, Text: "task one"}},
		verdicts: map[int]*taskVerdictRecord{1: {state: verdictPending}},
	}

	output := renderReviewPanel(sess, entry, 120, 40, 0, false, reviewTabTasks, nil)
	stripped := ansi.Strip(output)

	if !strings.Contains(stripped, "enter") {
		t.Errorf("footer must contain 'enter' hint; got:\n%s", stripped)
	}
	if strings.Contains(stripped, "pgdn") {
		t.Errorf("footer must not contain old 'pgdn' hint; got:\n%s", stripped)
	}
}

// TestRenderReviewPlaceholderTab verifies that renderReviewPlaceholderTab
// returns exactly height lines, that the first line contains the label text
// horizontally centered, and that remaining lines are empty.
func TestRenderReviewPlaceholderTab(t *testing.T) {
	label := "full diff browser coming soon"
	const width = 80
	lines := renderReviewPlaceholderTab(label, width, 20)
	if len(lines) != 20 {
		t.Fatalf("expected 20 lines, got %d", len(lines))
	}
	stripped := ansi.Strip(lines[0])
	if !strings.Contains(stripped, label) {
		t.Errorf("first line must contain label %q; got: %q", label, stripped)
	}
	// Centered: the first line must have leading spaces pushing the label toward
	// the middle. With width=80 and the label shorter than width, there should be
	// at least one leading space.
	if !strings.HasPrefix(lines[0], " ") {
		t.Errorf("first line must be horizontally centered (leading spaces expected); got: %q", lines[0])
	}
	// Remaining lines are empty.
	for i := 1; i < 20; i++ {
		if lines[i] != "" {
			t.Errorf("line %d: expected empty, got %q", i, lines[i])
		}
	}
}

// TestRenderReviewTabBar_HighlightsActiveTab verifies that renderReviewTabBar
// returns 2 lines, contains all three tab labels in the first line, and applies
// the correct styling: active tab in ColorSecondary bold, inactive tabs in StyleSubtle.
func TestRenderReviewTabBar_HighlightsActiveTab(t *testing.T) {
	tabNames := []string{"Tasks", "Diff", "Checks"}
	activeStyle := lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true)

	for activeIdx, activeName := range tabNames {
		lines := renderReviewTabBar(activeIdx, 120)
		if len(lines) != 2 {
			t.Fatalf("activeTab=%d: expected 2 lines, got %d", activeIdx, len(lines))
		}
		stripped := ansi.Strip(lines[0])
		for _, name := range tabNames {
			if !strings.Contains(stripped, name) {
				t.Errorf("activeTab=%d: label line must contain %q; got: %q", activeIdx, name, stripped)
			}
		}
		// Second line must be a divider.
		if !strings.Contains(ansi.Strip(lines[1]), "─") {
			t.Errorf("activeTab=%d: line 1 must be a divider; got: %q", activeIdx, lines[1])
		}

		// Active tab must use ColorSecondary bold; inactive tabs must use StyleSubtle.
		styledActive := activeStyle.Render(activeName)
		if !strings.Contains(lines[0], styledActive) {
			t.Errorf("activeTab=%d: active label %q not styled with ColorSecondary bold;\nraw line: %q",
				activeIdx, activeName, lines[0])
		}
		for i, name := range tabNames {
			if i == activeIdx {
				continue
			}
			styledInactive := StyleSubtle.Render(name)
			if !strings.Contains(lines[0], styledInactive) {
				t.Errorf("activeTab=%d: inactive label %q not styled with StyleSubtle;\nraw line: %q",
					activeIdx, name, lines[0])
			}
		}
	}
}

// TestRenderReviewPanel_NoDiffTask verifies that when a task group has an
// empty rawDiff, the panel renders without panic and shows the "no diff found"
// verdict badge in the detail pane. There is no inline diff placeholder since
// the diff pane has been removed.
func TestRenderReviewPanel_NoDiffTask(t *testing.T) {
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

	// Must not panic and must show the "no diff found" verdict badge.
	output := renderReviewPanel(sess, entry, 140, 40, 0, false, 0, nil)
	if !strings.Contains(output, "no diff found") {
		t.Errorf("must show 'no diff found' verdict badge; got:\n%s", output)
	}
}

// capturingReviewer captures the ReviewRequest passed to Review for assertion.
type capturingReviewer struct {
	captured agent.ReviewRequest
}

func (c *capturingReviewer) Review(_ context.Context, req agent.ReviewRequest) (agent.ReviewVerdict, error) {
	c.captured = req
	return agent.ReviewVerdict{Kind: agent.VerdictPass, Rationale: "stub"}, nil
}

// TestReviewTaskCmd_PassesTaskDetail verifies that reviewTaskCmd populates
// TaskDetail from PlanTask.Body and ChangedFiles from the group's file list.
func TestReviewTaskCmd_PassesTaskDetail(t *testing.T) {
	sess := agent.NewSessionForTest("sess-1", "add-widget")

	app := NewApp()
	const repoPath = "/test/repo"
	const taskIndex = 2

	// Pre-populate the cache with tasks that have Body set.
	app.reviewDiffCache[cacheKey(repoPath, sess.ID)] = &reviewDiffEntry{
		tasks: []agent.PlanTask{
			{Index: 1, Text: "First task", Body: "  - Files: internal/other.go"},
			{Index: 2, Text: "Add widget", Body: "  - Files: internal/widget.go\n  - Implement: add NewWidget"},
		},
	}

	group := taskReviewGroup{
		taskIndex: taskIndex,
		rawDiff:   "diff --git a/widget.go b/widget.go\n+func NewWidget() {}",
		files: []git.FileStat{
			{Path: "internal/widget.go", Insertions: 10, Deletions: 0},
			{Path: "internal/widget_test.go", Insertions: 20, Deletions: 0},
		},
	}

	reviewer := &capturingReviewer{}
	cmd := app.reviewTaskCmd(sess, repoPath, group, reviewer)
	cmd() // execute synchronously; reviewer.Review is called inline

	req := reviewer.captured
	if req.TaskDetail == "" {
		t.Error("TaskDetail must be populated from PlanTask.Body, got empty")
	}
	if !strings.Contains(req.TaskDetail, "Files: internal/widget.go") {
		t.Errorf("TaskDetail must contain Files sub-bullet, got: %q", req.TaskDetail)
	}
	if !strings.Contains(req.TaskDetail, "Implement: add NewWidget") {
		t.Errorf("TaskDetail must contain Implement sub-bullet, got: %q", req.TaskDetail)
	}

	if len(req.ChangedFiles) != 2 {
		t.Fatalf("ChangedFiles must have 2 entries, got %d: %v", len(req.ChangedFiles), req.ChangedFiles)
	}
	foundWidget := false
	foundTest := false
	for _, f := range req.ChangedFiles {
		if f == "internal/widget.go" {
			foundWidget = true
		}
		if f == "internal/widget_test.go" {
			foundTest = true
		}
	}
	if !foundWidget {
		t.Errorf("ChangedFiles must include internal/widget.go, got: %v", req.ChangedFiles)
	}
	if !foundTest {
		t.Errorf("ChangedFiles must include internal/widget_test.go, got: %v", req.ChangedFiles)
	}
}

// TestRenderChecksTab_ShowsCheckNames verifies the check list renders names and spinner.
func TestRenderChecksTab_ShowsCheckNames(t *testing.T) {
	cs := &checksTabState{
		checks: []config.ValidationCheck{
			{Name: "Tests", Command: "go test ./..."},
			{Name: "Lint", Command: "golangci-lint run"},
		},
		results: []validationCheckResult{
			{state: checkRunning},
			{state: checkRunning},
		},
	}
	lines := renderChecksTab(cs, 80, 20)
	out := strings.Join(lines, "\n")
	stripped := ansi.Strip(out)

	if !strings.Contains(stripped, "Tests") {
		t.Error("must contain check name 'Tests'")
	}
	if !strings.Contains(stripped, "Lint") {
		t.Error("must contain check name 'Lint'")
	}
}

// TestRenderChecksTab_PassedCheck verifies a passed check shows the ✓ icon.
func TestRenderChecksTab_PassedCheck(t *testing.T) {
	cs := &checksTabState{
		checks:  []config.ValidationCheck{{Name: "Tests", Command: "go test ./..."}},
		results: []validationCheckResult{{state: checkPassed, exitCode: 0}},
	}
	lines := renderChecksTab(cs, 80, 20)
	out := ansi.Strip(strings.Join(lines, "\n"))

	if !strings.Contains(out, "✓") {
		t.Errorf("passed check must show ✓; got:\n%s", out)
	}
}

// TestRenderChecksTab_OutputPane verifies the selected check's output appears in the pane.
func TestRenderChecksTab_OutputPane(t *testing.T) {
	cs := &checksTabState{
		checks:  []config.ValidationCheck{{Name: "Tests", Command: "go test ./..."}},
		results: []validationCheckResult{{state: checkPassed, output: "PASS: all 42 tests"}},
		cursor:  0,
	}
	lines := renderChecksTab(cs, 80, 20)
	out := ansi.Strip(strings.Join(lines, "\n"))

	if !strings.Contains(out, "PASS: all 42 tests") {
		t.Errorf("output pane must contain check output; got:\n%s", out)
	}
}

// TestRenderChecksTab_NilState_ShowsPlaceholder verifies the no-checks placeholder.
func TestRenderChecksTab_NilState_ShowsPlaceholder(t *testing.T) {
	sess := agent.NewSessionForTest("sess-1", "fix-auth")
	sess.SetOriginalPrompt("Fix auth")
	sess.MarkDone()

	output := renderReviewPanel(sess, nil, 120, 40, 0, false, reviewTabChecks, nil)
	stripped := ansi.Strip(output)

	if !strings.Contains(stripped, "No validation checks configured") {
		t.Errorf("must show no-checks placeholder; got:\n%s", stripped)
	}
}
