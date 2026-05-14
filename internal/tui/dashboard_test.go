package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/baton/internal/agent"
	"github.com/devenjarvis/baton/internal/hook"
)

func TestSelectionRect_Inactive(t *testing.T) {
	d := dashboardModel{}
	if _, _, _, _, ok := d.selectionRect(); ok {
		t.Error("selectionRect on zero dashboard should report ok=false")
	}

	// active but no drag: still not a usable rect.
	d.selection = selection{anchorX: 1, anchorY: 1, cursorX: 1, cursorY: 1, active: true}
	if _, _, _, _, ok := d.selectionRect(); ok {
		t.Error("selectionRect with dragSeen=false should report ok=false")
	}
}

func TestSelectionRect_AnchorBeforeCursor(t *testing.T) {
	d := dashboardModel{
		selection: selection{
			anchorX: 2, anchorY: 1,
			cursorX: 7, cursorY: 4,
			active: true, dragSeen: true,
		},
	}
	sx, sy, ex, ey, ok := d.selectionRect()
	if !ok {
		t.Fatal("expected ok=true for drag-seen selection")
	}
	if sx != 2 || sy != 1 || ex != 7 || ey != 4 {
		t.Errorf("anchor-before-cursor: got (%d,%d)-(%d,%d), want (2,1)-(7,4)", sx, sy, ex, ey)
	}
}

func TestSelectionRect_AnchorAfterCursor(t *testing.T) {
	d := dashboardModel{
		selection: selection{
			anchorX: 7, anchorY: 4,
			cursorX: 2, cursorY: 1,
			active: true, dragSeen: true,
		},
	}
	sx, sy, ex, ey, ok := d.selectionRect()
	if !ok {
		t.Fatal("expected ok=true")
	}
	if sx != 2 || sy != 1 || ex != 7 || ey != 4 {
		t.Errorf("anchor-after-cursor: got (%d,%d)-(%d,%d), want (2,1)-(7,4)", sx, sy, ex, ey)
	}
}

func TestSelectionRect_SameRowAnchorAfterCursor(t *testing.T) {
	// Drag right-to-left on the same row should still produce a normalized
	// left-to-right rect.
	d := dashboardModel{
		selection: selection{
			anchorX: 9, anchorY: 3,
			cursorX: 4, cursorY: 3,
			active: true, dragSeen: true,
		},
	}
	sx, sy, ex, ey, ok := d.selectionRect()
	if !ok {
		t.Fatal("expected ok=true")
	}
	if sx != 4 || sy != 3 || ex != 9 || ey != 3 {
		t.Errorf("same-row reverse drag: got (%d,%d)-(%d,%d), want (4,3)-(9,3)", sx, sy, ex, ey)
	}
}

func TestSelectionRect_MultiRowReverseDrag(t *testing.T) {
	// Drag bottom-up: anchor on a later row but cursor X to the right of anchor X.
	// Normalization is by row first, so anchor's row becomes the bottom-right.
	d := dashboardModel{
		selection: selection{
			anchorX: 2, anchorY: 5,
			cursorX: 10, cursorY: 1,
			active: true, dragSeen: true,
		},
	}
	sx, sy, ex, ey, ok := d.selectionRect()
	if !ok {
		t.Fatal("expected ok=true")
	}
	if sx != 10 || sy != 1 || ex != 2 || ey != 5 {
		t.Errorf("reverse multi-row drag: got (%d,%d)-(%d,%d), want (10,1)-(2,5)", sx, sy, ex, ey)
	}
}

func TestClearSelection(t *testing.T) {
	d := dashboardModel{
		selection: selection{
			anchorX: 1, anchorY: 1, cursorX: 5, cursorY: 5,
			active: true, dragSeen: true, agentID: "abc",
		},
	}
	d.clearSelection()
	if d.selection.active || d.selection.dragSeen || d.selection.agentID != "" {
		t.Errorf("clearSelection left residue: %+v", d.selection)
	}
	if _, _, _, _, ok := d.selectionRect(); ok {
		t.Error("after clearSelection, selectionRect should report ok=false")
	}
}

// tickerDashboard builds a minimal dashboardModel for ticker tests with
// sidebarWidth=30 (yielding maxNameLen=20 in the ticker tests below).
func tickerDashboard(sessions ...*agent.Session) dashboardModel {
	d := newDashboardModel()
	d.sidebarWidth = 30
	d.prCache = make(map[string]*prCacheEntry)
	d.closingSessions = make(map[string]bool)
	for _, s := range sessions {
		d.items = append(d.items, listItem{kind: listItemSession, session: s})
	}
	return d
}

// past returns a time in the past so ticker pause/advance checks pass immediately.
func past() time.Time { return time.Now().Add(-time.Hour) }

func TestTickerSlice_Basic(t *testing.T) {
	got := tickerSlice("hello world", 6, 5)
	if got != "world" {
		t.Errorf("got %q, want %q", got, "world")
	}
}

func TestTickerSlice_OffsetAtEnd(t *testing.T) {
	got := tickerSlice("hello", 5, 10)
	if got != "" {
		t.Errorf("offset=len: got %q, want %q", got, "")
	}
}

func TestTickerSlice_OffsetPastEnd(t *testing.T) {
	got := tickerSlice("hello", 10, 5)
	if got != "" {
		t.Errorf("offset>len: got %q, want %q", got, "")
	}
}

func TestTickerSlice_MultibyteRunes(t *testing.T) {
	// "日本語" — 3 runes, each 2 display cells wide; offset=1 skips "日".
	got := tickerSlice("日本語", 1, 10)
	if got != "本語" {
		t.Errorf("multibyte: got %q, want %q", got, "本語")
	}
}

func TestAdvanceTickers_NameFits_NoTickerCreated(t *testing.T) {
	// sidebarW=30 → maxNameLen=20; "short" (5 chars) fits easily.
	sess := &agent.Session{ID: "s1", Name: "short"}
	d := tickerDashboard(sess)
	d.advanceTickers(time.Now())
	if _, exists := d.tickers["s1"]; exists {
		t.Error("ticker should not be created for a name that fits")
	}
}

func TestAdvanceTickers_NameFits_ClearsStale(t *testing.T) {
	// Stale ticker entry from a previous long name should be removed.
	sess := &agent.Session{ID: "s1", Name: "short"}
	d := tickerDashboard(sess)
	d.tickers["s1"] = &sessionTicker{offset: 5}
	d.advanceTickers(time.Now())
	if _, exists := d.tickers["s1"]; exists {
		t.Error("stale ticker should be removed when name fits")
	}
}

func TestAdvanceTickers_OverflowCreatesTickerWithPause(t *testing.T) {
	// sidebarW=30 → maxNameLen=20; long name (26 chars) overflows.
	longName := "abcdefghijklmnopqrstuvwxyz"
	sess := &agent.Session{ID: "s1", Name: longName}
	d := tickerDashboard(sess)
	now := time.Now()
	d.advanceTickers(now)
	tk := d.tickers["s1"]
	if tk == nil {
		t.Fatal("expected ticker to be created for overflowing name")
	}
	if tk.offset != 0 {
		t.Errorf("offset: got %d, want 0", tk.offset)
	}
	if !tk.pauseUntil.After(now) {
		t.Error("ticker should start in paused state")
	}
}

func TestAdvanceTickers_AdvancePastPause_IncrementsOffset(t *testing.T) {
	longName := "abcdefghijklmnopqrstuvwxyz"
	sess := &agent.Session{ID: "s1", Name: longName}
	d := tickerDashboard(sess)
	// Pre-seed expired ticker so initial pause is already over.
	d.tickers["s1"] = &sessionTicker{pauseUntil: past(), nextAdvance: past()}
	d.advanceTickers(time.Now())
	tk := d.tickers["s1"]
	if tk.offset != 1 {
		t.Errorf("offset after advance: got %d, want 1", tk.offset)
	}
}

func TestAdvanceTickers_WideCharName_ScrollsNotStuck(t *testing.T) {
	// 12 CJK runes = 24 display cells, overflows maxNameLen=20.
	// len(fullRunes) = 14. Old rune-count check (offset+20 >= 14) would fire at
	// offset=0 (0+20=20 >= 14), preventing the name from ever scrolling.
	wideName := strings.Repeat("日", 12)
	sess := &agent.Session{ID: "s1", Name: wideName}
	d := tickerDashboard(sess)
	d.tickers["s1"] = &sessionTicker{pauseUntil: past(), nextAdvance: past()}
	d.advanceTickers(time.Now())
	tk := d.tickers["s1"]
	if tk.atEnd {
		t.Error("wide-char name: atEnd should not fire on first advance")
	}
	if tk.offset != 1 {
		t.Errorf("wide-char name: offset after first advance: got %d, want 1", tk.offset)
	}
}

func TestAdvanceTickers_EndReached_SnapsBack(t *testing.T) {
	// sidebarW=30 → maxNameLen=20
	// longName=26 chars → fullName "…" + " ·" = 28 runes
	// end condition: offset+20 >= 28 → offset >= 8
	longName := "12345678901234567890123456"
	sess := &agent.Session{ID: "s1", Name: longName}
	d := tickerDashboard(sess)

	// Set offset to 7 (one step before end); advance once → hits 8 → atEnd=true.
	d.tickers["s1"] = &sessionTicker{offset: 7, pauseUntil: past(), nextAdvance: past()}
	d.advanceTickers(time.Now())
	tk := d.tickers["s1"]
	if !tk.atEnd {
		t.Fatal("expected atEnd=true after reaching end")
	}
	if tk.offset != 8 {
		t.Errorf("offset at end: got %d, want 8", tk.offset)
	}

	// Simulate end-pause expiry and advance again → snap back.
	tk.pauseUntil = past()
	tk.nextAdvance = past()
	beforeSnap := time.Now()
	d.advanceTickers(time.Now())
	if tk.offset != 0 {
		t.Errorf("offset after snap: got %d, want 0", tk.offset)
	}
	if tk.atEnd {
		t.Error("atEnd should be false after snap")
	}
	if !tk.pauseUntil.After(beforeSnap) {
		t.Error("snap should set a fresh start pause")
	}
}

// TestSessionFocusPriority_DefaultIsIdle verifies that a session whose agents
// have not received any status-changing events (StatusStarting, the zero value)
// reports priority 3 (idle/other).
func TestSessionFocusPriority_DefaultIsIdle(t *testing.T) {
	sess := &agent.Session{ID: "s1", Name: "s1"}
	sess.SetLifecyclePhase(agent.LifecycleInProgress)
	ag := &agent.Agent{Name: "a1"}

	d := newDashboardModel()
	d.prCache = make(map[string]*prCacheEntry)
	d.items = []listItem{
		{kind: listItemSession, session: sess},
		{kind: listItemAgent, session: sess, agent: ag},
	}

	if got := d.sessionFocusPriority(sess); got != 3 {
		t.Errorf("sessionFocusPriority with StatusStarting agent: got %d, want 3", got)
	}
}

// TestSessionFocusPriority_ActiveBeforeIdle verifies that a session with an
// active agent (priority 2) sorts ahead of one with only idle/default agents
// (priority 3). We drive StatusActive via OnHookEvent(KindSessionStart).
func TestSessionFocusPriority_ActiveBeforeIdle(t *testing.T) {
	sessActive := &agent.Session{ID: "sa", Name: "active"}
	sessActive.SetLifecyclePhase(agent.LifecycleInProgress)
	agActive := &agent.Agent{Name: "ag-active"}
	// Drive to StatusActive.
	agActive.OnHookEvent(hook.Event{Kind: hook.KindSessionStart})

	sessIdle := &agent.Session{ID: "si", Name: "idle"}
	sessIdle.SetLifecyclePhase(agent.LifecycleInProgress)
	agIdle := &agent.Agent{Name: "ag-idle"}

	d := newDashboardModel()
	d.prCache = make(map[string]*prCacheEntry)
	d.items = []listItem{
		{kind: listItemSession, session: sessActive},
		{kind: listItemAgent, session: sessActive, agent: agActive},
		{kind: listItemSession, session: sessIdle},
		{kind: listItemAgent, session: sessIdle, agent: agIdle},
	}

	pa := d.sessionFocusPriority(sessActive)
	pi := d.sessionFocusPriority(sessIdle)
	if pa != 2 {
		t.Errorf("active session priority: got %d, want 2", pa)
	}
	if pi != 3 {
		t.Errorf("idle session priority: got %d, want 3", pi)
	}
}

// TestAllInProgressSessions_SortOrder verifies that after building the result
// slice, sessions with lower priority (more urgent) appear first.
// Session order in d.items: idle first, then active — after sort active should
// be first.
func TestAllInProgressSessions_SortOrder(t *testing.T) {
	sessIdle := &agent.Session{ID: "si", Name: "idle"}
	sessIdle.SetLifecyclePhase(agent.LifecycleInProgress)
	agIdle := &agent.Agent{Name: "ag-idle"}

	sessActive := &agent.Session{ID: "sa", Name: "active"}
	sessActive.SetLifecyclePhase(agent.LifecycleInProgress)
	agActive := &agent.Agent{Name: "ag-active"}
	// Drive to StatusActive.
	agActive.OnHookEvent(hook.Event{Kind: hook.KindSessionStart})

	d := newDashboardModel()
	d.prCache = make(map[string]*prCacheEntry)
	// Idle session is listed first in items; active second — sort should invert.
	d.items = []listItem{
		{kind: listItemSession, session: sessIdle},
		{kind: listItemAgent, session: sessIdle, agent: agIdle},
		{kind: listItemSession, session: sessActive},
		{kind: listItemAgent, session: sessActive, agent: agActive},
	}

	sessions := d.buildingSessions()
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	if sessions[0].session != sessActive {
		t.Errorf("first session after sort should be active (priority 2), got %q", sessions[0].session.Name)
	}
	if sessions[1].session != sessIdle {
		t.Errorf("second session after sort should be idle (priority 3), got %q", sessions[1].session.Name)
	}
}

// TestAllInProgressSessions_StableWithinPriority pins the ordering of
// same-priority sessions to CreatedAt rather than a continuously-changing
// signal like LastOutputTime. This is what prevents focus-mode list flicker
// while agents stream output: rebuilds during a render must produce the same
// order until a session's status crosses a priority boundary.
func TestAllInProgressSessions_StableWithinPriority(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	sessOlder := &agent.Session{ID: "so", Name: "older", CreatedAt: t0}
	sessOlder.SetLifecyclePhase(agent.LifecycleInProgress)

	sessNewer := &agent.Session{ID: "sn", Name: "newer", CreatedAt: t0.Add(time.Minute)}
	sessNewer.SetLifecyclePhase(agent.LifecycleInProgress)

	d := newDashboardModel()
	d.prCache = make(map[string]*prCacheEntry)
	// Insert newer first so we can confirm the sort actually runs and the
	// result is keyed on CreatedAt, not insertion order.
	d.items = []listItem{
		{kind: listItemSession, session: sessNewer},
		{kind: listItemSession, session: sessOlder},
	}

	for i := 0; i < 5; i++ {
		sessions := d.buildingSessions()
		if len(sessions) != 2 {
			t.Fatalf("iter %d: expected 2 sessions, got %d", i, len(sessions))
		}
		if sessions[0].session != sessOlder || sessions[1].session != sessNewer {
			t.Fatalf("iter %d: same-priority order should be CreatedAt ASC; got [%q, %q]",
				i, sessions[0].session.Name, sessions[1].session.Name)
		}
	}
}

// TestReviewQueueSessions_IncludesInReview verifies that sessions whose phase
// has progressed to LifecycleInReview still appear in the queue. Without this,
// pressing ESC out of the review panel (which intentionally leaves the phase
// at InReview) would orphan the session — invisible in both SESSIONS and
// REVIEW QUEUE, even though the pipeline IN REVIEW count includes it.
func TestReviewQueueSessions_IncludesInReview(t *testing.T) {
	sessReady := &agent.Session{ID: "sr", Name: "ready"}
	sessReady.SetLifecyclePhase(agent.LifecycleReadyForReview)

	sessInReview := &agent.Session{ID: "si", Name: "in-review"}
	sessInReview.SetLifecyclePhase(agent.LifecycleInReview)

	sessProgress := &agent.Session{ID: "sp", Name: "in-progress"}
	sessProgress.SetLifecyclePhase(agent.LifecycleInProgress)

	d := newDashboardModel()
	d.prCache = make(map[string]*prCacheEntry)
	d.items = []listItem{
		{kind: listItemSession, session: sessReady},
		{kind: listItemSession, session: sessInReview},
		{kind: listItemSession, session: sessProgress},
	}

	queue := d.reviewQueueSessions()
	if len(queue) != 2 {
		t.Fatalf("expected 2 sessions in queue (Ready+InReview), got %d", len(queue))
	}
	got := map[string]bool{}
	for _, item := range queue {
		got[item.session.Name] = true
	}
	if !got["ready"] || !got["in-review"] {
		t.Errorf("expected queue to contain ready and in-review sessions, got %v", got)
	}
	if got["in-progress"] {
		t.Error("InProgress session must not appear in review queue")
	}
}

// TestRenderPipelineWidget_CountsDraftingAsPlanning verifies that a session in
// LifecycleDrafting is counted toward the PLANNING cell of the pipeline widget,
// matching planningSessions() which includes Drafting alongside Planning. Without
// this, a session whose plan is still being drafted appears in the PLANNING
// list but reads as 0 in the widget — the same structural mismatch fixed for
// LifecycleInReview / reviewQueueSessions.
func TestRenderPipelineWidget_CountsDraftingAsPlanning(t *testing.T) {
	sessDrafting := &agent.Session{ID: "sd", Name: "drafting"}
	sessDrafting.SetLifecyclePhase(agent.LifecycleDrafting)

	d := newDashboardModel()
	d.prCache = make(map[string]*prCacheEntry)
	d.items = []listItem{
		{kind: listItemSession, session: sessDrafting},
	}

	out := ansi.Strip(d.renderPipelineWidget(120))
	// Cells are joined horizontally so the labels share a row and the counts
	// share the row below. Find the count row by locating the line that holds
	// the BUILDING/REVIEWING/SHIPPING zeros, then slice out the leading
	// PLANNING-cell column and confirm it shows 1, not 0.
	lines := strings.Split(out, "\n")
	var labelLine, countLine string
	for i, line := range lines {
		if strings.Contains(line, "PLANNING") && strings.Contains(line, "BUILDING") {
			labelLine = line
			if i+1 < len(lines) {
				countLine = lines[i+1]
			}
			break
		}
	}
	if labelLine == "" || countLine == "" {
		t.Fatalf("expected PLANNING/BUILDING label row with a following count row in widget output, got:\n%s", out)
	}
	buildingCol := strings.Index(labelLine, "BUILDING")
	if buildingCol <= 0 || buildingCol > len(countLine) {
		t.Fatalf("BUILDING column %d out of range for count line of length %d", buildingCol, len(countLine))
	}
	planningCountCell := countLine[:buildingCol]
	if !strings.Contains(planningCountCell, "1") {
		t.Errorf("expected PLANNING count cell to contain 1 for a Drafting session, got %q (full count line %q)", planningCountCell, countLine)
	}
	if strings.Contains(planningCountCell, "0") {
		t.Errorf("PLANNING count cell should not show 0 when a Drafting session is present, got %q", planningCountCell)
	}
}

// TestPlanningSessions_OnlyPlanningPhase pins the planning section to
// LifecyclePlanning so a session that has advanced to Building doesn't
// double-show on the dashboard.
func TestPlanningSessions_OnlyPlanningPhase(t *testing.T) {
	sessPlanning := &agent.Session{ID: "sp", Name: "planning"}
	// LifecyclePlanning is the zero value; explicit for clarity.
	sessPlanning.SetLifecyclePhase(agent.LifecyclePlanning)

	sessBuilding := &agent.Session{ID: "sb", Name: "building"}
	sessBuilding.SetLifecyclePhase(agent.LifecycleInProgress)

	sessReady := &agent.Session{ID: "sr", Name: "ready"}
	sessReady.SetLifecyclePhase(agent.LifecycleReadyForReview)

	sessShipping := &agent.Session{ID: "ss", Name: "shipping"}
	sessShipping.SetLifecyclePhase(agent.LifecycleShipping)

	d := newDashboardModel()
	d.prCache = make(map[string]*prCacheEntry)
	d.items = []listItem{
		{kind: listItemSession, session: sessPlanning},
		{kind: listItemSession, session: sessBuilding},
		{kind: listItemSession, session: sessReady},
		{kind: listItemSession, session: sessShipping},
	}

	planning := d.planningSessions()
	if len(planning) != 1 || planning[0].session != sessPlanning {
		t.Fatalf("expected exactly the planning session, got %d entries", len(planning))
	}
}

// TestShippingSessions_OnlyShippingPhase mirrors planningSessions but for the
// Shipping section: only LifecycleShipping rows appear, so a session that has
// already merged (LifecycleComplete) leaves the dashboard.
func TestShippingSessions_OnlyShippingPhase(t *testing.T) {
	sessShipping := &agent.Session{ID: "ss", Name: "shipping"}
	sessShipping.SetLifecyclePhase(agent.LifecycleShipping)

	sessComplete := &agent.Session{ID: "sc", Name: "merged"}
	sessComplete.SetLifecyclePhase(agent.LifecycleComplete)

	sessReady := &agent.Session{ID: "sr", Name: "ready"}
	sessReady.SetLifecyclePhase(agent.LifecycleReadyForReview)

	d := newDashboardModel()
	d.prCache = make(map[string]*prCacheEntry)
	d.items = []listItem{
		{kind: listItemSession, session: sessShipping},
		{kind: listItemSession, session: sessComplete},
		{kind: listItemSession, session: sessReady},
	}

	shipping := d.shippingSessions()
	if len(shipping) != 1 || shipping[0].session != sessShipping {
		t.Fatalf("expected exactly the shipping session, got %d entries", len(shipping))
	}
}

func TestPlanTaskCounts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		plan      string
		wantTotal int
		wantDone  int
	}{
		{"empty", "", 0, 0},
		{"no tasks", "# Goal\nDo a thing\n", 0, 0},
		{"all open", "# Goal\nx\n\n## Tasks\n- [ ] one\n- [ ] two\n", 2, 0},
		{"mixed", "## Tasks\n- [x] done\n- [ ] open\n- [X] also done\n", 3, 2},
		{"indented", "## Tasks\n  - [ ] indented\n\t- [x] tabbed\n", 2, 1},
		{"prefix only is not a task", "- [ x] not a task\n- [ ] real\n", 1, 0},
		{"non-task lines ignored", "narrative line\n## Tasks\n- [ ] a\nmore prose\n- [x] b\n", 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			total, done := planTaskCounts(tc.plan)
			if total != tc.wantTotal || done != tc.wantDone {
				t.Errorf("planTaskCounts(%q) = (%d, %d), want (%d, %d)",
					tc.plan, total, done, tc.wantTotal, tc.wantDone)
			}
		})
	}
}

func TestFirstUncompletedTask(t *testing.T) {
	for _, tc := range []struct {
		name string
		plan string
		want string
	}{
		{"empty", "", ""},
		{"all done", "- [x] one\n- [X] two\n", ""},
		{"first open", "- [x] done\n- [ ] next thing\n- [ ] later\n", "next thing"},
		{"trims surrounding whitespace", "  - [ ]   trim me  \n", "trim me"},
		{"skips blank task body", "- [ ]   \n- [ ] real one\n", "real one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstUncompletedTask(tc.plan); got != tc.want {
				t.Errorf("firstUncompletedTask(%q) = %q, want %q", tc.plan, got, tc.want)
			}
		})
	}
}


func TestPlanningStatusBadge_PhaseTransitions(t *testing.T) {
	dir := t.TempDir()
	sess := agent.NewSessionForTestWithPath("id", "name", dir)
	sess.SetLifecyclePhase(agent.LifecyclePlanning)

	// No plan yet → "no plan yet" surface.
	if got := planningStatusBadge(sess); !strings.Contains(ansi.Strip(got), "no plan yet") {
		t.Errorf("badge for fresh planning session = %q, want 'no plan yet'", got)
	}

	// Plan with tasks → counts surface.
	if err := sess.WritePlan("- [x] done\n- [ ] todo\n"); err != nil {
		t.Fatal(err)
	}
	if got := planningStatusBadge(sess); !strings.Contains(ansi.Strip(got), "1/2 tasks") {
		t.Errorf("badge with mixed tasks = %q, want '1/2 tasks'", got)
	}

	// Draft error supersedes the task count.
	sess.SetDraftError(errAnything{})
	if got := planningStatusBadge(sess); !strings.Contains(ansi.Strip(got), "draft failed") {
		t.Errorf("badge with draft error = %q, want 'draft failed'", got)
	}
}

type errAnything struct{}

func (errAnything) Error() string { return "anything" }

// TestRenderQueueRow_PRDraftBadge verifies that the "drafting PR…" spinner
// badge appears on line 2 of a REVIEWING row when prDraftSessionID matches,
// and that clearing prDraftSessionID restores the normal task display.
func TestRenderQueueRow_PRDraftBadge(t *testing.T) {
	sess := agent.NewSessionForTest("sess-1", "fix-auth")
	sess.SetOriginalPrompt("Fix the auth bug")
	sess.MarkDone()

	// With prDraftSessionID matching the session: badge should appear.
	d := dashboardModel{prDraftSessionID: sess.ID}
	rows := d.renderQueueRow(sess, "", false, ColorWarning, 80)
	if len(rows) < 2 {
		t.Fatalf("renderQueueRow returned %d lines, want 2", len(rows))
	}
	if !strings.Contains(ansi.Strip(rows[1]), "drafting PR") {
		t.Errorf("in-flight row line 2 = %q, want 'drafting PR…'", rows[1])
	}

	// With prDraftSessionID cleared: normal prompt should appear.
	d.prDraftSessionID = ""
	rows = d.renderQueueRow(sess, "", false, ColorWarning, 80)
	if strings.Contains(ansi.Strip(rows[1]), "drafting PR") {
		t.Error("cleared state must not show badge; got 'drafting PR' on line 2")
	}
	if !strings.Contains(ansi.Strip(rows[1]), "Fix the auth bug") {
		t.Errorf("cleared state line 2 = %q, want original prompt", rows[1])
	}
}
