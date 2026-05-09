### Added

- Plan-first revise loop: pressing `r` in the plan editor and submitting a critique now invokes `Manager.RevisePlan`, which snapshots the current plan to `<worktree>/.claude/plan.prev.md`, calls `PlanDrafter.Revise` (Sonnet by default), and writes the revised plan in place. While revising, the editor renders a "Revising plan…" status with the current plan greyed out underneath. On failure, `Session.ReviseError` is set and the prior plan is left untouched.
- `u` in the plan editor restores `plan.prev.md` over `plan.md` for single-step undo and removes the snapshot. The hint only renders when a snapshot exists.
- Dashboard PLANNING card: drafting / revising sessions render a "✎ drafting…" or "✎ revising…" status badge; planned sessions render a "✎ N/M tasks" badge plus the first uncompleted task as the description; failed drafts surface a "✗ draft failed" badge with the error excerpt. `LifecycleDrafting` sessions now show up in the PLANNING section instead of being invisible until the draft lands.
- `space`/`enter`/click on a PLANNING row opens the plan editor (previously fell through to `openSessionInFocusLaunch`, which is a no-op for sessions with no agent yet).

### Changed

- `DefaultPlanFirstEnabled` flipped to `true`. The `n` keybind opens the prompt modal + plan editor by default; users who want today's immediate-spawn behaviour can set `"plan_first_enabled": false` in global or per-repo config, or hold `ctrl+enter` on the modal to skip the plan step on a per-session basis.
