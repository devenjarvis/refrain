### Removed

- The session lifecycle machinery (`LifecyclePhase`, its persistence in `state.json`, and every remaining transition call site) — sessions no longer have phases at all. Legacy `"lifecyclePhase"` keys in old state files are silently ignored.
- The retired pipeline dashboard (`dashboard_*.go`), its four-section cursor machinery (`focuscursor.go`, `focusSection`), and the auto-promote-on-idle behavior. The session list is the only home screen.
- Wellness features: the focus-block timer, break overlay, breathing animation, soft session cap, review-backlog gate, and the `.refrain/logs/wellness.log` writer (existing log files are never read or deleted).
- Config fields `focus_session_minutes`, `focus_break_minutes`, `max_concurrent_sessions`, `max_review_backlog`, and `plan_first_enabled`, plus their global-settings form rows. Old settings files carrying these keys still load cleanly.
- `Manager.ActiveSessionCount` (lifecycle-aware) — replaced by a plain `SessionCount`.
- Unused design tokens: the pipeline-stage accents (`ColorBuilding`/`ColorReviewing`/`ColorShipping`), break-overlay colors, `BreatheColors`/`CompleteColors` animation palettes, `PadCell`, and `GlyphBranch`.

### Changed

- Detach now snapshots every session (merged/done sessions included) instead of cleaning up "complete" ones — nothing leaves the list on its own.
- Approving a session in the review panel (`m`) records completion via `MarkDone` and tears the session down; defer (`d`) simply closes the panel.
- `README.md`, `CLAUDE.md`, `DESIGN.md`, and `CONVENTIONS.md` rewritten for the session-list ADE: the pipeline/wellness framing is gone, while the signal-noise-budget and batch-over-stream design principles remain.
