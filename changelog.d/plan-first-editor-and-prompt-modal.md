### Added

- `PlanFirstEnabled` (default `false`) and `BuildFromPlanPrompt` settings (global + per-repo). When `PlanFirstEnabled` is on, pressing `n` opens a centered prompt modal asking "What are you working on?". `enter` runs the planning path: creates a session in `LifecycleDrafting`, kicks off `Manager.StartDraft` in the background, and returns focus to the dashboard — the session card shows a `✎ drafting…` badge while the draft runs. Press `enter`/`space` on the Planning card to open the plan editor when ready. If the planner raises a clarifying question, the plan editor opens automatically so the question is never silently skipped. `ctrl+enter` runs the skip path (today's `n` flow — agent spawns immediately, session lands directly in BUILDING). `esc` cancels.
- `internal/tui/planeditor.go` — full-page plan editor (`focusPlanEditor` panel) with three modes: scroll (`j`/`k`/`g`/`G`/`pgup`/`pgdn`), edit (`i` enters textarea, `ctrl+s` saves, `esc` returns to scroll preserving unsaved edits), and revise-input (`r`). `a` approves: writes any pending textarea content to disk, transitions the session to `LifecycleInProgress`, and spawns the real agent with the resolved `BuildFromPlanPrompt`. `q` abandons the session. Approve is a no-op on an empty/whitespace-only plan and surfaces "Plan is empty — edit or revise first." inline.
- `internal/tui/promptmodal.go` — centered, bordered textarea modal used by the plan-first `n` flow. Multi-line input via shift+enter; respects `MaxConcurrentAgents` and `MaxReviewBacklog` warnings (they fire before the modal opens, same as today's `n`). Modal width clamps between 40 and 80 cols.
- `Manager.CreateSessionForPlanning(cfg)` — creates a session worktree without spawning an agent so the plan-first flow can draft + review the plan before any real agent runs.

### Notes

- `r` in the editor stubs to "Revise lands in a follow-up PR." until the revise loop ships in PR 3.
- Skip-path sessions never enter the editor, matching today's behaviour exactly. The legacy `n` path is unchanged when `PlanFirstEnabled` is `false`.
- E2E coverage for the approve→spawn flow is deferred to PR 3 alongside the revise loop and dashboard polish.
