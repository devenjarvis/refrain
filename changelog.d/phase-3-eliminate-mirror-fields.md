### Changed

- Internal architecture (CONVENTIONS.md §5/§6): the dashboard view no longer mirrors root `App` state onto its own model. Modal state, the wellness snapshot, the pipeline cursor, per-session caches, and the session/agent row list are now read live each frame through a freshly built `dashboardProps` (the dashboard's analogue of `PanelServices`), eliminating the `syncModalsToDashboard` / `syncFocusCursorToDashboard` / `refreshAgentList` / `updateDashboardPRCache` sync path and the drift it allowed between ticks.
- Resolved the dual cursor: `App.cursor` (the pipeline cursor) is now the sole owner of selection; the vestigial flat-list `dashboard.selected` index and its `selectedItem`/`clampToRepo`/`clampToAgent` helpers were removed.
- Removed the dead diff-stats refresh path (it was computed and cached but never rendered).
