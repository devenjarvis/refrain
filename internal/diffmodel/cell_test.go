package diffmodel_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/refrain/internal/diffmodel"
)

func TestWrapCell_NoWrap(t *testing.T) {
	out := diffmodel.WrapCell("hello", 20)
	if len(out) != 1 {
		t.Fatalf("expected 1 line, got %d", len(out))
	}
	if out[0] != "hello" {
		t.Errorf("expected %q, got %q", "hello", out[0])
	}
}

func TestWrapCell_ExactFit(t *testing.T) {
	out := diffmodel.WrapCell("exactly10!", 10)
	if len(out) != 1 {
		t.Fatalf("exact-fit should not wrap; got %d lines: %v", len(out), out)
	}
}

func TestWrapCell_LongLineWraps(t *testing.T) {
	s := strings.Repeat("x", 25)
	out := diffmodel.WrapCell(s, 10)
	if len(out) < 3 {
		t.Fatalf("expected >=3 physical lines, got %d: %v", len(out), out)
	}
	// All lines except the last carry the wrap marker.
	for i := 0; i < len(out)-1; i++ {
		if !strings.HasSuffix(out[i], diffmodel.WrapMarker) {
			t.Errorf("line %d missing wrap marker: %q", i, out[i])
		}
		// Total visible width must not exceed the cell width.
		if ansi.StringWidth(out[i]) > 10 {
			t.Errorf("line %d width %d > 10: %q", i, ansi.StringWidth(out[i]), out[i])
		}
	}
	// Last line should not have a marker.
	last := out[len(out)-1]
	if strings.HasSuffix(last, diffmodel.WrapMarker) {
		t.Errorf("last line should not carry marker: %q", last)
	}
}

func TestWrapCell_ANSIStyled(t *testing.T) {
	// Build an ANSI-styled string that visibly measures 25 wide.
	styled := "\x1b[31m" + strings.Repeat("x", 25) + "\x1b[0m"
	out := diffmodel.WrapCell(styled, 10)
	if len(out) < 3 {
		t.Fatalf("expected >=3 lines, got %d", len(out))
	}
	// Each line's visible width must respect the cell budget.
	for i, line := range out {
		w := ansi.StringWidth(line)
		budget := 10
		if i < len(out)-1 {
			// Continuation lines include the wrap marker (1 wide).
			if w > budget {
				t.Errorf("continuation %d width %d > %d: %q", i, w, budget, line)
			}
		} else if w > budget {
			t.Errorf("last line %d width %d > %d", i, w, budget)
		}
	}
}

func TestWrapCell_CJKWide(t *testing.T) {
	// CJK chars are typically 2 cells wide.
	s := strings.Repeat("あ", 10) // visible width = 20
	out := diffmodel.WrapCell(s, 10)
	if len(out) < 2 {
		t.Fatalf("expected wrapping for CJK > width, got %d: %v", len(out), out)
	}
	for i, line := range out {
		w := ansi.StringWidth(line)
		if w > 10 {
			t.Errorf("line %d width %d > 10: %q", i, w, line)
		}
	}
}

func TestWrapCell_ZeroWidthFallback(t *testing.T) {
	out := diffmodel.WrapCell("hello world", 0)
	// Must not panic or return nothing.
	if len(out) == 0 {
		t.Fatal("expected non-empty output for zero width")
	}
}

func TestZipWrappedRow_EqualLength(t *testing.T) {
	l, r := diffmodel.ZipWrappedRow([]string{"a", "b"}, []string{"X", "Y"})
	if len(l) != 2 || len(r) != 2 {
		t.Fatalf("expected 2x2, got %d/%d", len(l), len(r))
	}
	if l[0] != "a" || l[1] != "b" || r[0] != "X" || r[1] != "Y" {
		t.Errorf("zip mismatch: %v / %v", l, r)
	}
}

func TestZipWrappedRow_LeftLonger(t *testing.T) {
	l, r := diffmodel.ZipWrappedRow([]string{"a", "b", "c"}, []string{"X"})
	if len(l) != 3 || len(r) != 3 {
		t.Fatalf("expected 3x3, got %d/%d", len(l), len(r))
	}
	if r[0] != "X" || r[1] != "" || r[2] != "" {
		t.Errorf("right padding wrong: %v", r)
	}
}

func TestZipWrappedRow_RightLonger(t *testing.T) {
	l, r := diffmodel.ZipWrappedRow([]string{"a"}, []string{"X", "Y", "Z"})
	if len(l) != 3 || len(r) != 3 {
		t.Fatalf("expected 3x3, got %d/%d", len(l), len(r))
	}
	if l[0] != "a" || l[1] != "" || l[2] != "" {
		t.Errorf("left padding wrong: %v", l)
	}
	if r[2] != "Z" {
		t.Errorf("right unchanged wrong: %v", r)
	}
}

func TestZipWrappedRow_BothEmpty(t *testing.T) {
	lines, rights := diffmodel.ZipWrappedRow(nil, nil)
	if len(lines) != 0 || len(rights) != 0 {
		t.Errorf("expected empty output, got %d/%d", len(lines), len(rights))
	}
}
