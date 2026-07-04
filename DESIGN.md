# DESIGN.md — Refrain's design system

This is the visual-vocabulary reference for Refrain's TUI: colors, glyphs,
borders, spacing, and animation. It is written for both human contributors and
Claude agents. `CLAUDE.md` says *what* the product does; `CONVENTIONS.md` says
*how* the code is structured; this file says *how it should look* and where the
tokens live.

## Source of truth

Token **values** live in code, in the leaf package
[`internal/tui/theme`](internal/tui/theme). This document is the navigable map
and rationale — when a value and this doc disagree, the code wins, and this doc
should be corrected. The registry is a leaf package with **no internal
dependencies**, so it can be imported by the top-level `internal/tui` package
*and* by the `internal/tui/diff` and `internal/tui/mdrender` subpackages without
violating the layering rules in `.go-arch-lint.yml`.

## How to use it (the rule)

**Never hardcode a hex color, glyph rune, border, or padding value at a call
site.** Reference a token instead.

- Subpackages and new code: `import "github.com/devenjarvis/refrain/internal/tui/theme"`
  and reference `theme.ColorPrimary`, `theme.GlyphSuccess`, `theme.BorderModal()`,
  `theme.PadModal`, etc.
- Inside the top-level `tui` package, the color roles and composed `Style*`
  objects are also available unqualified (e.g. `ColorPrimary`, `StyleHeading`)
  via the thin bridge in `internal/tui/theme.go`; those aliases point straight
  at the registry, which remains the single source of truth.
- If you need a color/glyph that has no token, **add a role** to the registry
  and a row to this doc — do not inline a literal. See *Adding or changing a
  token* below.

This is the mechanical enforcement of `CONVENTIONS.md` §5 ("Styles come from one
theme/style registry").

## Color roles

Roles are semantic, not hue-named: pick the role that matches the *meaning*, and
the palette can be retuned in one place. (`internal/tui/theme/colors.go`.)

### Surfaces & text

| Role | Hex | Use when… |
|---|---|---|
| `ColorBg` | `#111827` | the base application surface |
| `ColorSurfaceRaised` | `#1F2937` | a raised surface above the base (status bar, chips) |
| `ColorText` | `#F9FAFB` | primary, high-contrast body/title text |
| `ColorTextProse` | `#D1D5DB` | long-form prose (markdown body); deliberately dimmer than `ColorText` to cut glare |
| `ColorMuted` | `#6B7280` | de-emphasized text, separators, borders, gutters |
| `ColorMutedLight` | `#9CA3AF` | secondary de-emphasized text, one step brighter than `ColorMuted` |
| `ColorHairline` | `#374151` | thin dividers / horizontal rules |

### Brand accents

| Role | Hex | Use when… |
|---|---|---|
| `ColorPrimary` | `#7C3AED` | primary accent: titles, primary borders, markdown H1 |
| `ColorSecondary` | `#06B6D4` | secondary accent: links, active state, hunk headers, markdown H2 |
| `ColorPrimaryLight` | `#A78BFA` | lighter purple accent: markdown H5, bullets, list numbers |

### Status roles (agent run-state)

| Role | Hex | Use when… |
|---|---|---|
| `ColorSuccess` | `#10B981` | success / pass / additions / checked boxes |
| `ColorWarning` | `#F59E0B` | warnings / concerns |
| `ColorError` | `#EF4444` | errors / failures / deletions |
| `ColorWaiting` | `#D946EF` | an agent is blocked on input (permission prompt) |

### Diff backgrounds

Diff add/del **foregrounds** reuse `ColorSuccess`/`ColorError`. The four
backgrounds are distinct because normal vs. word-diff emphasis is load-bearing.

| Role | Hex | Use when… |
|---|---|---|
| `ColorDiffAddBg` | `#0a2e1f` | row background for added lines |
| `ColorDiffDelBg` | `#2e0a14` | row background for deleted lines |
| `ColorDiffAddBgEmph` | `#165c3f` | intra-line emphasis background, additions |
| `ColorDiffDelBgEmph` | `#5c1629` | intra-line emphasis background, deletions |

### Markdown code

| Role | Hex | Use when… |
|---|---|---|
| `ColorCodeFg` | `#FBBF24` | inline-code and fenced-fallback code foreground |
| `ColorCodeBg` | `#1A1D23` | fenced code-block background |
| `ColorInlineCodeBg` | adaptive `{Dark:#2D2D2D, Light:#E8E8E8}` | inline-code background (the only adaptive token) |

Markdown heading colors are not separate roles — they map through
`theme.MarkdownHeadingColor(level)` onto the roles above (H1→Primary,
H2→Secondary, H3→Success, H4→Warning, H5→PrimaryLight, H6→MutedLight).

## Glyph & icon set

Every single-rune marker the TUI emits is a token in
`internal/tui/theme/glyphs.go`. Reference the constant; don't paste the rune.

| Token | Glyph | Meaning | Emitted by |
|---|---|---|---|
| `GlyphError` | `✗` | error / fail | `sessionListStatus`, `verdictBadge`, `checkBadge`, `checkSymbolFor` |
| `GlyphSuccess` | `✓` | success / pass | same set |
| `GlyphWaiting` | `⏸` | waiting on input | `sessionListStatus` |
| `GlyphQuestion` | `?` | may need input | `checkSymbolFor` |
| `GlyphActive` | `●` | actively running | `sessionListStatus`, `focusLaunchTabDot` |
| `GlyphIdle` | `○` | idle / pending | `sessionListStatus`, `focusLaunchTabDot`, `checkSymbolFor` |
| `GlyphCross` | `×` | closed / done tab | `focusLaunchTabDot` |
| `GlyphPending` | `⋯` | pending verdict | `verdictBadge`, `checkBadge` |
| `GlyphFlagged` | `⚑` | flagged for rework | `verdictBadge` |
| `GlyphConcerns` | `!` | verdict: concerns | `verdictBadge` |
| `GlyphNoDiff` | `⊘` | verdict: no diff | `verdictBadge` |
| `GlyphManual` | `·` | AI verdict skipped — manual review | `verdictBadge` |
| `GlyphStripe` | `▎` | card attention stripe | `renderSessionCard` |
| `GlyphCursor` | `▍` | selected-row cursor | diff tree |
| `GlyphCaret` | `›` | inline list cursor | checks list |
| `GlyphArrow` | `→` | stacked-PR chain sep | `prIndicator` |
| `GlyphFolderOpen` / `GlyphFolderClosed` | `▾` / `▸` | diff-tree folder | diff tree |
| `GlyphCheckboxDone` / `GlyphCheckboxTodo` | `✓` / `☐` | markdown task box | mdrender |
| `GlyphFenceBar` | `│` | fenced-line / quote bar | mdrender |
| `GlyphRuleThin` / `GlyphRuleHeavy` | `─` / `━` | horizontal rule | mdrender |

## Spinner & animation

- `SpinnerBraille` — the braille spinner frame set; `theme.SpinnerFrame(now)`
  derives the current frame from the model's tick timestamp (no clock read at
  render time, per `CONVENTIONS.md` §5), keeping every running row in sync.

## Borders & chrome

Border *shape and color* are design decisions and live as constructors in
`internal/tui/theme/borders.go`; *sizing* (Width/Height/Padding) is the caller's.

- `BorderModal()` — rounded primary-accent border for centered overlay boxes
  (repo-config, repo-checks, global-settings, note modals). Combine with
  `PadModal`.
- `BorderPaneLeft()` — muted normal border on the right edge only, for the left
  pane of a two-column split (file-browser, branch-picker, repo-picker).

Width/height *arithmetic* (border, sidebar, separator, modal-chrome math) is a
separate concern and lives in `internal/tui/layout.go` as pure helpers
(`innerWidth`, `modalContentWidth`, `splitColumns`, …). Layout math goes there;
fixed design constants go in the registry.

## Spacing scale

`internal/tui/theme/spacing.go`. Padding pairs are `{vertical, horizontal}` to
match `lipgloss.Padding(v, h)`.

| Token | Value | Use |
|---|---|---|
| `PadModal` | `{1, 2}` | modal box inner padding |
| `PadStatusBar` | `{0, 1}` | status bar inner padding |
| `IndentTree` | `2` | per-depth indent of the diff file tree |
| `LabelWidth` | `22` | fixed width of a form field label |

## NO_COLOR & color profiles

Every color token is gated by `initColor` (and `initAdaptive`), which honor the
`NO_COLOR` convention: when `NO_COLOR` is set to any value, tokens degrade to the
terminal's default color. Because the `diff` and `mdrender` subpackages now build
their styles from the registry, they honor `NO_COLOR` too (they did not before —
a deliberate improvement). One color path is **not** ours: chroma's
`terminal256` syntax highlighting in fenced code blocks and diffs is governed by
chroma's own style, not these tokens.

Render tests force a TrueColor profile so token output is deterministic. To smoke
the degraded path, run the TUI with `NO_COLOR=1`.

## Adding or changing a token

1. Add or edit the role in the relevant `internal/tui/theme/*.go` file, with a
   godoc comment explaining when to use it.
2. If it's a color used by the top-level `tui` package unqualified, add an alias
   in `internal/tui/theme.go`.
3. Add or update the row in this doc.
4. Keep the build green: `go build ./...`, `go test -race ./...`, `go vet ./...`,
   `gofmt -w .`, `golangci-lint run`, `go-arch-lint check`.
5. Add a `changelog.d/` fragment.
6. Any **visual** change (a retuned hue, a swapped glyph) must be deliberate and
   called out in the PR — the design system exists to make drift visible, not to
   smuggle it in.
