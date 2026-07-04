### Changed

- The home screen is now a repo-grouped session list (rollback Phase 2). The
  four-section PLANNING → BUILDING → REVIEWING → SHIPPING pipeline is gone:
  sessions render as two-line cards in creation order with a single cursor,
  status word (error/waiting/active/starting/idle/done), badges (`plan D/T`,
  `✎ drafting…`, PR state, `closing…`), a context tag (`worktree` vs the
  accented `checkout @ <branch>`), age, and agent count.
- `enter`/`space` on any row opens the fullscreen agent terminal — the old
  per-phase dispatch (review panel for Reviewing rows, shipping panel for
  Shipping rows) is gone. A plan-only session with no agents opens its plan
  editor instead. `p` opens the PR panel when a PR exists, and otherwise
  pushes + drafts one once the session is quiet. `r` opens the review panel
  for the selected session — there is no separate review queue.
- The new-session screen is raw-by-default: `enter` spawns claude immediately
  with the prompt as its task, an **empty** `enter` opens a blank claude REPL,
  and `ctrl+p` runs the plan-first draft flow (replacing the inverted
  enter=plan / ctrl+enter=skip pair). A new Context field chooses between
  `Worktree (new branch)` (default) and `Current checkout` (a checkout
  session in the real working tree, one per repo). Copy is task-neutral.
- Agents now always run at fullscreen terminal dimensions — the sidebar
  preview geometry and its resize churn are gone.

### Removed

- The `b` (advance to Building) and `m` (mark ready) keys — phases no longer
  drive list placement, so there is nothing to advance.
- The wellness break overlay, breathing animation, session timer auto-break,
  soft session cap, and review-backlog gate no longer trigger from the new
  home screen (full removal of the remaining plumbing lands in Phase 5).
