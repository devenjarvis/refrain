### Changed

- Introduce `tui.KeyMap` (action → key bindings) and decompose `updateDashboard`'s `tea.KeyPressMsg` case into focused helpers: `handleKeysPlanEditor`, `handleKeysReviewPanel`, `handleKeysShippingPanel`, and `handleKeysWorkflow`. The dashboard key dispatch is now testable per-panel, and rebinding work has a single source of truth in `keymap.go`. Adds `keymap_test.go` covering no-duplicate-bindings and every-action-bound invariants.
