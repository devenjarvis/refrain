package theme

import (
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// TestInitColorNoColor verifies the NO_COLOR gate: when noColor is set, a hex
// degrades to the empty (terminal-default) color; otherwise it passes through.
func TestInitColorNoColor(t *testing.T) {
	orig := noColor
	defer func() { noColor = orig }()

	noColor = true
	if got := initColor("#FFFFFF"); got != lipgloss.Color("") {
		t.Errorf("initColor with NO_COLOR = %q, want empty", got)
	}
	if got := initAdaptive("#2D2D2D", "#E8E8E8"); got != lipgloss.Color("") {
		t.Errorf("initAdaptive with NO_COLOR = %v, want empty Color", got)
	}

	noColor = false
	if got := initColor("#FFFFFF"); got != lipgloss.Color("#FFFFFF") {
		t.Errorf("initColor = %q, want #FFFFFF", got)
	}
	if _, ok := initAdaptive("#2D2D2D", "#E8E8E8").(lipgloss.AdaptiveColor); !ok {
		t.Errorf("initAdaptive without NO_COLOR is not an AdaptiveColor")
	}
}

// TestMarkdownHeadingColor pins the level→role mapping so the heading scale
// stays in sync with the brand/status roles it borrows.
func TestMarkdownHeadingColor(t *testing.T) {
	cases := map[int]lipgloss.Color{
		1: ColorPrimary,
		2: ColorSecondary,
		3: ColorSuccess,
		4: ColorWarning,
		5: ColorPrimaryLight,
		6: ColorMutedLight,
		7: ColorPrimary, // out of range falls back to H1
	}
	for level, want := range cases {
		if got := MarkdownHeadingColor(level); got != want {
			t.Errorf("MarkdownHeadingColor(%d) = %q, want %q", level, got, want)
		}
	}
}

// TestGlyphsNonEmpty guards against an accidentally blanked glyph token, which
// would silently drop a status/icon marker from the UI.
func TestGlyphsNonEmpty(t *testing.T) {
	glyphs := []string{
		GlyphError, GlyphSuccess, GlyphWaiting, GlyphQuestion, GlyphActive,
		GlyphIdle, GlyphCross, GlyphPending, GlyphFlagged, GlyphConcerns,
		GlyphNoDiff, GlyphManual, GlyphBranch, GlyphStripe, GlyphCursor, GlyphCaret,
		GlyphArrow, GlyphFolderOpen, GlyphFolderClosed, GlyphCheckboxDone,
		GlyphCheckboxTodo, GlyphFenceBar, GlyphRuleThin, GlyphRuleHeavy,
	}
	for i, g := range glyphs {
		if g == "" {
			t.Errorf("glyph at index %d is empty", i)
		}
	}
}

// TestSpinnerFrame returns a frame from the braille set and advances over time.
func TestSpinnerFrame(t *testing.T) {
	base := time.UnixMilli(0)
	in := func(s string) bool {
		for _, f := range SpinnerBraille {
			if f == s {
				return true
			}
		}
		return false
	}
	if got := SpinnerFrame(base); !in(got) {
		t.Errorf("SpinnerFrame returned %q, not in SpinnerBraille", got)
	}
	if SpinnerFrame(base) == SpinnerFrame(base.Add(100*time.Millisecond)) {
		t.Errorf("SpinnerFrame did not advance across a 100ms tick")
	}
}
