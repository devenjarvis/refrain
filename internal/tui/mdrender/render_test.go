package mdrender

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/refrain/internal/tui/mdrender/testutil"
	"github.com/muesli/termenv"
)

// TestMain forces TrueColor so lipgloss emits ANSI escape codes even when the
// test binary's stdout isn't a TTY. Without this, all our "must contain CSI"
// assertions would fail in CI / non-tty runs because lipgloss strips colors
// in the "ascii" profile.
func TestMain(m *testing.M) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	code := m.Run()
	lipgloss.SetColorProfile(prev)
	os.Exit(code)
}

// containsCSI checks that s contains at least one CSI escape sequence — i.e.
// styling actually happened. The exact SGR parameters depend on the chroma
// style and are intentionally not asserted; we only want to know the renderer
// emitted *some* color.
func containsCSI(s string) bool {
	return strings.Contains(s, "\x1b[")
}

func TestRenderLines_HeadingsLevels1To6(t *testing.T) {
	r := New("monokai")
	plan := "# h1\n## h2\n### h3\n#### h4\n##### h5\n###### h6\n"
	got := r.RenderLines(plan, 80)
	// H1 and H2 each get an underline line, so total ≥ 8 (6 headings + 2 underlines).
	if len(got) < 8 {
		t.Fatalf("got %d display lines, want >= 8\n%v", len(got), got)
	}
	// H1 underline at got[1], H2 underline at got[3]; headings at got[0,2,4,5,6,7].
	type headingCheck struct {
		idx  int
		want string
	}
	checks := []headingCheck{
		{0, "# h1"},
		{2, "## h2"},
		{4, "### h3"},
		{5, "#### h4"},
		{6, "##### h5"},
		{7, "###### h6"},
	}
	for _, c := range checks {
		line := got[c.idx]
		if !containsCSI(line) {
			t.Errorf("got[%d]: missing ANSI styling: %q", c.idx, line)
		}
		if testutil.StripANSI(line) != c.want {
			t.Errorf("got[%d]: stripped = %q, want %q", c.idx, testutil.StripANSI(line), c.want)
		}
	}
	// H1 underline: all ━ characters.
	h1Under := testutil.StripANSI(got[1])
	for _, ch := range h1Under {
		if ch != '━' {
			t.Errorf("H1 underline got[1] contains non-━ rune %q: %q", string(ch), h1Under)
			break
		}
	}
	// H2 underline: all ─ characters.
	h2Under := testutil.StripANSI(got[3])
	for _, ch := range h2Under {
		if ch != '─' {
			t.Errorf("H2 underline got[3] contains non-─ rune %q: %q", string(ch), h2Under)
			break
		}
	}
	// H1 and H2 heading lines should have distinct foregrounds (different levels).
	if got[0] == got[2] {
		t.Errorf("h1 and h2 styled identically; expected distinct foregrounds")
	}
	if got[4] == got[5] {
		t.Errorf("h3 and h4 styled identically; expected distinct foregrounds")
	}
}

func TestRenderLines_H1UnderlineDecoration(t *testing.T) {
	r := New("monokai")
	got := r.RenderLines("# Title\nbody\n", 80)
	if len(got) < 3 {
		t.Fatalf("got %d lines, want >= 3 (heading + underline + body)", len(got))
	}
	under := testutil.StripANSI(got[1])
	if len(under) == 0 {
		t.Fatal("H1 underline line is empty")
	}
	for _, ch := range under {
		if ch != '━' {
			t.Errorf("H1 underline contains non-━ rune %q: %q", string(ch), under)
			break
		}
	}
	if !containsCSI(got[1]) {
		t.Errorf("H1 underline has no ANSI styling: %q", got[1])
	}
}

func TestRenderLines_H2UnderlineDecoration(t *testing.T) {
	r := New("monokai")
	got := r.RenderLines("## Sub\nbody\n", 80)
	if len(got) < 3 {
		t.Fatalf("got %d lines, want >= 3 (heading + underline + body)", len(got))
	}
	under := testutil.StripANSI(got[1])
	if len(under) == 0 {
		t.Fatal("H2 underline line is empty")
	}
	for _, ch := range under {
		if ch != '─' {
			t.Errorf("H2 underline contains non-─ rune %q: %q", string(ch), under)
			break
		}
	}
	// H2 underline should not exceed the heading text width.
	headingWidth := ansi.StringWidth("## Sub")
	underWidth := ansi.StringWidth(under)
	if underWidth > headingWidth {
		t.Errorf("H2 underline width %d > heading width %d", underWidth, headingWidth)
	}
}

func TestRenderLines_InlineSpans(t *testing.T) {
	r := New("monokai")
	cases := []struct {
		plan string
		want string
	}{
		{"plain **bold** here\n", "plain **bold** here"},
		{"plain *italic* here\n", "plain *italic* here"},
		{"plain _italic_ here\n", "plain _italic_ here"},
		{"plain `code` here\n", "plain `code` here"},
		{"see [link](https://x)\n", "see [link](https://x)"},
		{"mix **b** and *i* and `c`\n", "mix **b** and *i* and `c`"},
	}
	for _, c := range cases {
		got := r.RenderLines(c.plan, 80)
		if len(got) == 0 {
			t.Fatalf("no output for %q", c.plan)
		}
		if !containsCSI(got[0]) {
			t.Errorf("%q: expected styled output, got %q", c.plan, got[0])
		}
		if testutil.StripANSI(got[0]) != c.want {
			t.Errorf("%q: stripped = %q, want %q", c.plan, testutil.StripANSI(got[0]), c.want)
		}
	}
}

func TestRenderLines_FenceGoLexed(t *testing.T) {
	r := New("monokai")
	plan := "```go\npackage main\n\nfunc main() {}\n```\n"
	got := r.RenderLines(plan, 80)
	if len(got) < 5 {
		t.Fatalf("got %d display lines, want >= 5\n%v", len(got), got)
	}
	// Fence open and close lines should still have some styling (muted color).
	if !containsCSI(got[0]) {
		t.Errorf("fence open not styled: %q", got[0])
	}
	// Content lines carry a │ bar prefix and chroma-injected SGR for keywords.
	pkgLine := got[1]
	if !containsCSI(pkgLine) {
		t.Errorf("package line not styled: %q", pkgLine)
	}
	if testutil.StripANSI(pkgLine) != "│ package main" {
		t.Errorf("package line stripped = %q, want %q", testutil.StripANSI(pkgLine), "│ package main")
	}
	funcLine := got[3]
	if !containsCSI(funcLine) {
		t.Errorf("func line not styled: %q", funcLine)
	}
	if testutil.StripANSI(funcLine) != "│ func main() {}" {
		t.Errorf("func line stripped = %q, want %q", testutil.StripANSI(funcLine), "│ func main() {}")
	}
}

func TestRenderLines_FenceBashLexed(t *testing.T) {
	r := New("monokai")
	plan := "```bash\necho hello\n```\n"
	got := r.RenderLines(plan, 80)
	if len(got) < 3 {
		t.Fatalf("got %d display lines, want >= 3", len(got))
	}
	if !containsCSI(got[1]) {
		t.Errorf("bash content not styled: %q", got[1])
	}
	if testutil.StripANSI(got[1]) != "│ echo hello" {
		t.Errorf("bash line stripped = %q, want %q", testutil.StripANSI(got[1]), "│ echo hello")
	}
}

func TestRenderLines_FenceNoLangFallsBackToPlaintext(t *testing.T) {
	r := New("monokai")
	plan := "```\nsome text\n```\n"
	got := r.RenderLines(plan, 80)
	if len(got) < 3 {
		t.Fatalf("got %d display lines, want >= 3", len(got))
	}
	// Content line has │ bar prefix; plain text is preserved after it.
	if testutil.StripANSI(got[1]) != "│ some text" {
		t.Errorf("plaintext stripped = %q, want %q", testutil.StripANSI(got[1]), "│ some text")
	}
}

func TestRenderLines_FenceUnknownLangDoesNotPanic(t *testing.T) {
	r := New("monokai")
	plan := "```not-a-real-lang-xyzzy\nfoo bar\n```\n"
	got := r.RenderLines(plan, 80)
	// chroma falls through to a generic lexer; we assert it doesn't crash
	// and the plain text survives (with bar prefix).
	found := false
	for _, line := range got {
		stripped := testutil.StripANSI(line)
		if stripped == "│ foo bar" || strings.HasPrefix(stripped, "│ ") && strings.Contains(stripped, "foo bar") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no output line contained '│ foo bar'; got:\n%v", got)
	}
}

func TestRenderLines_Caching(t *testing.T) {
	r := New("monokai")
	plan := "# Title\n\nBody text.\n"
	first := r.RenderLines(plan, 80)
	second := r.RenderLines(plan, 80)
	if len(first) != len(second) {
		t.Fatalf("cache mismatch: %d vs %d lines", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("line %d cache mismatch:\n%q\nvs\n%q", i, first[i], second[i])
		}
	}
	// Different width key should produce a fresh result, not return the old
	// cached entry.
	narrow := r.RenderLines(plan, 12)
	if len(narrow) == 0 {
		t.Fatalf("narrow render returned no lines")
	}
}

func TestLineContexts_ClassifiesEachKind(t *testing.T) {
	r := New("monokai")
	plan := "# heading\n" +
		"\n" +
		"plain paragraph\n" +
		"> quoted\n" +
		"- bullet\n" +
		"1. ordered\n" +
		"---\n" +
		"```go\n" +
		"code\n" +
		"```\n"
	ctxs := r.LineContexts(plan)
	wantKinds := []LineKind{
		LineHeading,
		LineBlank,
		LineParagraph,
		LineBlockquote,
		LineList,
		LineList,
		LineHR,
		LineFenceOpen,
		LineFenceContent,
		LineFenceClose,
	}
	if len(ctxs) < len(wantKinds) {
		t.Fatalf("got %d contexts, want >= %d", len(ctxs), len(wantKinds))
	}
	for i, want := range wantKinds {
		if ctxs[i].Kind != want {
			t.Errorf("ctx[%d].Kind = %v, want %v (line: %q)", i, ctxs[i].Kind, want, strings.Split(plan, "\n")[i])
		}
	}
	if ctxs[0].HeadingLevel != 1 {
		t.Errorf("heading level = %d, want 1", ctxs[0].HeadingLevel)
	}
	if ctxs[7].FenceLang != "go" {
		t.Errorf("fence lang = %q, want %q", ctxs[7].FenceLang, "go")
	}
	if ctxs[4].ListBullet != "-" {
		t.Errorf("list bullet = %q, want %q", ctxs[4].ListBullet, "-")
	}
	if ctxs[5].ListBullet != "1." {
		t.Errorf("ordered bullet = %q, want %q", ctxs[5].ListBullet, "1.")
	}
}

func TestStyleSegment_BlankLineUnstyled(t *testing.T) {
	r := New("monokai")
	got := r.StyleSegment("", LineCtx{Kind: LineBlank}, false)
	if got != "" {
		t.Errorf("blank line styled = %q, want empty", got)
	}
}

func TestStyleSegment_HeadingHasStyling(t *testing.T) {
	r := New("monokai")
	got := r.StyleSegment("# hello", LineCtx{Kind: LineHeading, HeadingLevel: 1}, false)
	if !containsCSI(got) {
		t.Errorf("heading not styled: %q", got)
	}
	if testutil.StripANSI(got) != "# hello" {
		t.Errorf("stripped = %q, want %q", testutil.StripANSI(got), "# hello")
	}
}

func TestStyleLine_FenceContentMultiSegmentColumnTracking(t *testing.T) {
	r := New("monokai")
	// Pre-populate fence cache via a render pass on a long Go line.
	long := "func foo(a, b, c, d, e, f, g, h, i, j int) (string, error)"
	plan := "```go\n" + long + "\n```\n"
	r.RenderLines(plan, 80)

	ctxs := r.LineContexts(plan)
	// ctxs[1] is the fence content line.
	if ctxs[1].Kind != LineFenceContent {
		t.Fatalf("ctx[1].Kind = %v, want LineFenceContent", ctxs[1].Kind)
	}
	// Wrap+style at narrow width — multiple segments must be produced.
	segs := r.StyleLine(long, ctxs[1], 16)
	if len(segs) < 2 {
		t.Fatalf("expected multiple segments at width 16; got %d", len(segs))
	}
	// Concatenated stripped segments must reconstruct the original line.
	var rebuilt strings.Builder
	for _, s := range segs {
		rebuilt.WriteString(testutil.StripANSI(s))
	}
	if !strings.Contains(rebuilt.String(), "func foo") {
		t.Errorf("rebuilt = %q; expected to contain %q", rebuilt.String(), "func foo")
	}
}

func TestStyleInline_InlineCodeHasBackground(t *testing.T) {
	r := New("monokai")
	got := r.RenderLines("see `code` here\n", 80)
	if len(got) == 0 {
		t.Fatal("no output")
	}
	// Background SGR is 48;2;R;G;B in TrueColor mode (forced by TestMain).
	// Lipgloss may combine foreground and background in one escape sequence.
	if !strings.Contains(got[0], "48;2;") {
		t.Errorf("no background-color SGR in inline code span; line: %q", got[0])
	}
}

func TestRenderLines_BlockquoteLeftBar(t *testing.T) {
	r := New("monokai")
	got := r.RenderLines("> quoted text\n", 80)
	if len(got) == 0 {
		t.Fatal("no output lines")
	}
	stripped := testutil.StripANSI(got[0])
	if !strings.HasPrefix(stripped, "│ ") {
		t.Errorf("blockquote line does not start with '│ ': %q", stripped)
	}
	if strings.Contains(stripped, ">") {
		t.Errorf("blockquote line still contains '>': %q", stripped)
	}
}

func TestRenderLines_FenceContentHasLeftBar(t *testing.T) {
	r := New("monokai")
	got := r.RenderLines("```go\nfunc X() {}\n```\n", 80)
	// Lines: [0]=open, [1]=content, [2]=close, [3]=trailing empty
	if len(got) < 3 {
		t.Fatalf("got %d lines, want >= 3", len(got))
	}
	// Fence content line must start with "│ ".
	contentStripped := testutil.StripANSI(got[1])
	if !strings.HasPrefix(contentStripped, "│ ") {
		t.Errorf("fence content line[1] does not start with '│ ': %q", contentStripped)
	}
	// Fence open line must also have the bar.
	openStripped := testutil.StripANSI(got[0])
	if !strings.HasPrefix(openStripped, "│ ") {
		t.Errorf("fence open line[0] does not start with '│ ': %q", openStripped)
	}
}

func TestRenderLines_CheckboxUncheckedRendersGlyph(t *testing.T) {
	r := New("monokai")
	got := r.RenderLines("- [ ] task\n", 80)
	if len(got) == 0 {
		t.Fatal("no output lines")
	}
	stripped := testutil.StripANSI(got[0])
	if strings.Contains(stripped, "[ ]") {
		t.Errorf("unchecked checkbox still shows [ ] in %q", stripped)
	}
	if !strings.Contains(stripped, "☐") {
		t.Errorf("unchecked checkbox missing ☐ glyph in %q", stripped)
	}
}

func TestRenderLines_CheckboxCheckedRendersGlyph(t *testing.T) {
	r := New("monokai")
	got := r.RenderLines("- [x] done\n", 80)
	if len(got) == 0 {
		t.Fatal("no output lines")
	}
	stripped := testutil.StripANSI(got[0])
	if strings.Contains(stripped, "[x]") {
		t.Errorf("checked checkbox still shows [x] in %q", stripped)
	}
	if !strings.Contains(stripped, "✓") {
		t.Errorf("checked checkbox missing ✓ glyph in %q", stripped)
	}
}

func TestRenderLines_ListContinuationIndents(t *testing.T) {
	r := New("monokai")
	plan := "- this is a list item that is long enough to wrap somewhere\n"
	got := r.RenderLines(plan, 20)
	if len(got) < 2 {
		t.Fatalf("expected wrap into 2+ segments at width 20; got %d:\n%v", len(got), got)
	}
	first := testutil.StripANSI(got[0])
	cont := testutil.StripANSI(got[1])
	if !strings.HasPrefix(first, "- ") {
		t.Errorf("first segment lost bullet: %q", first)
	}
	if strings.HasPrefix(cont, "- ") {
		t.Errorf("continuation re-emitted bullet: %q", cont)
	}
	if !strings.HasPrefix(cont, "  ") {
		t.Errorf("continuation not indented: %q", cont)
	}
}

func TestRenderLines_PreservesTrailingBlankLine(t *testing.T) {
	r := New("monokai")
	// Same source, with and without trailing newline. Trailing newline => one
	// extra empty display row, matching what the textarea renders.
	noTrail := r.RenderLines("abc", 80)
	withTrail := r.RenderLines("abc\n", 80)
	if len(withTrail) != len(noTrail)+1 {
		t.Errorf("expected trailing newline to produce one extra row: noTrail=%d, withTrail=%d", len(noTrail), len(withTrail))
	}
}

func TestRenderLines_EmptyPlan(t *testing.T) {
	r := New("monokai")
	got := r.RenderLines("", 80)
	if len(got) != 0 {
		t.Errorf("empty plan should produce zero lines, got %d: %v", len(got), got)
	}
}

func TestNew_UnknownStyleFallsBack(t *testing.T) {
	r := New("not-a-real-style")
	if r.style == nil {
		t.Fatal("style fallback not applied")
	}
	if r.formatter == nil {
		t.Fatal("formatter fallback not applied")
	}
}

func TestStyleName_Roundtrip(t *testing.T) {
	r := New("monokai")
	if r.StyleName() != "monokai" {
		t.Errorf("StyleName() = %q, want monokai", r.StyleName())
	}
}

func TestContentMeasure_CapsAtMaxAndCenters(t *testing.T) {
	tests := []struct {
		termWidth, maxMeasure, wantMeasure, wantPad int
	}{
		{120, 72, 72, 24},
		{60, 72, 60, 0},
		{72, 72, 72, 0},
	}
	for _, tt := range tests {
		m, p := ContentMeasure(tt.termWidth, tt.maxMeasure)
		if m != tt.wantMeasure || p != tt.wantPad {
			t.Errorf("ContentMeasure(%d, %d) = (%d, %d), want (%d, %d)",
				tt.termWidth, tt.maxMeasure, m, p, tt.wantMeasure, tt.wantPad)
		}
	}
}
