package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/refrain/internal/agent"
	"github.com/devenjarvis/refrain/internal/config"
	"github.com/devenjarvis/refrain/internal/git"
	"github.com/devenjarvis/refrain/internal/tui/theme"
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

	lines := renderReviewHeader(sess, width, time.Now())

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

// TestRenderTaskLedger_CardLayout verifies that each task card occupies exactly
// 4 lines with the expected content in each line.
func TestRenderTaskLedger_CardLayout(t *testing.T) {
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{
			{Index: 1, Text: "Implement feature"},
			{Index: 2, Text: "Write tests"},
		},
		groups: []taskReviewGroup{
			{
				taskIndex: 1,
				commits:   []git.Commit{{Hash: "abc1234", Subject: "feat: implement"}},
				files:     []git.FileStat{{Path: "auth.go", Insertions: 42, Deletions: 3}},
				stats:     &git.DiffStats{Files: 1, Insertions: 42, Deletions: 3},
			},
			{
				taskIndex: 2,
				commits:   []git.Commit{{Hash: "def5678", Subject: "test: add tests"}},
				files:     []git.FileStat{{Path: "auth_test.go", Insertions: 10, Deletions: 0}},
				stats:     &git.DiffStats{Files: 1, Insertions: 10, Deletions: 0},
			},
		},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictPass, Rationale: "Solid impl"}},
			2: {state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictConcerns, Rationale: "missing test"}},
		},
	}

	const width = 60
	// Height: 2 header + 2 cards × 4 lines = 10 minimum.
	lines := renderTaskListPane(entry, width, 12, 0, time.Now())

	// Skip 2-line header; card 0 starts at lines[2].
	const headerLen = 2
	if len(lines) < headerLen+8 {
		t.Fatalf("expected at least %d lines, got %d:\n%s", headerLen+8, len(lines), strings.Join(lines, "\n"))
	}

	card0 := lines[headerLen : headerLen+4]
	card1 := lines[headerLen+4 : headerLen+8]

	// Card 0: pass verdict.
	l0 := ansi.Strip(card0[0])
	if !strings.Contains(l0, "[1]") {
		t.Errorf("card0 line1: missing [1]; got: %q", l0)
	}
	if !strings.Contains(l0, "Implement feature") {
		t.Errorf("card0 line1: missing task text; got: %q", l0)
	}
	if !strings.Contains(l0, "+42") {
		t.Errorf("card0 line1: missing +42 stat; got: %q", l0)
	}
	if !strings.Contains(l0, "-3") {
		t.Errorf("card0 line1: missing -3 stat; got: %q", l0)
	}
	l1 := ansi.Strip(card0[1])
	if !strings.Contains(l1, "pass") {
		t.Errorf("card0 line2: missing 'pass' verdict; got: %q", l1)
	}
	if !strings.Contains(l1, "Solid impl") {
		t.Errorf("card0 line2: missing rationale; got: %q", l1)
	}
	l2 := ansi.Strip(card0[2])
	if !strings.Contains(l2, "auth.go") {
		t.Errorf("card0 line3: missing top file name; got: %q", l2)
	}
	if card0[3] != "" {
		t.Errorf("card0 line4: expected blank separator, got: %q", card0[3])
	}

	// Card 1: concerns verdict.
	l0c1 := ansi.Strip(card1[0])
	if !strings.Contains(l0c1, "[2]") {
		t.Errorf("card1 line1: missing [2]; got: %q", l0c1)
	}
	l1c1 := ansi.Strip(card1[1])
	if !strings.Contains(l1c1, "concerns") {
		t.Errorf("card1 line2: missing 'concerns' verdict; got: %q", l1c1)
	}
	if !strings.Contains(l1c1, "missing test") {
		t.Errorf("card1 line2: missing rationale; got: %q", l1c1)
	}
	if card1[3] != "" {
		t.Errorf("card1 line4: expected blank separator, got: %q", card1[3])
	}
}

// TestRenderTaskLedger_CursorStripeOnSelected verifies that the selected card's
// lines start and end with the cursor-stripe character '│'.
func TestRenderTaskLedger_CursorStripeOnSelected(t *testing.T) {
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{
			{Index: 1, Text: "task one"},
			{Index: 2, Text: "task two"},
		},
		groups: []taskReviewGroup{
			{taskIndex: 1, stats: &git.DiffStats{Insertions: 1}},
			{taskIndex: 2, stats: &git.DiffStats{Insertions: 2}},
		},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictPending},
			2: {state: verdictPending},
		},
	}

	// cursor=1: card 1 is selected.
	lines := renderTaskListPane(entry, 60, 12, 1, time.Now())

	const headerLen = 2
	if len(lines) < headerLen+8 {
		t.Fatalf("expected at least %d lines, got %d", headerLen+8, len(lines))
	}

	card0 := lines[headerLen : headerLen+4]   // unselected
	card1 := lines[headerLen+4 : headerLen+8] // selected

	// Unselected card must NOT have cursor stripes on first line.
	if strings.Contains(card0[0], "│") {
		t.Errorf("unselected card line1 must not contain │; got: %q", card0[0])
	}

	// Selected card must have cursor stripes on first line.
	if !strings.Contains(card1[0], "│") {
		t.Errorf("selected card line1 must contain │ cursor stripe; got: %q", card1[0])
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
				commits:   []git.Commit{{Hash: "abc123", Subject: "feat: add middleware"}},
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

	// height=16 fits all 3 cards (2 header + 3×4 card lines).
	const width, height, cursor = 40, 16, 0
	lines := renderTaskListPane(entry, width, height, cursor, time.Now())
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

// TestRenderReviewPanel_RightPaneShowsFocusedTaskDiff verifies that the right
// pane in wide mode shows the diff for the cursor-selected task and swaps
// when the cursor moves.
func TestRenderReviewPanel_RightPaneShowsFocusedTaskDiff(t *testing.T) {
	rawDiff1 := "diff --git a/a.go b/a.go\nindex 000..111 100644\n--- a/a.go\n+++ b/a.go\n@@ -1 +1,2 @@\n package main\n+// MARKER_TASK_1\n"
	rawDiff2 := "diff --git a/b.go b/b.go\nindex 000..222 100644\n--- a/b.go\n+++ b/b.go\n@@ -1 +1,2 @@\n package main\n+// MARKER_TASK_2\n"

	sess := agent.NewSessionForTest("sess-rightpane", "fix-auth")
	sess.SetOriginalPrompt("Fix auth")
	sess.MarkDone()

	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{
			{Index: 1, Text: "task one"},
			{Index: 2, Text: "task two"},
		},
		groups: []taskReviewGroup{
			{taskIndex: 1, rawDiff: rawDiff1},
			{taskIndex: 2, rawDiff: rawDiff2},
		},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictPending},
			2: {state: verdictPending},
		},
	}

	app := reviewTestApp()
	app.reviewDiffCache[cacheKey("", sess.ID)] = entry
	panel := newReviewPanel(sess, "", 140, 40, app.buildReviewDeps())

	// Prime the viewport via WindowSizeMsg (triggers syncDiffPane for cursor=0).
	_, _ = panel.Update(tea.WindowSizeMsg{Width: 140, Height: 40})

	// cursor=0 → task 1 diff in right pane.
	out0 := ansi.Strip(panel.View())
	if !strings.Contains(out0, "MARKER_TASK_1") {
		t.Errorf("cursor=0: right pane must show MARKER_TASK_1; got:\n%s", out0)
	}
	if strings.Contains(out0, "MARKER_TASK_2") {
		t.Errorf("cursor=0: right pane must NOT show MARKER_TASK_2; got:\n%s", out0)
	}

	// cursor=1 → task 2 diff in right pane (j key triggers syncDiffPane).
	_, _ = panel.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	out1 := ansi.Strip(panel.View())
	if !strings.Contains(out1, "MARKER_TASK_2") {
		t.Errorf("cursor=1: right pane must show MARKER_TASK_2; got:\n%s", out1)
	}
	if strings.Contains(out1, "MARKER_TASK_1") {
		t.Errorf("cursor=1: right pane must NOT show MARKER_TASK_1; got:\n%s", out1)
	}
}

// TestRenderReviewPanel_TwoPaneLayout verifies that the card ledger renders
// task data: PLAN TASKS header, task index, verdict label, and footer hint.
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

	output := renderReviewPanel(sess, entry, 140, 30, 0, false, nil, time.Now())

	if !strings.Contains(output, "PLAN TASKS") {
		t.Error("must contain PLAN TASKS from task ledger")
	}
	if !strings.Contains(output, "[1]") {
		t.Error("must contain [1] task index from card line 1")
	}
	if !strings.Contains(output, "pass") {
		t.Error("must contain 'pass' verdict label from card line 2")
	}
	if !strings.Contains(output, "ship") {
		t.Error("footer must still advertise 'ship' (p ship)")
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

	output := renderReviewPanel(sess, entry, 120, 40, 0, false, nil, time.Now())

	if !strings.Contains(output, "PLAN TASKS") {
		t.Error("must contain PLAN TASKS from task ledger")
	}
	if !strings.Contains(output, "[1]") {
		t.Error("must contain [1] task index")
	}
	// Full-width: the long task name must not be truncated.
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

	output := renderReviewPanel(sess, entry, 70, 30, 0, false, nil, time.Now())

	if !strings.Contains(output, "PLAN TASKS") {
		t.Error("must contain PLAN TASKS from task ledger")
	}
	if !strings.Contains(output, "[1]") {
		t.Error("must contain [1] task index")
	}
	if !strings.Contains(output, "pass") {
		t.Error("must contain 'pass' verdict in card line 2")
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

	output := renderReviewPanel(sess, entry, 120, 40, 0, false, nil, time.Now())

	if !strings.Contains(output, "Fix the auth bug") {
		t.Error("review panel must show the original prompt in the header")
	}
}

func TestRenderReviewPanel_NilDiffEntry(t *testing.T) {
	sess := agent.NewSessionForTest("sess-1", "fix-auth")
	sess.SetOriginalPrompt("Fix the auth bug")

	// nil entry — should not panic
	output := renderReviewPanel(sess, nil, 120, 40, 0, false, nil, time.Now())
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

	output := renderReviewPanel(sess, entry, 120, 30, 0, false, nil, time.Now())

	// The pass verdict icon should carry the ColorSuccess ANSI escape.
	expectedPrefix := StyleSuccess.Render("✓")
	if !strings.Contains(output, expectedPrefix) {
		t.Errorf("pass verdict must use StyleSuccess ANSI color; styled icon %q not found in output", expectedPrefix)
	}
}

// TestReviewPanel_ClickHitsLeftPaneOnly verifies that at width=140 (two-pane)
// a click in the left pane moves the cursor but a click in the right pane
// (x=80) leaves the cursor unchanged. At width=80 (stacked), clicks in the
// task-ledger area move the cursor.
func TestReviewPanel_ClickHitsLeftPaneOnly(t *testing.T) {
	sess := agent.NewSessionForTest("s1", "fix-auth")
	// renderReviewHeader for this session returns 3 lines (title+prompt+divider).
	// No tab bar → paneTop = dashboardTopY(0) + headerH(3) = 3.
	// listHeaderLines=2 → first card at Y=5.
	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{
			{Index: 1, Text: "task one"},
			{Index: 2, Text: "task two"},
		},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictPending},
			2: {state: verdictPending},
		},
	}
	app := reviewTestApp()
	app.reviewDiffCache[cacheKey("", sess.ID)] = entry

	// Width=140: two-pane mode. leftPaneWidth = clamp(140*40/100, 38, 52) = 52.
	panel140 := newReviewPanel(sess, "", 140, 40, app.buildReviewDeps())
	// Move cursor to 1 so we can test a click that should return it to 0.
	panel140.taskCursor = 1

	// Click at x=10, Y=5 (first card, within left pane width=52) → cursor 0.
	_, _ = panel140.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 10, Y: 5})
	if panel140.TaskCursor() != 0 {
		t.Errorf("wide: click at x=10 (left pane) should move cursor to 0, got %d", panel140.TaskCursor())
	}

	// Reset cursor.
	panel140.taskCursor = 1
	// Click at x=80 (right pane: x >= leftPaneWidth+1 = 53) → cursor unchanged.
	_, _ = panel140.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 80, Y: 5})
	if panel140.TaskCursor() != 1 {
		t.Errorf("wide: click at x=80 (right pane) must not change cursor, got %d", panel140.TaskCursor())
	}

	// Width=80: stacked mode. Full width for task ledger. leftPaneWidth = 0.
	panel80 := newReviewPanel(sess, "", 80, 40, app.buildReviewDeps())
	panel80.taskCursor = 1
	// Click at x=10, Y=5 (first card) → cursor 0.
	_, _ = panel80.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 10, Y: 5})
	if panel80.TaskCursor() != 0 {
		t.Errorf("stacked: click at Y=5 should move cursor to 0, got %d", panel80.TaskCursor())
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

	// With 4-line cards: header at paneTop+0,+1; card 0 at paneTop+2..+5; card 1 at +6..+9; card 2 at +10..+13.
	tests := []struct {
		name    string
		mouseX  int
		mouseY  int
		wantRow int
	}{
		{"click card 0 line 0", 5, paneTop + 2, 0},
		{"click card 0 line 3", 5, paneTop + 5, 0}, // still within card 0
		{"click card 1 line 0", 5, paneTop + 6, 1},
		{"click card 2 line 0", 5, paneTop + 10, 2},
		{"Y below all cards", 5, paneTop + 14, -1}, // 3 cards = 12 lines, so +14 is beyond
		{"in PLAN TASKS header", 5, paneTop, -1},
		{"in underline header line", 5, paneTop + 1, -1},
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

// TestRenderReviewPanel_FileModeShowsPerFileCards verifies the file-mode path
// (no plan, no commits — rollback design §4.6 mode 3): one card per changed
// file under a CHANGED FILES header, with AI verdicts disabled.
func TestRenderReviewPanel_FileModeShowsPerFileCards(t *testing.T) {
	sess := agent.NewSessionForTest("sess-1", "fix-auth")
	sess.SetOriginalPrompt("Fix the auth bug")
	sess.MarkDone()

	entry := &reviewDiffEntry{
		mode: reviewModeFiles,
		files: []git.FileStat{
			{Path: "auth.go", Status: "M", Insertions: 10, Deletions: 2},
			{Path: "auth_test.go", Status: "M", Insertions: 20, Deletions: 0},
		},
		aggregate: &git.DiffStats{Files: 2, Insertions: 30, Deletions: 2},
		groups: []taskReviewGroup{
			{taskIndex: 1, files: []git.FileStat{{Path: "auth.go", Status: "M", Insertions: 10, Deletions: 2}}, stats: &git.DiffStats{Files: 1, Insertions: 10, Deletions: 2}},
			{taskIndex: 2, files: []git.FileStat{{Path: "auth_test.go", Status: "M", Insertions: 20, Deletions: 0}}, stats: &git.DiffStats{Files: 1, Insertions: 20, Deletions: 0}},
		},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictSkipped},
			2: {state: verdictSkipped},
		},
	}

	output := renderReviewPanel(sess, entry, 120, 30, 0, false, nil, time.Now())

	if !strings.Contains(output, "CHANGED FILES") {
		t.Error("file mode must show CHANGED FILES header")
	}
	if strings.Contains(output, "Overview") {
		t.Error("legacy synthetic 'Overview' card must not render")
	}
	if !strings.Contains(output, "auth.go") || !strings.Contains(output, "auth_test.go") {
		t.Error("must show one card per changed file")
	}
	if !strings.Contains(output, "manual review") {
		t.Error("file-mode cards must show the 'manual review' badge (AI verdicts disabled)")
	}
}

// TestRenderReviewPanel_NoChangesEmptyState verifies that an entry with no
// cards at all (clean tree, no commits, no plan) renders the empty-state hint.
func TestRenderReviewPanel_NoChangesEmptyState(t *testing.T) {
	sess := agent.NewSessionForTest("sess-1", "fix-auth")
	sess.SetOriginalPrompt("Fix the auth bug")
	sess.MarkDone()

	entry := &reviewDiffEntry{
		mode:      reviewModeFiles,
		aggregate: &git.DiffStats{},
	}

	output := renderReviewPanel(sess, entry, 120, 30, 0, false, nil, time.Now())

	if !strings.Contains(output, "no changes to review yet") {
		t.Error("empty entry must render the no-changes hint")
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

	output := renderReviewPanel(sess, nil, 120, 40, 0, false, nil, time.Now())

	for _, want := range []string{
		"ship", "rework", "approve", "flag", "defer", "editor", "terminal", "expand", "spec", "back",
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
				commits:   []git.Commit{{Hash: "abc123", Subject: "feat: add middleware"}},
				stats:     &git.DiffStats{Files: 1, Insertions: 10, Deletions: 2},
			},
		},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictPass, Rationale: "looks good"}},
			2: {state: verdictPending},
		},
	}

	output := renderReviewPanel(sess, entry, 120, 40, 0, false, nil, time.Now())

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
				commits:   []git.Commit{{Hash: "abc123", Subject: "feat: add middleware"}},
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

	output := renderReviewPanel(sess, entry, 120, 40, 0, false, nil, time.Now())

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
			"aggregate only, no cards (clean no-plan session)",
			&reviewDiffEntry{
				mode:      reviewModeFiles,
				aggregate: &git.DiffStats{Files: 2, Insertions: 30, Deletions: 2},
			},
			0,
		},
		{
			"commit mode counts one row per group",
			&reviewDiffEntry{
				mode: reviewModeCommits,
				groups: []taskReviewGroup{
					{taskIndex: 1, commits: []git.Commit{{Hash: "a1"}}},
					{taskIndex: 2, commits: []git.Commit{{Hash: "b2"}}},
				},
			},
			2,
		},
		{
			"file mode counts one row per group",
			&reviewDiffEntry{
				mode: reviewModeFiles,
				groups: []taskReviewGroup{
					{taskIndex: 1, files: []git.FileStat{{Path: "a.go"}}},
				},
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
	for _, f := range theme.SpinnerBraille {
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
			icon, label, _ := verdictBadge(tt.rec, time.Now())
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

	lines := renderReviewHeader(sess, 120, time.Now())
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

	lines := renderReviewHeader(sess, 120, time.Now())
	out := strings.Join(lines, "\n")

	for _, l := range lines {
		if strings.Contains(ansi.Strip(l), "Goal:") {
			t.Errorf("renderReviewHeader must not include a 'Goal:' line when session has no plan; got:\n%s", out)
		}
	}
}

// TestRenderReviewPanel_UnifiedFooter verifies the unified single-line footer
// contains all the expected action keywords and is a single hint line.
func TestRenderReviewPanel_UnifiedFooter(t *testing.T) {
	sess := agent.NewSessionForTest("sess-footer-unified", "fix-auth")
	sess.SetOriginalPrompt("Fix auth")
	sess.MarkDone()

	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 1, Text: "Fix handler"}},
		groups: []taskReviewGroup{{
			taskIndex: 1,
			commits:   []git.Commit{{Hash: "abc1234", Subject: "fix: fix"}},
		}},
		verdicts: map[int]*taskVerdictRecord{1: {state: verdictPending}},
	}

	// Without checks — should not contain "rerun checks".
	output := renderReviewPanel(sess, entry, 140, 40, 0, false, nil, time.Now())
	stripped := ansi.Strip(output)

	for _, want := range []string{"ship", "rework", "approve", "flag", "defer", "editor", "terminal", "expand", "spec", "back"} {
		if !strings.Contains(stripped, want) {
			t.Errorf("unified footer must contain %q; got:\n%s", want, stripped)
		}
	}
	// "rerun checks" should only appear when checks are configured (tested below).

	// With checks configured — should contain "rerun checks".
	cs := &checksTabState{
		checks:  []config.ValidationCheck{{Name: "Tests", Command: "go test ./..."}},
		results: []validationCheckResult{{state: checkPassed}},
	}
	outputChecks := renderReviewPanel(sess, entry, 140, 40, 0, false, cs, time.Now())
	strippedChecks := ansi.Strip(outputChecks)
	if !strings.Contains(strippedChecks, "rerun checks") {
		t.Errorf("unified footer must contain 'rerun checks' when checks are configured; got:\n%s", strippedChecks)
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

	output := renderReviewPanel(sess, nil, 120, 40, 0, true, nil, time.Now())

	if !strings.Contains(output, "Pushing branch and drafting PR") {
		t.Error("in-flight state must show draft spinner line")
	}
	if !strings.Contains(output, "in progress") {
		t.Error("in-flight state must show disabled p hint")
	}
	if strings.Contains(output, " ship") {
		t.Error("in-flight state must not show normal p-ship hint")
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
			commits:   []git.Commit{{Hash: "abc1234", Subject: "fix: fix handler"}},
			stats:     &git.DiffStats{Files: 1, Insertions: 1, Deletions: 0},
			rawDiff:   rawDiff,
		}},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictDone, verdict: agent.ReviewVerdict{Kind: agent.VerdictPass}},
		},
	}

	output := renderReviewPanel(sess, entry, 140, 40, 0, false, nil, time.Now())

	if !strings.Contains(output, "pass") {
		t.Error("must contain verdict label 'pass'")
	}
	// Diff hunk header should NOT appear inline — the diff is accessed via enter.
	if strings.Contains(output, "@@") {
		t.Errorf("inline diff must NOT appear in stacked layout; got:\n%s", output)
	}
}

// TestReviewPanel_CursorMoveSelectsCard verifies that the cursor stripe (│) moves
// to the correct card as the cursor changes — cursor=0 selects task [1] and
// cursor=1 selects task [2].
func TestReviewPanel_CursorMoveSelectsCard(t *testing.T) {
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

	// cursor=0 → [1] card has the cursor stripe, [2] card does not.
	out0Lines := strings.Split(renderReviewPanel(sess, entry, 120, 40, 0, false, nil, time.Now()), "\n")
	found1Stripe, found2Stripe := false, false
	for _, l := range out0Lines {
		if strings.Contains(l, "│") && strings.Contains(l, "[1]") {
			found1Stripe = true
		}
		if strings.Contains(l, "│") && strings.Contains(l, "[2]") {
			found2Stripe = true
		}
	}
	if !found1Stripe {
		t.Error("cursor=0: [1] card must have cursor stripe │")
	}
	if found2Stripe {
		t.Error("cursor=0: [2] card must NOT have cursor stripe │")
	}

	// cursor=1 → [2] card has the stripe.
	out1Lines := strings.Split(renderReviewPanel(sess, entry, 120, 40, 1, false, nil, time.Now()), "\n")
	found1Stripe, found2Stripe = false, false
	for _, l := range out1Lines {
		if strings.Contains(l, "│") && strings.Contains(l, "[1]") {
			found1Stripe = true
		}
		if strings.Contains(l, "│") && strings.Contains(l, "[2]") {
			found2Stripe = true
		}
	}
	if found1Stripe {
		t.Error("cursor=1: [1] card must NOT have cursor stripe │")
	}
	if !found2Stripe {
		t.Error("cursor=1: [2] card must have cursor stripe │")
	}
}

// TestRenderReviewPanel_FooterUsesThemeStyles asserts that footer key names use
// StyleActive. In environments with color output the ANSI-coded styled key must
// appear; in no-color environments the test verifies structural completeness.
func TestRenderReviewPanel_FooterUsesThemeStyles(t *testing.T) {
	sess := agent.NewSessionForTest("sess-1", "fix-auth")
	sess.SetOriginalPrompt("Fix auth")
	sess.MarkDone()

	output := renderReviewPanel(sess, nil, 120, 40, 0, false, nil, time.Now())

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

	lines := renderTaskListPane(entry, 40, 10, 0, time.Now())
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

	output := renderReviewPanel(sess, entry, 120, 40, 0, false, nil, time.Now())
	stripped := ansi.Strip(output)

	if !strings.Contains(stripped, "enter") {
		t.Errorf("footer must contain 'enter' hint; got:\n%s", stripped)
	}
	if strings.Contains(stripped, "pgdn") {
		t.Errorf("footer must not contain old 'pgdn' hint; got:\n%s", stripped)
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
			commits:   []git.Commit{{Hash: "abc1234", Subject: "fix: fix handler"}},
			stats:     &git.DiffStats{Files: 0, Insertions: 0, Deletions: 0},
			rawDiff:   "",
		}},
		verdicts: map[int]*taskVerdictRecord{
			1: {state: verdictNoDiff},
		},
	}

	// Must not panic and must show the "no diff found" verdict badge.
	output := renderReviewPanel(sess, entry, 140, 40, 0, false, nil, time.Now())
	if !strings.Contains(output, "no diff found") {
		t.Errorf("must show 'no diff found' verdict badge; got:\n%s", output)
	}
}

// TestReviewRedesign_LegacyStringsRemoved is a scaffold test that pins the
// desired end state for the review panel redesign. Assertions for legacy
// strings pass as the refactor progresses; new-layout assertions pass when
// the corresponding tasks land.
func TestReviewRedesign_LegacyStringsRemoved(t *testing.T) {
	sess := agent.NewSessionForTest("sess-redesign", "redesign-review")
	sess.SetOriginalPrompt("Redesign the review panel")
	sess.MarkDone()

	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{{Index: 1, Text: "Refactor handler"}},
		groups: []taskReviewGroup{{
			taskIndex: 1,
			commits:   []git.Commit{{Hash: "abc1234", Subject: "refactor: refactor"}},
			stats:     &git.DiffStats{Files: 1, Insertions: 5, Deletions: 1},
		}},
		verdicts: map[int]*taskVerdictRecord{1: {state: verdictPending}},
	}

	app := reviewTestApp()
	app.reviewDiffCache[cacheKey("", sess.ID)] = entry
	// Seed validation runs so the checks strip shows after refactor.
	app.validationRuns[cacheKey("", sess.ID)] = &validationRunState{
		runID:   1,
		checks:  []config.ValidationCheck{{Name: "Tests", Command: "go test ./..."}},
		results: []validationCheckResult{{state: checkPassed}},
	}
	panel := newReviewPanel(sess, "", 140, 40, app.buildReviewDeps())

	output := panel.View()
	stripped := ansi.Strip(output)

	// Should NOT contain legacy tab-bar labels after the refactor.
	if strings.Contains(stripped, "Tasks") {
		t.Error("panel must NOT contain 'Tasks' as a tab label (tab bar removed)")
	}
	if strings.Contains(stripped, "Diff") {
		t.Error("panel must NOT contain 'Diff' as a tab label (tab bar removed)")
	}
	if strings.Contains(stripped, "(full diff browser coming soon)") {
		t.Error("panel must NOT contain the old Diff-tab placeholder")
	}

	// Should contain new layout markers after the refactor.
	if !strings.Contains(stripped, "PLAN TASKS") {
		t.Error("panel must contain 'PLAN TASKS' header in the task ledger")
	}
	if !strings.Contains(stripped, "Checks") {
		t.Error("panel must contain 'Checks' as the inline checks strip header")
	}
	if !strings.Contains(stripped, "enter expand") {
		t.Error("footer must contain 'enter expand' hint")
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
// TaskDetail from the card's detail (PlanTask.Body in plan mode) and
// ChangedFiles from the group's file list.
func TestReviewTaskCmd_PassesTaskDetail(t *testing.T) {
	sess := agent.NewSessionForTest("sess-1", "add-widget")

	const repoPath = "/test/repo"
	const taskIndex = 2

	entry := &reviewDiffEntry{
		tasks: []agent.PlanTask{
			{Index: 1, Text: "First task", Body: "  - Files: internal/other.go"},
			{Index: 2, Text: "Add widget", Body: "  - Files: internal/widget.go\n  - Implement: add NewWidget"},
		},
	}
	card := entry.ledgerCards()[1]

	group := taskReviewGroup{
		taskIndex: taskIndex,
		rawDiff:   "diff --git a/widget.go b/widget.go\n+func NewWidget() {}",
		files: []git.FileStat{
			{Path: "internal/widget.go", Insertions: 10, Deletions: 0},
			{Path: "internal/widget_test.go", Insertions: 20, Deletions: 0},
		},
	}

	reviewer := &capturingReviewer{}
	cmd := reviewTaskCmd(sess, repoPath, group, card, reviewer)
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
	lines := renderChecksTab(cs, 80, 20, time.Now())
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
	lines := renderChecksTab(cs, 80, 20, time.Now())
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
	lines := renderChecksTab(cs, 80, 20, time.Now())
	out := ansi.Strip(strings.Join(lines, "\n"))

	if !strings.Contains(out, "PASS: all 42 tests") {
		t.Errorf("output pane must contain check output; got:\n%s", out)
	}
}

// TestRenderChecksStrip_AllPassOneLine verifies that when all checks have
// passed, renderChecksStrip returns exactly one summary line containing
// "Checks", a pass count with ✓, and a duration suffix.
func TestRenderChecksStrip_AllPassOneLine(t *testing.T) {
	cs := &checksTabState{
		checks: []config.ValidationCheck{
			{Name: "Tests", Command: "go test ./..."},
			{Name: "Lint", Command: "golangci-lint run"},
		},
		results: []validationCheckResult{
			{state: checkPassed, duration: 2 * 1e9},
			{state: checkPassed, duration: 1 * 1e9},
		},
	}

	lines := renderChecksStrip(cs, 120, time.Now())

	if len(lines) != 1 {
		t.Fatalf("all-pass strip must be exactly 1 line, got %d: %v", len(lines), lines)
	}
	stripped := ansi.Strip(lines[0])
	if !strings.Contains(stripped, "Checks") {
		t.Errorf("summary line must contain 'Checks'; got: %q", stripped)
	}
	if !strings.Contains(stripped, "✓") {
		t.Errorf("summary line must contain '✓'; got: %q", stripped)
	}
	if !strings.Contains(stripped, "2") {
		t.Errorf("summary line must contain check count '2'; got: %q", stripped)
	}
	if !strings.Contains(stripped, "s") {
		t.Errorf("summary line must contain a duration suffix 's'; got: %q", stripped)
	}
}

// TestRenderChecksStrip_FailureExpandsWithTail verifies that a failed check
// expands the strip to a summary + the last 4 lines of the failed output.
func TestRenderChecksStrip_FailureExpandsWithTail(t *testing.T) {
	cs := &checksTabState{
		checks: []config.ValidationCheck{
			{Name: "Tests", Command: "go test ./..."},
		},
		results: []validationCheckResult{
			{
				state:  checkFailed,
				output: "line1\nline2\nline3\nline4\nFAIL: x\n",
			},
		},
	}

	lines := renderChecksStrip(cs, 120, time.Now())

	out := strings.Join(lines, "\n")
	stripped := ansi.Strip(out)

	if !strings.Contains(stripped, "Checks") {
		t.Errorf("summary line must contain 'Checks'; got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "Tests") {
		t.Errorf("summary line must contain the failed check name 'Tests'; got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "line2") {
		t.Errorf("strip must contain 'line2' (tail line); got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "line3") {
		t.Errorf("strip must contain 'line3' (tail line); got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "line4") {
		t.Errorf("strip must contain 'line4' (tail line); got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "FAIL: x") {
		t.Errorf("strip must contain 'FAIL: x' (last output line); got:\n%s", stripped)
	}
	// line1 is beyond the last 4 lines (output has 5 non-empty lines).
	if strings.Contains(stripped, "line1") {
		t.Errorf("strip must only show last 4 output lines; 'line1' should be omitted; got:\n%s", stripped)
	}
}
