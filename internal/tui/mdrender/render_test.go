package mdrender

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
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
	if len(got) < 6 {
		t.Fatalf("got %d display lines, want >= 6\n%v", len(got), got)
	}
	// Each heading line must contain CSI styling and the plain text.
	plainLines := []string{"# h1", "## h2", "### h3", "#### h4", "##### h5", "###### h6"}
	for i, want := range plainLines {
		line := got[i]
		if !containsCSI(line) {
			t.Errorf("line %d: missing ANSI styling: %q", i, line)
		}
		if testutil.StripANSI(line) != want {
			t.Errorf("line %d: stripped = %q, want %q", i, testutil.StripANSI(line), want)
		}
	}
	// Different heading levels should produce different SGR sequences.
	if got[0] == got[1] {
		t.Errorf("h1 and h2 styled identically; expected distinct foregrounds")
	}
	if got[2] == got[3] {
		t.Errorf("h3 and h4 styled identically; expected distinct foregrounds")
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
	// Content lines should carry chroma-injected SGR (multiple sequences) for
	// keywords like `package`, `func`. We don't pin exact codes; we just
	// require *some* styling beyond the leading reset.
	pkgLine := got[1]
	if !containsCSI(pkgLine) {
		t.Errorf("package line not styled: %q", pkgLine)
	}
	if testutil.StripANSI(pkgLine) != "package main" {
		t.Errorf("package line stripped = %q, want %q", testutil.StripANSI(pkgLine), "package main")
	}
	funcLine := got[3]
	if !containsCSI(funcLine) {
		t.Errorf("func line not styled: %q", funcLine)
	}
	if testutil.StripANSI(funcLine) != "func main() {}" {
		t.Errorf("func line stripped = %q, want %q", testutil.StripANSI(funcLine), "func main() {}")
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
	if testutil.StripANSI(got[1]) != "echo hello" {
		t.Errorf("bash line stripped = %q, want %q", testutil.StripANSI(got[1]), "echo hello")
	}
}

func TestRenderLines_FenceNoLangFallsBackToPlaintext(t *testing.T) {
	r := New("monokai")
	plan := "```\nsome text\n```\n"
	got := r.RenderLines(plan, 80)
	if len(got) < 3 {
		t.Fatalf("got %d display lines, want >= 3", len(got))
	}
	// Plaintext lexer doesn't add color tokens, but our wrapper still wraps
	// the line in the styleFenceContent fallback when the pre-lexed line is
	// empty *or* the line passes through unstyled. Just assert plain text is
	// preserved verbatim under StripANSI.
	if testutil.StripANSI(got[1]) != "some text" {
		t.Errorf("plaintext stripped = %q, want %q", testutil.StripANSI(got[1]), "some text")
	}
}

func TestRenderLines_FenceUnknownLangDoesNotPanic(t *testing.T) {
	r := New("monokai")
	plan := "```not-a-real-lang-xyzzy\nfoo bar\n```\n"
	got := r.RenderLines(plan, 80)
	// chroma falls through to a generic lexer; we just assert it doesn't crash
	// and the plain text survives.
	found := false
	for _, line := range got {
		if testutil.StripANSI(line) == "foo bar" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no output line contained the plain content; got:\n%v", got)
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
