package tui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/devenjarvis/refrain/internal/tui/mdrender"
	"github.com/devenjarvis/refrain/internal/tui/mdtextarea"
)

// docEditorChromaStyle is the chroma style used by the markdown renderer.
// Hardcoded for now — a follow-up will plumb this through config.Settings.
const docEditorChromaStyle = "monokai"

// docEditorMaxMeasure is the maximum content column width for plan/PR text.
// On wider terminals the content is centered with equal left/right margins.
const docEditorMaxMeasure = 72

// docEditor is the shared display/edit component used by planEditorModel
// and prComposeModal. It owns the textarea, markdown renderer, scroll
// offset, and dimensional fields that both views formerly duplicated.
type docEditor struct {
	textarea  mdtextarea.Model
	renderer  *mdrender.Renderer
	scrollOff int
	width     int
	height    int
}

// newDocEditor constructs a docEditor sized to w×h.
func newDocEditor(width, height int) docEditor {
	r := mdrender.New(docEditorChromaStyle)
	ta := mdtextarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.SetMarkdownRenderer(r)
	d := docEditor{
		renderer: r,
		width:    width,
		height:   height,
	}
	ta.SetWidth(d.ContentWidth())
	ta.SetHeight(textareaHeight(height))
	d.textarea = ta
	return d
}

// ContentWidth returns the effective column width for wrap and styling.
// Capped at docEditorMaxMeasure so wide terminals produce comfortable margins.
func (d *docEditor) ContentWidth() int {
	measure, _ := mdrender.ContentMeasure(textareaWidth(d.width), docEditorMaxMeasure)
	return measure
}

// DisplayLeftPad returns the left-margin padding to center content on wide terminals.
func (d *docEditor) DisplayLeftPad() int {
	_, pad := mdrender.ContentMeasure(textareaWidth(d.width), docEditorMaxMeasure)
	return pad
}

// CenteredBlock prepends DisplayLeftPad() spaces to each line of s, matching
// scroll-mode centering.
func (d *docEditor) CenteredBlock(s string) string {
	pad := d.DisplayLeftPad()
	if pad == 0 {
		return s
	}
	prefix := strings.Repeat(" ", pad)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// BodyHeight returns the number of lines available for content given the
// fixed overhead rows (header, dividers, footer, etc.) for the calling view.
func (d *docEditor) BodyHeight(overhead int) int {
	h := d.height - overhead
	if h < 1 {
		return 1
	}
	return h
}

// ClampScroll adjusts d.scrollOff to [0, totalLines].
func (d *docEditor) ClampScroll(totalLines int) {
	if d.scrollOff > totalLines {
		d.scrollOff = totalLines
	}
	if d.scrollOff < 0 {
		d.scrollOff = 0
	}
}

// ScrollWindow extracts a viewport window from lines using d.scrollOff and bodyH.
// The local-start clamping ensures the last page fills the viewport without
// mutating d.scrollOff, keeping the render path pure.
func (d *docEditor) ScrollWindow(lines []string, bodyH int) string {
	start := d.scrollOff
	if start > len(lines)-bodyH {
		start = len(lines) - bodyH
	}
	if start < 0 {
		start = 0
	}
	end := start + bodyH
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start:end], "\n")
}

// RenderLines renders the current value into styled display lines.
func (d *docEditor) RenderLines() []string {
	return d.renderer.RenderLines(d.Value(), d.ContentWidth())
}

// StyledScrollLines wraps and styles a source line for scroll-mode display,
// applying fence-block bar prefixes that are omitted in edit mode to keep
// mdtextarea cursor-splice math unaffected.
func (d *docEditor) StyledScrollLines(src string, ctx mdrender.LineCtx, width int) []string {
	isFenceLine := ctx.Kind == mdrender.LineFenceContent || ctx.Kind == mdrender.LineFenceOpen || ctx.Kind == mdrender.LineFenceClose
	lineWidth := width
	if isFenceLine && width > 2 {
		lineWidth = width - 2
	}
	lines := d.renderer.StyleLine(src, ctx, lineWidth)
	if isFenceLine {
		bar := StyleSubtle.Render("│") + " "
		for i, l := range lines {
			lines[i] = bar + l
		}
	}
	return lines
}

// SetSize updates the editor dimensions and resizes the textarea.
func (d *docEditor) SetSize(w, h int) {
	d.width = w
	d.height = h
	d.textarea.SetWidth(d.ContentWidth())
	d.textarea.SetHeight(textareaHeight(h))
}

// Focus focuses the textarea.
func (d *docEditor) Focus() tea.Cmd {
	return d.textarea.Focus()
}

// Blur unfocuses the textarea.
func (d *docEditor) Blur() {
	d.textarea.Blur()
}

// Value returns the current textarea content.
func (d *docEditor) Value() string {
	return d.textarea.Value()
}

// SetValue sets the textarea content.
func (d *docEditor) SetValue(s string) {
	d.textarea.SetValue(s)
}

// Focused reports whether the textarea is focused.
func (d *docEditor) Focused() bool {
	return d.textarea.Focused()
}

// UpdateTextarea forwards a message to the textarea and returns the resulting cmd.
func (d *docEditor) UpdateTextarea(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	d.textarea, cmd = d.textarea.Update(msg)
	return cmd
}

// textareaWidth/textareaHeight reserve space for the header, divider, status,
// and footer when sizing the embedded textarea.
func textareaWidth(w int) int {
	if w < 8 {
		return 8
	}
	return w - 2
}

func textareaHeight(h int) int {
	if h < 6 {
		return 1
	}
	return h - 5
}

func fmtSeconds(s int) string {
	if s < 0 {
		s = 0
	}
	if s < 60 {
		return strconv.Itoa(s) + "s"
	}
	return strconv.Itoa(s/60) + "m" + strconv.Itoa(s%60) + "s"
}
