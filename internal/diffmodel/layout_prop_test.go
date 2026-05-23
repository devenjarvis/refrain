package diffmodel_test

import (
	"testing"

	"github.com/devenjarvis/refrain/internal/diffmodel"
	"pgregory.net/rapid"
)

// No non-header row has both sides blank simultaneously.
func TestLayoutHunks_NoBothSidesBlank(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		hunks := genHunks(t)
		rows := diffmodel.LayoutHunks(hunks, 80)
		for i, r := range rows {
			if r.HunkHeader {
				continue
			}
			if r.LeftBlank && r.RightBlank {
				t.Fatalf("row %d has both sides blank (hunks=%v)", i, hunks)
			}
		}
	})
}

// The number of hunk header rows equals the number of input hunks.
func TestLayoutHunks_HeaderCountEqualsHunkCount(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		hunks := genHunks(t)
		rows := diffmodel.LayoutHunks(hunks, 80)
		headers := 0
		for _, r := range rows {
			if r.HunkHeader {
				headers++
			}
		}
		if headers != len(hunks) {
			t.Fatalf("got %d headers for %d hunks", headers, len(hunks))
		}
	})
}

// The number of non-blank left-side rows equals delete+context line count in the input.
func TestLayoutHunks_LeftSideRowsMatchDelPlusCtx(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		hunks := genHunks(t)
		rows := diffmodel.LayoutHunks(hunks, 80)
		delCtx, _ := countInputLines(hunks)

		leftNonBlank := 0
		for _, r := range rows {
			if r.HunkHeader {
				continue
			}
			if !r.LeftBlank {
				leftNonBlank++
			}
		}
		if leftNonBlank != delCtx {
			t.Fatalf("left non-blank rows = %d, want %d (delete+context lines)", leftNonBlank, delCtx)
		}
	})
}

// The number of non-blank right-side rows equals add+context line count in the input.
func TestLayoutHunks_RightSideRowsMatchAddPlusCtx(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		hunks := genHunks(t)
		rows := diffmodel.LayoutHunks(hunks, 80)
		_, addCtx := countInputLines(hunks)

		rightNonBlank := 0
		for _, r := range rows {
			if r.HunkHeader {
				continue
			}
			if !r.RightBlank {
				rightNonBlank++
			}
		}
		if rightNonBlank != addCtx {
			t.Fatalf("right non-blank rows = %d, want %d (add+context lines)", rightNonBlank, addCtx)
		}
	})
}

// Every context input line produces a row where both sides carry the same text.
func TestLayoutHunks_ContextLinesAppearsOnBothSides(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		hunks := genHunks(t)
		rows := diffmodel.LayoutHunks(hunks, 80)

		// Collect context texts from input in order.
		var wantCtx []string
		for _, h := range hunks {
			for _, l := range h.Lines {
				if l.Kind == diffmodel.LineContext {
					wantCtx = append(wantCtx, l.Text)
				}
			}
		}

		// Collect context rows from output in order. We must guard against
		// LeftBlank/RightBlank rows whose LeftKind is the zero value (LineContext)
		// even though they are not genuine context rows.
		var gotCtx []string
		for _, r := range rows {
			if r.HunkHeader {
				continue
			}
			if r.LeftKind == diffmodel.LineContext && !r.LeftBlank && !r.RightBlank {
				if r.LeftText != r.RightText {
					t.Fatalf("context row has mismatched sides: left=%q right=%q", r.LeftText, r.RightText)
				}
				gotCtx = append(gotCtx, r.LeftText)
			}
		}

		if len(gotCtx) != len(wantCtx) {
			t.Fatalf("context row count: got %d, want %d", len(gotCtx), len(wantCtx))
		}
		for i := range wantCtx {
			if gotCtx[i] != wantCtx[i] {
				t.Fatalf("context row %d: got %q, want %q", i, gotCtx[i], wantCtx[i])
			}
		}
	})
}
