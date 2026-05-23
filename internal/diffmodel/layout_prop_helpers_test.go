package diffmodel_test

import (
	"fmt"

	"github.com/devenjarvis/refrain/internal/diffmodel"
	"pgregory.net/rapid"
)

// genHunks produces a slice of 1-5 Hunks with valid, monotonically increasing
// OldNum/NewNum line numbers. Each hunk contains a random mix of context,
// delete, and add lines. The generator never produces an empty hunk.
func genHunks(t *rapid.T) []diffmodel.Hunk {
	numHunks := rapid.IntRange(1, 5).Draw(t, "num_hunks")
	hunks := make([]diffmodel.Hunk, numHunks)

	// Start line numbers at 1 and advance across hunks so they don't overlap.
	oldCursor := 1
	newCursor := 1

	for h := 0; h < numHunks; h++ {
		// Each hunk contains 1-10 lines.
		numLines := rapid.IntRange(1, 10).Draw(t, fmt.Sprintf("hunk%d_lines", h))
		lines := make([]diffmodel.Line, 0, numLines)

		hunkOld := oldCursor
		hunkNew := newCursor

		for i := 0; i < numLines; i++ {
			// 0=context, 1=delete, 2=add
			kind := rapid.IntRange(0, 2).Draw(t, fmt.Sprintf("h%d_l%d_kind", h, i))
			text := rapid.StringOf(rapid.Map(rapid.IntRange(0x20, 0x7E), func(n int) rune { return rune(n) })).Draw(t, fmt.Sprintf("h%d_l%d_text", h, i))
			switch kind {
			case 0: // context
				lines = append(lines, diffmodel.Line{
					Kind: diffmodel.LineContext, Text: text,
					OldNum: oldCursor, NewNum: newCursor,
				})
				oldCursor++
				newCursor++
			case 1: // delete
				lines = append(lines, diffmodel.Line{
					Kind: diffmodel.LineDelete, Text: text,
					OldNum: oldCursor,
				})
				oldCursor++
			case 2: // add
				lines = append(lines, diffmodel.Line{
					Kind: diffmodel.LineAdd, Text: text,
					NewNum: newCursor,
				})
				newCursor++
			}
		}

		header := fmt.Sprintf("@@ -%d +%d @@", hunkOld, hunkNew)
		hunks[h] = diffmodel.Hunk{Header: header, Lines: lines}

		// Advance cursors past a small gap to simulate non-contiguous hunks.
		oldCursor += rapid.IntRange(1, 5).Draw(t, fmt.Sprintf("gap_old_%d", h))
		newCursor += rapid.IntRange(1, 5).Draw(t, fmt.Sprintf("gap_new_%d", h))
	}

	return hunks
}

// countInputLines returns the number of delete+context and add+context lines
// in a slice of hunks (used to cross-check against LayoutHunks output).
func countInputLines(hunks []diffmodel.Hunk) (delPlusCtx, addPlusCtx int) {
	for _, h := range hunks {
		for _, l := range h.Lines {
			switch l.Kind {
			case diffmodel.LineDelete, diffmodel.LineContext:
				delPlusCtx++
			}
			switch l.Kind {
			case diffmodel.LineAdd, diffmodel.LineContext:
				addPlusCtx++
			}
		}
	}
	return
}
