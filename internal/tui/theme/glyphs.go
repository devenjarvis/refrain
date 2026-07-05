package theme

import "time"

// Glyph tokens. Every single-rune marker the TUI emits lives here so the icon
// language stays consistent and a glyph swap is a one-line edit. Group by role.
const (
	// ── Status badges / session glyphs ─────────────────────────────────────

	GlyphError    = "✗" // error / fail (U+2717)
	GlyphSuccess  = "✓" // success / pass (U+2713)
	GlyphWaiting  = "⏸" // waiting on input / permission
	GlyphQuestion = "?" // may need input
	GlyphActive   = "●" // actively running
	GlyphIdle     = "○" // idle / pending (U+25CB)
	GlyphCross    = "×" // closed / done agent tab

	// ── Verdict / check badges ─────────────────────────────────────────────

	GlyphPending  = "⋯" // pending verdict
	GlyphFlagged  = "⚑" // user-flagged for rework
	GlyphConcerns = "!" // verdict: concerns
	GlyphNoDiff   = "⊘" // verdict: no diff found
	GlyphManual   = "·" // AI verdict intentionally skipped — manual review

	// ── Navigation / chrome ────────────────────────────────────────────────

	GlyphStripe       = "▎" // session-card status stripe
	GlyphCursor       = "▍" // selected-row cursor bar
	GlyphCaret        = "›" // inline list cursor
	GlyphArrow        = "→" // stacked-PR chain separator
	GlyphFolderOpen   = "▾" // expanded diff-tree folder
	GlyphFolderClosed = "▸" // collapsed diff-tree folder

	// ── Markdown ───────────────────────────────────────────────────────────

	GlyphCheckboxDone = "✓" // checked task checkbox
	GlyphCheckboxTodo = "☐" // unchecked task checkbox
	GlyphFenceBar     = "│" // prefix bar on fenced lines
	GlyphRuleThin     = "─" // thin horizontal rule
	GlyphRuleHeavy    = "━" // heavy horizontal rule
)

// SpinnerBraille is the braille spinner sequence shown while work is running.
var SpinnerBraille = []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}

// SpinnerFrame returns the spinner glyph for the given render clock. now is the
// model's tick-refreshed timestamp (no clock read at render time); deriving the
// frame from it keeps all running rows in sync.
func SpinnerFrame(now time.Time) string {
	return SpinnerBraille[int(now.UnixMilli()/100)%len(SpinnerBraille)]
}
