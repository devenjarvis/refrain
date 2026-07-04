// Package theme is Refrain's design-system registry: the single source of truth
// for every color, glyph, border, spacing, and animation token used across the
// TUI. It has no internal dependencies, so the leaf rendering subpackages
// (internal/tui/diff, internal/tui/mdrender) can consume the same tokens as the
// top-level internal/tui package without violating the layering rules in
// .go-arch-lint.yml.
//
// See DESIGN.md at the repo root for the role catalog and usage guidance. The
// rule for contributors and agents alike: never hardcode a hex, glyph, border,
// or padding value — add or reference a token here.
package theme

import (
	"os"

	"github.com/charmbracelet/lipgloss"
)

// noColor mirrors the NO_COLOR convention: when the env var is set (to any
// value), every token degrades to the terminal's default color. It is read once
// at package init.
var noColor = os.Getenv("NO_COLOR") != ""

// initColor gates a hex color behind NO_COLOR. A blank lipgloss.Color renders
// with the terminal default, so styles built from it carry no ANSI color.
func initColor(hex string) lipgloss.Color {
	if noColor {
		return lipgloss.Color("")
	}
	return lipgloss.Color(hex)
}

// initAdaptive gates a light/dark adaptive color behind NO_COLOR.
func initAdaptive(dark, light string) lipgloss.TerminalColor {
	if noColor {
		return lipgloss.Color("")
	}
	return lipgloss.AdaptiveColor{Dark: dark, Light: light}
}

// Color roles. Identifiers are organized by role; the value comments give the
// raw hex. Collapsed near-duplicates (e.g. markdown headings that were
// byte-identical to brand accents) are noted at their canonical role below.
var (
	// ── Surfaces & text ────────────────────────────────────────────────────

	// ColorBg is the base application surface.
	ColorBg = initColor("#111827")
	// ColorSurfaceRaised is a raised surface (status bar, chips) above the base.
	ColorSurfaceRaised = initColor("#1F2937")
	// ColorText is primary, high-contrast body/title text.
	ColorText = initColor("#F9FAFB")
	// ColorTextProse is softer prose text, deliberately dimmer than ColorText
	// to reduce glare across long markdown reading.
	ColorTextProse = initColor("#D1D5DB")
	// ColorMuted is de-emphasized text, separators, borders, and gutters.
	ColorMuted = initColor("#6B7280")
	// ColorMutedLight is secondary de-emphasized text one step brighter than
	// ColorMuted (markdown blockquotes and H6 collapse here).
	ColorMutedLight = initColor("#9CA3AF")
	// ColorHairline is the thin divider/rule color.
	ColorHairline = initColor("#374151")

	// ── Brand accents ──────────────────────────────────────────────────────

	// ColorPrimary is the primary brand accent (titles, primary borders; md H1).
	ColorPrimary = initColor("#7C3AED")
	// ColorSecondary is the secondary accent (links, active, hunk headers; md H2).
	ColorSecondary = initColor("#06B6D4")
	// ColorPrimaryLight is a lighter purple accent (md H5, bullets, list numbers).
	ColorPrimaryLight = initColor("#A78BFA")

	// ── Status roles (agent run-state) ─────────────────────────────────────

	// ColorSuccess marks success/pass/additions (md H3, checkboxes).
	ColorSuccess = initColor("#10B981")
	// ColorWarning marks warnings/concerns (md H4).
	ColorWarning = initColor("#F59E0B")
	// ColorError marks errors/failures/deletions.
	ColorError = initColor("#EF4444")
	// ColorWaiting accents agents in StatusWaiting (permission prompts, input
	// blocks). Kept distinct from status colors so waiting reads unambiguously.
	ColorWaiting = initColor("#D946EF")

	// ── Diff backgrounds ───────────────────────────────────────────────────
	// Diff add/del FOREGROUNDS reuse ColorSuccess/ColorError. The four
	// backgrounds stay distinct: normal vs. word-diff emphasis is load-bearing.

	// ColorDiffAddBg is the row background for added lines.
	ColorDiffAddBg = initColor("#0a2e1f")
	// ColorDiffDelBg is the row background for deleted lines.
	ColorDiffDelBg = initColor("#2e0a14")
	// ColorDiffAddBgEmph is the intra-line emphasis background for additions.
	ColorDiffAddBgEmph = initColor("#165c3f")
	// ColorDiffDelBgEmph is the intra-line emphasis background for deletions.
	ColorDiffDelBgEmph = initColor("#5c1629")

	// ── Markdown code ──────────────────────────────────────────────────────

	// ColorCodeFg is inline-code and fenced-fallback code foreground.
	ColorCodeFg = initColor("#FBBF24")
	// ColorCodeBg is the fenced code-block background.
	ColorCodeBg = initColor("#1A1D23")
	// ColorInlineCodeBg is the inline-code background; the only adaptive token.
	ColorInlineCodeBg = initAdaptive("#2D2D2D", "#E8E8E8")
)

// MarkdownHeadingColor maps a markdown heading level (1–6) onto a color role.
// Levels beyond 6 fall back to the H1 accent. This is the documented token for
// the heading scale; callers should not redeclare per-level heading colors.
func MarkdownHeadingColor(level int) lipgloss.Color {
	switch level {
	case 1:
		return ColorPrimary
	case 2:
		return ColorSecondary
	case 3:
		return ColorSuccess
	case 4:
		return ColorWarning
	case 5:
		return ColorPrimaryLight
	case 6:
		return ColorMutedLight
	default:
		return ColorPrimary
	}
}
