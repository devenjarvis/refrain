// Package mdrender turns markdown source into ANSI-styled, soft-wrapped
// display lines for the planeditor TUI.
//
// The renderer is intentionally minimal: ATX headers, bold/italic/inline-code
// spans, links, blockquotes, list bullets, horizontal rules, and fenced code
// blocks. Reference-style links, tables, GFM autolinks, and task-list
// checkboxes are out of scope.
//
// Two cardinal rules:
//
//  1. Wrap order is plain -> wrap -> style. ANSI is not visible-width and
//     breaks wrappers, so the renderer wraps raw segments first and applies
//     SGR escapes only after the segments are sized.
//  2. Caches are content-addressed, never index-addressed. RenderLines is
//     keyed on (sha256(plan), styleName, width); fence-block lex output is
//     keyed on (language, sha256(blockBody)). Lines and blocks that survive
//     an edit hit the cache.
package mdrender

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// LineKind labels what role a source line plays in the markdown stream.
type LineKind int

const (
	// LineParagraph is plain prose.
	LineParagraph LineKind = iota
	// LineBlank is an empty (or whitespace-only) line.
	LineBlank
	// LineHeading is an ATX header (#-######).
	LineHeading
	// LineList is a bulleted or ordered-list item.
	LineList
	// LineBlockquote is a `> ...` line.
	LineBlockquote
	// LineFenceOpen is the opening ``` line of a fenced block.
	LineFenceOpen
	// LineFenceClose is the closing ``` line of a fenced block.
	LineFenceClose
	// LineFenceContent is a content line inside a fenced block.
	LineFenceContent
	// LineHR is a horizontal rule (---, ***, ___).
	LineHR
)

// LineCtx is the per-source-line context the textarea wrapper uses to style
// each wrapped segment without re-parsing the buffer.
//
// Index correspondence: LineContexts returns one entry per `\n`-separated
// source line of the input plan. Callers must use the same split convention.
type LineCtx struct {
	Kind             LineKind
	HeadingLevel     int    // 1..6 when Kind == LineHeading; 0 otherwise
	FenceLang        string // language tag from the opening ``` (empty for plaintext)
	BlockquoteDepth  int    // count of leading `>` markers
	ListBullet       string // "-", "*", "+", or "1." (the literal marker token)
	ListIndent       int    // visible-width of the bullet+space prefix
	IsCheckbox       bool   // true when list item starts with "[ ] " or "[x] "
	CheckboxChecked  bool   // true when IsCheckbox and the box is checked
	// fencedStyledLine is the chroma-formatted ANSI output for this source
	// line when Kind == LineFenceContent. Empty for everything else.
	fencedStyledLine string
}

// Renderer converts markdown plans into ANSI-styled display lines. Construct
// one per editor and reuse it across frames; the chroma init is not free.
type Renderer struct {
	styleName string
	style     *chroma.Style
	formatter chroma.Formatter

	mu          sync.Mutex
	renderCache map[renderKey][]string
	fenceCache  map[fenceKey][]string // value: per-source-line ANSI strings
}

type renderKey struct {
	planHash  [32]byte
	styleName string
	width     int
}

type fenceKey struct {
	lang     string
	bodyHash [32]byte
}

// ContentMeasure returns the effective content width and left-margin padding
// for centering content within a terminal. When termWidth > maxMeasure the
// content is capped at maxMeasure and centered with equal left/right margins;
// otherwise no margin is applied and the full termWidth is used.
func ContentMeasure(termWidth, maxMeasure int) (measure, leftPad int) {
	if termWidth > maxMeasure {
		return maxMeasure, (termWidth - maxMeasure) / 2
	}
	return termWidth, 0
}

// New constructs a renderer that uses the given chroma style id for fence
// highlighting. Unknown style names fall through to chroma's Fallback so
// rendering never panics; callers that want strict validation should
// resolve styleName via config before calling here.
func New(styleName string) *Renderer {
	r := &Renderer{
		styleName:   styleName,
		renderCache: make(map[renderKey][]string),
		fenceCache:  make(map[fenceKey][]string),
	}
	r.style = styles.Get(styleName)
	if r.style == nil {
		r.style = styles.Fallback
	}
	r.formatter = formatters.Get("terminal256")
	if r.formatter == nil {
		r.formatter = formatters.Fallback
	}
	return r
}

// StyleName returns the chroma style id this renderer was constructed with.
func (r *Renderer) StyleName() string { return r.styleName }

// headingUnderline returns a synthetic underline display line for H1/H2
// headings. H1 uses a heavy ━ rule spanning maxWidth; H2 uses a thin ─ rule
// spanning min(headingWidth, maxWidth) in a muted color.
func (r *Renderer) headingUnderline(level, headingWidth, maxWidth int) string {
	if level == 1 {
		color := styleH1.GetForeground()
		return lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("━", maxWidth))
	}
	// H2: thin rule no wider than the heading text.
	w := headingWidth
	if w > maxWidth {
		w = maxWidth
	}
	if w < 1 {
		w = 1
	}
	return lipgloss.NewStyle().Foreground(colMuted).Render(strings.Repeat("─", w))
}

// RenderLines is the full-buffer pass: parse, wrap, style, and return the
// post-wrap ANSI display lines for scroll mode. Result is cached by
// (sha256(plan), styleName, width).
func (r *Renderer) RenderLines(plan string, width int) []string {
	if width < 1 {
		width = 1
	}
	key := renderKey{
		planHash:  sha256.Sum256([]byte(plan)),
		styleName: r.styleName,
		width:     width,
	}
	r.mu.Lock()
	if cached, ok := r.renderCache[key]; ok {
		r.mu.Unlock()
		return append([]string(nil), cached...)
	}
	r.mu.Unlock()

	ctxs := r.LineContexts(plan)
	sourceLines := splitLines(plan)
	out := make([]string, 0, len(sourceLines))
	for i, line := range sourceLines {
		var ctx LineCtx
		if i < len(ctxs) {
			ctx = ctxs[i]
		}
		segments := wrapPlain(line, width)
		if len(segments) == 0 {
			out = append(out, r.StyleSegment("", ctx, false))
		} else {
			col := 0
			for j, seg := range segments {
				styled := r.styleSegmentWithFence(seg, ctx, j > 0, col)
				out = append(out, styled)
				col += ansi.StringWidth(seg)
			}
		}
		// Inject underline decoration after H1/H2 heading lines.
		if ctx.Kind == LineHeading && ctx.HeadingLevel <= 2 {
			out = append(out, r.headingUnderline(ctx.HeadingLevel, ansi.StringWidth(line), width))
		}
	}

	r.mu.Lock()
	r.renderCache[key] = append([]string(nil), out...)
	r.mu.Unlock()
	return out
}

// LineContexts returns the per-source-line context for the given plan,
// including pre-lexed chroma output for fence content. Callers should split
// the plan with the same convention this package uses (strings.Split on
// "\n", preserving trailing-empty lines from a final newline).
func (r *Renderer) LineContexts(plan string) []LineCtx {
	lines := splitLines(plan)
	ctxs := make([]LineCtx, len(lines))

	// First pass: classify each line and collect fence ranges.
	type fenceRange struct {
		open, close int
		lang        string
	}
	var fences []fenceRange

	inFence := false
	fenceStart := -1
	fenceLang := ""

	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if inFence {
			if isFenceMarker(trimmed) {
				ctxs[i] = LineCtx{Kind: LineFenceClose, FenceLang: fenceLang}
				fences = append(fences, fenceRange{open: fenceStart, close: i, lang: fenceLang})
				inFence = false
				fenceStart = -1
				fenceLang = ""
				continue
			}
			ctxs[i] = LineCtx{Kind: LineFenceContent, FenceLang: fenceLang}
			continue
		}
		if isFenceMarker(trimmed) {
			lang := fenceLanguage(trimmed)
			ctxs[i] = LineCtx{Kind: LineFenceOpen, FenceLang: lang}
			inFence = true
			fenceStart = i
			fenceLang = lang
			continue
		}
		ctxs[i] = classifyLine(line)
	}

	// Unterminated fence: treat lines as content but skip the lex pass since
	// we never saw a close. Mark them content with the saved lang.
	if inFence {
		fences = append(fences, fenceRange{open: fenceStart, close: len(lines), lang: fenceLang})
	}

	// Second pass: lex each fence body once, attach ANSI lines to ctxs.
	for _, fr := range fences {
		bodyStart := fr.open + 1
		bodyEnd := fr.close
		if bodyStart >= bodyEnd {
			continue
		}
		body := strings.Join(lines[bodyStart:bodyEnd], "\n")
		styled := r.lexFenceBody(fr.lang, body)
		// lexFenceBody returns one styled line per source line in body.
		for i, s := range styled {
			idx := bodyStart + i
			if idx >= len(ctxs) {
				break
			}
			c := ctxs[idx]
			c.fencedStyledLine = s
			ctxs[idx] = c
		}
	}

	return ctxs
}

// StyleSegment styles a single wrapped segment given its source-line context
// and whether it is a continuation of a wrapped line. Continuations of list
// items are indented to align with the first segment's text rather than
// re-emitting the bullet glyph. Fence content is sliced from column 0 of the
// pre-lexed line; callers that wrap a fence line into multiple segments
// should use StyleLine, which tracks the cumulative column for each segment.
func (r *Renderer) StyleSegment(segment string, parent LineCtx, isContinuation bool) string {
	return r.styleSegmentWithFence(segment, parent, isContinuation, 0)
}

// StyleLine wraps and styles one source line into width-respecting display
// segments, tracking the cumulative column so multi-segment fence lines hit
// the right slice of the pre-lexed chroma output. This is the single-call
// helper used by mdtextarea's View().
func (r *Renderer) StyleLine(line string, ctx LineCtx, width int) []string {
	segments := wrapPlain(line, width)
	if len(segments) == 0 {
		return []string{r.styleSegmentWithFence("", ctx, false, 0)}
	}
	out := make([]string, 0, len(segments))
	col := 0
	for j, seg := range segments {
		out = append(out, r.styleSegmentWithFence(seg, ctx, j > 0, col))
		col += ansi.StringWidth(seg)
	}
	return out
}

func (r *Renderer) styleSegmentWithFence(segment string, parent LineCtx, isContinuation bool, fenceCol int) string {
	switch parent.Kind {
	case LineFenceContent:
		// Use the pre-lexed styled line if we have it; slice it at fenceCol
		// for this segment's plain width. ansi.Cut is grapheme/ANSI-aware.
		if parent.fencedStyledLine == "" {
			return styleFenceContent.Render(segment)
		}
		segWidth := ansi.StringWidth(segment)
		styled := ansi.Cut(parent.fencedStyledLine, fenceCol, fenceCol+segWidth)
		// chroma may not paint a background; the leading whitespace of an
		// indented code line should still feel like code, but we keep it
		// understated to avoid overpowering the plan body.
		return styled
	case LineFenceOpen, LineFenceClose:
		return styleFenceMarker.Render(segment)
	case LineHeading:
		return styleHeading(parent.HeadingLevel).Render(segment)
	case LineHR:
		return styleHR.Render(segment)
	case LineBlockquote:
		// Blockquotes get a subtle marker treatment over the whole line; we
		// don't try to style the `>` glyph differently from the body since
		// the wrap can split between marker and content.
		return styleBlockquote.Render(segment)
	case LineList:
		styled := styleListSegment(segment, parent, isContinuation)
		return styled
	case LineBlank:
		return segment
	default:
		return styleInline(segment)
	}
}

// lexFenceBody runs chroma over the whole fence body once and returns one
// ANSI-styled string per source line. Cache key is (lang, sha256(body)).
func (r *Renderer) lexFenceBody(lang, body string) []string {
	if body == "" {
		return nil
	}
	key := fenceKey{lang: lang, bodyHash: sha256.Sum256([]byte(body))}
	r.mu.Lock()
	if cached, ok := r.fenceCache[key]; ok {
		out := append([]string(nil), cached...)
		r.mu.Unlock()
		return out
	}
	r.mu.Unlock()

	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Get("plaintext")
	}
	if lexer == nil {
		// Final fallback: treat as plain text.
		return strings.Split(body, "\n")
	}

	it, err := lexer.Tokenise(nil, body)
	if err != nil {
		return strings.Split(body, "\n")
	}
	var buf bytes.Buffer
	if err := r.formatter.Format(&buf, r.style, it); err != nil {
		return strings.Split(body, "\n")
	}

	// chroma typically emits a trailing reset+newline. Strip ANSI from the
	// final entry and check if it's blank — if so, drop it so per-line
	// indices align with the input body's line count. ansi.Strip is
	// trailer-format-agnostic; an earlier hand-rolled TrimRight only worked
	// because chroma's terminal256 formatter happens to end in `\x1b[0m`,
	// which is fragile across chroma versions and per-style trailers.
	out := strings.Split(buf.String(), "\n")
	bodyLines := strings.Count(body, "\n") + 1
	for len(out) > bodyLines && strings.TrimSpace(ansi.Strip(out[len(out)-1])) == "" {
		out = out[:len(out)-1]
	}

	r.mu.Lock()
	r.fenceCache[key] = append([]string(nil), out...)
	r.mu.Unlock()
	return out
}

// ── line classification ──────────────────────────────────────────────────────

func classifyLine(line string) LineCtx {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" {
		return LineCtx{Kind: LineBlank}
	}
	if isHR(trimmed) {
		return LineCtx{Kind: LineHR}
	}
	if level, ok := atxHeadingLevel(trimmed); ok {
		return LineCtx{Kind: LineHeading, HeadingLevel: level}
	}
	if depth := blockquoteDepth(trimmed); depth > 0 {
		return LineCtx{Kind: LineBlockquote, BlockquoteDepth: depth}
	}
	if bullet, indent, ok := listMarker(line); ok {
		ctx := LineCtx{Kind: LineList, ListBullet: bullet, ListIndent: indent}
		// Detect GFM-style checkbox syntax after the bullet+space.
		body := line[indent:]
		switch {
		case strings.HasPrefix(body, "[ ] ") || body == "[ ]":
			ctx.IsCheckbox = true
		case strings.HasPrefix(body, "[x] ") || body == "[x]" ||
			strings.HasPrefix(body, "[X] ") || body == "[X]":
			ctx.IsCheckbox = true
			ctx.CheckboxChecked = true
		}
		return ctx
	}
	return LineCtx{Kind: LineParagraph}
}

func isFenceMarker(trimmed string) bool {
	if !strings.HasPrefix(trimmed, "```") {
		return false
	}
	// Any number of backticks >= 3 followed by an optional info string is a
	// fence boundary. We don't enforce the closing-fence-must-match-opening
	// rule because it adds parser state for negligible TUI benefit.
	rest := strings.TrimLeft(trimmed, "`")
	// Reject lines like "```` text" inline if there are non-` chars before the
	// triple-backtick — but we already checked HasPrefix so this is safe.
	_ = rest
	return true
}

func fenceLanguage(trimmed string) string {
	rest := strings.TrimLeft(trimmed, "`")
	rest = strings.TrimSpace(rest)
	// Info strings can include arguments after the language; first whitespace
	// token is the language tag.
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

func isHR(trimmed string) bool {
	if len(trimmed) < 3 {
		return false
	}
	c := trimmed[0]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	count := 0
	for _, r := range trimmed {
		switch r {
		case rune(c):
			count++
		case ' ', '\t':
			// allowed between markers
		default:
			return false
		}
	}
	return count >= 3
}

func atxHeadingLevel(trimmed string) (int, bool) {
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return 0, false
	}
	if level == len(trimmed) {
		return level, true // "###" with no content is still a heading
	}
	if trimmed[level] != ' ' && trimmed[level] != '\t' {
		return 0, false
	}
	return level, true
}

func blockquoteDepth(trimmed string) int {
	depth := 0
	for _, r := range trimmed {
		switch r {
		case '>':
			depth++
		case ' ', '\t':
			// allowed between markers
		default:
			if depth > 0 {
				return depth
			}
			return 0
		}
	}
	return depth
}

// listMarker detects unordered (-, *, +) and simple ordered (1.) bullets.
// Returns the bullet token and the visible-width indent of the bullet+space
// prefix (used for continuation alignment).
func listMarker(line string) (bullet string, indent int, ok bool) {
	// Skip leading whitespace.
	leading := 0
	for leading < len(line) && (line[leading] == ' ' || line[leading] == '\t') {
		leading++
	}
	rest := line[leading:]
	if rest == "" {
		return "", 0, false
	}
	switch rest[0] {
	case '-', '*', '+':
		if len(rest) < 2 || (rest[1] != ' ' && rest[1] != '\t') {
			return "", 0, false
		}
		return string(rest[0]), leading + 2, true
	}
	// Ordered: digits followed by '.' or ')' then space.
	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	if digits == 0 || digits > 9 {
		return "", 0, false
	}
	if digits >= len(rest) {
		return "", 0, false
	}
	punct := rest[digits]
	if punct != '.' && punct != ')' {
		return "", 0, false
	}
	if digits+1 >= len(rest) || (rest[digits+1] != ' ' && rest[digits+1] != '\t') {
		return "", 0, false
	}
	return rest[:digits+1], leading + digits + 2, true
}

// splitLines splits on "\n" without dropping the trailing empty entry that
// strings.Split produces when input ends with "\n". This matches what the
// bubbles textarea considers "rows" — a value of "abc\n" has two rows:
// "abc" and "". Keeping the convention symmetric here is what lets scroll-
// mode and edit-mode produce the same display-line count for the same plan.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// ── styles ───────────────────────────────────────────────────────────────────

var (
	colHeading1 = lipgloss.Color("#7C3AED") // primary purple — matches ColorPrimary
	colHeading2 = lipgloss.Color("#06B6D4") // cyan — matches ColorSecondary
	colHeading3 = lipgloss.Color("#10B981") // green — matches ColorSuccess
	colHeading4 = lipgloss.Color("#F59E0B") // amber — matches ColorWarning
	colHeading5 = lipgloss.Color("#A78BFA") // light purple
	colHeading6 = lipgloss.Color("#9CA3AF") // light gray

	colMuted          = lipgloss.Color("#6B7280")
	colCodeFG         = lipgloss.Color("#FBBF24") // amber for inline code
	colLink           = lipgloss.Color("#06B6D4")
	colQuote          = lipgloss.Color("#9CA3AF")
	colHR             = lipgloss.Color("#374151")
	colBullet         = lipgloss.Color("#A78BFA")
	colListNum        = lipgloss.Color("#A78BFA")
	colCheckboxDone   = lipgloss.Color("#10B981") // success green

	styleH1 = lipgloss.NewStyle().Foreground(colHeading1).Bold(true)
	styleH2 = lipgloss.NewStyle().Foreground(colHeading2).Bold(true)
	styleH3 = lipgloss.NewStyle().Foreground(colHeading3).Bold(true)
	styleH4 = lipgloss.NewStyle().Foreground(colHeading4).Bold(true)
	styleH5 = lipgloss.NewStyle().Foreground(colHeading5).Bold(true)
	styleH6 = lipgloss.NewStyle().Foreground(colHeading6).Bold(true)

	styleBold       = lipgloss.NewStyle().Bold(true)
	styleItalic     = lipgloss.NewStyle().Italic(true)
	styleInlineCode = lipgloss.NewStyle().Foreground(colCodeFG)
	styleLink       = lipgloss.NewStyle().Foreground(colLink).Underline(true)
	styleBlockquote = lipgloss.NewStyle().Foreground(colQuote).Italic(true)
	styleHR         = lipgloss.NewStyle().Foreground(colHR)
	styleBullet     = lipgloss.NewStyle().Foreground(colBullet).Bold(true)
	styleListOrdNum = lipgloss.NewStyle().Foreground(colListNum).Bold(true)

	// Fence-marker line ("```go", "```"): muted to keep attention on content.
	styleFenceMarker = lipgloss.NewStyle().Foreground(colMuted)
	// Fallback for fence content when chroma fails.
	styleFenceContent = lipgloss.NewStyle().Foreground(colCodeFG)
)

func styleHeading(level int) lipgloss.Style {
	switch level {
	case 1:
		return styleH1
	case 2:
		return styleH2
	case 3:
		return styleH3
	case 4:
		return styleH4
	case 5:
		return styleH5
	case 6:
		return styleH6
	default:
		return styleH1
	}
}

// styleListSegment styles a list-item segment. The first segment renders the
// bullet glyph in an emphasis color and the remainder via the inline pass.
// Continuation segments get a leading indent matching the bullet width so
// wrapped text aligns with the first segment's text rather than re-bulleting.
func styleListSegment(segment string, ctx LineCtx, isContinuation bool) string {
	if isContinuation {
		// We can't simply pad here — the wrap step already produced a segment
		// with no bullet, but the segment's text may have been stripped of
		// its leading whitespace by the wrapper. Pad to the bullet column so
		// the visible text aligns with the first line's text.
		if ctx.ListIndent > 0 {
			pad := strings.Repeat(" ", ctx.ListIndent)
			return pad + styleInline(segment)
		}
		return styleInline(segment)
	}
	// First segment: locate bullet within the leading whitespace + marker
	// region. We trust the segment starts with leading whitespace then the
	// bullet token (because that's how the source line begins).
	leading := 0
	for leading < len(segment) && (segment[leading] == ' ' || segment[leading] == '\t') {
		leading++
	}
	if leading >= len(segment) {
		return segment
	}
	rest := segment[leading:]
	// Match the same bullet token we detected in classification. Fall back
	// to the inline pass if the segment doesn't start with one (defensive).
	if ctx.ListBullet == "" {
		return styleInline(segment)
	}
	if !strings.HasPrefix(rest, ctx.ListBullet) {
		return styleInline(segment)
	}
	bulletStyle := styleBullet
	if len(ctx.ListBullet) > 1 {
		// Ordered-list marker like "1." gets the number color.
		bulletStyle = styleListOrdNum
	}
	bulletEnd := leading + len(ctx.ListBullet)
	// Include a trailing space after the bullet in the styled segment so the
	// emphasis runs to the start of the body text without leaving a gap.
	tailStart := bulletEnd
	if tailStart < len(segment) && segment[tailStart] == ' ' {
		tailStart++
	}
	prefix := segment[:leading] + bulletStyle.Render(segment[leading:tailStart])
	body := segment[tailStart:]

	// Render checkbox glyphs for task-list items on the first segment.
	if ctx.IsCheckbox {
		if ctx.CheckboxChecked {
			for _, pfx := range []string{"[x] ", "[X] "} {
				if strings.HasPrefix(body, pfx) {
					glyph := lipgloss.NewStyle().Foreground(colCheckboxDone).Render("✓")
					return prefix + glyph + " " + styleInline(body[4:])
				}
			}
		} else if strings.HasPrefix(body, "[ ] ") {
			glyph := lipgloss.NewStyle().Foreground(colMuted).Render("☐")
			return prefix + glyph + " " + styleInline(body[4:])
		}
	}

	return prefix + styleInline(body)
}

// ── inline span rendering ────────────────────────────────────────────────────

// styleInline parses bold/italic/inline-code/link spans and applies styles.
// The parser is a single forward pass; nested spans are handled greedily
// (inner-most close wins). Markdown sequences that don't match a close are
// rendered as plain text.
func styleInline(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		c := s[i]
		switch c {
		case '`':
			if end := findInlineCodeEnd(s, i+1); end > 0 {
				b.WriteString(styleInlineCode.Render(s[i : end+1]))
				i = end + 1
				continue
			}
		case '*':
			// Bold (**) takes priority over italic (*).
			if i+1 < len(s) && s[i+1] == '*' {
				if end := findBoldEnd(s, i+2, "**"); end > 0 {
					b.WriteString(styleBold.Render(s[i : end+2]))
					i = end + 2
					continue
				}
			} else {
				if end := findItalicEnd(s, i+1, '*'); end > 0 {
					b.WriteString(styleItalic.Render(s[i : end+1]))
					i = end + 1
					continue
				}
			}
		case '_':
			if end := findItalicEnd(s, i+1, '_'); end > 0 {
				b.WriteString(styleItalic.Render(s[i : end+1]))
				i = end + 1
				continue
			}
		case '[':
			if end := findLinkEnd(s, i); end > 0 {
				b.WriteString(styleLink.Render(s[i : end+1]))
				i = end + 1
				continue
			}
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// findInlineCodeEnd returns the index of the closing backtick, or -1.
func findInlineCodeEnd(s string, from int) int {
	for i := from; i < len(s); i++ {
		if s[i] == '`' {
			return i
		}
	}
	return -1
}

// findBoldEnd returns the index of the first character of the closing "**".
func findBoldEnd(s string, from int, marker string) int {
	for i := from; i+len(marker) <= len(s); i++ {
		if s[i:i+len(marker)] == marker {
			return i
		}
	}
	return -1
}

// findItalicEnd returns the index of the closing italic marker. Refuses to
// match across whitespace at the start (e.g. "* foo *" is not italic) so
// stray asterisks don't accidentally style large regions.
func findItalicEnd(s string, from int, marker byte) int {
	if from >= len(s) || s[from] == ' ' || s[from] == '\t' {
		return -1
	}
	for i := from; i < len(s); i++ {
		if s[i] == marker {
			// Ensure the closing marker isn't followed by another of itself
			// (which would make this "**" a bold opener, not italic close).
			if i+1 < len(s) && s[i+1] == marker {
				continue
			}
			return i
		}
	}
	return -1
}

// findLinkEnd matches "[text](url)" and returns the index of the closing ')'.
func findLinkEnd(s string, from int) int {
	if from >= len(s) || s[from] != '[' {
		return -1
	}
	// Find closing ']'.
	closeText := -1
	for i := from + 1; i < len(s); i++ {
		if s[i] == ']' {
			closeText = i
			break
		}
		if s[i] == '\n' {
			return -1
		}
	}
	if closeText < 0 || closeText+1 >= len(s) || s[closeText+1] != '(' {
		return -1
	}
	for i := closeText + 2; i < len(s); i++ {
		if s[i] == ')' {
			return i
		}
		if s[i] == '\n' {
			return -1
		}
	}
	return -1
}
