### Changed

- Consolidated the `internal/tui` foundations toward CONVENTIONS.md: semantic
  colors and styles now live in a single theme registry (no inline
  `lipgloss.NewStyle()` for semantic styling), width/height arithmetic is
  centralized in `layout.go` (one source for border/sidebar/modal math, with a
  shared two-column split helper), and concrete dependency wiring (config,
  per-repo managers, GitHub client) moved into `cmd/` behind an injected
  manager factory. The new-session sidebar is unified to 30 columns to match
  the dashboard. No behavioral change beyond the sidebar width.
