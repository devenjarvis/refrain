package mdrender

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestWrapPlain_ShortLineFits(t *testing.T) {
	got := wrapPlain("hello world", 80)
	if len(got) != 1 || got[0] != "hello world" {
		t.Errorf("got %v, want [%q]", got, "hello world")
	}
}

func TestWrapPlain_EmptyLineYieldsSingleEmpty(t *testing.T) {
	got := wrapPlain("", 80)
	if len(got) != 1 || got[0] != "" {
		t.Errorf("got %v, want [\"\"]", got)
	}
}

func TestWrapPlain_WordBoundaryWrap(t *testing.T) {
	got := wrapPlain("alpha beta gamma delta", 12)
	for _, seg := range got {
		if ansi.StringWidth(seg) > 12 {
			t.Errorf("segment exceeds width: %q (width=%d)", seg, ansi.StringWidth(seg))
		}
	}
	// Reassembled (joined with space, allowing for trimmed trailing whitespace)
	// should contain every word.
	joined := strings.Join(got, " ")
	for _, w := range []string{"alpha", "beta", "gamma", "delta"} {
		if !strings.Contains(joined, w) {
			t.Errorf("rejoined output missing %q: %q", w, joined)
		}
	}
}

func TestWrapPlain_HardBreakLongWord(t *testing.T) {
	long := strings.Repeat("x", 30)
	got := wrapPlain(long, 8)
	if len(got) < 4 {
		t.Errorf("expected hard-break into 4+ segments, got %d: %v", len(got), got)
	}
	for _, seg := range got {
		if ansi.StringWidth(seg) > 8 {
			t.Errorf("hard-break segment exceeds width: %q", seg)
		}
	}
	// All chunks reconcatenated reconstruct the original word.
	if strings.Join(got, "") != long {
		t.Errorf("reassembled = %q, want %q", strings.Join(got, ""), long)
	}
}

func TestWrapPlain_WidthZeroIsNoOp(t *testing.T) {
	got := wrapPlain("anything", 0)
	if len(got) != 1 || got[0] != "anything" {
		t.Errorf("got %v; expected single passthrough", got)
	}
}

func TestWrapPlain_TabsAndSpacesAtBoundary(t *testing.T) {
	// Word boundary at tabs. Long-enough line forces wrap.
	got := wrapPlain("first\tsecond third\tfourth fifth sixth", 14)
	for _, seg := range got {
		if ansi.StringWidth(seg) > 14 {
			t.Errorf("segment too wide: %q (width %d)", seg, ansi.StringWidth(seg))
		}
	}
}

func TestStyleLine_WrappingPreservesPlainText(t *testing.T) {
	r := New("monokai")
	line := "the quick brown fox jumps over the lazy dog"
	ctx := LineCtx{Kind: LineParagraph}
	segs := r.StyleLine(line, ctx, 12)

	var b strings.Builder
	for _, s := range segs {
		b.WriteString(stripCSI(s))
	}
	rebuilt := b.String()
	// Wrap may collapse trailing whitespace at break points; check every word
	// survives.
	for _, w := range strings.Fields(line) {
		if !strings.Contains(rebuilt, w) {
			t.Errorf("missing %q in rebuilt %q", w, rebuilt)
		}
	}
}

// stripCSI is a local copy of testutil.StripANSI to keep wrap_test.go free of
// extra dependencies; the testutil package is the canonical helper for
// cross-package use.
func stripCSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] == ';' || (s[j] >= '0' && s[j] <= '9') || s[j] == '?') {
				j++
			}
			if j < len(s) {
				j++ // consume final byte
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
