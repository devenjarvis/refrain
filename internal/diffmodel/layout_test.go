package diffmodel_test

import (
	"testing"

	"github.com/devenjarvis/refrain/internal/diffmodel"
)

// mkHunk builds a hunk from a compact spec: each string is " ", "-" or "+"
// followed by the text. Old/new line numbers start at 1/1.
func mkHunk(lines ...string) diffmodel.Hunk {
	h := diffmodel.Hunk{Header: "@@ test @@"}
	oldNum, newNum := 1, 1
	for _, s := range lines {
		var l diffmodel.Line
		l.Text = s[1:]
		switch s[0] {
		case ' ':
			l.Kind = diffmodel.LineContext
			l.OldNum, l.NewNum = oldNum, newNum
			oldNum++
			newNum++
		case '-':
			l.Kind = diffmodel.LineDelete
			l.OldNum = oldNum
			oldNum++
		case '+':
			l.Kind = diffmodel.LineAdd
			l.NewNum = newNum
			newNum++
		}
		h.Lines = append(h.Lines, l)
	}
	return h
}

func TestLayoutHunks_BalancedPair(t *testing.T) {
	h := mkHunk("-a", "-b", "-c", "+A", "+B", "+C")
	rows := diffmodel.LayoutHunks([]diffmodel.Hunk{h}, 80)

	// 1 header + 3 paired rows.
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(rows))
	}
	if !rows[0].HunkHeader {
		t.Error("row 0 should be header")
	}
	for i, exp := range []struct{ l, r string }{{"a", "A"}, {"b", "B"}, {"c", "C"}} {
		r := rows[i+1]
		if r.LeftBlank || r.RightBlank {
			t.Errorf("row %d should not be blank: %+v", i+1, r)
		}
		if r.LeftText != exp.l || r.RightText != exp.r {
			t.Errorf("row %d mismatch: L=%q R=%q", i+1, r.LeftText, r.RightText)
		}
		if r.LeftKind != diffmodel.LineDelete || r.RightKind != diffmodel.LineAdd {
			t.Errorf("row %d kinds: L=%v R=%v", i+1, r.LeftKind, r.RightKind)
		}
	}
}

func TestLayoutHunks_MoreDeletesThanAdds(t *testing.T) {
	h := mkHunk("-a", "-b", "-c", "-d", "-e", "+A", "+B")
	rows := diffmodel.LayoutHunks([]diffmodel.Hunk{h}, 80)

	// 1 header + 5 rows (max(5,2)).
	if len(rows) != 6 {
		t.Fatalf("expected 6 rows, got %d", len(rows))
	}
	body := rows[1:]
	for i, exp := range []struct {
		l, r           string
		lBlank, rBlank bool
	}{
		{"a", "A", false, false},
		{"b", "B", false, false},
		{"c", "", false, true},
		{"d", "", false, true},
		{"e", "", false, true},
	} {
		r := body[i]
		if r.LeftText != exp.l || r.RightText != exp.r {
			t.Errorf("row %d: L=%q R=%q", i, r.LeftText, r.RightText)
		}
		if r.LeftBlank != exp.lBlank || r.RightBlank != exp.rBlank {
			t.Errorf("row %d blanks: %t/%t want %t/%t", i, r.LeftBlank, r.RightBlank, exp.lBlank, exp.rBlank)
		}
	}
}

func TestLayoutHunks_MoreAddsThanDeletes(t *testing.T) {
	h := mkHunk("-a", "+A", "+B", "+C")
	rows := diffmodel.LayoutHunks([]diffmodel.Hunk{h}, 80)

	// 1 header + 3 rows.
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(rows))
	}
	body := rows[1:]
	if body[0].LeftText != "a" || body[0].RightText != "A" {
		t.Errorf("row 0 wrong: %+v", body[0])
	}
	for i := 1; i < 3; i++ {
		if !body[i].LeftBlank {
			t.Errorf("row %d: expected left blank", i)
		}
	}
}

func TestLayoutHunks_PureAdd(t *testing.T) {
	h := mkHunk("+x", "+y", "+z")
	rows := diffmodel.LayoutHunks([]diffmodel.Hunk{h}, 80)

	// 1 header + 3 rows, all left-blank.
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(rows))
	}
	for i, r := range rows[1:] {
		if !r.LeftBlank {
			t.Errorf("row %d: expected left blank", i)
		}
		if r.RightBlank {
			t.Errorf("row %d: right should not be blank", i)
		}
	}
}

func TestLayoutHunks_PureDelete(t *testing.T) {
	h := mkHunk("-x", "-y")
	rows := diffmodel.LayoutHunks([]diffmodel.Hunk{h}, 80)

	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	for i, r := range rows[1:] {
		if r.LeftBlank {
			t.Errorf("row %d: left should not be blank", i)
		}
		if !r.RightBlank {
			t.Errorf("row %d: expected right blank", i)
		}
	}
}

func TestLayoutHunks_ContextIntermixed(t *testing.T) {
	// Context → 2del+1add → context → 1add.
	h := mkHunk(" head", "-x", "-y", "+X", " mid", "+z", " tail")
	rows := diffmodel.LayoutHunks([]diffmodel.Hunk{h}, 80)

	// rows: header + head + (x,X) + (y,blank) + mid + (blank,z) + tail = 7
	if len(rows) != 7 {
		t.Fatalf("expected 7 rows, got %d\nrows: %+v", len(rows), rows)
	}
	body := rows[1:]
	// head context
	if body[0].LeftText != "head" || body[0].RightText != "head" {
		t.Errorf("context head wrong: %+v", body[0])
	}
	// paired
	if body[1].LeftText != "x" || body[1].RightText != "X" {
		t.Errorf("paired row wrong: %+v", body[1])
	}
	// unpaired delete (right blank)
	if body[2].LeftText != "y" || !body[2].RightBlank {
		t.Errorf("unpaired delete wrong: %+v", body[2])
	}
	// mid context
	if body[3].LeftText != "mid" || body[3].RightText != "mid" {
		t.Errorf("context mid wrong: %+v", body[3])
	}
	// unpaired add (left blank)
	if body[4].RightText != "z" || !body[4].LeftBlank {
		t.Errorf("unpaired add wrong: %+v", body[4])
	}
	if body[5].LeftText != "tail" {
		t.Errorf("context tail wrong: %+v", body[5])
	}
}

func TestLayoutHunks_MultiHunkRowCount(t *testing.T) {
	h1 := mkHunk("-a", "+A")
	h2 := mkHunk(" x", "-y", "+Y", " z")
	rows := diffmodel.LayoutHunks([]diffmodel.Hunk{h1, h2}, 80)

	// h1: header + 1 paired = 2
	// h2: header + ctx + paired + ctx = 4
	if len(rows) != 6 {
		t.Fatalf("expected 6 rows, got %d", len(rows))
	}
	if !rows[0].HunkHeader || !rows[2].HunkHeader {
		t.Error("expected headers at 0 and 2")
	}
}

// TestLayoutHunks_CentralSeparatorStable checks the critical invariant: for
// every hunk body row, either both sides have content or one side is
// explicitly blank — there is never an unpaired emission that would shift the
// gutter out of phase.
func TestLayoutHunks_CentralSeparatorStable(t *testing.T) {
	h := mkHunk(" a", "-b", "-c", "-d", "+B", "+C", " e", "+F", "+G", " h")
	rows := diffmodel.LayoutHunks([]diffmodel.Hunk{h}, 80)

	for i, r := range rows {
		if r.HunkHeader {
			continue
		}
		// Every non-header row has a defined L and R state (either content or blank).
		// This is exactly the invariant — left and right are independent slots.
		_ = i
		if r.LeftBlank && r.RightBlank {
			t.Errorf("row %d has both sides blank", i)
		}
	}
}
