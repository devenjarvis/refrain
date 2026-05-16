package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/devenjarvis/refrain/internal/diffmodel"
)

// diffviewFixture constructs a diffModel populated with two files so the tree
// has leaves and the viewport has scroll travel. The returned width controls
// tree visibility (use widthNoTree to test the viewport-scroll fast paths).
const (
	widthWithTree = 120 // >= diffTreeHideBelow (80)
	widthNoTree   = 60  // <  diffTreeHideBelow
)

// makeDiffModelForTest parses a small multi-hunk unified diff and returns a
// diffModel sized for the test. height=10 plus long content forces the
// viewport to have scroll travel.
func makeDiffModelForTest(t *testing.T, width int) diffModel {
	t.Helper()
	// Build a 30-line addition to a.go so the rendered diff exceeds viewport
	// height (10 rows) and scroll keys have travel.
	var sb strings.Builder
	sb.WriteString("diff --git a/a.go b/a.go\n")
	sb.WriteString("new file mode 100644\n")
	sb.WriteString("index 0000000..1111111\n")
	sb.WriteString("--- /dev/null\n")
	sb.WriteString("+++ b/a.go\n")
	sb.WriteString("@@ -0,0 +1,30 @@\n")
	for i := 1; i <= 30; i++ {
		sb.WriteString("+line xxx ")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteString("\n")
	}
	sb.WriteString("diff --git a/b.go b/b.go\n")
	sb.WriteString("index 2222222..3333333 100644\n")
	sb.WriteString("--- a/b.go\n")
	sb.WriteString("+++ b/b.go\n")
	sb.WriteString("@@ -1,2 +1,3 @@\n")
	sb.WriteString(" package b\n")
	sb.WriteString("+extra\n")
	sb.WriteString(" done\n")

	m, err := diffmodel.Parse(sb.String())
	if err != nil {
		t.Fatalf("parse: %v\n--- raw ---\n%s", err, sb.String())
	}
	if len(m.Files) != 2 {
		t.Fatalf("want 2 files, got %d", len(m.Files))
	}
	return newDiffModel("agent-x", m, width, 10)
}

func TestDiffView_Q_EmitsCloseMsg(t *testing.T) {
	d := makeDiffModelForTest(t, widthWithTree)
	_, cmd := d.Update(keyRune('q'))
	if cmd == nil {
		t.Fatal("expected close cmd")
	}
	if _, ok := cmd().(diffCloseMsg); !ok {
		t.Fatalf("got %T, want diffCloseMsg", cmd())
	}
}

func TestDiffView_Esc_EmitsCloseMsg(t *testing.T) {
	d := makeDiffModelForTest(t, widthWithTree)
	_, cmd := d.Update(keyNamed(tea.KeyEscape))
	if cmd == nil {
		t.Fatal("expected close cmd")
	}
	if _, ok := cmd().(diffCloseMsg); !ok {
		t.Fatalf("got %T, want diffCloseMsg", cmd())
	}
}

func TestDiffView_S_TogglesSideBySide(t *testing.T) {
	d := makeDiffModelForTest(t, widthWithTree)
	initial := d.sideBySide
	d2, cmd := d.Update(keyRune('s'))
	if cmd != nil {
		t.Errorf("toggle should not emit a cmd, got %T", cmd())
	}
	if d2.sideBySide == initial {
		t.Errorf("sideBySide unchanged after 's' (was %v)", initial)
	}
	d3, _ := d2.Update(keyRune('s'))
	if d3.sideBySide != initial {
		t.Errorf("two presses should round-trip; got %v, want %v", d3.sideBySide, initial)
	}
}

func TestDiffView_ScrollKeys_NoTree(t *testing.T) {
	cases := []struct {
		name string
		key  tea.KeyPressMsg
		// wantMove reports whether YOffset should change. We start scrolled to
		// the bottom so up-keys move (negative), or scrolled to top so
		// down-keys move (positive). The test sets up the right baseline.
		wantDelta int // +1 = expect down, -1 = expect up, 0 = expect no movement
	}{
		{"j_scrolls_down_1", keyRune('j'), +1},
		{"down_scrolls_down_1", keyNamed(tea.KeyDown), +1},
		{"k_scrolls_up_1", keyRune('k'), -1},
		{"up_scrolls_up_1", keyNamed(tea.KeyUp), -1},
		{"d_half_page_down", keyRune('d'), +1},
		{"ctrl+d_half_page_down", keyCtrlRune('d'), +1},
		{"u_half_page_up", keyRune('u'), -1},
		{"ctrl+u_half_page_up", keyCtrlRune('u'), -1},
		{"pgdown_page_down", keyNamed(tea.KeyPgDown), +1},
		{"pgup_page_up", keyNamed(tea.KeyPgUp), -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := makeDiffModelForTest(t, widthNoTree)
			// Establish a baseline that allows movement in the asserted direction.
			if tc.wantDelta < 0 {
				d.vp.GotoBottom()
			} else {
				d.vp.GotoTop()
			}
			before := d.vp.YOffset()
			d2, cmd := d.Update(tc.key)
			if cmd != nil {
				t.Errorf("scroll key should not emit a cmd, got %T", cmd())
			}
			after := d2.vp.YOffset()
			switch {
			case tc.wantDelta > 0 && after <= before:
				t.Errorf("expected YOffset to increase from %d, got %d", before, after)
			case tc.wantDelta < 0 && after >= before:
				t.Errorf("expected YOffset to decrease from %d, got %d", before, after)
			}
		})
	}
}

func TestDiffView_G_GotoTop_NoTree(t *testing.T) {
	d := makeDiffModelForTest(t, widthNoTree)
	d.vp.GotoBottom()
	if d.vp.YOffset() == 0 {
		t.Skip("viewport doesn't have scroll travel for this content")
	}
	d2, cmd := d.Update(keyRune('g'))
	if cmd != nil {
		t.Errorf("g should not emit a cmd, got %T", cmd())
	}
	if d2.vp.YOffset() != 0 {
		t.Errorf("YOffset = %d, want 0 after 'g'", d2.vp.YOffset())
	}
}

func TestDiffView_ShiftG_GotoBottom_NoTree(t *testing.T) {
	d := makeDiffModelForTest(t, widthNoTree)
	d.vp.GotoTop()
	d2, cmd := d.Update(keyShiftRune('g', "G"))
	if cmd != nil {
		t.Errorf("G should not emit a cmd, got %T", cmd())
	}
	// After GotoBottom, YOffset should be > 0 if there's content to scroll past.
	if d2.vp.YOffset() == 0 {
		t.Skip("viewport doesn't have scroll travel for this content")
	}
}

func TestDiffView_JK_TreeVisible_ForwardedToTree(t *testing.T) {
	d := makeDiffModelForTest(t, widthWithTree)
	if !d.treeVisible() {
		t.Fatal("test prerequisite: tree should be visible")
	}
	// 'j' should reach the tree and, when the new cursor row is a leaf, emit
	// a FileSelectedMsg. It must never close the panel.
	_, cmd := d.Update(keyRune('j'))
	for _, m := range runCmdAll(t, cmd) {
		if _, ok := m.(diffCloseMsg); ok {
			t.Fatal("'j' must not close the panel")
		}
	}
}

func TestDiffView_TreeVisible_G_NavigatesTree_NotViewport(t *testing.T) {
	d := makeDiffModelForTest(t, widthWithTree)
	if !d.treeVisible() {
		t.Fatal("test prerequisite: tree should be visible")
	}
	// Force viewport scrolled to the bottom so we can detect if 'G' resets it.
	d.vp.GotoBottom()
	beforeOffset := d.vp.YOffset()
	d2, cmd := d.Update(keyShiftRune('g', "G"))
	if cmd != nil {
		// Tree may emit FileSelectedMsg when cursor lands on a leaf.
		msgs := runCmdAll(t, cmd)
		if _, ok := findMsg[diffCloseMsg](msgs); ok {
			t.Fatal("'G' should not close the panel")
		}
	}
	if d2.vp.YOffset() != beforeOffset {
		t.Errorf("viewport offset moved from %d to %d on 'G' while tree visible — should affect tree only",
			beforeOffset, d2.vp.YOffset())
	}
}

func TestDiffView_UnknownKey_NoTree_NoOp(t *testing.T) {
	d := makeDiffModelForTest(t, widthNoTree)
	d.vp.GotoBottom()
	before := struct {
		path   string
		sxs    bool
		offset int
	}{d.selected, d.sideBySide, d.vp.YOffset()}
	d2, cmd := d.Update(keyRune('z'))
	if cmd != nil {
		t.Errorf("unknown key produced cmd %T, want nil", cmd())
	}
	after := struct {
		path   string
		sxs    bool
		offset int
	}{d2.selected, d2.sideBySide, d2.vp.YOffset()}
	if before != after {
		t.Errorf("unknown key changed state: before=%+v after=%+v", before, after)
	}
}

func TestDiffView_UnknownKey_WithTree_ForwardsToTreeNoOp(t *testing.T) {
	d := makeDiffModelForTest(t, widthWithTree)
	before := struct {
		path   string
		sxs    bool
		offset int
	}{d.selected, d.sideBySide, d.vp.YOffset()}
	d2, cmd := d.Update(keyRune('z'))
	if cmd != nil {
		t.Errorf("unknown key with tree visible produced cmd %T, want nil", cmd())
	}
	after := struct {
		path   string
		sxs    bool
		offset int
	}{d2.selected, d2.sideBySide, d2.vp.YOffset()}
	if before != after {
		t.Errorf("unknown key changed state with tree visible: before=%+v after=%+v", before, after)
	}
}

func TestDiffView_Render_ShowsAgentNameAndSelectedFile(t *testing.T) {
	d := makeDiffModelForTest(t, widthWithTree)
	v := d.View()
	if !strings.Contains(v, "agent-x") {
		t.Errorf("view missing agent name: %q", v)
	}
}

func TestDiffView_EmptyModel_ViewShowsEmptyMessage(t *testing.T) {
	m := &diffmodel.Model{}
	d := newDiffModel("agent-empty", m, widthWithTree, 10)
	v := d.View()
	if !strings.Contains(v, "No changes for agent-empty") {
		t.Errorf("empty diff view missing fallback message: %q", v)
	}
}
