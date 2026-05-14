// Package mdtextarea wraps charm.land/bubbles/v2 textarea.Model with a
// View() override that applies mdrender's syntax highlighting in real time.
//
// Editing semantics — gap buffer, key bindings, undo, paste — are entirely
// inherited from the upstream textarea. We touch the render path only.
//
// When no markdown renderer has been set, View() is a passthrough so callers
// get byte-identical output to the upstream textarea (and a regression test
// asserts this).
package mdtextarea

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/devenjarvis/refrain/internal/tui/mdrender"
)

// Model embeds textarea.Model and adds an mdrender.Renderer hook for live
// syntax highlighting.
type Model struct {
	textarea.Model

	renderer *mdrender.Renderer
}

// New returns a new Model with no renderer attached. Callers should call
// SetMarkdownRenderer to enable highlighting; without one, View() delegates
// to the upstream textarea unchanged.
func New() Model {
	return Model{Model: textarea.New()}
}

// SetMarkdownRenderer attaches a renderer used by View() for live styling.
// Pass nil to disable highlighting and fall back to upstream rendering.
func (m *Model) SetMarkdownRenderer(r *mdrender.Renderer) { m.renderer = r }

// MarkdownRenderer returns the currently attached renderer, or nil if none.
func (m *Model) MarkdownRenderer() *mdrender.Renderer { return m.renderer }

// Update forwards the message to the embedded textarea and returns the new
// state through our wrapper. Without this override, callers would receive a
// bare textarea.Model and lose the renderer reference.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	inner, cmd := m.Model.Update(msg)
	m.Model = inner
	return m, cmd
}

// View overrides the upstream textarea View(). When no renderer is attached
// the call is a passthrough; otherwise we wrap+style each source line via
// mdrender, splice the cursor cell into the active wrapped row, apply the
// scroll offset from the embedded textarea's viewport, and pad to height.
func (m Model) View() string {
	if m.renderer == nil {
		return m.Model.View()
	}
	val := m.Value()
	width := m.Width()
	height := m.Height()
	if width < 1 || height < 1 {
		return m.Model.View()
	}
	// Fall through to upstream for the empty+placeholder path; the upstream
	// renders a one-line prompt+placeholder we don't try to replicate.
	if val == "" && m.Placeholder != "" {
		return m.Model.View()
	}

	sourceLines := splitSourceLines(val)
	if len(sourceLines) == 0 {
		// Truly empty buffer (no placeholder to display either): pad height.
		return m.padToHeight(nil, height, width)
	}

	ctxs := m.renderer.LineContexts(val)

	// Build display rows alongside the per-source-line plain wrap segments,
	// so the cursor splice can index into both at the active row.
	type row struct {
		styled string
		plain  string
	}
	var rows []row
	sourceStarts := make([]int, len(sourceLines))
	for i, src := range sourceLines {
		sourceStarts[i] = len(rows)
		var ctx mdrender.LineCtx
		if i < len(ctxs) {
			ctx = ctxs[i]
		}
		styledSegs := m.renderer.StyleLine(src, ctx, width)
		// Re-derive plain segments by stripping ANSI from styled. We don't
		// expose wrapPlain externally; this is a few microseconds even on
		// 500-line plans.
		for _, s := range styledSegs {
			rows = append(rows, row{styled: s, plain: ansi.Strip(s)})
		}
	}

	// Cursor splice (focused only). When unfocused, no cursor is drawn.
	if m.Focused() {
		li := m.LineInfo()
		cursorLine := m.Line()
		if cursorLine >= 0 && cursorLine < len(sourceStarts) {
			rowIdx := sourceStarts[cursorLine] + li.RowOffset
			if rowIdx >= 0 && rowIdx < len(rows) {
				rows[rowIdx].styled = spliceCursor(rows[rowIdx].styled, rows[rowIdx].plain, li.ColumnOffset, m.Styles())
			}
		}
	}

	// Pad each row to width with trailing spaces so background styles fill
	// the line. The upstream textarea does this; matching keeps the editor
	// chrome consistent across the visible viewport.
	displayLines := make([]string, len(rows))
	for i, rw := range rows {
		displayLines[i] = padRowToWidth(rw.styled, ansi.StringWidth(rw.plain), width)
	}

	// Apply scroll offset and crop to height.
	scroll := m.ScrollYOffset()
	if scroll < 0 {
		scroll = 0
	}
	if scroll > len(displayLines) {
		scroll = len(displayLines)
	}
	end := scroll + height
	if end > len(displayLines) {
		end = len(displayLines)
	}
	visible := displayLines[scroll:end]

	out := m.padToHeight(visible, height, width)

	// Wrap in the active style's Base — matches upstream's
	// `styles.Base.Render(view)` outer wrap so the editor still picks up
	// padding/border styles set on the textarea.
	styles := m.activeStyleState()
	return styles.Base.Render(out)
}

// padToHeight ensures the rendered output is exactly `height` rows. Missing
// rows are filled with the textarea's EndOfBufferCharacter under the
// computed end-of-buffer style — same as upstream.
func (m Model) padToHeight(visible []string, height, width int) string {
	styles := m.activeStyleState()
	eob := styles.EndOfBuffer.Inline(true)
	leftGutter := string(m.EndOfBufferCharacter)
	gutterWidth := ansi.StringWidth(leftGutter)
	pad := strings.Repeat(" ", maxInt(0, width-gutterWidth))
	eobLine := eob.Render(leftGutter + pad)

	rows := make([]string, 0, height)
	rows = append(rows, visible...)
	for len(rows) < height {
		rows = append(rows, eobLine)
	}
	return strings.Join(rows, "\n")
}

// activeStyleState returns the focused or blurred StyleState matching the
// embedded textarea's focus state.
func (m Model) activeStyleState() textarea.StyleState {
	s := m.Styles()
	if m.Focused() {
		return s.Focused
	}
	return s.Blurred
}

// spliceCursor inserts a reverse-video cursor cell into the styled row at
// visible column `col`. plain is the un-styled version of the same row used
// to look up the rune under the cursor. If the rune at col is wide (e.g. a
// double-width CJK glyph mid-cell) we bail out and return the row untouched
// — IME/CJK live composition is explicit graceful-degradation territory per
// the implementation plan.
func spliceCursor(styled, plain string, col int, styles textarea.Styles) string {
	plainWidth := ansi.StringWidth(plain)

	// Cursor past end of line (e.g. user is at column N of an N-char line):
	// append a space cell with reverse styling so the cursor is visible at
	// the end of the row.
	if col >= plainWidth {
		cell := cursorStyle(styles).Render(" ")
		return styled + cell
	}

	// Locate the rune at visible column `col` in `plain`. Reject wide chars.
	var ch rune
	colSeen := 0
	found := false
	for _, r := range plain {
		w := ansi.StringWidth(string(r))
		if colSeen == col {
			if w != 1 {
				return styled // graceful degradation
			}
			ch = r
			found = true
			break
		}
		if colSeen+w > col {
			// Cursor lands in the middle of a wide rune — bail.
			return styled
		}
		colSeen += w
	}
	if !found {
		return styled
	}

	left := ansi.Cut(styled, 0, col)
	right := ansi.Cut(styled, col+1, plainWidth)
	cell := cursorStyle(styles).Render(string(ch))
	return left + cell + right
}

// cursorStyle returns a lipgloss style for the cursor cell. Reverse-video
// alone keeps the cell visible against any underlying syntax color without
// needing to interrogate the upstream Cursor.Color (which uses a non-lipgloss
// color interface and isn't trivially convertible here).
func cursorStyle(_ textarea.Styles) lipgloss.Style {
	return lipgloss.NewStyle().Reverse(true)
}

// padRowToWidth pads a styled row out to the given target width with plain
// trailing spaces. visibleWidth is the row's plain visible width; we use it
// (rather than ansi.StringWidth on the styled string) to avoid double-counting
// SGR escapes.
func padRowToWidth(styled string, visibleWidth, target int) string {
	if visibleWidth >= target {
		return styled
	}
	return styled + strings.Repeat(" ", target-visibleWidth)
}

// splitSourceLines splits a buffer's value into source-line strings. The
// convention matches the bubbles textarea: a value of "abc\n" has two rows
// ("abc" and ""), so we use a plain strings.Split rather than mdrender's
// trailing-empty-aware splitLines (which is itself unified to this convention,
// but we keep a direct call here so the wrapper isn't coupled to that detail).
func splitSourceLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
