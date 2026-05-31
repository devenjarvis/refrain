### Added

- Design system: a new leaf package `internal/tui/theme` is the single registry for every visual token — color roles, glyphs, spinner/animation palettes, border styles, and spacing — and a new `DESIGN.md` documents the role catalog for contributors and agents. The `diff` and `mdrender` subpackages now consume the same tokens instead of redeclaring their own colors.

### Changed

- Consolidated ~48 scattered hex color literals into ~26 semantic role tokens, collapsing exact duplicates and centralizing glyphs, animation palettes, and the two-pane/modal border styles. The `diff` and `mdrender` subpackages now honor `NO_COLOR` (they previously rendered colors regardless). No intended change to the default-profile appearance.
