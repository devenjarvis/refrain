### Changed

- Removed the dead `focusTerminal` panel-focus state (preview-panel terminal focus is gone now that the pipeline view is the only dashboard). Drops ~150 lines of unreachable paste/key/wheel/cursor handling, the unused `screenToTermCell` and `forwardWheelToAgent` helpers, the `focusTerminalHints` status bar, and the now-orphaned `previewTermWidth/Height` and `previewMetadataRows` accessors.
- Removed `agent.Status.Symbol()` — no callers since the focus-mode redesign.
- Selection drag-copy now goes through a single focusLaunch path; the stale `selectedAgent()`-via-`d.selected` path was unreachable (selections only seed in focusLaunch) and could have copied with the wrong viewport size.
- Trimmed always-same-value parameters from a few helpers: `addEditorFields(inputWidth)`, `renderReviewPanel(height)`, `writeUnifiedLine(width)`, and the test helpers `waitForStatus(d)`, `waitForBranch(d)`, `tickerDashboard(sidebarW)`, `sandboxGitConfig` return value, and `makeFocusModeApp` unused returns.
