// Package diff is the TUI-side rendering layer for diff views. It consumes
// parsed/laid-out data from internal/diffmodel and turns it into styled
// strings, with caching to keep layout cheap on steady-state.
package diff

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/devenjarvis/refrain/internal/diffmodel"
)

// SideBySideMinWidth is the terminal-width threshold (inclusive) above which
// the renderer uses two-column side-by-side mode; below it, a unified layout
// is used instead. Lowered from the old diffrender.go constant of 120.
const SideBySideMinWidth = 100

// defaultChromaStyle is the chroma style used if no other is configured. It
// blends well with the refrain dark theme.
const defaultChromaStyle = "monokai"

// Renderer paints a single file's diff. Caller is expected to construct one
// per file selection and reuse it across frames; internally it caches the
// expensive chroma lexing + layout pass keyed on (width, mode).
type Renderer struct {
	file *diffmodel.File

	// styleName is the chroma style id. Defaults to defaultChromaStyle.
	styleName string

	// Cached state.
	lexer     chroma.Lexer
	style     *chroma.Style
	formatter chroma.Formatter

	// cache of rendered output keyed on the pair (width, sideBySide).
	cache map[cacheKey]string
}

type cacheKey struct {
	width      int
	sideBySide bool
}

// NewRenderer builds a renderer for the given file.
func NewRenderer(f *diffmodel.File) *Renderer {
	r := &Renderer{
		file:      f,
		styleName: defaultChromaStyle,
		cache:     make(map[cacheKey]string),
	}
	r.resolveChroma()
	return r
}

func (r *Renderer) resolveChroma() {
	if r.file != nil && r.file.Path != "" {
		r.lexer = lexers.Match(r.file.Path)
	}
	if r.lexer == nil {
		r.lexer = lexers.Get("plaintext")
	}
	r.style = styles.Get(r.styleName)
	if r.style == nil {
		r.style = styles.Fallback
	}
	r.formatter = formatters.Get("terminal256")
	if r.formatter == nil {
		r.formatter = formatters.Fallback
	}
}

// Render returns the painted string for this file at the given width. If
// sideBySide is true and width is large enough, two columns are produced;
// otherwise the unified layout is used. The result is ready to be placed into
// a bubbles viewport; it does NOT include a file header.
func (r *Renderer) Render(width int, sideBySide bool) string {
	if r.file == nil {
		return ""
	}
	if width < 1 {
		width = 1
	}
	if sideBySide && width < SideBySideMinWidth {
		sideBySide = false
	}
	key := cacheKey{width: width, sideBySide: sideBySide}
	if s, ok := r.cache[key]; ok {
		return s
	}

	var out string
	if r.file.IsBinary {
		out = r.renderBinary(width)
	} else if sideBySide {
		out = r.renderSideBySide(width)
	} else {
		out = r.renderUnified(width)
	}
	r.cache[key] = out
	return out
}

// ── styles ───────────────────────────────────────────────────────────────────

var (
	colAdd         = lipgloss.Color("#10B981")
	colDel         = lipgloss.Color("#EF4444")
	colMuted       = lipgloss.Color("#6B7280")
	colSecondary   = lipgloss.Color("#06B6D4")
	colAddBg       = lipgloss.Color("#0a2e1f")
	colDelBg       = lipgloss.Color("#2e0a14")
	colAddBgBright = lipgloss.Color("#165c3f")
	colDelBgBright = lipgloss.Color("#5c1629")

	styleAddRow    = lipgloss.NewStyle().Background(colAddBg)
	styleDelRow    = lipgloss.NewStyle().Background(colDelBg)
	styleAddEmph   = lipgloss.NewStyle().Background(colAddBgBright)
	styleDelEmph   = lipgloss.NewStyle().Background(colDelBgBright)
	styleGutter    = lipgloss.NewStyle().Foreground(colMuted)
	styleHunkBan   = lipgloss.NewStyle().Foreground(colSecondary)
	styleAddMark   = lipgloss.NewStyle().Foreground(colAdd).Bold(true)
	styleDelMark   = lipgloss.NewStyle().Foreground(colDel).Bold(true)
	styleCtxMark   = lipgloss.NewStyle().Foreground(colMuted)
	styleBinaryBan = lipgloss.NewStyle().Foreground(colMuted).Italic(true)
)

// ── binary ───────────────────────────────────────────────────────────────────

func (r *Renderer) renderBinary(width int) string {
	msg := fmt.Sprintf("Binary file %s — no preview", r.file.Path)
	line := truncateVisible(msg, width)
	return styleBinaryBan.Render(padTo(line, width))
}

// ── unified ──────────────────────────────────────────────────────────────────

func (r *Renderer) renderUnified(width int) string {
	var b strings.Builder
	gutter := 6 // 5 digits + space
	content := width - gutter - 2
	if content < 4 {
		content = 4
	}
	for _, h := range r.file.Hunks {
		b.WriteString(styleHunkBan.Render(truncateVisible(h.Header, width)))
		b.WriteByte('\n')
		for _, l := range h.Lines {
			r.writeUnifiedLine(&b, l, gutter, content)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (r *Renderer) writeUnifiedLine(b *strings.Builder, l diffmodel.Line, gutter, content int) {
	var num int
	var mark string
	var rowStyle, markStyle lipgloss.Style
	switch l.Kind {
	case diffmodel.LineAdd:
		num = l.NewNum
		mark = "+"
		rowStyle = styleAddRow
		markStyle = styleAddMark
	case diffmodel.LineDelete:
		num = l.OldNum
		mark = "-"
		rowStyle = styleDelRow
		markStyle = styleDelMark
	default:
		if l.NewNum > 0 {
			num = l.NewNum
		} else {
			num = l.OldNum
		}
		mark = " "
		markStyle = styleCtxMark
	}

	text := expandTabs(l.Text)
	colored := r.colorize(text, content)
	wrapped := diffmodel.WrapCell(colored, content)
	numStr := formatGutter(num, gutter-1)

	for i, w := range wrapped {
		var gutterStr string
		if i == 0 {
			gutterStr = styleGutter.Render(numStr)
		} else {
			gutterStr = styleGutter.Render(strings.Repeat(" ", gutter-1))
		}
		markRendered := markStyle.Render(mark)
		cell := padTo(w, content)
		row := gutterStr + " " + markRendered + cell
		if l.Kind != diffmodel.LineContext {
			row = rowStyle.Render(row)
		}
		b.WriteString(row)
		b.WriteByte('\n')
	}
}

// ── side-by-side ─────────────────────────────────────────────────────────────

func (r *Renderer) renderSideBySide(width int) string {
	// Column layout: [numL:5][space][markL:1][cellL:content][sep:" │ "][numR:5][space][markR:1][cellR:content]
	const numW = 5
	const markW = 1
	const sepW = 3
	fixed := 2*(numW+1+markW) + sepW
	content := (width - fixed) / 2
	if content < 4 {
		content = 4
	}

	var b strings.Builder
	for _, h := range r.file.Hunks {
		// Header spans the full width.
		b.WriteString(styleHunkBan.Render(truncateVisible(h.Header, width)))
		b.WriteByte('\n')

		rows := diffmodel.LayoutHunks([]diffmodel.Hunk{h}, content)
		// Skip the synthetic header row we already emitted.
		for _, row := range rows {
			if row.HunkHeader {
				continue
			}
			r.writeSideBySideRow(&b, row, numW, content)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (r *Renderer) writeSideBySideRow(
	b *strings.Builder, row diffmodel.Row,
	numW, content int,
) {
	leftMark, leftStyle, leftNum, leftText := cellParts(
		row.LeftBlank, row.LeftKind, row.LeftNum, row.LeftText,
	)
	rightMark, rightStyle, rightNum, rightText := cellParts(
		row.RightBlank, row.RightKind, row.RightNum, row.RightText,
	)

	leftText = expandTabs(leftText)
	rightText = expandTabs(rightText)

	var leftColored, rightColored string
	if !row.LeftBlank {
		leftColored = r.colorize(leftText, content)
	}
	if !row.RightBlank {
		rightColored = r.colorize(rightText, content)
	}

	// Apply intra-line emphasis only on paired add/delete rows.
	paired := row.LeftKind == diffmodel.LineDelete && row.RightKind == diffmodel.LineAdd &&
		!row.LeftBlank && !row.RightBlank
	if paired {
		leftColored, rightColored = overlayWordDiff(leftColored, leftText, rightColored, rightText)
	}

	// Side-by-side truncates to exactly one physical terminal line per row,
	// eliminating wrap-count mismatches between the two panes.
	var leftCell, rightCell string
	if row.LeftBlank {
		leftCell = strings.Repeat(" ", content)
	} else {
		leftCell = padTo(truncateVisible(leftColored, content), content)
	}
	if row.RightBlank {
		rightCell = strings.Repeat(" ", content)
	} else {
		rightCell = padTo(truncateVisible(rightColored, content), content)
	}

	lNumStr := strings.Repeat(" ", numW)
	rNumStr := strings.Repeat(" ", numW)
	if !row.LeftBlank {
		lNumStr = formatGutter(leftNum, numW)
	}
	if !row.RightBlank {
		rNumStr = formatGutter(rightNum, numW)
	}

	lMarkRendered := " "
	rMarkRendered := " "
	if !row.LeftBlank {
		lMarkRendered = leftStyle.Render(leftMark)
	}
	if !row.RightBlank {
		rMarkRendered = rightStyle.Render(rightMark)
	}

	leftBlock := styleGutter.Render(lNumStr) + " " + lMarkRendered + leftCell
	rightBlock := styleGutter.Render(rNumStr) + " " + rMarkRendered + rightCell

	if !row.LeftBlank {
		leftBlock = applyRowBg(row.LeftKind, leftBlock)
	}
	if !row.RightBlank {
		rightBlock = applyRowBg(row.RightKind, rightBlock)
	}

	sep := styleGutter.Render(" │ ")
	b.WriteString(leftBlock + sep + rightBlock + "\n")
}

func cellParts(blank bool, kind diffmodel.LineKind, num int, text string) (mark string, ms lipgloss.Style, n int, t string) {
	if blank {
		return " ", lipgloss.NewStyle(), 0, ""
	}
	switch kind {
	case diffmodel.LineAdd:
		return "+", styleAddMark, num, text
	case diffmodel.LineDelete:
		return "-", styleDelMark, num, text
	default:
		return " ", styleCtxMark, num, text
	}
}

func applyRowBg(kind diffmodel.LineKind, s string) string {
	switch kind {
	case diffmodel.LineAdd:
		return styleAddRow.Render(s)
	case diffmodel.LineDelete:
		return styleDelRow.Render(s)
	default:
		return s
	}
}

// ── chroma + intra-line diff ─────────────────────────────────────────────────

// colorize runs chroma over the text and returns the ANSI-styled output. The
// width parameter is an upper bound for display; colorize does not truncate
// or wrap. If lexer/formatter fail, the plain text is returned unchanged.
func (r *Renderer) colorize(text string, _ int) string {
	if r.lexer == nil || r.style == nil || r.formatter == nil {
		return text
	}
	it, err := r.lexer.Tokenise(nil, text)
	if err != nil {
		return text
	}
	var buf bytes.Buffer
	if err := r.formatter.Format(&buf, r.style, it); err != nil {
		return text
	}
	// chroma often emits a trailing reset+newline; strip the newline so the
	// caller controls line boundaries.
	out := buf.String()
	return strings.TrimRight(out, "\n")
}

// overlayWordDiff runs sergi's char-level diff between the plain (uncolored)
// strings and returns both sides with an emphasis background overlaid on the
// ranges of changed characters. We operate on the plain text to decide *what*
// to emphasize, then apply the emphasis by splicing into the *colored*
// strings using cell positions.
//
// This function is intentionally best-effort: if the splice fails for any
// reason (e.g. the colored text has structure that resists indexing), it
// returns the colored strings unmodified.
func overlayWordDiff(leftColored, leftPlain, rightColored, rightPlain string) (string, string) {
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(leftPlain, rightPlain, false)
	diffs = dmp.DiffCleanupSemantic(diffs)

	leftRanges := collectRanges(diffs, diffmatchpatch.DiffDelete, diffmatchpatch.DiffEqual)
	rightRanges := collectRanges(diffs, diffmatchpatch.DiffInsert, diffmatchpatch.DiffEqual)

	return applyEmphasis(leftColored, leftPlain, leftRanges, styleDelEmph),
		applyEmphasis(rightColored, rightPlain, rightRanges, styleAddEmph)
}

type runeRange struct {
	start, end int // [start, end) over runes of the plain text
}

// collectRanges walks a sergi diff and returns rune ranges on ONE side of the
// diff corresponding to the 'which' op. `passOp` is the operation that
// appears on the same side (e.g. DiffEqual is on both sides so it advances
// both counters).
func collectRanges(diffs []diffmatchpatch.Diff, which, passOp diffmatchpatch.Operation) []runeRange {
	var out []runeRange
	pos := 0
	for _, d := range diffs {
		n := len([]rune(d.Text))
		switch d.Type {
		case which:
			out = append(out, runeRange{start: pos, end: pos + n})
			pos += n
		case passOp:
			pos += n
		default:
			// Opposite-side op: does not advance this side.
		}
	}
	return out
}

// applyEmphasis wraps each rune range in the plain text with the emphasis
// style while leaving the surrounding colored text intact. Because ANSI
// sequences in colored may add zero-width bytes at arbitrary positions, we
// parse SGR escapes explicitly and toggle emphasis at plain-rune boundaries.
//
// The function also re-emits the emphasis "on" sequence after any SGR reset
// inside an active emphasis range so resets embedded by chroma do not clear
// our background.
func applyEmphasis(colored, plain string, ranges []runeRange, emph lipgloss.Style) string {
	if len(ranges) == 0 || colored == "" {
		return colored
	}
	plainRunes := []rune(plain)
	inRange := make([]bool, len(plainRunes))
	for _, rg := range ranges {
		for i := rg.start; i < rg.end && i < len(inRange); i++ {
			inRange[i] = true
		}
	}

	on := emph.Render("")
	// Strip any terminating reset from `on` so we only get the opening SGR.
	if i := strings.Index(on, "\x1b[0m"); i >= 0 {
		on = on[:i]
	}
	off := "\x1b[49m" // reset background only

	var b strings.Builder
	plainIdx := 0
	emphasizing := false
	colRunes := []rune(colored)
	i := 0
	for i < len(colRunes) {
		c := colRunes[i]
		if c == 0x1b && i+1 < len(colRunes) && colRunes[i+1] == '[' {
			// CSI sequence: ESC [ params final. Final byte is 0x40–0x7E; we
			// start looking past the '[' so the '[' itself is not treated as
			// a terminator.
			end := i + 2
			for end < len(colRunes) {
				ch := colRunes[end]
				end++
				if ch >= 0x40 && ch <= 0x7E {
					break
				}
			}
			seq := string(colRunes[i:end])
			b.WriteString(seq)
			// If that escape is a full reset AND we are currently
			// emphasizing, reapply the emphasis so the background survives.
			if emphasizing && isSGRReset(seq) {
				b.WriteString(on)
			}
			i = end
			continue
		}
		if c == 0x1b {
			// Non-CSI escape: copy ESC and one following byte, if present.
			end := i + 1
			if end < len(colRunes) {
				end++
			}
			b.WriteString(string(colRunes[i:end]))
			i = end
			continue
		}

		shouldEmph := plainIdx < len(inRange) && inRange[plainIdx]
		if shouldEmph && !emphasizing {
			b.WriteString(on)
			emphasizing = true
		} else if !shouldEmph && emphasizing {
			b.WriteString(off)
			emphasizing = false
		}
		b.WriteRune(c)
		plainIdx++
		i++
	}
	if emphasizing {
		b.WriteString(off)
	}
	return b.String()
}

// isSGRReset reports whether seq is an ANSI reset (\x1b[0m or \x1b[m).
func isSGRReset(seq string) bool {
	return seq == "\x1b[0m" || seq == "\x1b[m"
}

// ── helpers ──────────────────────────────────────────────────────────────────

const tabSize = 4

// expandTabs replaces tab characters with spaces so ansi.StringWidth measures
// the correct display width before any wrapping or truncation occurs.
func expandTabs(s string) string {
	return strings.ReplaceAll(s, "\t", strings.Repeat(" ", tabSize))
}

// padTo pads s with spaces on the right until its visible width reaches n.
func padTo(s string, n int) string {
	w := ansi.StringWidth(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}

// truncateVisible hard-truncates to n display cells, appending ellipsis when
// the string actually exceeds n. ANSI-aware.
func truncateVisible(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= n {
		return s
	}
	// ansi.Truncate trims visible width while preserving ANSI.
	return ansi.Truncate(s, n, "…")
}

// formatGutter right-justifies the number in a field of `width` cells. Zero
// is rendered as blank.
func formatGutter(n, width int) string {
	if n == 0 {
		return strings.Repeat(" ", width)
	}
	s := fmt.Sprintf("%*d", width, n)
	if len(s) > width {
		s = s[len(s)-width:]
	}
	return s
}
